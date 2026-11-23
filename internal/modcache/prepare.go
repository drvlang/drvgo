// Package modcache prepares an immutable Go module cache from a fixed proxy.
package modcache

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Prepare uses the selected SDK's downloader to validate and extract each zip.
// The proxy's metadata-only modules are retained too: graph loading can need
// their go.mod files even when none of their packages are selected. The output
// depends on the fixed proxy, not on the application's source or build tags.
func Prepare(goTool, proxy, out string) error {
	var err error
	proxy, err = filepath.Abs(proxy)
	if err != nil {
		return err
	}
	// Dynamic derivations forward the verified proxy as a store symlink.
	// Walk its directory while still rejecting symlinks within the proxy.
	proxy, err = filepath.EvalSymlinks(proxy)
	if err != nil {
		return err
	}
	out, err = filepath.Abs(out)
	if err != nil {
		return err
	}
	// Nix creates a fresh output. Refuse to alter an existing directory when
	// the internal operation is invoked directly.
	if err := os.Mkdir(out, 0755); err != nil {
		return err
	}
	download := filepath.Join(out, "cache", "download")
	var modules []string
	err = filepath.WalkDir(proxy, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(proxy, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(download, rel), 0755)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported module proxy file %s", rel)
		}
		if strings.HasSuffix(rel, ".lock") {
			return nil
		}
		if strings.HasSuffix(rel, ".zip") {
			path, version, ok := strings.Cut(filepath.ToSlash(rel), "/@v/")
			if !ok || strings.Contains(version, "/") {
				return fmt.Errorf("invalid module zip path %s", rel)
			}
			module, err := unescape(path + "@" + strings.TrimSuffix(version, ".zip"))
			if err != nil {
				return fmt.Errorf("module zip %s: %w", rel, err)
			}
			// A native go.sum can pin a zip without a separate /go.mod line.
			// go mod download also requests metadata. Derive that metadata
			// from the already verified archive when the proxy omits it.
			mod := strings.TrimSuffix(rel, ".zip") + ".mod"
			if _, err := os.Stat(filepath.Join(proxy, mod)); os.IsNotExist(err) {
				if err := writeZipMod(filepath.Join(proxy, rel), module, filepath.Join(download, mod)); err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
			modules = append(modules, module)
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(download, rel), data, 0644)
	})
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "drvgo-module-cache-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	proxyURL := (&url.URL{Scheme: "file", Path: proxy}).String()
	for len(modules) != 0 {
		// Bound command length on both Linux and macOS. No shell interprets
		// module names; cmd/go validates their path and version syntax.
		count, size := 0, 0
		for count < len(modules) && count < 64 && (size < 32768 || count == 0) {
			size += len(modules[count]) + 1
			count++
		}
		args := append([]string{"mod", "download", "-json"}, modules[:count]...)
		cmd := exec.Command(goTool, args...)
		cmd.Dir = tmp
		cmd.Env = []string{
			"PATH=" + filepath.Dir(goTool), "HOME=" + tmp, "TMPDIR=" + tmp,
			"GOENV=off", "GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local", "GOSUMDB=off",
			"GOPATH=" + filepath.Join(tmp, "go"), "GOCACHE=" + filepath.Join(tmp, "cache"),
			"GOMODCACHE=" + out, "GOPROXY=" + proxyURL,
		}
		if cores, err := strconv.Atoi(os.Getenv("NIX_BUILD_CORES")); err == nil && cores > 0 {
			cmd.Env = append(cmd.Env, "GOMAXPROCS="+strconv.Itoa(cores))
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if b, err := cmd.Output(); err != nil {
			if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 1 || !onlyNewerModules(b, modules[:count]) {
				return fmt.Errorf("prepare modules: %w\n%s%s", err, stderr.Bytes(), b)
			}
		}
		modules = modules[count:]
	}
	// Fully extracted caches need .ziphash for go.sum validation, but neither
	// the duplicate zip payload nor download locks is needed for read-only use.
	return filepath.WalkDir(download, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && (strings.HasSuffix(path, ".zip") || strings.HasSuffix(path, ".lock")) {
			return os.Remove(path)
		}
		return nil
	})
}

// A go.sum can retain unused versions requiring a newer SDK. The downloader
// reports this only after verifying/extracting their archives. Keep those
// archives in the cache, leaving the selected project's Go version check to
// normal package discovery. All other download and verification errors fail.
func onlyNewerModules(data []byte, modules []string) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	newer := false
	for _, module := range modules {
		var result struct{ Path, Version, Error, Zip, Sum, GoModSum string }
		if decoder.Decode(&result) != nil || result.Path+"@"+result.Version != module {
			return false
		}
		if result.Error == "" {
			continue
		}
		if !strings.HasPrefix(result.Error, module+" requires go >= ") || result.Sum == "" || result.GoModSum == "" {
			return false
		}
		newer = true
		// A partial marker means cmd/go did not finish extracting this zip.
		base := strings.TrimSuffix(result.Zip, ".zip")
		if base == result.Zip {
			return false
		}
		if _, err := os.Stat(base + ".partial"); !os.IsNotExist(err) {
			return false
		}
		if hash, err := os.ReadFile(base + ".ziphash"); err != nil || string(hash) != result.Sum {
			return false
		}
	}
	return newer && decoder.Decode(new(any)) == io.EOF
}

func writeZipMod(path, module, out string) error {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer archive.Close()
	file, err := archive.Open(module + "/go.mod")
	if os.IsNotExist(err) {
		// Go uses this directive for legacy modules without a go.mod.
		name, _, _ := strings.Cut(module, "@")
		return os.WriteFile(out, []byte("module "+name+"\n"), 0644)
	}
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 16<<20+1))
	if err != nil {
		return err
	}
	if len(data) > 16<<20 {
		return fmt.Errorf("%s: go.mod exceeds 16 MiB", module)
	}
	return os.WriteFile(out, data, 0644)
}

// Go proxy paths escape uppercase ASCII as an exclamation mark and lowercase
// letter. cmd/go performs the remaining module-path and version validation.
func unescape(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '!' {
			i++
			if i == len(s) || s[i] < 'a' || s[i] > 'z' {
				return "", fmt.Errorf("invalid escaped module name %q", s)
			}
			c = s[i] - 'a' + 'A'
		} else if c >= 'A' && c <= 'Z' {
			return "", fmt.Errorf("unescaped uppercase in module name %q", s)
		}
		b.WriteByte(c)
	}
	return b.String(), nil
}
