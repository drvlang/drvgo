package nixstore

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestStorePathsAndPlaceholders(t *testing.T) {
	if got, want := HashPlaceholder("out"), "/1rz4g4znpzjwh1xymhjpm42vipw92pr73vdgl6xs1hycac8kf2n9"; got != want {
		t.Fatalf("HashPlaceholder(out) = %q, want %q", got, want)
	}
	if got, want := HashPlaceholder("api"), "/1rnw55pzmbd4s9wf9m4h75sxs9zi3y2p7za3057njd4cavbgnv8l"; got != want {
		t.Fatalf("HashPlaceholder(api) = %q, want %q", got, want)
	}

	// Verify against values produced by nix-instantiate.
	dep := Derivation{
		Outputs: []DerivationOutput{
			{Name: "out", HashAlgo: "r:sha256"},
			{Name: "api", HashAlgo: "r:sha256"},
		},
		Platform: "x86_64-linux",
		Builder:  "/bin/sh",
		Env: map[string]string{
			"api":            HashPlaceholder("api"),
			"builder":        "/bin/sh",
			"name":           "go-dep",
			"out":            HashPlaceholder("out"),
			"outputHashAlgo": "sha256",
			"outputHashMode": "recursive",
			"outputs":        "out api",
			"system":         "x86_64-linux",
		},
	}
	depText := dep.Format()
	depPath := dep.Path("/nix/store", []byte(depText))
	if want := "/nix/store/djfyjbvc6bfwqnlywj18dp7v1sjd3s16-go-dep.drv"; depPath != want {
		t.Fatalf("depPath = %q, want %q\nATerm: %s", depPath, want, depText)
	}
	if got, want := DownstreamPlaceholder(depPath, "api"), "/1gp296rcj0j0mrxkmlm0dqkbisqb0cqphc44s0y2qwj5kjv35p9c"; got != want {
		t.Fatalf("DownstreamPlaceholder(dep, api) = %q, want %q", got, want)
	}
	if got, want := DownstreamPlaceholder(depPath, "out"), "/1ncfbkbav92lqly56xh3mnjiar19i41zlbd8mw37c1q1ml7rj7cb"; got != want {
		t.Fatalf("DownstreamPlaceholder(dep, out) = %q, want %q", got, want)
	}

	mid := Derivation{
		Outputs: []DerivationOutput{
			{Name: "out", HashAlgo: "r:sha256"},
			{Name: "api", HashAlgo: "r:sha256"},
		},
		InputDrvs: map[string][]string{depPath: {"api"}},
		Platform:  "x86_64-linux",
		Builder:   "/bin/sh",
		Env: map[string]string{
			"api":            HashPlaceholder("api"),
			"builder":        "/bin/sh",
			"depApi":         DownstreamPlaceholder(depPath, "api"),
			"name":           "go-mid",
			"out":            HashPlaceholder("out"),
			"outputHashAlgo": "sha256",
			"outputHashMode": "recursive",
			"outputs":        "out api",
			"system":         "x86_64-linux",
		},
	}
	midText := mid.Format()
	midPath := mid.Path("/nix/store", []byte(midText))
	if want := "/nix/store/qd8l7amm3nkd8isayb32254xacn3prv7-go-mid.drv"; midPath != want {
		t.Fatalf("midPath = %q, want %q\nATerm: %s", midPath, want, midText)
	}
	if got, want := DownstreamPlaceholder(midPath, "out"), "/15lrf27lm9q0g7j2mk7n2vm5463ak2wlwn6993anfj8czdiy0gj6"; got != want {
		t.Fatalf("DownstreamPlaceholder(mid, out) = %q, want %q", got, want)
	}

	flatSum := sha256.Sum256([]byte("batch flat file\n"))
	if got, want := MakeFixedOutputPath("/nix/store", "drvgo-module-file", false, hex.EncodeToString(flatSum[:])),
		"/nix/store/7ks8xnwlk9r5syynwrda8d341w4h8cj1-drvgo-module-file"; got != want {
		t.Fatalf("MakeFixedOutputPath(flat) = %q, want %q", got, want)
	}
}

func TestWritePathNARMatchesRegular(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "hello.txt")
	content := []byte("hello world\n")
	if err := os.WriteFile(file, content, 0644); err != nil {
		t.Fatal(err)
	}
	h1, h2 := sha256.New(), sha256.New()
	if err := WritePathNAR(h1, file); err != nil {
		t.Fatal(err)
	}
	if err := WriteRegularNAR(h2, content, false); err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(h1.Sum(nil)) != hex.EncodeToString(h2.Sum(nil)) {
		t.Fatalf("WritePathNAR and WriteRegularNAR differ")
	}
}

func TestBuildGraph(t *testing.T) {
	dirs := []DirItem{{StorePath: "src1"}}
	items := []TextItem{
		{StorePath: "top", Refs: []string{"mid1", "mid2", "external"}},
		{StorePath: "mid1", Refs: []string{"leaf", "src1"}},
		{StorePath: "mid2", Refs: []string{"leaf"}},
		{StorePath: "leaf", Refs: []string{"external"}},
	}
	gp, err := buildGraph(dirs, items)
	if err != nil {
		t.Fatal(err)
	}
	if len(gp.order) != 5 {
		t.Fatalf("unexpected order length: %v", gp.order)
	}
	pos := make(map[int]int, len(gp.order))
	for i, idx := range gp.order {
		pos[idx] = i
	}
	// Indices: 0=src1, 1=top, 2=mid1, 3=mid2, 4=leaf
	if !(pos[0] < pos[2] && pos[4] < pos[2] && pos[4] < pos[3] && pos[2] < pos[1] && pos[3] < pos[1]) {
		t.Fatalf("invalid topological order: %v", gp.order)
	}
	if _, err := buildGraph(nil, []TextItem{
		{StorePath: "a", Refs: []string{"b"}},
		{StorePath: "b", Refs: []string{"a"}},
	}); err == nil {
		t.Fatal("expected cycle error")
	}
}
