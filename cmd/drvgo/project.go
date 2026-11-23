package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	exporter "github.com/ryantm/drvgo/internal/export"
)

// Set to a filtered flake source when packaging the CLI.
var projectFlake string

func flakeSource() string {
	if projectFlake != "" {
		return projectFlake
	}
	// Development builds can exercise their own checkout before publication.
	executable, _ := os.Executable()
	cwd, _ := os.Getwd()
	for _, root := range []string{filepath.Dir(filepath.Dir(executable)), cwd} {
		mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
		if err == nil && strings.Contains(string(mod), "module github.com/ryantm/drvgo") {
			if _, err = os.Stat(filepath.Join(root, "flake.nix")); err == nil {
				return "git+file://" + root
			}
		}
	}
	return "github:drvlang/drvgo"
}

func projectBuild(opts exporter.Options, link string, execute, noLink bool) (string, error) {
	args := map[string]any{
		"package": opts.Package, "tags": opts.Tags, "ldflags": opts.LDFlags, "test": opts.Test,
		"race": opts.Race, "allowEmptyTests": opts.AllowEmptyTests, "experimentalGo": opts.ExperimentalGo,
	}
	if opts.CGOEnabled != nil {
		args["cgoEnabled"] = *opts.CGOEnabled
	}
	if opts.GoVersion != "" {
		args["go"] = strings.TrimPrefix(opts.GoVersion, "go")
	}
	if opts.ModuleCache != "" {
		path, err := filepath.Abs(opts.ModuleCache)
		if err != nil {
			return "", err
		}
		args["cacheDirectory"] = path
	}
	if opts.ModuleProxy != "" {
		args["moduleProxy"] = opts.ModuleProxy
	}
	if opts.Toolchain != "" {
		if opts.GoVersion != "" {
			return "", fmt.Errorf("choose either -go or -toolchain")
		}
		path, err := filepath.EvalSymlinks(opts.Toolchain)
		if err != nil {
			return "", err
		}
		path, err = filepath.Abs(path)
		if err != nil {
			return "", err
		}
		tc := map[string]string{"outPath": path, "go": path}
		for key, role := range map[string]string{"compile": "compile", "link": "link", "native": "native", "internalLink": "internal-link"} {
			file := filepath.Join(path, "nix-support", "drvgo-"+role+"-toolchain")
			value, err := os.ReadFile(file)
			if err == nil {
				tc[key] = strings.TrimSpace(string(value))
			} else if !os.IsNotExist(err) {
				return "", err
			}
		}
		args["toolchainPaths"] = tc
	}
	data, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	const expression = `{ flake, directory, options, linkName }:
	  let f = builtins.getFlake flake;
	      o = builtins.fromJSON options;
	      base = builtins.removeAttrs o [ "cacheDirectory" "toolchainPaths" ];
	      src = builtins.path { path = /. + directory; name = "go-source";
	        filter = p: t: !(builtins.elem (baseNameOf p) [ ".git" ".drvgo" ".devenv" ".direnv" ]) &&
	          !(t == "symlink" && (p == linkName || baseNameOf p == "result")); };
	      args = base // { inherit src; system = builtins.currentSystem; }
	        // (if o ? cacheDirectory then { moduleCache = /. + o.cacheDirectory; } else {});
	      supplied = if o ? toolchainPaths then {
            toolchain = (builtins.mapAttrs (_: p: builtins.storePath p) o.toolchainPaths) //
              { pkgs = import f.inputs.nixpkgs { inherit (args) system; }; };
          } else {};
      in f.lib.mkPackage (args // supplied)`
	command := []string{"build", "--impure", "--print-out-paths", "--expr", expression, "--argstr", "flake", flakeSource(), "--argstr", "directory", opts.Dir,
		"--argstr", "options", string(data), "--argstr", "linkName", link, "--option", "allow-import-from-derivation", "false", "--max-jobs", "4", "--cores", "2"}
	if noLink {
		command = append(command, "--no-link")
	} else {
		command = append(command, "--out-link", link)
	}
	build := exec.Command("nix", command...)
	build.Stderr = os.Stderr
	output, err := build.Output()
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(string(output))
	if !execute {
		fmt.Println(path)
	}
	return path, nil
}

func resolveTarget(root, target string) (string, error) {
	if target == "." || strings.HasPrefix(target, "./") {
		return target, nil
	}
	data, err := os.ReadFile(filepath.Join(root, "drvlang.json"))
	if err == nil {
		var conf struct {
			Commands map[string]string `json:"commands"`
		}
		if err := json.Unmarshal(data, &conf); err != nil {
			return "", fmt.Errorf("drvlang.json: %w", err)
		}
		if dir, ok := conf.Commands[target]; ok {
			dir = filepath.Clean(dir)
			if !filepath.IsLocal(dir) {
				return "", fmt.Errorf("invalid command path %q", dir)
			}
			return "./" + dir, nil
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if stat, err := os.Stat(filepath.Join(root, target)); err == nil && stat.IsDir() {
		return "./" + target, nil
	}
	for _, base := range []string{"app", "cmd"} {
		if stat, err := os.Stat(filepath.Join(root, base, target)); err == nil && stat.IsDir() {
			return "./" + filepath.Join(base, target), nil
		}
	}
	return target, nil
}
