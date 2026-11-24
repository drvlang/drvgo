{ pkgs, nixpkgsSource, drvgo, dynamicFile, src, moduleProxy }:
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
  lock = import ./module-lock.nix { inherit pkgs drvgo moduleProxy; src = toString src; };
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
  fromInputs = { entries, prepare ? "", members ? ''"$PWD/members.json"'' }: dynamic {
    name = "drvgo-proxy";
    prepare = ''
      ${prepare}
      ${pkgs.nix}/bin/nix build --impure --no-link --print-out-paths --file ${./module-members.nix} \
        --argstr pkgsPath ${nixpkgsSource} --argstr system ${system} --argstr tool ${drvgo} \
        --argstr certPath ${pkgs.cacert} --argstr entries ${entries} \
        --argstr members ${members} > members-path
    '';
    expression = ''
      let
        drvgo = builtins.storePath "${drvgo}";
        members = builtins.storePath (builtins.replaceStrings [ "\n" ] [ "" ] (builtins.readFile ./members-path));
      in builtins.derivation {
        __contentAddressed = true; outputHashMode = "recursive"; outputHashAlgo = "sha256";
        name = "drvgo-locked-proxy"; system = "${system}";
        builder = "''${drvgo}/bin/drvgo";
        args = [ "internal-module-proxy" members (builtins.placeholder "out") ];
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
