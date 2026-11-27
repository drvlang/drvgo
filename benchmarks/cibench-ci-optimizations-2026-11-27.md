# Optimizing drvgo for CI

Across all seven CIBench CI cases, optimizing the worker build, toolchain packaging, and direct Nix daemon planners reduced cold builds from 84.36 s to 21.61 s (3.90×), remote cache restores from 13.20 s / 407.3 MiB to 3.85 s / 214.7 MiB (3.42× faster, 47% less traffic), remote edits from 17.68 s to 5.21 s (1.95× faster than Nixpkgs `buildGoModule`), local edits from 4.74 s to 2.37 s (2.93× faster than `buildGoModule`), remote dependency updates from 57.85 s to 5.39 s (1.83× faster than `buildGoModule`), and local dependency updates from 43.59 s to 3.26 s (2.20× faster than `buildGoModule`).

CIBench builds a UUID client and checks the output of its installed executable. The source edit changes the expected output. The independent dependency update changes `github.com/google/uuid` from v1.5.0 to v1.6.0 using the consumer's `go.mod` and `go.sum`.

| CIBench case | drvgo `f003540` (s) | drvgo optimized (s) | buildGoModule (s) | drvgo cache transfer (before → after) | drvgo builds (before → after) |
| --- | ---: | ---: | ---: | ---: | ---: |
| cold | 84.36 | 21.61 | 8.30 | 0.0 → 0.0 MiB | 139 → 136 |
| remote-warm | 13.20 | 3.85 | 1.10 | 407.3 → 214.7 MiB | 0 → 0 |
| local-warm | 1.23 | 0.79 | 0.74 | 0.0 → 0.0 MiB | 0 → 0 |
| edit-remote | 17.68 | 5.21 | 10.18 | 403.2 → 210.6 MiB | 5 → 5 |
| edit-local | 4.74 | 2.37 | 6.94 | 0.0 → 0.0 MiB | 5 → 5 |
| dependency-remote | 57.85 | 5.39 | 9.86 | 403.2 → 210.3 MiB | 14 → 13 |
| dependency-local | 43.59 | 3.26 | 7.19 | 0.0 → 0.0 MiB | 14 → 13 |

Times are medians of two trials for `f003540`, three trials for the optimized tree, and five trials for the shared Nixpkgs `buildGoModule` comparator, run in alternating order (`before-1`, `after-1` ×2, `before-2`, `after-2`) with no competing host workloads. Both builders used Go 1.26.5. Each sample timed evaluation and realization of the package and check together. [Individual timings, build counts, cache traffic and realized outputs](cibench-ci-optimizations-2026-11-27.csv)

## Summary of optimizations

