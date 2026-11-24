{ pkgs }:
let
  cliSource = pkgs.lib.fileset.toSource {
    root = ../.;
    fileset = pkgs.lib.fileset.unions [ ../flake.nix ../flake.lock ../go.mod ../cmd ../internal ./. ];
  };
  worker = pkgs.buildGoModule {
    __contentAddressed = true; outputHashMode = "recursive"; outputHashAlgo = "sha256";
    pname = "drvgo-worker";
    version = "0.1.0";
    src = pkgs.lib.fileset.toSource {
      root = ../.;
      fileset = pkgs.lib.fileset.unions [ ../go.mod ../cmd ../internal ];
    };
    vendorHash = null;
    subPackages = [ "cmd/drvgo" ];
    ldflags = [ "-s" "-w" ];
    # The packaged CLI is stripped. Skip generating compiler DWARF that will
    # be discarded; GOFLAGS also lets tests reuse the same compiled packages.
    preBuild = ''
      export GOFLAGS="$GOFLAGS -gcflags=all=-dwarf=false"
    '';
    # Test library packages too, rather than only the CLI subpackage.
    checkPhase = ''
      runHook preCheck
      go test ./...
      go vet ./...
      runHook postCheck
    '';
    meta = {
      description = "Package-granular Go builds with Nix";
      homepage = "https://github.com/drvlang/drvgo";
      license = pkgs.lib.licenses.cc0;
      platforms = [ "x86_64-linux" "aarch64-linux" "aarch64-darwin" ];
      mainProgram = "drvgo";
    };
  };
in pkgs.runCommand "drvgo-${worker.version}" {
  __contentAddressed = true;
  nativeBuildInputs = [ pkgs.makeWrapper ];
  inherit (worker) meta;
  passthru = { pname = "drvgo"; inherit (worker) version; inherit worker; };
} ''
  mkdir -p "$out/bin"
  makeWrapper ${worker}/bin/drvgo "$out/bin/drvgo" --inherit-argv0 --set DRVGO_PROJECT_FLAKE 'path:${cliSource}'
''
