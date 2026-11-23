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

Downloaded dependencies are verified directly against `go.sum`. Update `go.mod` and `go.sum` with Go, then run the same build command. Vendored modules also work.

### Multiple commands in one Go module

Use `drvgo.lib.mkMonorepoFlake` with a source list such as `[ ./go.mod ./go.sum ./cmd ./pkg ]`. Add `./drvlang.json` when using a target map. It discovers commands in immediate `app/<name>/` directories containing a non-test Go source file. Non-Go projects can sit alongside Go code in both `app/` and `pkg/`. Each planner sees its own command and shared inputs such as `pkg/`, so editing a sibling command skips replanning. When `app/` is absent, discovery uses `cmd/<name>/`. For explicit targets, put `{"commands":{"api":"cmd/api"}}` in `drvlang.json` or pass the same `commands` map to the flake helper.

```sh
nix build .#api
nix run .#worker -- --help
nix flake check                 # run each command package’s tests
```

If `app/` also contains Go libraries, set `commands = [ "api" "worker" ];` to identify the programs explicitly. Other directories remain available as shared inputs.

For per-command source lists, set `srcForTarget = { name, target }: ...;` to return a `projectSource` containing the selected command, shared code and root manifests. With an explicit `commands` map, you can omit the project-wide `src`.

Set `moduleTests = true;` in `mkMonorepoFlake` to include library and test-only packages in `nix flake check`. This example checks the command in `app/fern` and the tests in `garden/soil_test.go`.

```nix
drvgo.lib.mkMonorepoFlake {
  moduleTests = true;
  src = drvgo.lib.projectSource { root = ./.; files = [ ./go.mod ./app ./garden ]; };
}
```

Run the garden tests alone with `nix build .#checks.x86_64-linux.module-tests/garden`. Commands keep their usual check names. Other package checks use `module-tests/<directory>`, or `module-tests` for the module root.

The option requires a module-wide `src` containing `go.mod`. Discovery skips nested modules, vendor, testdata, and hidden or underscore-prefixed directories. Go applies build constraints and module ignore rules at build time, so excluded candidates can appear as empty checks. With `srcForTarget`, each additional check passes its check name and module-relative directory to the callback.

[CC0-1.0 license](LICENSE)
