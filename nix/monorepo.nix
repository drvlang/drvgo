lib: { src, commands ? null, ... }@args:
let
  cmdDir = "${toString src}/cmd";
  entries = builtins.readDir cmdDir;
  visible = name: !(lib.hasPrefix "." name || lib.hasPrefix "_" name);
  isGoSource = file: type:
    type == "regular" && visible file && lib.hasSuffix ".go" file && !(lib.hasSuffix "_test.go" file);
  isCommand = name: type:
    type == "directory" && visible name && !(builtins.elem name [ "internal" "testdata" "vendor" ])
    && lib.filterAttrs isGoSource (builtins.readDir "${cmdDir}/${name}") != {};
  dirs = if commands == null then lib.filterAttrs isCommand entries
    else lib.genAttrs commands (name: entries.${name} or null);
in builtins.mapAttrs (name: type:
  if type != "directory" then throw "drvgo: expected a command directory at cmd/${name}"
  else (builtins.removeAttrs args [ "commands" ]) // {
    name = args.name or (lib.strings.sanitizeDerivationName name);
    package = "./cmd/${name}";
    src = builtins.path {
      name = "go-source";
      path = src;
      # Sibling commands have their own planners. Keep all shared inputs from src.
      filter = path: _: let base = builtins.baseNameOf path; in
        builtins.dirOf path != cmdDir || base == name || !(builtins.hasAttr base dirs);
    };
  }) dirs
