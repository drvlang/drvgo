# Emit the proxy recipe without realizing any member inside the planner.
{ pkgs, drvgo, moduleManifest ? import ./module-manifest.nix, entries, members }:
let
  inherit (pkgs) lib;
  inherit (pkgs.stdenv.hostPlatform) system;
  artifacts = builtins.fromJSON (builtins.readFile entries);
  manifests = builtins.fromJSON (builtins.readFile members);
  groups = lib.imap0 (i: a: {
    inherit a;
    members = builtins.elemAt manifests i;
    # A cache may supply the signed manifest without its locally seeded files.
    # Schedule one bulk recovery before running any missing member fetchers.
    recover = moduleManifest {
      inherit pkgs drvgo; inherit (a) url kind checksum; recovery = true;
    };
  }) artifacts;
  # Identical bytes occur across module versions, license files, and fixtures.
  # A flat fixed-output path needs just one recovery URL and derivation.
  files = builtins.listToAttrs (lib.concatMap ({ a, members, recover }: map (m: {
    name = m.hash;
    value = builtins.derivation ({
      name = "drvgo-module-file"; inherit system;
      builder = "${drvgo}/bin/drvgo";
      args = [ "internal-module-fetch" a.url a.kind a.checksum (builtins.placeholder "out") m.name ];
      SSL_CERT_FILE = "${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt";
      outputHashAlgo = "sha256"; outputHashMode = "flat"; outputHash = m.hash;
      impureEnvVars = lib.fetchers.proxyImpureEnvVars;
    } // lib.optionalAttrs (a.kind == "zip" && builtins.length members > 1) {
      bulkRecovery = recover;
    });
  }) members) groups);
  archive = members: let
    config = builtins.toJSON (map (m: { inherit (m) name; file = files.${m.hash}; }) members);
    serialized = builtins.toFile "drvgo-archive-inputs" (builtins.unsafeDiscardStringContext config);
  in builtins.derivation {
    __contentAddressed = true; outputHashMode = "recursive"; outputHashAlgo = "sha256";
    name = "drvgo-module.zip"; inherit system;
    builder = "${drvgo}/bin/drvgo";
    args = [ "internal-module-archive" serialized (builtins.placeholder "out") ];
    memberInputs = builtins.appendContext "" (builtins.getContext config);
  };
  plan = map ({ a, members, ... }: {
    inherit (a) proxyPath version kind;
    file = if a.kind == "zip" then archive members else
      assert builtins.length members == 1 && (builtins.head members).name == "go.mod";
      files.${(builtins.head members).hash};
  }) groups;
in builtins.derivation {
  __contentAddressed = true; outputHashMode = "recursive"; outputHashAlgo = "sha256";
  name = "drvgo-locked-proxy"; inherit system;
  builder = "${drvgo}/bin/drvgo";
  args = [ "internal-module-proxy" (builtins.placeholder "out") ];
  passAsFile = [ "manifest" ];
  manifest = builtins.toJSON plan;
}
