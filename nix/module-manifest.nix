{ system, drvgo, cacert, nix ? null, impureEnvVars }:
let
  fetchAttrs = {
    inherit system impureEnvVars;
    builder = "${drvgo}/bin/drvgo";
    SSL_CERT_FILE = "${cacert}/etc/ssl/certs/ca-bundle.crt";
  };
in {
  inherit fetchAttrs;
  manifest = { url, kind, checksum, recovery ? false }: builtins.derivation (fetchAttrs // {
    name = if recovery then "drvgo-module-recovery" else "drvgo-module-manifest";
    args = [ "internal-module-fetch" url kind checksum (builtins.placeholder "out") ];
    requiredSystemFeatures = [ "recursive-nix" ];
    # module-lock.nix has validated the h1: prefix.
    outputHash = "sha256-${builtins.substring 3 (-1) checksum}";
    outputHashMode = "flat";
    allowSubstitutes = !recovery;
  } // (if nix == null then {} else {
    drvgoNixStore = "${nix}/bin/nix-store";
  }));
}
