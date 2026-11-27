package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	exporter "github.com/ryantm/drvgo/internal/export"
	"github.com/ryantm/drvgo/internal/nixstore"
)

// plan runs the recursive builder without invoking a shell or coreutils.
// Native mode registers derivations directly over NIX_REMOTE; external
// evaluator modes (nix, iets) keep the exporter in a child process so its heap
// is released before starting the evaluator.
func plan(evaluator, mode string, args []string) error {
	out := os.Getenv("out")
	if out == "" {
		return fmt.Errorf("internal-plan: missing out")
	}
	if mode == "native" {
		data, err := planNative(args)
		if err != nil {
			return err
		}
		return os.WriteFile(out, data, 0644)
	}
	drv, err := evaluatePlan(evaluator, mode, args)
	if err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		// A macOS sandbox cannot grant filesystem access to newly registered
		// paths. The recursive daemon enforces the same restricted store view.
		return nixstore.Copy(os.Getenv("NIX_REMOTE"), drv, out)
	}
	data, err := os.ReadFile(drv)
	if err != nil {
		return err
	}
	return os.WriteFile(out, data, 0644)
}

func planNative(args []string) ([]byte, error) {
	for _, name := range []string{"TMPDIR", "src"} {
		if os.Getenv(name) == "" {
			return nil, fmt.Errorf("internal-plan: missing %s", name)
		}
	}
	tmp := os.Getenv("TMPDIR")
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0755); err != nil {
		return nil, err
	}
	if err := os.Setenv("HOME", home); err != nil {
		return nil, err
	}
	var opts exporter.Options
	opts.Dir = os.Getenv("src")
	opts.TestSelector = os.Getenv("testSelector")
	opts.ModuleTest = os.Getenv("moduleTest") == "1"
	f := flag.NewFlagSet("internal-plan", flag.ContinueOnError)
	f.StringVar(&opts.Toolchain, "toolchain", "", "")
	f.BoolVar(&opts.ExperimentalGo, "experimental-go", false, "")
	f.BoolVar(&opts.Test, "test", false, "")
	f.BoolVar(&opts.AllowEmptyTests, "allow-empty-tests", false, "")
	f.BoolVar(&opts.Race, "race", false, "")
	cgo := f.String("cgo", "", "")
	f.StringVar(&opts.ModuleProxy, "module-proxy", "", "")
	f.StringVar(&opts.ModuleCache, "module-cache", "", "")
	f.StringVar(&opts.Tags, "tags", "", "")
	f.StringVar(&opts.LDFlags, "ldflags", "", "")
	if err := f.Parse(args); err != nil {
		return nil, err
	}
	if *cgo != "" {
		if *cgo != "0" && *cgo != "1" {
			return nil, fmt.Errorf("-cgo must be 0 or 1")
		}
		v := *cgo == "1"
		opts.CGOEnabled = &v
	}
	if f.NArg() > 1 {
		return nil, fmt.Errorf("internal-plan: unexpected arguments: %v", f.Args())
	}
	if f.NArg() == 1 {
		opts.Package = f.Arg(0)
	}
	tcRef := func(outEnv, drvEnv string) exporter.ToolchainRef {
		return exporter.ToolchainRef{Out: os.Getenv(outEnv), Drv: os.Getenv(drvEnv)}
	}
	return exporter.PlanDirect(exporter.DirectPlanOptions{
		Export:       opts,
		Remote:       os.Getenv("NIX_REMOTE"),
		ResultName:   os.Getenv("resultName"),
		ExportData:   os.Getenv("exportData") != "0",
		Drvgo:        tcRef("drvgoOut", "drvgoDrv"),
		Compile:      tcRef("compileToolchainOut", "compileToolchainDrv"),
		Link:         tcRef("linkToolchainOut", "linkToolchainDrv"),
		Native:       tcRef("nativeToolchainOut", "nativeToolchainDrv"),
		InternalLink: tcRef("internalLinkToolchainOut", "internalLinkToolchainDrv"),
	})
}

func evaluatePlan(evaluator, mode string, args []string) (string, error) {
	if mode != "nix" && mode != "iets" {
		return "", fmt.Errorf("internal-plan: unknown evaluator mode %q", mode)
	}
	if !filepath.IsAbs(evaluator) {
		return "", fmt.Errorf("internal-plan: evaluator must be an absolute path")
	}
	for _, name := range []string{"TMPDIR", "src", "expressionPath"} {
		if os.Getenv(name) == "" {
			return "", fmt.Errorf("internal-plan: missing %s", name)
		}
	}
	tmp := os.Getenv("TMPDIR")
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0755); err != nil {
		return "", err
	}
	if err := os.Setenv("HOME", home); err != nil {
		return "", err
	}
	if err := os.Setenv("NIX_CONFIG", "extra-experimental-features = nix-command ca-derivations dynamic-derivations recursive-nix\nallow-import-from-derivation = false"); err != nil {
		return "", err
	}
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	exportArgs := append([]string{"internal-export", "-C", os.Getenv("src"), "-out", filepath.Join(tmp, "graph")}, args...)
	export := exec.Command(self, exportArgs...)
	export.Stdout, export.Stderr = os.Stdout, os.Stderr
	if err := export.Run(); err != nil {
		return "", fmt.Errorf("export graph: %w", err)
	}
	expression, err := os.ReadFile(os.Getenv("expressionPath"))
	if err != nil {
		return "", err
	}
	file := filepath.Join(tmp, "build.nix")
	if err := os.WriteFile(file, expression, 0644); err != nil {
		return "", err
	}
	command := exec.Command(evaluator, file)
	command.Args[0] = "nix-instantiate"
	command.Stderr = os.Stderr
	if mode == "iets" {
		remote := os.Getenv("NIX_REMOTE")
		if !nixstore.IsUnixSocket(remote) {
			return "", fmt.Errorf("expected a recursive Nix Unix socket, got %q", remote)
		}
		command.Env = append(os.Environ(), "IETS_NIX_FALLBACK=never", "IETS_PROGRESS=false", "IETS_WORKERS=1", "IETS_CACHE=false",
			"NIX_DAEMON_SOCKET_PATH="+strings.TrimPrefix(remote, "unix://"))
	}
	data, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("instantiate graph: %w", err)
	}
	drv := strings.TrimSpace(string(data))
	if !filepath.IsAbs(drv) || !strings.HasSuffix(drv, ".drv") || strings.ContainsAny(drv, "\r\n") {
		return "", fmt.Errorf("evaluator returned an invalid derivation path: %q", drv)
	}
	return drv, nil
}
