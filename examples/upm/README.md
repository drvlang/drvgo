# Building UPM with drvgo

This example builds Replit’s UPM command and its database generator. Nix reuses their common Go packages, runs the generator, and bundles the database with UPM.

Enable the flake build features, then run these commands from the repository root. [Project README](../../README.md)

```sh
nix-build examples/upm/dynamic.nix -A package
./result/bin/upm --version
```

The [build](dynamic.nix) pins the upstream source and module hash. The [packaging expression](package.nix) assembles the executable and database. Run `examples/upm/test.sh` to exercise the standalone CLI export.
