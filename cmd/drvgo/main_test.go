package main

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestForwardSymlink(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	target := filepath.Join(dir, "target")
	forward := func(target string) error { return run([]string{"internal-symlink", target, out}) }

	if err := forward(target); err != nil {
		t.Fatal(err)
	}
	if err := forward(target); err != nil {
		t.Fatalf("retrying the same link: %v", err)
	}
	if err := forward(target + "-other"); !errors.Is(err, os.ErrExist) {
		t.Fatalf("conflicting link: got %v, want file exists", err)
	}
	if got, err := os.Readlink(out); err != nil || got != target {
		t.Fatalf("forwarded link = %q, %v; want %q", got, err, target)
	}
	if err := os.Remove(out); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, []byte("existing output"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := forward(target); !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing regular file: got %v, want file exists", err)
	}
	if data, err := os.ReadFile(out); err != nil || string(data) != "existing output" {
		t.Fatalf("existing output changed: %q, %v", data, err)
	}
}

func TestPackageArguments(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		execute bool
		pkg     string
		forward []string
	}{
		{nil, true, ".", nil},
		{[]string{"-v", "--", "-h"}, true, ".", []string{"-h"}},
		{[]string{"-tags", "--", "./cmd/tool", "--", "-h"}, true, "./cmd/tool", []string{"-h"}},
		{[]string{"--tags=fast", "--", "two words"}, true, ".", []string{"two words"}},
		{[]string{"./cmd/tool", "a", "--", "b"}, true, "./cmd/tool", []string{"a", "--", "b"}},
		{[]string{"--", "./cmd/tool"}, false, "./cmd/tool", []string{}},
	} {
		f := flag.NewFlagSet("test", flag.ContinueOnError)
		f.Bool("v", false, "")
		f.String("tags", "", "")
		pkg, forwarded, err := parsePackage(f, tc.args, tc.execute)
		if err != nil || pkg != tc.pkg || !slices.Equal(forwarded, tc.forward) {
			t.Errorf("%q: got package %q, arguments %q, error %v", tc.args, pkg, forwarded, err)
		}
	}
}
