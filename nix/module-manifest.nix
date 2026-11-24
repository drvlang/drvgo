{ pkgs, drvgo, url, kind, checksum, recovery ? false }:
builtins.derivation {
  name = if recovery then "drvgo-module-recovery" else "drvgo-module-manifest";
  inherit (pkgs.stdenv.hostPlatform) system;
  builder = "${drvgo}/bin/drvgo";
  args = [ "internal-module-fetch" url kind checksum (builtins.placeholder "out") ];
  SSL_CERT_FILE = "${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt";
  drvgoNixStore = "${pkgs.nix}/bin/nix-store";
  requiredSystemFeatures = [ "recursive-nix" ];
  outputHash = "sha256-${pkgs.lib.removePrefix "h1:" checksum}";
  outputHashMode = "flat";
  impureEnvVars = pkgs.lib.fetchers.proxyImpureEnvVars;
  allowSubstitutes = !recovery;
}
