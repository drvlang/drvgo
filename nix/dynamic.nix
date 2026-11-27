# Source-only, package-granular Go builds. Requires dynamic-derivations,
# ca-derivations and recursive-nix in both the client and build daemon.
{ toolchain ? import ../internal/export/templates/toolchain.nix
, drvkit ? import ./drvkit.nix
, dynamicFile ? drvkit + "/dynamic.nix"
, drvgo ? (import ./package.nix { pkgs = toolchain.pkgs; }).worker
}:
{ src, package ? ".", vendorHash ? null, moduleProxy ? "https://proxy.golang.org", tags ? "", ldflags ? "", name ? "drvgo"
, experimentalGo ? false, test ? false, race ? false, cgoEnabled ? null
, allowPlannerSubstitutes ? true, allowEmptyTests ? false, moduleTest ? false
, runtimeInputs ? []
, previousPlan ? null
, moduleCache ? null
, prepareModules ? null
, exportData ? true
, earlyCutoff ? null # Compatibility with callers predating content-addressed defaults.
, iets ? null
}:
let
  pkgs = toolchain.pkgs;
  inherit (pkgs) lib;
  modules = if moduleCache != null || moduleProxy == "off" then null
    else if lib.hasPrefix "file://" moduleProxy then
      let path = lib.removePrefix "file://" moduleProxy; in
      if lib.isStorePath path then
        if builtins.hasContext path then path else builtins.appendContext path { ${path}.path = true; }
      else builtins.path { path = if lib.hasPrefix "/" path then path else /. + path; name = "go-proxy"; }
    # Older callers may still pass vendorHash. go.sum determines the proxy.
    else import ./locked-modules.nix {
      inherit pkgs drvgo dynamicFile src moduleProxy;
      readSource = import (drvkit + "/source-reader.nix") lib;
      nixpkgsSource = import (drvkit + "/nixpkgs-source.nix") pkgs (toolchain.nixpkgs or null);
    };
  # Dry-run discovery needs the driver, sources and tool-version metadata.
  # Native compiler probes are emitted for workers to replay instead.
  # Empty-test validation compiles packages with ordinary source files,
  # including their cgo dependencies. Keep actual compilers available for
  # that path. Explicitly disabling cgo avoids native tools.
  compactPlanningToolchain = import ./planning-toolchain.nix { inherit pkgs drvgo; go = toolchain.go; };
  planningToolchain = if allowEmptyTests then
    (if cgoEnabled == false then toolchain.go else toolchain)
  else
    compactPlanningToolchain;
  # Reuse verified native dependencies across commands and source edits.
  # Hand-supplied file proxies retain their lazy extraction behavior.
  prepare = if prepareModules == null then !(lib.hasPrefix "file://" moduleProxy) else prepareModules;
  preparedModules = if moduleCache != null then moduleCache
    else if !prepare || modules == null then null
    else import ./module-cache.nix { inherit pkgs drvgo modules; go = compactPlanningToolchain; };
  # Only the recipe is needed during planning. Restore its output context in
  # the nested evaluator or direct planner, so generated packages keep the same
  # dependencies as ordinary exports without fetching unused worker tools.
  recipe = tc: if tc ? drvPath then builtins.unsafeDiscardOutputDependency tc.drvPath else tc;
  recipeDrv = tc: if tc ? drvPath then builtins.unsafeDiscardOutputDependency tc.drvPath else "";
  recipeOut = tc: if tc ? drvPath then builtins.unsafeDiscardStringContext (toString tc) else tc;
  outputExpr = tc: if !(tc ? drvPath) then ''builtins.storePath "${tc}"'' else ''builtins.appendContext ${builtins.toJSON (builtins.unsafeDiscardStringContext (toString tc))} {
    ${builtins.toJSON (builtins.unsafeDiscardOutputDependency tc.drvPath)} = { outputs = [ "out" ]; };
  }'';
  toolchainExpr = ''{
    outPath = ${outputExpr toolchain};
    compile = ${outputExpr (toolchain.compile or toolchain)};
    link = ${outputExpr (toolchain.link or toolchain)};
    native = ${outputExpr (toolchain.native or toolchain)};
    internalLink = ${outputExpr (toolchain.internalLink or (toolchain.native or toolchain))};
  }'';
  # Excluded test directories need go/build from the selected SDK. Share its
  # executable across planners, whose writable Go build caches start empty.
  testSelector = pkgs.runCommand "drvgo-test-selector" {} ''
    export HOME="$TMPDIR" GOCACHE="$TMPDIR/cache" GOTOOLCHAIN=local CGO_ENABLED=0
    cp ${../internal/export/templates/test-selection.go} selection.go
    ${toolchain.go or toolchain}/bin/go build -p "$NIX_BUILD_CORES" -trimpath -gcflags='all=-dwarf=false -l' -ldflags='-s -w' \
      -o drvgo-test-selector selection.go
    install -m 0555 drvgo-test-selector "$out"
  '';
  # These wrappers only run explicit tools; a stdenv setup and its dependency
  # closure are unnecessary on the planning and final forwarding paths.
  compileTc = toolchain.compile or toolchain;
  linkTc = toolchain.link or toolchain;
  nativeTc = toolchain.native or toolchain;
  internalLinkTc = toolchain.internalLink or nativeTc;
  # Passing a .drv by unsafeDiscardOutputDependency bind-mounts its entire
  # transitive bootstrap .drv closure (~850 mounts) into the recursive sandbox,
  # making every daemon store registration ~5-10x slower on Linux. Pass the
  # compact realized worker bundles directly when they will be used, keeping
  # the sandbox mount table tiny and letting toolchain helpers build in parallel.
  realizeNative = cgoEnabled != false;
  realizeInternalLink = lib.hasInfix "linkmode" ldflags;
  planner = builtins.derivation ({
    name = "${name}-planner.drv";
    system = pkgs.stdenv.hostPlatform.system;
    builder = "${drvgo}/bin/drvgo";
    args = [ "internal-plan"
      (if iets == null then "native" else "iets")
    ] ++ lib.optional experimentalGo "-experimental-go"
      ++ lib.optional test "-test"
      ++ lib.optional race "-race"
      ++ lib.optional allowEmptyTests "-allow-empty-tests"
      ++ lib.optionals (cgoEnabled != null) [ "-cgo" (if cgoEnabled then "1" else "0") ]
      ++ [ "-toolchain" planningToolchain ]
      ++ (if preparedModules != null then [ "-module-cache" preparedModules ]
          else [ "-module-proxy" (if modules == null then "off" else "file://${modules}") ])
      ++ [ "-tags" tags "-ldflags" ldflags package ];
    PATH = lib.makeBinPath [ drvgo ];
    # Remote build hooks do not reliably copy newly generated derivation closures.
    # Plan locally; the generated package derivations can still build remotely.
    preferLocalBuild = true;
    # Fresh workers can substitute a published plan and its generated graph.
    allowSubstitutes = allowPlannerSubstitutes;
    __contentAddressed = true;
    outputHashMode = "text";
    outputHashAlgo = "sha256";
    requiredSystemFeatures = [ "recursive-nix" ];
    inherit src;
  } // (if iets == null then {
    compileToolchainOut = compileTc;
    linkToolchainOut = linkTc;
    nativeToolchainOut = if realizeNative then nativeTc else recipeOut nativeTc;
    nativeToolchainDrv = if realizeNative then "" else recipeDrv nativeTc;
    internalLinkToolchainOut = if realizeInternalLink then internalLinkTc else "";
    internalLinkToolchainDrv = "";
    drvgoOut = builtins.unsafeDiscardStringContext (toString drvgo);
    exportData = if exportData then "1" else "0";
    resultName = "${name}-result";
  } else {
    toolchainDrv = recipe toolchain;
    compileToolchainDrv = recipeDrv compileTc;
    linkToolchainDrv = recipeDrv linkTc;
    nativeToolchainDrv = recipeDrv nativeTc;
    internalLinkToolchainDrv = recipeDrv internalLinkTc;
    drvgoDrv = recipeDrv drvgo;
    plannerEvaluator = "${iets}/bin/iets";
    passAsFile = [ "expression" ];
    # Keep the emitted recipe distinct from the text-addressed planner output.
    # A forwarding derivation supplies a stable name independent of Go import paths.
    expression = ''
      let tc = ${toolchainExpr};
          drvgo = ${outputExpr drvgo};
          command = (import ./graph {
            toolchain = tc;
            exportData = ${builtins.toJSON exportData};
          }).default;
      in derivation {
        __contentAddressed = true; outputHashMode = "recursive"; outputHashAlgo = "sha256";
        name = ${builtins.toJSON "${name}-result"};
        system = "${pkgs.stdenv.hostPlatform.system}";
        # Keep local forwarding cheap when the output is absent from caches.
        preferLocalBuild = true;
        # Unlike coreutils ln, the native helper leaves stdout/stderr open
        # until exit, avoiding Nix's EOF-triggered builder cleanup race.
        builder = "''${drvgo}/bin/drvgo";
        args = [ "internal-symlink" command (builtins.placeholder "out") ];
      }
    '';
  }) // lib.optionalAttrs allowEmptyTests {
    inherit testSelector;
  } // lib.optionalAttrs moduleTest {
    moduleTest = "1";
  } // lib.optionalAttrs (runtimeInputs != []) {
    # Literal paths in an existing checkout have no Nix string context.
    # Make their data available to the restricted recursive store; the graph
    # retains only references found in each selected package or replay action.
    inherit runtimeInputs;
    passAsFile = (if iets == null then [] else [ "expression" ]) ++ [ "runtimeInputs" ];
  } // lib.optionalAttrs (previousPlan != null) {
    # Make an earlier graph's closure visible to the restricted store. Source
    # discovery still runs from scratch; only identical content-addressed
    # sources and derivation recipes can reuse their existing registrations.
    inherit previousPlan;
  });
  result = builtins.derivation {
    __contentAddressed = true; outputHashMode = "recursive"; outputHashAlgo = "sha256";
    inherit name;
    system = pkgs.stdenv.hostPlatform.system;
    preferLocalBuild = true;
    builder = "${drvgo}/bin/drvgo";
    args = [ "internal-symlink" (builtins.outputOf planner.outPath "out") (builtins.placeholder "out") ];
  };
in result // { inherit planner modules planningToolchain; moduleCache = preparedModules;
  passthru = { inherit planner modules planningToolchain; moduleCache = preparedModules; }; }
