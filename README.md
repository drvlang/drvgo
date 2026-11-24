# drvgo

drvgo builds each Go package as a separate Nix derivation. When you change a package, CI rebuilds it and its dependents. Fresh runners and other commands can reuse unchanged dependencies, including the standard library, from your Nix binary cache.

Package archives, compiler interfaces, linked programs, and test outputs use content addressing by default. When a private implementation edit leaves the compiler interface unchanged, downstream packages can reuse their compilations. Nix relinks the program with the updated implementation. Commands and checks also share the module cache prepared from `go.sum`. Publish the generated plans and build traces so fresh workers can reuse them. [CI cache guide](https://drvlang.org/monorepos/)

## Try it

From a Go module with a main package, use Nix with flakes on Linux (amd64 or arm64) or Apple Silicon macOS. drvgo supplies the Go toolchain.

```sh
nix shell github:drvlang/drvgo
drvgo build                     # executable in result/bin/
drvgo run -- --help             # pass --help to your program
```

Each command accepts one package, defaulting to `.`. Put build flags before the package.

```sh
drvgo build -go 1.27.4 ./cmd/app
drvgo test -v ./pkg/example
drvgo export -o build-nix ./cmd/app
nix-build build-nix -A default
```

The export includes the sources and can be built on its own. Use `drvgo help build` for options such as build tags, linker flags, and the race detector.

## Build from a flake

Add this `flake.nix` to your Go project. In a Git repository, stage it and the Go sources so Nix sees them.

```nix
{
  inputs.drvgo.url = "github:drvlang/drvgo";
  outputs = { drvgo, ... }: drvgo.lib.mkFlake {
    src = drvgo.lib.projectSource {
      root = ./.;
      files = [ ./go.mod ./main.go ];
    };
  };
}
```

The example builds a module with its program in `main.go`. Include the directories and files your program and tests read, including `./go.sum` for downloaded dependencies, test files, embedded assets, and native sources. A self-contained module in a subdirectory can use `src = ./backend;`.

The CLI and flake builders use dynamic derivations. Enable these settings in the client and daemon’s `nix.conf`.

```ini
extra-experimental-features = ca-derivations dynamic-derivations recursive-nix
extra-system-features = recursive-nix ca-derivations
```

Use `nix build` to build, or `nix run . -- --help` to pass an argument to the command. For a command in a subdirectory, add `package = "./cmd/app";`. Set `go = "1.27.4";` to select a compiler. Both interfaces default to Go 1.26.5 and also support 1.25.12, 1.26.6, and 1.27.4.

To add a single package to existing flake outputs, pass the same source to `drvgo.lib.mkPackage { inherit system src; }`.

Downloaded dependencies are verified directly against `go.sum`. Update `go.mod` and `go.sum` with Go, then run the same build command. Vendored modules also work. drvgo caches each verified module ZIP separately, so commands and projects can share it. A first build from `go.sum` can still generate a recovery recipe for each distinct file in the dependency archives.

When comparing drvgo with an existing Nixpkgs `buildGoModule` package that uses `proxyVendor = true;`, both can use the same downloads. Pass `moduleProxy = "file://${existingPackage.goModules}";` and `prepareModules = true;` to `mkPackage`. This skips the file recovery graph and shares one extracted module cache across commands.

### Multiple commands in one Go module

Use `drvgo.lib.mkMonorepoFlake` to expose a package and app for each command. `nix flake check` automatically checks every Go package in the supplied module source, including directories containing only `_test.go` files.

```nix
drvgo.lib.mkMonorepoFlake {
  src = drvgo.lib.projectSource {
    root = ./.;
    files = [ ./go.mod ./go.sum ./cmd ./pkg ];
  };
}
```

```sh
nix build .#api
nix run .#worker -- --help
nix flake check                 # check commands and libraries across the module
nix build .#checks.x86_64-linux.module-tests/pkg/parser  # check pkg/parser alone
```

Commands are discovered in immediate `app/<name>/` directories containing a non-test Go source file, falling back to `cmd/<name>/`. If these directories also contain libraries, identify the programs with `commands = [ "api" "worker" ];`. For custom locations or names, use a map such as `commands.api = "tools/server";`, or put the map under `commands` in a `drvlang.json` included in your source.

Command checks use their command names. Other checks use `module-tests/<directory>`, or `module-tests` for the module root. Discovery skips nested modules, `vendor`, `testdata`, and hidden or underscore-prefixed directories. Go applies build constraints and module ignore rules at build time, so excluded candidates can appear as empty checks.

For per-target source filters, set `srcForTarget = { name, target }: ...;` to return a `projectSource` containing the root manifests, that package, its dependencies, and its test data. The callback receives the command or check name and the module-relative package directory. With an explicit command map and no module-wide `src`, drvgo discovers packages across the supplied command sources. Include standalone test directories in `src` if they fall outside every command’s source filter.

[CC0-1.0 license](LICENSE)
