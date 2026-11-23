lib: { src, commands ? null, ... }@args:
let
  appDir = "${toString src}/app";
  entries = builtins.readDir appDir;
  visible = name: !(lib.hasPrefix "." name || lib.hasPrefix "_" name);
  isGoSource = file: type:
    type == "regular" && visible file && lib.hasSuffix ".go" file && !(lib.hasSuffix "_test.go" file);
  isCommand = name: type:
    type == "directory" && visible name && !(builtins.elem name [ "internal" "testdata" "vendor" ])
    && lib.filterAttrs isGoSource (builtins.readDir "${appDir}/${name}") != {};
  dirs = if commands == null then lib.filterAttrs isCommand entries
    else lib.genAttrs commands (name: entries.${name} or null);
in builtins.mapAttrs (name: type:
  if type != "directory" then throw "drvgo: expected a command directory at app/${name}"
  else (builtins.removeAttrs args [ "commands" ]) // {
    name = args.name or (lib.strings.sanitizeDerivationName name);
    package = "./app/${name}";
    src = builtins.path {
      name = "go-source";
      path = src;
      # Sibling commands have their own planners. Keep all shared inputs from src.
      filter = path: _: let base = builtins.baseNameOf path; in
        builtins.dirOf path != appDir || base == name || !(builtins.hasAttr base dirs);
    };
  }) dirs
