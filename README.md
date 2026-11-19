# drvgo

drvgo builds each Go package as a separate Nix derivation. When you change a package, CI rebuilds it and its dependents. Fresh runners and other commands can reuse unchanged dependencies, including the standard library, from your Nix binary cache.

## Try it

From a Go module with a main package, use Nix with flakes on Linux (amd64 or arm64) or Apple Silicon macOS. drvgo supplies the Go toolchain.

```sh
nix shell github:ryantm/drvgo
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
  inputs.drvgo.url = "github:ryantm/drvgo";
  outputs = { self, drvgo }: drvgo.lib.mkFlake {
    src = self;
  };
}
```

The CLI works with ordinary flakes. For `mkFlake`, enable these additional settings in the client and daemon’s `nix.conf`.

```ini
extra-experimental-features = ca-derivations dynamic-derivations recursive-nix
extra-system-features = recursive-nix ca-derivations
```

Use `nix build` to build, or `nix run . -- --help` to pass an argument to the command. For a command in a subdirectory, add `package = "./cmd/app";`. Set `go = "1.27.4";` to select a compiler. Both interfaces default to Go 1.26.5 and also support 1.25.12, 1.26.6, and 1.27.4.

To add a single package to existing flake outputs, use `drvgo.lib.mkGoPackage { inherit system; src = self; }`.

For downloaded dependencies, set `vendorHash = "";`, build once, then use the hash Nix reports. Vendored and standard-library-only projects can omit it.

[CC0-1.0 license](LICENSE)
