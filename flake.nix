{
  description = "drvgo: package-granular Go builds with Nix";

  # Keep this aligned with the standalone export's Go 1.26.5 toolchain.
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/6b5e5b7a6631f065bf6908986990b37d845f847f";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "aarch64-darwin" ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
      packageSets = forAllSystems (system: import nixpkgs { inherit system; });
    in {
      packages = forAllSystems (system: rec {
        drvgo = import ./nix/package.nix { pkgs = packageSets.${system}; };
        default = drvgo;
        toolchain-go1_25 = self.lib.mkToolchain { inherit system; go = "1.25.12"; };
        toolchain-go1_26 = self.lib.mkToolchain { inherit system; };
        toolchain-go1_26_6 = self.lib.mkToolchain { inherit system; go = "1.26.6"; };
        toolchain-go1_27 = self.lib.mkToolchain { inherit system; go = "1.27.4"; };
      });
      apps = forAllSystems (system: rec {
        drvgo = {
          type = "app";
          program = "${self.packages.${system}.drvgo}/bin/drvgo";
          meta.description = "Package-granular Go builds with Nix";
        };
        default = drvgo;
      });
      checks = forAllSystems (system: {
        drvgo = self.packages.${system}.drvgo;
      });
      # Publish the same project as the default package on each supported system.
      lib.mkFlake = args: rec {
        packages = forAllSystems (system: {
          default = self.lib.mkGoPackage (args // { inherit system; });
        });
        apps = forAllSystems (system: let package = packages.${system}.default; in {
          default = {
            type = "app";
            # Go determines the command name during the build. Each result has one binary.
            program = toString (packageSets.${system}.writeShellScript "drvgo-run" ''
              if [[ -f ${package}/nix-support/no-tests ]]; then exit 0; fi
              shopt -s dotglob
              exec ${package}/bin/* "$@"
            '');
          };
        });
      };
      lib.mkToolchain = { system, go ? packageSets.${system}.go }:
        import ./internal/export/templates/toolchain-builder.nix {
          pkgs = packageSets.${system};
          go = if builtins.isString go
            then import ./internal/export/templates/go-version.nix packageSets.${system} go
            else go;
        };
      lib.mkGoPackage = { system, go ? packageSets.${system}.go, ... }@args:
        (import ./nix/dynamic.nix {
          toolchain = self.lib.mkToolchain { inherit system go; };
          drvgo = self.packages.${system}.drvgo;
        }) (builtins.removeAttrs args [ "system" "go" ]);
    };
}
