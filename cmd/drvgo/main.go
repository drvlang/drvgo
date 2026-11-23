package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	exporter "github.com/ryantm/drvgo/internal/export"
	"github.com/ryantm/drvgo/internal/modcache"
	"github.com/ryantm/drvgo/internal/nixstore"
	"github.com/ryantm/drvgo/internal/toolversion"
)

func main() {
	if toolversion.IsTool(os.Args[0]) {
		if err := toolversion.Print(os.Stdout, os.Args[0], os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "drvgo:", err)
			os.Exit(1)
		}
		return
	}
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "drvgo:", err)
		if e, ok := err.(*exec.ExitError); ok && e.ExitCode() > 0 {
			os.Exit(e.ExitCode())
		}
		os.Exit(1)
	}
}

const usage = `Usage: drvgo <command> [options] [package]

  build    Build a command into result/bin/
  run      Build and run a command
  test     Build and run one package's tests
  export   Write a standalone Nix build to .drvgo/

The package defaults to the current directory. Use -- before program arguments.
Run drvgo <command> -h for options.
`

func run(args []string) error {
	if (len(args) == 5 || len(args) == 6) && args[0] == "internal-module-fetch" {
		return modcache.Fetch(args[1], args[2], args[3], args[4], args[5:])
	}
	if len(args) == 2 && args[0] == "internal-module-members" {
		return modcache.Members(args[1], os.Stdout)
	}
	if len(args) == 2 && args[0] == "internal-module-manifests" {
		return modcache.ManifestMembers(args[1], os.Stdout)
	}
	if len(args) == 3 && args[0] == "internal-module-proxy" {
		return modcache.Assemble(args[1], args[2])
	}
	if len(args) == 2 && args[0] == "internal-module-proxy" {
		return modcache.Assemble(os.Getenv("manifestPath"), args[1])
	}
	if len(args) >= 2 && args[0] == "internal-plan" {
		return plan(os.Getenv("plannerEvaluator"), args[1], args[2:])
	}
	// A Nix builder must keep stdout/stderr open until exit. Coreutils ln
	// closes both early, which can race Nix's EOF-triggered process cleanup.
	if len(args) == 3 && args[0] == "internal-symlink" {
		return os.Symlink(args[1], args[2])
	}
	if len(args) == 4 && args[0] == "internal-prepare-module-cache" {
		return modcache.Prepare(args[1], args[2], args[3])
	}
	// Internal planner operation for stores not readable from the sandbox.
	if len(args) == 3 && args[0] == "internal-copy-derivation" {
		return nixstore.Copy(os.Getenv("NIX_REMOTE"), args[1], args[2])
	}
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || (len(args) == 1 && args[0] == "help") {
		fmt.Fprint(os.Stdout, usage)
		return nil
	}
	if len(args) == 2 && args[0] == "help" {
		args = []string{args[1], "-h"}
	}
	command := args[0]
	var opts exporter.Options
	if command == "internal-export" {
		// Only the Nix planner supplies a selector built with its chosen SDK.
		opts.TestSelector = os.Getenv("testSelector")
		command = "export"
	}
	if command != "build" && command != "export" && command != "run" && command != "test" {
		return fmt.Errorf("unknown command %q. Run drvgo --help", command)
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	f.StringVar(&opts.Dir, "C", ".", "project directory")

	output := "result"
	var noLink bool
	if command == "export" {
		f.StringVar(&opts.Out, "o", ".drvgo", "export directory (relative to project)")
		f.StringVar(&opts.Out, "out", ".drvgo", "alias for -o")
	} else {
		f.StringVar(&opts.Out, "out", ".drvgo", "export directory (relative to project)")
		f.BoolVar(&noLink, "no-link", false, "print the built path without creating a symlink")
		f.StringVar(&output, "out-link", "result", "Nix output symlink (relative to project)")
		f.StringVar(&output, "o", "result", "Nix output symlink (relative to project)")
	}
	f.StringVar(&opts.GoVersion, "go", "", "target Go version, e.g. 1.27.4")
	f.StringVar(&opts.Toolchain, "toolchain", "", "prebuilt pinned toolchain path")
	f.BoolVar(&opts.ExperimentalGo, "experimental-go", false, "try an untested numbered Go toolchain (no compatibility guarantee)")
	opts.Test = command == "test"
	var verbose bool
	var testRun string
	if opts.Test {
		f.BoolVar(&verbose, "v", false, "show individual test results")
		f.StringVar(&testRun, "run", "", "run tests matching this regular expression")
	} else {
		f.BoolVar(&opts.Test, "test", false, "build a test binary")
	}
	f.BoolVar(&opts.AllowEmptyTests, "allow-empty-tests", command == "test", "emit an explicit empty result for a package with no eligible tests (test builds only)")
	f.BoolVar(&opts.Race, "race", false, "enable Go's race detector")
	cgo := f.String("cgo", "", "override cgo: 0 or 1")
	f.StringVar(&opts.ModuleProxy, "module-proxy", "", "explicit module proxy (off or file:// path for offline planning)")
	f.StringVar(&opts.ModuleCache, "module-cache", "", "prepared module cache for offline planning")
	f.StringVar(&opts.Tags, "tags", "", "Go build tags")
	f.StringVar(&opts.LDFlags, "ldflags", "", "Go linker flags")
	execute := command == "run" || command == "test"
	f.Usage = func() {
		fmt.Fprintf(f.Output(), "Usage: drvgo %s [options] [package]", command)
		if execute {
			fmt.Fprint(f.Output(), " [-- arguments]")
		}
		fmt.Fprintln(f.Output())
		f.PrintDefaults()
	}
	pkg, programArgs, err := parsePackage(f, args[1:], execute)
	if err == flag.ErrHelp {
		return nil
	}
	if err != nil {
		return err
	}
	opts.Dir, err = filepath.Abs(opts.Dir)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(opts.Out) {
		opts.Out = filepath.Join(opts.Dir, opts.Out)
	}
	if *cgo != "" {
		if *cgo != "0" && *cgo != "1" {
			return fmt.Errorf("-cgo must be 0 or 1")
		}
		value := *cgo == "1"
		opts.CGOEnabled = &value
	}
	opts.Package, err = resolveTarget(opts.Dir, pkg)
	if err != nil {
		return err
	}
	// Preserve -out for callers that request a standalone export alongside the build.
	explicitExport := false
	f.Visit(func(option *flag.Flag) {
		if option.Name == "out" {
			explicitExport = true
		}
	})
	if command == "export" || explicitExport {
		graph, err := exporter.Generate(opts)
		if err != nil {
			return err
		}
		if graph.ExperimentalGo {
			fmt.Fprintf(os.Stderr, "drvgo: experimental Go driver %s; compatibility is unverified\n", graph.GoVersion)
		}
		for _, message := range graph.Diagnostics {
			fmt.Fprintln(os.Stderr, "drvgo:", message)
		}
		fmt.Fprintf(os.Stderr, "Exported %d package derivations to %s\n", len(graph.Units), opts.Out)
		if command == "export" {
			return nil
		}
	}
	link := output
	if !filepath.IsAbs(link) {
		link = filepath.Join(opts.Dir, link)
	}
	result, err := projectBuild(opts, link, execute, noLink)
	if err != nil {
		return err
	}
	if execute {
		if _, err = os.Stat(filepath.Join(result, "nix-support", "no-tests")); err == nil {
			fmt.Fprintln(os.Stderr, "drvgo: no eligible tests")
			return nil
		}
		binaries, err := os.ReadDir(filepath.Join(result, "bin"))
		if err != nil {
			return err
		}
		if len(binaries) != 1 {
			return fmt.Errorf("expected one executable in %s/bin", result)
		}
		if verbose {
			programArgs = append([]string{"-test.v"}, programArgs...)
		}
		if testRun != "" {
			programArgs = append([]string{"-test.run", testRun}, programArgs...)
		}
		c := exec.Command(filepath.Join(result, "bin", binaries[0].Name()), programArgs...)
		c.Dir = opts.Dir
		if command == "test" {
			c.Dir = filepath.Join(opts.Dir, opts.Package)
		}
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	}
	return nil
}

// Before a package, -- means "use the current package and pass the rest to it".
// After a package, flag.Parse already leaves the complete argument list intact.
func parsePackage(f *flag.FlagSet, args []string, execute bool) (string, []string, error) {
	var forwarded []string
	if execute {
		for i := 0; i < len(args); i++ {
			arg := args[i]
			if arg == "--" {
				forwarded, args = args[i+1:], args[:i]
				break
			}
			name, _, attached := strings.Cut(arg, "=")
			opt := f.Lookup(strings.TrimLeft(name, "-"))
			if !strings.HasPrefix(name, "-") || opt == nil {
				break
			}
			// A string flag may itself take "--" as its value.
			if _, boolean := opt.Value.(interface{ IsBoolFlag() bool }); !boolean && !attached {
				i++
			}
		}
	}
	if err := f.Parse(args); err != nil {
		return "", nil, err
	}
	if f.NArg() == 0 {
		return ".", forwarded, nil
	}
	pkg, rest := f.Arg(0), f.Args()[1:]
	if len(rest) > 0 && rest[0] == "--" {
		rest = rest[1:]
	}
	if len(rest) > 0 && !execute {
		return "", nil, fmt.Errorf("unexpected arguments: %v", rest)
	}
	return pkg, rest, nil
}
