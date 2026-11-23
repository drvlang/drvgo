{ pkgs }:
let
  inherit (pkgs) lib;
  project = lib.fileset.toSource {
    root = ../.;
    fileset = lib.fileset.unions [ ../flake.nix ../flake.lock ../go.mod ../cmd ../internal ../nix ];
  };
in pkgs.rustPlatform.buildRustPackage {
  pname = "drvgo-bench";
  version = "0.1.0";
  src = lib.fileset.toSource {
    root = ./.;
    fileset = lib.fileset.unions [ ./Cargo.toml ./Cargo.lock ./src ];
  };
  cargoLock = { lockFile = ./Cargo.lock; allowBuiltinFetchGit = true; };
  nativeBuildInputs = [ pkgs.makeWrapper ];
  postInstall = ''
    wrapProgram "$out/bin/drvgo-bench" \
      --set-default DRVGO_BENCH_REPO ${project} \
      --suffix PATH : ${lib.makeBinPath [ pkgs.nix pkgs.gitMinimal pkgs.coreutils ]}
  '';
  meta.mainProgram = "drvgo-bench";
}
