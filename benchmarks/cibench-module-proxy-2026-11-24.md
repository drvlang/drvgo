# Planning verified Go modules on CI

Planning module fetches with resolved tool paths reduced the local dependency update from 49.66 s to 7.74 s. Nixpkgs’ `buildGoModule` took 7.24 s.

CIBench builds a UUID client and checks the output of its installed executable. The source edit changes the expected output. The independent dependency update changes `github.com/google/uuid` from v1.5.0 to v1.6.0 using the consumer's `go.mod` and `go.sum`.

| CIBench case | drvgo before (s) | drvgo after (s) | buildGoModule (s) |
| --- | ---: | ---: | ---: |
| cold | 103.25 | 40.90 | 8.74 |
| remote-warm | 15.53 | 7.17 | 1.16 |
| local-warm | 1.34 | 1.31 | 0.76 |
| edit-remote | 19.92 | 11.49 | 10.39 |
| edit-local | 5.53 | 5.49 | 7.18 |
| dependency-remote | 72.52 | 13.09 | 10.06 |
| dependency-local | 49.66 | 7.74 | 7.24 |

Times are medians of two trials per drvgo revision and four for the shared Nixpkgs comparator. Both builders used Go 1.26.5. The baseline is `f003540`. The run order was baseline, two modified trials, then baseline again. Each sample timed evaluation and realization of the package and check together. [Individual timings, build counts, cache traffic and realized outputs](cibench-module-proxy-2026-11-24.csv)

The proxy planner previously imported Nixpkgs inside its recursive sandbox to construct module fetch recipes. The outer evaluator now supplies the worker, Nix and TLS certificate bundle paths plus the proxy environment variable names. For a checked-in lock, it can also write the manifest list at evaluation time because `go.sum` determines each fixed-output path. The planner declares the fetch recipes as direct inputs.

This removed the 202 MiB Nixpkgs source tree from the planner's direct inputs. The emitted UUID proxy recipe referenced 35 derivation files transitively, down from 1,708 (`nix-store -qR` on the realized `modules.planner` output).

The local dependency update built 13 recipes, down from 14. It ran the module fetch and assembly steps, the Go planner, the UUID library and application builds, and the output check. The source edit rebuilt the application without recompiling UUID. An unchanged remote restore still downloaded 407.5 MiB from the project cache for drvgo and 12.9 MiB for `buildGoModule`.

Both drvgo revisions produced the same installed package and check paths for each source variant. The original project also retained its module proxy and prepared module cache paths. Unit tests, vet, and `tests/locked-project.sh` passed, including generated sources and archive recovery. A modified checksum failed verification. On replit/upm at `35a76c282507fb942445c0ee98427b3d1ef772bc`, drvgo built the command and its upstream `internal/pkg` check together with IFD disabled. The installed command passed `upm list-languages`.

## Reproduction

The runner used x86_64 Linux, an Intel Core i7-1280P, Nix 2.35.3, four logical CPUs (16–19), four jobs, four cores per builder, 8 GiB RAM and zero swap. Sandboxing was enabled and IFD disabled. The simulated project cache used 5 ms latency and 100 MiB/s aggregate bandwidth. Both builders could use the same local mirror of signed NixOS packages. Cold jobs started with an empty local store and project cache. Subsequent cases independently used the original job's local store or its published project cache. Go proxy requests could access the network. Host-side correctness checks overlapped portions of the first baseline and modified trials.

Run from the modified drvgo checkout with the sibling drvkit checkout containing CIBench. The recorded runner was `/nix/store/vpysz8ih9lhm154cjaamrncwjn08njp6-cibench-0.1.0`.

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
run "$PWD" after 2
run "$work/before" before-2 1
```
