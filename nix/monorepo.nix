lib: { src ? null, commands ? null, srcForTarget ? null, ... }@args:
let
  root = if src == null then throw "drvgo: specify src for discovery or commands and srcForTarget" else toString src;
  config = if builtins.pathExists (root + "/drvlang.json")
    then builtins.fromJSON (builtins.readFile (root + "/drvlang.json")) else {};
  declared = if commands != null then commands else config.commands or null;
  base = if builtins.pathExists (root + "/app") && discover "app" != {} then "app" else "cmd";
  visible = name: !(lib.hasPrefix "." name || lib.hasPrefix "_" name);
  isGo = file: type: type == "regular" && visible file && lib.hasSuffix ".go" file && !(lib.hasSuffix "_test.go" file);
  discover = candidate: lib.filterAttrs (name: type: type == "directory" && visible name
    && !(builtins.elem name [ "internal" "testdata" "vendor" ])
    && lib.filterAttrs isGo (builtins.readDir (root + "/${candidate}/${name}")) != {})
    (if builtins.pathExists (root + "/${candidate}") then builtins.readDir (root + "/${candidate}") else {});
  discovered = discover base;
  targets = if builtins.isAttrs declared then declared else lib.genAttrs
    (if declared == null then builtins.attrNames discovered else declared) (name: "./${base}/${name}");
  normal = path: let p = lib.removePrefix "./" path; in if p == "." || (p != "" && builtins.all
    (s: !(builtins.elem s [ "" "." ".." ])) (lib.splitString "/" p)) then p
    else throw "drvgo: invalid root-relative command path ${path}";
  dirs = map (p: root + "/${normal p}") (builtins.attrValues targets);
in builtins.mapAttrs (name: path: let
  dir = normal path;
  input = if srcForTarget == null then src else srcForTarget { inherit name; target = dir; };
  selected = toString input + "/${dir}";
in if !builtins.pathExists selected then throw "drvgo: missing command directory ${dir}"
  else (builtins.removeAttrs args [ "commands" "srcForTarget" ]) // {
    name = args.name or (lib.strings.sanitizeDerivationName name);
    package = if dir == "." then "." else "./${dir}";
    src = if srcForTarget != null then input else builtins.path {
      name = "go-source"; path = src;
      filter = file: type: !(builtins.elem file dirs && file != selected &&
        !lib.hasPrefix (file + "/") selected && !lib.hasPrefix (selected + "/") file);
    };
  }) targets
