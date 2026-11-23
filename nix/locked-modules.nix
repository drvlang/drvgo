{ pkgs, drvgo, dynamicFile, src, moduleProxy }:
let
  inherit (pkgs) lib;
  dynamic = args: (import dynamicFile { inherit pkgs; }) (args // lib.optionalAttrs pkgs.stdenv.hostPlatform.isDarwin {
    copyDerivation = "${drvgo}/bin/drvgo";
  });
  fetcher = "${drvgo}/bin/drvgo";
  system = pkgs.stdenv.hostPlatform.system;
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
    manifest = builtins.derivation {
      name = "drvgo-module-manifest";
      inherit system;
      builder = fetcher;
      args = [ "internal-module-fetch" url kind checksum (builtins.placeholder "out") ];
      SSL_CERT_FILE = "${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt";
      drvgoNixStore = "${pkgs.nix}/bin/nix-store";
      requiredSystemFeatures = [ "recursive-nix" ];
      outputHash = "sha256-${digest}";
      outputHashMode = "flat";
      impureEnvVars = lib.fetchers.proxyImpureEnvVars;
    };
  in if fields == null || !builtins.all (s: !(builtins.elem s [ "." ".." "" ])) (lib.splitString "/" module)
    || builtins.match "v[0-9][^/]*" version == null
    then throw "drvgo: invalid go.sum entry: ${line}"
    else { inherit manifest; metadata = { inherit proxyPath version kind url checksum; }; };
  # A source produced by a derivation can only be inspected inside a planner.
  fromSum = source:
    let
      data = builtins.readFile (source + "/go.sum");
      artifacts = map parse (builtins.filter (l: l != "") (lib.splitString "\n" data));
      metadata = map (a: a.metadata) artifacts;
    in dynamic {
      name = "drvgo-proxy";
      prepare = ''
        i=0
        while IFS= read -r manifest; do
          ${fetcher} internal-module-members "$manifest" > "members-$i.json"
          i=$((i+1))
        done < ${pkgs.writeText "drvgo-manifests" (lib.concatMapStrings (a: "${a.manifest}\n") artifacts)}
      '';
      expression = ''
        let
          pkgs = import ${pkgs.path} { system = "${system}"; };
          drvgo = builtins.storePath "${drvgo}";
          cert = builtins.storePath "${pkgs.cacert}";
          artifacts = builtins.fromJSON (builtins.readFile ${pkgs.writeText "drvgo-module-entries.json" (builtins.toJSON metadata)});
          member = a: m: {
            inherit (m) name;
            file = builtins.derivation {
              name = "drvgo-module-file"; system = "${system}";
              builder = "''${drvgo}/bin/drvgo";
              args = [ "internal-module-fetch" a.url a.kind a.checksum (builtins.placeholder "out") m.name ];
              SSL_CERT_FILE = "''${cert}/etc/ssl/certs/ca-bundle.crt";
              outputHashAlgo = "sha256"; outputHashMode = "flat"; outputHash = m.hash;
              impureEnvVars = pkgs.lib.fetchers.proxyImpureEnvVars;
            };
          };
          plan = pkgs.lib.imap0 (i: a: a // {
            members = map (member a) (builtins.fromJSON (builtins.readFile (./. + "/members-" + toString i + ".json")));
          }) artifacts;
        in builtins.derivation {
          name = "drvgo-locked-proxy"; system = "${system}";
          builder = "''${drvgo}/bin/drvgo";
          args = [ "internal-module-proxy" (pkgs.writeText "drvgo-proxy.json" (builtins.toJSON plan)) (builtins.placeholder "out") ];
        }
      '';
    };
  fromSource = source: if builtins.pathExists (source + "/vendor/modules.txt") || !builtins.pathExists (source + "/go.sum")
    then null else fromSum source;
in if lib.isDerivation src then dynamic {
  name = "drvgo-source-proxy";
  expression = ''
    let pkgs = import ${pkgs.path} { system = "${system}"; };
        proxy = import ${./locked-modules.nix} {
          inherit pkgs; drvgo = builtins.storePath "${drvgo}"; dynamicFile = ${dynamicFile};
          src = builtins.storePath "${src}"; moduleProxy = builtins.readFile ${pkgs.writeText "drvgo-proxy-url" moduleProxy};
        };
    in if proxy == null then pkgs.runCommand "drvgo-empty-proxy" {} "mkdir $out" else proxy
  '';
} else fromSource (toString src)
