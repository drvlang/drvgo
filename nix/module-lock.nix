# Derive the verified file-manifest inputs directly from native Go checksums.
{ lib, fetch, src, moduleProxy, source ? {
    exists = rel: builtins.pathExists (src + "/" + rel);
    readFile = rel: builtins.readFile (src + "/" + rel);
  } }:
let
  # Proxy paths escape uppercase ASCII; the signed member names retain their
  # original spelling. Go module paths cannot contain spaces or URL fragments.
  upper = lib.stringToCharacters "ABCDEFGHIJKLMNOPQRSTUVWXYZ";
  escape = builtins.replaceStrings upper (map (c: "!" + lib.toLower c) upper);
  parse = line: let
    fields = builtins.match "([^[:space:]]+) +([^[:space:]]+) +h1:([a-zA-Z0-9+/]+=*)[[:space:]]*" line;
    module = builtins.elemAt fields 0;
    versionAndKind = builtins.elemAt fields 1;
    version = lib.removeSuffix "/go.mod" versionAndKind;
    kind = if lib.hasSuffix "/go.mod" versionAndKind then "mod" else "zip";
    digest = builtins.elemAt fields 2;
    proxyPath = "${escape module}/@v/${escape version}";
    url = "${lib.removeSuffix "/" moduleProxy}/${proxyPath}.${kind}";
    checksum = "h1:${digest}";
    manifest = fetch.manifest { inherit url kind checksum; };
  in if fields == null || !builtins.all (s: !(builtins.elem s [ "." ".." "" ])) (lib.splitString "/" module)
    || builtins.match "v[0-9][^/]*" version == null
    then throw "drvgo: invalid go.sum entry: ${line}"
    else { inherit manifest; metadata = { inherit proxyPath version kind url checksum; }; };
  artifacts = map parse (builtins.filter (line: line != "") (lib.splitString "\n" (source.readFile "go.sum")));
in if source.exists "vendor/modules.txt" || !(source.exists "go.sum") then null else {
  entries = builtins.toJSON (map (a: a.metadata) artifacts);
  manifests = lib.concatMapStrings (a: "${a.manifest}\n") artifacts;
}
