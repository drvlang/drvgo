#!/usr/bin/env bash
set -euo pipefail
repo=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cli=$(nix build "$repo#drvgo" --print-out-paths --no-link)/bin/drvgo
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
for version in v1.5.0 v1.6.0; do
  cat > go.mod <<MOD
module example.org/check
go 1.25
require github.com/google/uuid $version
MOD
  go mod tidy
  test "$("$cli" run app)" = 00000000-0000-0000-0000-000000000000
  "$cli" test app -- -test.run TestValue
  nix build "${opts[@]}" --no-link ".#checks.$system.app"
done
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
