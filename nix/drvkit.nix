# Standalone nix-build and flake consumers use the same locked helper.
let locked = (builtins.fromJSON (builtins.readFile ../flake.lock)).nodes.drvkit.locked;
in (builtins.fetchTree (builtins.intersectAttrs {
  type = null; owner = null; repo = null; rev = null; narHash = null;
} locked)).outPath + "/dynamic.nix"
