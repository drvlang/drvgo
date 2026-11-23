# Go benchmarks

Run `nix run .#bench -- --help` from the repository root to see the Rust benchmark suite. Each subcommand lists its inputs, trial count, and report options with `--help`.

```sh
nix run .#bench -- ci --before ../drvgo-before --after . --source /path/to/go/project --runs 7
```

Available workloads include `bootstrap`, `build`, `ci`, `client`, `closure`, `evaluation`, `export`, `export-data`, `pack`, `planner-closure`, `relocation`, and `source-edit`. Comparisons accept explicit before/after inputs so the compiler, projects, and cache conditions can be held constant. The dated files here contain the measurements and workloads used for earlier releases.

## Reusing locked modules and plans on CI

On replit/upm at `35a76c282507fb942445c0ee98427b3d1ef772bc`, the `cmd/upm` build plans 554 Go units from 416 `go.sum` entries. Five alternating trials with fresh planner names and warm package outputs took a median 9.27 s on drvgo `346d85d` and 6.15 s with the prepared module cache and batched hash-manifest reader. Both produced the same executable store path, and `upm list-languages` ran successfully. The run used Nix 2.35.3 on x86_64-linux with four jobs, two cores per builder, strict sandboxing, substitutions disabled, and one excluded warmup. These timings measure steady-state CI. Preparing an uncached lockfile extracts all its signed module archives once.

```sh
nix run .#bench -- ci --before ../drvgo-before --after . \
  --source ../upm --package ./cmd/upm --runs 5 \
  --nix-option sandbox true --nix-option sandbox-fallback false \
  --nix-option substituters '' --nix-option always-allow-substitutes false
```

An independent signed-cache check restored the fixture's generated plan, compiler inputs, and executable on a fresh store with zero builders, IFD disabled, and Nix's normal substitution eligibility.
