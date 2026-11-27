# Go benchmarks

Run `nix run .#bench -- --help` from the repository root to see the Rust benchmark suite. Each subcommand lists its inputs, trial count, and report options with `--help`.

```sh
nix run .#bench -- ci --before ../drvgo-before --after . --source /path/to/go/project --runs 7
```

Available workloads include `bootstrap`, `build`, `ci`, `client`, `closure`, `evaluation`, `export`, `export-data`, `pack`, `planner-closure`, `relocation`, and `source-edit`. Comparisons accept explicit before/after inputs so the compiler, projects, and cache conditions can be held constant. The dated files here contain the measurements and workloads used for earlier releases.

## Reusing locked modules and plans on CI

On replit/upm at `35a76c282507fb942445c0ee98427b3d1ef772bc`, the `cmd/upm` build plans 554 Go units from 416 `go.sum` entries. Seven alternating trials with fresh planner names and warm package outputs took a median **3.72 s** on drvgo `346d85d` and **2.97 s** on `9451d4e` with the prepared module cache and the lock's content-addressed member index. Both produced the same executable store path. The public package and upstream `internal/pkg` test check also built together, and the executable passed `upm list-languages`.

The run used Nix 2.35.3 on x86_64-linux, four jobs, two cores per builder, strict sandboxing, substitutions disabled, and one excluded warmup. The first run with the new, unpublished index and cache took 171 s on this host. The index retains all 35,098 distinct store files authenticated by 41,320 fixed-output recipes. Source-only revisions reuse the index and extracted modules. A fresh runner can restore them from a signed cache.

```sh
nix run .#bench -- ci --before ../drvgo-before --after . \
  --source ../upm --package ./cmd/upm --runs 7 \
  --nix-option sandbox true --nix-option sandbox-fallback false \
  --nix-option substituters '' --nix-option always-allow-substitutes false
```

An independent signed-cache check restored a UUID-backed command and its upstream-style test from an empty store seeded with their recipes. Both outputs retained their content addresses with zero builders, IFD disabled, and Nix's normal substitution eligibility.

## Resource-controlled CI comparison

With the sibling drvkit checkout, run the same UUID client and output check under the current drvgo checkout and Nixpkgs’ `buildGoModule`.

```sh
make cibench CIBENCH_OUT="$(mktemp -d)" \
  CIBENCH_ARGS='--cpus 4 --memory-mib 8192 --latency-ms 5 --bandwidth-mib-s 100 --upstream https://cache.nixos.org'
```

Omit `--upstream` for an empty-store run that also builds missing Nixpkgs toolchains from source. CIBench writes each timing, cache transfer, and build log to the output directory. The shared harness documents all seven cases, prerequisites and language comparisons. [CIBench in drvkit](https://github.com/drvlang/drvkit/tree/main/cibench)

For before-and-after timings against `buildGoModule` across all seven cases, see the [CI optimization comparison](cibench-ci-optimizations-2026-11-27.md) and the earlier [Go module planner comparison](cibench-module-proxy-2026-11-24.md).
