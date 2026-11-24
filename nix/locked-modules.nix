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
  lock = import ./module-lock.nix { inherit pkgs drvgo moduleProxy src; source = readSource src; };
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
          moduleManifest = import ${./module-manifest.nix};
          inherit pkgs drvgo; src = builtins.storePath "${src}";
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
  fromInputs = { entries, prepare ? "", members ? "./members.json" }: dynamic {
    name = "drvgo-proxy";
    inherit prepare;
    expression = ''
      import ${./module-members.nix} {
        moduleManifest = import ${./module-manifest.nix};
        pkgs = import ${nixpkgsSource} { system = "${system}"; };
        drvgo = builtins.storePath "${drvgo}";
        entries = ${entries};
        members = ${members};
      }
    '';
  };
in if lib.isDerivation src then fromInputs {
  entries = "${generated}/entries.json";
  members = "${generated}/members.json";
} else if lock == null then null else fromInputs {
  entries = textInput "drvgo-module-entries.json" lock.entries;
  prepare = let list = (pkgs.writeText "drvgo-manifests" lock.manifests).overrideAttrs (_: cacheable);
            in "${fetcher} internal-module-manifests ${list} > members.json";
}
