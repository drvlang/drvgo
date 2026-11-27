# Emit the proxy recipe without realizing any member inside the planner.
{ fetch, entries, members }:
let
  inherit (fetch.fetchAttrs) system builder;
  artifacts = builtins.fromJSON (builtins.readFile entries);
  manifests = builtins.fromJSON (builtins.readFile members);
  groups = builtins.genList (i: let
    a = builtins.elemAt artifacts i;
    members = builtins.elemAt manifests i;
  in {
    inherit a members;
    # A cache may supply the signed manifest without its locally seeded files.
    # Schedule one bulk recovery before running any missing member fetchers.
    recoveryAttrs = if a.kind == "zip" && builtins.length members > 1 then {
      bulkRecovery = fetch.manifest { inherit (a) url kind checksum; recovery = true; };
    } else {};
  }) (builtins.length artifacts);
  # Identical bytes occur across module versions, license files, and fixtures.
  # A flat fixed-output path needs just one recovery URL and derivation.
  files = builtins.listToAttrs (builtins.concatMap ({ a, members, recoveryAttrs }: map (m: {
    name = m.hash;
    value = builtins.derivation (fetch.fetchAttrs // recoveryAttrs // {
      name = "drvgo-module-file";
      args = [ "internal-module-fetch" a.url a.kind a.checksum (builtins.placeholder "out") m.name ];
      outputHashAlgo = "sha256"; outputHashMode = "flat"; outputHash = m.hash;
    });
  }) members) groups);
  archive = members: let
    config = builtins.toJSON (map (m: { inherit (m) name; file = files.${m.hash}; }) members);
    serialized = builtins.toFile "drvgo-archive-inputs" (builtins.unsafeDiscardStringContext config);
  in builtins.derivation {
    __contentAddressed = true; outputHashMode = "recursive"; outputHashAlgo = "sha256";
    name = "drvgo-module.zip"; inherit system builder;
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
  name = "drvgo-locked-proxy"; inherit system builder;
  args = [ "internal-module-proxy" (builtins.placeholder "out") ];
  passAsFile = [ "manifest" ];
  manifest = builtins.toJSON plan;
}
