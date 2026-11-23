# Flake check names must be available during evaluation, including with IFD
# disabled. Discover candidate directories here and let the selected Go SDK
# decide package eligibility and build constraints when realizing each check.
lib: { src ? null, srcForTarget ? null, ... }@args: commands:
let
  root = if src == null || lib.isDerivation src then
    throw "drvgo: moduleTests requires a module-wide src readable during evaluation"
    else toString src;
  visible = name: !(lib.hasPrefix "." name || lib.hasPrefix "_" name);
  discover = relative:
    let
      entries = builtins.readDir (root + "/" + relative);
      names = builtins.filter visible (builtins.attrNames entries);
      goFile = name: entries.${name} != "directory" && lib.hasSuffix ".go" name;
      subdir = name: entries.${name} == "directory" && !(builtins.elem name [ "vendor" "testdata" ]);
      child = name: if relative == "." then name else "${relative}/${name}";
    in if relative != "." && entries ? "go.mod" && entries."go.mod" != "directory" then [] else
      lib.optional (builtins.any goFile names) relative
      ++ lib.concatMap (name: discover (child name)) (builtins.filter subdir names);

  commandDirs = lib.genAttrs (map (c: lib.removePrefix "./" c.package) (builtins.attrValues commands)) (_: true);
  packageCheck = target:
    let name = if target == "." then "module-tests" else "module-tests/${target}"; in
    lib.nameValuePair name ((removeAttrs args [ "commands" "srcForTarget" "target" ]) // {
      src = if srcForTarget == null then src else srcForTarget { inherit name target; };
      package = if target == "." then "." else "./${target}";
      name = args.name or (lib.strings.sanitizeDerivationName name);
      moduleTest = true;
    });
  extra = builtins.listToAttrs (map packageCheck (builtins.filter (dir: !(commandDirs ? ${dir})) (discover ".")));
  conflicts = builtins.attrNames (builtins.intersectAttrs commands extra);
in if !(builtins.pathExists (root + "/go.mod")) then throw "drvgo: moduleTests requires go.mod at the root of src"
else if conflicts == [] then commands // extra else
  throw "drvgo: module test check names conflict with command names: ${lib.concatStringsSep ", " conflicts}"
