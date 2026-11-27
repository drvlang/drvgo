//go:build integration

package tests

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// Start with a verified directory listing, as on a runner that substituted
// only the Go checksum manifest. The member bytes must be new to the store.
func TestRecoverLockedArchive(t *testing.T) {
	dir := t.TempDir()
	var data bytes.Buffer
	archive := zip.NewWriter(&data)
	var manifest strings.Builder
	var members []map[string]string
	for i := 0; i < 16; i++ {
		name := fmt.Sprintf("example.org/probe@v1.0.0/file%02d", i)
		content := fmt.Sprintf("%s %d\n", dir, i/2) // Two names for each payload.
		w, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
		fmt.Fprintf(&manifest, "%s  %s\n", hash, name)
		members = append(members, map[string]string{"name": name, "hash": hash})
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Write(data.Bytes())
	}))
	defer server.Close()
	hash := sha256.Sum256([]byte(manifest.String()))
	writeJSON := func(name string, v any) {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0644); err != nil {
			t.Fatal(err)
		}
	}
	const rel = "example.org/probe/@v/v1.0.0"
	artifact := map[string]string{
		"proxyPath": rel, "version": "v1.0.0", "kind": "zip", "url": server.URL,
		"checksum": "h1:" + base64.StdEncoding.EncodeToString(hash[:]),
	}
	writeJSON("entries", []map[string]string{artifact, artifact})
	writeJSON("members", [][]map[string]string{members, members})

	cmd := exec.Command("nix", "build", "--impure", "--no-link", "--print-out-paths",
		"--option", "allow-import-from-derivation", "false", "--option", "builders", "", "--max-jobs", "4",
		"--argstr", "data", dir, "--expr", `
		{ data }: let f = builtins.getFlake (toString ./.); system = builtins.currentSystem;
		    pkgs = f.inputs.nixpkgs.legacyPackages.${system};
		in import ./nix/module-members.nix {
		  fetch = import ./nix/module-manifest.nix {
		    inherit system; inherit (pkgs) cacert nix;
		    drvgo = f.packages.${system}.drvgo.worker;
		    impureEnvVars = pkgs.lib.fetchers.proxyImpureEnvVars;
		  };
		  entries = data + "/entries"; members = data + "/members";
		}`)
	cmd.Dir = ".."
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	result, err := cmd.Output()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, stderr.Bytes())
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("recovered one archive with %d HTTP requests, want 1", got)
	}
	path := filepath.Join(strings.TrimSpace(string(result)), rel+".zip")
	if _, err := os.Readlink(path); err != nil {
		t.Fatalf("expected proxy to reference its cached archive: %v", err)
	}
	z, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	if len(z.File) != len(members) {
		t.Fatalf("archive has %d members, want %d", len(z.File), len(members))
	}
}
