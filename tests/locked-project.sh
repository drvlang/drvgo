#!/usr/bin/env bash
set -euo pipefail
# Exercise the same store restrictions as a sandboxed remote builder.
export NIX_CONFIG="${NIX_CONFIG-}"$'\nsandbox = true\nallow-import-from-derivation = false'
repo=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cli=$(nix build "$repo#drvgo" --print-out-paths --no-link)/bin/drvgo
(cd "$repo" && go test -count=1 -tags=integration ./tests -run "^TestRecoverLockedArchive$")
cd "$work"
mkdir -p cmd/program pkg/shared
cat > drvlang.json <<'JSON'
{"commands":{"app":"cmd/program"}}
JSON
cat > pkg/shared/shared.go <<'GO'
package shared
import "github.com/google/uuid"
func Value() string { return uuid.Nil.String() }
GO
cat > cmd/program/main.go <<'GO'
package main
import ("fmt"; "example.org/check/pkg/shared")
func main(){ fmt.Println(shared.Value()) }
GO
cat > cmd/program/main_test.go <<'GO'
package main
import ("testing"; "example.org/check/pkg/shared")
func TestValue(t *testing.T){ if shared.Value() == "" { t.Fatal("empty UUID") } }
GO
cat > flake.nix <<'NIX'
{
  inputs.drvgo.url = "github:drvlang/drvgo";
  outputs = { drvgo, ... }: drvgo.lib.mkMonorepoFlake {
    src = drvgo.lib.projectSource {
      root = ./.; files = [ ./drvlang.json ./go.mod ./go.sum ./cmd ./pkg ];
    };
  };
}
NIX
opts=(--no-write-lock-file --override-input drvgo "$repo" --option allow-import-from-derivation false)
system=$(nix eval --raw --impure --expr builtins.currentSystem)
paths() {
  nix eval "${opts[@]}" --raw ".#packages.$system.app.drvPath"
  nix eval "${opts[@]}" --raw ".#checks.$system.app.drvPath"
}
previous_check=
for version in v1.5.0 v1.6.0; do
  cat > go.mod <<MOD
module example.org/check
go 1.25
require github.com/google/uuid $version
MOD
  go mod tidy
  test "$("$cli" run app)" = 00000000-0000-0000-0000-000000000000
  "$cli" test app -- -test.run TestValue
  checked=$(nix build "${opts[@]}" --no-link --print-out-paths ".#checks.$system.app")
  built=$(nix build "${opts[@]}" --no-link --print-out-paths .#app)
  for artifact in "$built" "$(readlink "$built")" "$checked"; do
    nix path-info --json "$artifact" | grep -q 'fixed:r:sha256:'
  done
  if [[ -n "$previous_check" ]]; then test "$previous_check" = "$checked"; fi
  previous_check=$checked
done
# Planning a checked-in lock only needs the resolved fetch tools and manifests.
proxy_planner=$(nix eval "${opts[@]}" --raw ".#packages.$system.app.modules.planner.drvPath")
nixpkgs_src=$(DRVGO_TEST_REPO="$repo" nix eval --impure --raw --expr '
  (builtins.getFlake (builtins.getEnv "DRVGO_TEST_REPO")).inputs.nixpkgs.outPath')
proxy_inputs=$(nix-store --query --references "$proxy_planner")
if grep -Fxq "$nixpkgs_src" <<< "$proxy_inputs"; then
  echo "proxy planner retained the full Nixpkgs source" >&2
  exit 1
fi
# A raw checkout must use the compiler's filtered source for the test runner too.
raw_check() {
  DRVGO_TEST_REPO="$repo" DRVGO_TEST_SRC="$work" nix eval --impure --raw --expr '
    let f = builtins.getFlake (builtins.getEnv "DRVGO_TEST_REPO"); in (f.lib.mkTest {
      system = builtins.currentSystem; src = /. + builtins.getEnv "DRVGO_TEST_SRC";
      package = "./cmd/program";
    }).drvPath'
}
before=$(raw_check)
mkdir -p .drvgo
echo 'local build artifact' > .drvgo/ignored
test "$(raw_check)" = "$before"
# A prepared file proxy can be passed either as a Nix input or as a file URL.
DRVGO_TEST_REPO="$repo" DRVGO_TEST_SRC="$work" nix eval --impure --json --expr '
  let f = builtins.getFlake (builtins.getEnv "DRVGO_TEST_REPO");
      src = /. + builtins.getEnv "DRVGO_TEST_SRC";
      proxy = builtins.path { path = src + "/pkg"; name = "test-module-proxy"; };
      selected = path: (f.lib.mkPackage {
        inherit src; system = builtins.currentSystem; package = "./cmd/program"; moduleProxy = "file://${path}";
      }).modules;
  in assert builtins.getContext (selected proxy) == builtins.getContext proxy;
     assert selected (builtins.unsafeDiscardStringContext proxy) == proxy;
     assert builtins.getContext (selected (builtins.unsafeDiscardStringContext proxy)) == builtins.getContext proxy;
     true'
# A generated source tree only reveals its go.sum inside the recursive planner.
generated=$(nix build --impure --no-link --print-out-paths --option allow-import-from-derivation false \
  --argstr source "$work" --argstr tool "$repo" --expr '
  { source, tool }:
  let
    flake = builtins.getFlake tool;
    system = builtins.currentSystem;
    pkgs = import flake.inputs.nixpkgs { inherit system; };
    root = /. + source;
    input = flake.lib.projectSource { inherit root; files = map (path: root + ("/" + path)) [ "go.mod" "go.sum" "cmd" "pkg" ]; };
    src = pkgs.runCommand "generated-go-source" {} "cp -r ${input} $out";
  in flake.lib.mkPackage { inherit system src; package = "./cmd/program"; }')
test "$("$generated/bin/program")" = 00000000-0000-0000-0000-000000000000
known_package=$(nix eval "${opts[@]}" --raw ".#packages.$system.app.drvPath")
known_check=$(nix eval "${opts[@]}" --raw ".#checks.$system.app.drvPath")
echo outside > notes.txt
test "$(paths)" = "$known_package$known_check"
echo '// source edit' >> pkg/shared/shared.go
test "$(nix eval "${opts[@]}" --raw ".#packages.$system.app.drvPath")" != "$known_package"
test "$(nix eval "${opts[@]}" --raw ".#checks.$system.app.drvPath")" != "$known_check"
awk '/src = drvgo.lib.projectSource/ {
  print "    commands.app = \"cmd/program\";"
  print "    srcForTarget = { name, target }: assert name == \"app\" && target == \"cmd/program\"; drvgo.lib.projectSource {"
  next
} { print }' flake.nix > per-target.nix
mv per-target.nix flake.nix
nix build "${opts[@]}" --no-link ".#checks.$system.app"
sed 's/shared.Value() == ""/shared.Value() != ""/' cmd/program/main_test.go > failing.go
mv failing.go cmd/program/main_test.go
if "$cli" test app; then echo "expected CLI test failure" >&2; exit 1; fi
if nix build "${opts[@]}" --no-link ".#checks.$system.app"; then echo "expected flake test failure" >&2; exit 1; fi
echo 'PASS: native module updates, target selection, source filtering and real test failures'
