# Realize the signed members once per native lock. The resulting CA file
# contains store references to every verified member used by proxy assembly.
{ pkgsPath, system, tool, certPath, entries, members }:
let
  pkgs = import (builtins.storePath pkgsPath) { inherit system; };
  drvgo = builtins.storePath tool;
  cert = builtins.storePath certPath;
  artifacts = builtins.fromJSON (builtins.readFile (builtins.toPath entries));
  manifests = builtins.fromJSON (builtins.readFile (builtins.toPath members));
  member = a: m: {
    inherit (m) name;
    file = builtins.derivation {
      name = "drvgo-module-file"; inherit system;
      builder = "${drvgo}/bin/drvgo";
      args = [ "internal-module-fetch" a.url a.kind a.checksum (builtins.placeholder "out") m.name ];
      SSL_CERT_FILE = "${cert}/etc/ssl/certs/ca-bundle.crt";
      outputHashAlgo = "sha256"; outputHashMode = "flat"; outputHash = m.hash;
      impureEnvVars = pkgs.lib.fetchers.proxyImpureEnvVars;
    };
  };
  plan = pkgs.lib.imap0 (i: a: a // {
    members = map (member a) (builtins.elemAt manifests i);
  }) artifacts;
  config = builtins.toJSON plan;
  # Every file has a known flat fixed-output path. Track all those inputs,
  # without repeatedly substituting them into the serialized JSON.
  serialized = builtins.toFile "drvgo-proxy-inputs" (builtins.unsafeDiscardStringContext config);
in builtins.derivation {
  __contentAddressed = true; outputHashMode = "recursive"; outputHashAlgo = "sha256";
  name = "drvgo-module-members.json"; inherit system;
  builder = "${pkgs.coreutils}/bin/cp";
  args = [ serialized (builtins.placeholder "out") ];
  memberInputs = builtins.appendContext "" (builtins.getContext config);
}
