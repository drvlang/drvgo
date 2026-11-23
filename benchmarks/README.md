# Go benchmarks

Run `nix run .#bench -- --help` from the repository root to see the Rust benchmark suite. Each subcommand lists its inputs, trial count, and report options with `--help`.

```sh
nix run .#bench -- ci --before ../drvgo-before --after . --source /path/to/go/project --runs 7
```

Available workloads include `bootstrap`, `build`, `ci`, `client`, `closure`, `evaluation`, `export`, `export-data`, `pack`, `planner-closure`, `relocation`, and `source-edit`. Comparisons accept explicit before/after inputs so the compiler, projects, and cache conditions can be held constant. The dated files here contain the measurements and workloads used for earlier releases.
