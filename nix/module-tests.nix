# Flake check names must be available during evaluation, including with IFD
# disabled. Discover candidate directories here and let the selected Go SDK
# decide package eligibility and build constraints when realizing each check.
{ lib, readSource }: { src ? null, srcForTarget ? null, ... }@args: commands:
let
  sourceRoot = source:
    if lib.isDerivation source then throw "drvgo: discovering Go package checks requires sources readable during evaluation"
    else readSource source;
  roots = if src != null then [ (sourceRoot src) ] else
    map (command: sourceRoot command.src) (builtins.attrValues commands);
  visible = name: !(lib.hasPrefix "." name || lib.hasPrefix "_" name);
  discover = root: relative:
    let
      entries = root.readDir relative;
      names = builtins.filter visible (builtins.attrNames entries);
      goFile = name: entries.${name} != "directory" && lib.hasSuffix ".go" name;
      subdir = name: entries.${name} == "directory" && !(builtins.elem name [ "vendor" "testdata" ]);
      child = name: if relative == "." then name else "${relative}/${name}";
    in if relative != "." && entries ? "go.mod" && entries."go.mod" != "directory" then [] else
      lib.optional (builtins.any goFile names) relative
      ++ lib.concatMap (name: discover root (child name)) (builtins.filter subdir names);
  directories = lib.concatMap (root:
    if root.exists "go.mod" then discover root "."
    else throw "drvgo: discovering Go package checks requires go.mod at the root of each source") roots;

  commandDirs = lib.genAttrs (map (c: lib.removePrefix "./" c.package) (builtins.attrValues commands)) (_: true);
  packageCheck = target:
    let name = if target == "." then "module-tests" else "module-tests/${target}"; in
    lib.nameValuePair name ((removeAttrs args [ "commands" "srcForTarget" "target" ]) // {
      src = if srcForTarget == null then src else srcForTarget { inherit name target; };
      package = if target == "." then "." else "./${target}";
      name = args.name or (lib.strings.sanitizeDerivationName name);
      moduleTest = true;
    });
  extra = builtins.listToAttrs (map packageCheck (builtins.filter (dir: !(commandDirs ? ${dir})) directories));
  conflicts = builtins.attrNames (builtins.intersectAttrs commands extra);
in if conflicts == [] then commands // extra else
  throw "drvgo: module test check names conflict with command names: ${lib.concatStringsSep ", " conflicts}"
