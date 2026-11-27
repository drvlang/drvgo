{ pkgs, nixpkgsSource, drvgo, dynamicFile, src, moduleProxy, readSource }:
let
  inherit (pkgs) lib;
  dynamic = args: (import dynamicFile { inherit pkgs; }) (args // lib.optionalAttrs pkgs.stdenv.hostPlatform.isDarwin {
    copyDerivation = "${drvgo}/bin/drvgo";
  });
  fetcher = "${drvgo}/bin/drvgo";
  system = pkgs.stdenv.hostPlatform.system;
  cacheable = { __contentAddressed = true; outputHashMode = "recursive"; outputHashAlgo = "sha256"; allowSubstitutes = true; };
  textInput = name: value: if builtins.hasContext value then (pkgs.writeText name value).overrideAttrs (_: cacheable)
    else builtins.toFile name value;
  impureEnvVars = lib.fetchers.proxyImpureEnvVars;
  fetch = import ./module-manifest.nix { inherit system drvgo impureEnvVars; inherit (pkgs) cacert; };
  # Resolve the fetch tools once. Importing Nixpkgs inside the restricted
  # recursive evaluator would instantiate their entire bootstrap graphs again.
  fetchExpr = ''
    import ${./module-manifest.nix} {
      system = ${builtins.toJSON system};
      drvgo = builtins.storePath "${drvgo}";
      cacert = builtins.storePath "${pkgs.cacert}";
      impureEnvVars = ${lib.generators.toPretty {} impureEnvVars};
    }
  '';
  lock = import ./module-lock.nix { inherit lib fetch moduleProxy src; source = readSource src; };
  # Generated sources must reveal go.sum in a builder. Emit a regular metadata
  # derivation here. The public proxy planner consumes its realized output, so
  # the Darwin recursive sandbox never has to open a newly-created planner recipe.
  generated = dynamic {
    name = "drvgo-source-lock";
    expression = ''
      let
        pkgs = import ${nixpkgsSource} { system = "${system}"; };
        drvgo = builtins.storePath "${drvgo}";
        lock = import ${./module-lock.nix} {
          fetch = ${fetchExpr}; inherit (pkgs) lib;
          src = builtins.storePath "${src}";
          moduleProxy = builtins.readFile ${textInput "drvgo-proxy-url" moduleProxy};
        };
      in pkgs.runCommand "drvgo-source-lock-inputs" {
        __contentAddressed = true; outputHashMode = "recursive"; outputHashAlgo = "sha256";
        passAsFile = [ "entries" "manifests" ];
        entries = if lock == null then "[]" else lock.entries;
        manifests = if lock == null then "" else lock.manifests;
      } ("mkdir $out\ncp $entriesPath $out/entries.json\n" +
         "''${drvgo}/bin/drvgo internal-module-manifests $manifestsPath > $out/members.json")
    '';
  };
  fromInputs = { entries, manifests ? null, members ? null }: let
    planner = builtins.derivation ({
      name = "drvgo-proxy-plan.drv";
      inherit system;
      builder = fetcher;
      args = [ "internal-proxy-plan" ];
      __contentAddressed = true;
      outputHashMode = "text";
      outputHashAlgo = "sha256";
      requiredSystemFeatures = [ "recursive-nix" ];
      preferLocalBuild = true;
      allowSubstitutes = true;
      drvgoOut = builtins.unsafeDiscardStringContext (toString drvgo);
      cacert = pkgs.cacert;
      drvgoImpureEnvVars = lib.concatStringsSep " " impureEnvVars;
      inherit entries;
    } // (if manifests != null then { inherit manifests; } else { inherit members; }));
    result = builtins.derivation {
      name = "drvgo-proxy";
      inherit system;
      __contentAddressed = true;
      outputHashMode = "recursive";
      outputHashAlgo = "sha256";
      preferLocalBuild = true;
      builder = fetcher;
      args = [ "internal-symlink" (builtins.outputOf planner.outPath "out") (builtins.placeholder "out") ];
    };
  in result // { inherit planner; passthru = { inherit planner; }; };
in if lib.isDerivation src then fromInputs {
  entries = "${generated}/entries.json";
  members = "${generated}/members.json";
} else if lock == null then null else fromInputs {
  entries = textInput "drvgo-module-entries.json" lock.entries;
  # The go.sum hashes already determine these fixed-output store paths. Write
  # the list at evaluation time and keep its fetch recipes as planner inputs.
  manifests = let
    list = builtins.toFile "drvgo-manifests" (builtins.unsafeDiscardStringContext lock.manifests);
  in builtins.appendContext list (builtins.getContext lock.manifests);
}
