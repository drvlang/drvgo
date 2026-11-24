{
  description = "drvgo: package-granular Go builds with Nix";

  # Keep this aligned with the standalone export's Go 1.26.5 toolchain.
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/6b5e5b7a6631f065bf6908986990b37d845f847f";

  inputs.drvkit = { url = "github:drvlang/drvkit"; flake = false; };
  outputs = { self, nixpkgs, drvkit }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "aarch64-darwin" ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
      ca = { __contentAddressed = true; outputHashMode = "recursive"; outputHashAlgo = "sha256"; };
      packageSets = forAllSystems (system: import nixpkgs { inherit system; });
      commandFlake = { commands, tests ? commands }: {
        packages = forAllSystems (system:
          builtins.mapAttrs (_: args: self.lib.mkGoPackage (args // { inherit system; })) commands);
        checks = forAllSystems (system:
          builtins.mapAttrs (_: args: self.lib.mkTest (args // { inherit system; })) tests);
        apps = forAllSystems (system:
          builtins.mapAttrs (_: args: self.lib.mkApp (args // { inherit system; })) commands);
      };
    in {
      packages = forAllSystems (system: rec {
        drvgo = import ./nix/package.nix { pkgs = packageSets.${system}; };
        default = drvgo;
        bench = import ./benchmarks/package.nix { pkgs = packageSets.${system}; };
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
        inherit (self.packages.${system}) bench;
      });
      # Publish the same project as the default package on each supported system.
      lib.mkFlake = args: commandFlake { commands.default = args; };
      lib.mkMonorepoFlake = args:
        let
          # Accept flakes written when module-wide checks required an opt-in.
          project = removeAttrs args [ "moduleTests" ];
          commands = import ./nix/monorepo.nix nixpkgs.lib project;
        in commandFlake {
          inherit commands;
          tests = import ./nix/module-tests.nix nixpkgs.lib project commands;
        };
      lib.mkToolchain = { system, go ? packageSets.${system}.go }:
        import ./internal/export/templates/toolchain-builder.nix {
          inherit nixpkgs;
          pkgs = packageSets.${system};
          go = if builtins.isString go
            then import ./internal/export/templates/go-version.nix packageSets.${system} go
            else go;
        };
      lib.projectSource = import (drvkit + "/project-source.nix") nixpkgs.lib;
      lib.mkPackage = self.lib.mkGoPackage;
      lib.mkApp = { system, ... }@args: let package = self.lib.mkGoPackage args; in {
        type = "app";
        program = toString ((packageSets.${system}.writeShellScript "drvgo-run" ''
          if [[ -f ${package}/nix-support/no-tests ]]; then exit 0; fi
          shopt -s dotglob
          exec ${package}/bin/* "$@"
        '').overrideAttrs (_: ca // { allowSubstitutes = true; }));
      };
      lib.mkTest = { system, src, package ? ".", target ? package, ... }@args:
        let pkgs = packageSets.${system};
            tests = self.lib.mkGoPackage (args // { test = true; allowEmptyTests = true; });
        in pkgs.runCommand "drvgo-tests" ca ''
          if [[ ! -f ${tests}/nix-support/no-tests ]]; then
            cp -r ${tests.src} project
            chmod -R u+w project
            cd project/${pkgs.lib.escapeShellArg tests.package}
            "${tests}"/bin/*
          fi
          mkdir "$out"
        '';
      lib.mkGoPackage = { system, src, package ? ".", target ? package, go ? packageSets.${system}.go, toolchain ? null, ... }@args:
        let pkgs = packageSets.${system};
            commands = import ./nix/monorepo.nix nixpkgs.lib { inherit src; };
            selected = if target != "." && !nixpkgs.lib.hasPrefix "./" target && commands ? ${target}
              then commands.${target}.package
              else if target != "." && !nixpkgs.lib.hasPrefix "./" target && builtins.pathExists (toString src + "/${target}")
                then "./${target}" else target;
            cleaned = if pkgs.lib.isDerivation src then src else pkgs.lib.cleanSourceWith {
              name = "go-source"; inherit src;
              filter = path: type: let n = baseNameOf path; in
                !(builtins.elem n [ ".git" ".drvgo" ".devenv" ".direnv" ])
                && !(type == "symlink" && (n == "result" || pkgs.lib.hasPrefix "result-" n));
            };
        in (
        (import ./nix/dynamic.nix {
          toolchain = if toolchain == null then self.lib.mkToolchain { inherit system go; } else toolchain;
          drvgo = self.packages.${system}.drvgo;
          inherit drvkit;
        }) ((builtins.removeAttrs args [ "system" "go" "target" "toolchain" ]) // {
          src = cleaned; package = selected;
        })) // { package = selected; src = cleaned; };
    };
}
