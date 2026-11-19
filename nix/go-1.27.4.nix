{ pkgs }:
pkgs.go_1_27.overrideAttrs (final: _: {
  version = "1.27.4";
  src = pkgs.fetchurl {
    url = "https://go.dev/dl/go${final.version}.src.tar.gz";
    hash = "sha256-c8+4VxrsDAEXdofbhxY9Rqog6XeWcncokaoXZU2aYJk=";
  };
})