1. **Direct Nix daemon store writer and native ATerm planners (`internal/nixstore/write.go`, `internal/modcache/plan_proxy.go`, `internal/export/plan_direct.go`)**: Replaced `nix-instantiate` and `nix-store --add-fixed` inside the proxy and Go planners with direct protocol-1.21 Nix daemon registration (`wopQueryValidPaths`, `wopAddToStore`, `wopAddTextToStore`) over a persistent concurrent connection pool (`RegisterGraph`). This removed `pkgs.nix` (~35 MiB / ~50 store paths) from runtime closures, eliminated evaluator startup overhead, avoided Nixpkgs evaluation inside the proxy planner, and avoided orphan forwarding `.drv` registrations.
2. **Reduced recursive sandbox mount overhead (`nix/dynamic.nix`)**: Passing `.drv` recipes via `unsafeDiscardOutputDependency` bind-mounted ~850 transitive bootstrap `.drv` files into the recursive sandbox, slowing every daemon store registration on Linux. Passing the compact realized worker bundles directly cut planner sandbox mounts to ~50 and allowed `go-libnix-helpers` to compile in parallel with `drvgo-worker` on cold builds.
3. **Eliminated duplicate GCC copy and refined static toolchain slices (`internal/export/templates/toolchain-builder.nix`, `nix/planning-toolchain.nix`, `nix/dynamic.nix`)**: Removed the 264 MiB `workerCC` copy-and-sed of `gcc-15.3.0` (~81 MiB compressed download on every remote CI job). Made pinned static toolchain slices (`tools`, `helpers`, `compile`, `link`, `internalLink`, `native`, `planning-toolchain`, `testSelector`) input-addressed with `allowSubstitutes = true` on `symlinkJoin` while keeping all drvlang-generated packages, compiled units, dependency assemblies, checks, planners, and forwarding derivations floating content-addressed. Passing `compactPlanningToolchain` to `module-cache.nix` also removed `go-1.26.5` (65 MiB) from `drvgo-module-cache`'s inputs.
4. **Fast pure-Go worker build and shared Nixpkgs evaluation (`nix/package.nix`, `flake.nix`)**: Built `drvgo-worker` directly with `pkgs.go` (`CGO_ENABLED=0`, `-gcflags='all=-dwarf=false -l'`, `-ldflags='-s -w'`, `disallowedReferences = [ pkgs.go pkgs.bash ]`), moving `go test ./... && go vet ./...` into `checks.<system>.drvgo-tests`. Switched `flake.nix` from re-importing Nixpkgs to `nixpkgs.legacyPackages.${system}`.
5. **Fast-path store-reference scanning (`internal/export/prepare_sources.go`, `internal/export/references.go`)**: Guarded `regexp` store-reference scans with `bytes.Contains` / `strings.Contains` for `/nix/store/`.

## Correctness checks

- Realized package and check outputs were deterministic across all three optimized trials and matched between `cold`, `remote-warm`, and `local-warm`, between `edit-remote` and `edit-local`, and between `dependency-remote` and `dependency-local`.
- `gofmt -l`, `CGO_ENABLED=0 go test ./...`, and `CGO_ENABLED=0 go vet ./...` passed.
- `nix flake show --all-systems` (`x86_64-linux`, `aarch64-linux`, `aarch64-darwin`) and `nix flake check -L` (`drvgo`, `bench`, `drvgo-tests`) passed.
- `tests/locked-project.sh` passed (native `go.sum` updates, target selection, source filtering, archive recovery, checksum failure, and real test failures).
- Full `tests/compatibility.py --flake-toolchain toolchain-go1_26` passed all 38 differential, integration, monorepo, module-cache, forwarding, native-store-queries, previous-plan, compact-discovery, export-data, internal-link, cgo-disabled, race, C++, native-object, and incremental checks with IFD disabled.

## Reproduction

The runner used x86_64 Linux, an Intel Core i7-1280P, Nix 2.35.3, four logical CPUs (16–19), four jobs, four cores per builder, 8 GiB RAM and zero swap. Sandboxing was enabled and IFD disabled. The simulated project cache used 5 ms latency and 100 MiB/s aggregate bandwidth. Both builders used the same local mirror of signed NixOS packages populated from `drvkit/cibench/results/bootstrap/go.txt`. Cold jobs started with an empty local store and project cache. Subsequent cases independently used the original job's local store or its published project cache. The recorded runner was `/nix/store/vpysz8ih9lhm154cjaamrncwjn08njp6-cibench-0.1.0`.

```sh
work=$(mktemp -d)
stock="file://$work/stock"
nix copy --from https://cache.nixos.org --to "$stock?compression=zstd&compression-level=1" \
  --stdin < ../drvkit/cibench/results/bootstrap/go.txt
runner="$(nix build --impure --no-link --print-out-paths -f ../drvkit/cibench)/bin/cibench"
mkdir "$work/before"
git archive f003540 | tar -x -C "$work/before"
run() {
  taskset -c 16-19 "$runner" go --builder "$1" --out "$work/$2" --trials "$3" \
    --cpus 4 --memory-mib 8192 --latency-ms 5 --bandwidth-mib-s 100 \
    --upstream "$stock?priority=40"
}
run "$work/before" before-1 1
run "$PWD" after-1 2
run "$work/before" before-2 1
run "$PWD" after-2 1
```
