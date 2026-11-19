package main

import (
	"flag"
	"slices"
	"testing"
)

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
