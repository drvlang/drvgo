pkgs: version:
let
  release = base: version: hash: base.overrideAttrs (_: {
    inherit version;
    src = pkgs.fetchurl {
      url = "https://go.dev/dl/go${version}.src.tar.gz";
      inherit hash;
    };
  });
  versions = {
    ${pkgs.go_1_25.version} = pkgs.go_1_25;
    ${pkgs.go.version} = pkgs.go;
    "1.26.6" = release pkgs.go_1_26 "1.26.6" "sha256-oHIcVMaIkBRI13rZs+x+p8R0cwdV/4kTgukuy5P/LLE=";
    "1.27.4" = release pkgs.go_1_27 "1.27.4" "sha256-c8+4VxrsDAEXdofbhxY9Rqog6XeWcncokaoXZU2aYJk=";
  };
in versions.${version} or (throw "Unknown Go version '${version}'. Choose ${builtins.concatStringsSep ", " (builtins.attrNames versions)}.")
