{ pkgs }:
let
  cliSource = pkgs.lib.fileset.toSource {
    root = ../.;
    fileset = pkgs.lib.fileset.unions [ ../flake.nix ../flake.lock ../go.mod ../cmd ../internal ./. ];
  };
  workerSource = pkgs.lib.fileset.toSource {
    root = ../.;
    fileset = pkgs.lib.fileset.unions [ ../go.mod ../cmd ../internal ];
  };
  version = "0.1.0";
  meta = {
    description = "Package-granular Go builds with Nix";
    homepage = "https://github.com/drvlang/drvgo";
    license = pkgs.lib.licenses.cc0;
    platforms = [ "x86_64-linux" "aarch64-linux" "aarch64-darwin" ];
    mainProgram = "drvgo";
  };
  # Build the pure-Go worker directly with pkgs.go so CI workers never need
  # stdenv, GCC, or unit-test execution to build or substitute it.
  worker = builtins.derivation {
    name = "drvgo-worker-${version}";
    system = pkgs.stdenv.hostPlatform.system;
    builder = pkgs.runtimeShell;
    args = [ "-eu" "-c" ''
      export HOME="$TMPDIR" GOCACHE="$TMPDIR/cache" GOPATH="$TMPDIR/go"
      export CGO_ENABLED=0 GOTOOLCHAIN=local GOENV=off GOFLAGS=
      exec ${pkgs.go}/bin/go build -C ${workerSource} \
        -p "$NIX_BUILD_CORES" -trimpath -gcflags='all=-dwarf=false -l' -ldflags='-s -w' \
        -o "$out/bin/drvgo" ./cmd/drvgo
    '' ];
    disallowedReferences = [ pkgs.go pkgs.bash ];
  } // { inherit version meta; };
  tests = pkgs.runCommand "drvgo-tests-${version}" {
    __contentAddressed = true; outputHashMode = "recursive"; outputHashAlgo = "sha256";
  } ''
    export HOME="$TMPDIR" GOCACHE="$TMPDIR/cache" GOPATH="$TMPDIR/go"
    export CGO_ENABLED=0 GOTOOLCHAIN=local GOENV=off GOFLAGS='-gcflags=all=-dwarf=false'
    cp -r ${workerSource} src
    chmod -R u+w src
    cd src
    ${pkgs.go}/bin/go test -p "$NIX_BUILD_CORES" ./...
    ${pkgs.go}/bin/go vet ./...
    mkdir "$out"
  '';
in pkgs.runCommand "drvgo-${version}" {
  __contentAddressed = true;
  nativeBuildInputs = [ pkgs.makeWrapper ];
  inherit meta;
  passthru = { pname = "drvgo"; inherit version worker tests; };
} ''
  mkdir -p "$out/bin"
  makeWrapper ${worker}/bin/drvgo "$out/bin/drvgo" --inherit-argv0 --set DRVGO_PROJECT_FLAKE 'path:${cliSource}'
''
