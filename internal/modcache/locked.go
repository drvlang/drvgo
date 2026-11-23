// Build Go's canonical h1 digest manifest and reconstruct a local module proxy.
// Its entries are ordinary flat SHA-256 hashes, so Nix can cache the module files.
package modcache

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type Member struct {
	Name string `json:"name"`
	File string `json:"file"`
}
type Artifact struct {
	ProxyPath string   `json:"proxyPath"`
	Version   string   `json:"version"`
	Kind      string   `json:"kind"`
	Members   []Member `json:"members"`
}

// Fetch retrieves a proxy .mod or .zip. Without a member argument, the output
// is the exact byte stream hashed by golang.org/x/mod/sumdb/dirhash.Hash1.
// Populate the matching flat Nix paths in batches while the archive is local.
// The graph still declares a verified fetch recipe for each member.
func Fetch(url, kind, checksum, out string, member []string) error {
	tmp, err := os.MkdirTemp("", "drvgo-download-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	response, err := (&http.Client{Timeout: 5 * time.Minute}).Get(url)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, response.Status)
	}
	const limit = 500 << 20 // Go's module zip limit.
	archive, err := os.Create(filepath.Join(tmp, "artifact"))
	if err != nil {
		return err
	}
	defer archive.Close()
	size, err := io.Copy(archive, io.LimitReader(response.Body, limit+1))
	if err != nil {
		return err
	}
	if size > limit {
		return fmt.Errorf("module download exceeds %d bytes", limit)
	}
	if _, err = archive.Seek(0, io.SeekStart); err != nil {
		return err
	}
	type entry struct {
		name string
		open func() (io.ReadCloser, error)
	}
	entries := []entry{}
	switch kind {
	case "mod":
		entries = append(entries, entry{"go.mod", func() (io.ReadCloser, error) { return os.Open(archive.Name()) }})
	case "zip":
		reader, err := zip.NewReader(archive, size)
		if err != nil {
			return err
		}
		for _, file := range reader.File {
			entries = append(entries, entry{file.Name, file.Open})
		}
	default:
		return fmt.Errorf("invalid archive type %q", kind)
	}
	slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.name, b.name) })
	var manifest strings.Builder
	var paths []string
	var total int64
	for i, e := range entries {
		if strings.ContainsAny(e.name, "\r\n") || (i > 0 && entries[i-1].name == e.name) {
			return fmt.Errorf("invalid module entry %q", e.name)
		}
		if len(member) > 0 && member[0] != e.name {
			continue
		}
		dest := out
		if len(member) == 0 {
			dir := filepath.Join(tmp, fmt.Sprint(i))
			if err := os.Mkdir(dir, 0755); err != nil {
				return err
			}
			dest = filepath.Join(dir, "drvgo-module-file")
			paths = append(paths, dest)
		}
		in, err := e.open()
		if err != nil {
			return err
		}
		file, err := os.Create(dest)
		if err != nil {
			in.Close()
			return err
		}
		digest := sha256.New()
		written, err := io.Copy(io.MultiWriter(file, digest), io.LimitReader(in, limit+1-total))
		in.Close()
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		total += written
		if total > limit {
			return fmt.Errorf("module contents exceed %d bytes", limit)
		}
		if len(member) > 0 {
			return nil
		}
		fmt.Fprintf(&manifest, "%x  %s\n", digest.Sum(nil), e.name)
	}
	if len(member) > 0 {
		return fmt.Errorf("archive member %q missing", member[0])
	}
	content := []byte(manifest.String())
	sum := sha256.Sum256(content)
	if "h1:"+base64.StdEncoding.EncodeToString(sum[:]) != checksum {
		return fmt.Errorf("%s: go.sum checksum mismatch", url)
	}
	if store := os.Getenv("drvgoNixStore"); store != "" {
		for group := range slices.Chunk(paths, 256) {
			cmd := exec.Command(store, append([]string{"--add-fixed", "sha256"}, group...)...)
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("seed fixed module files: %w", err)
			}
		}
	}
	return os.WriteFile(out, content, 0644)
}

// Members converts one verified manifest to JSON without evaluating its paths
// as Nix or shell source.
func Members(manifest string, out io.Writer) error {
	data, err := os.ReadFile(manifest)
	if err != nil {
		return err
	}
	var result []map[string]string
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		digest, name, ok := strings.Cut(line, "  ")
		raw, err := hex.DecodeString(digest)
		if !ok || err != nil || len(raw) != 32 || name == "" {
			return fmt.Errorf("invalid module manifest")
		}
		result = append(result, map[string]string{"hash": digest, "name": name})
	}
	if result == nil {
		result = []map[string]string{}
	}
	return json.NewEncoder(out).Encode(result)
}

func Assemble(config, out string) error {
	var artifacts []Artifact
	data, err := os.ReadFile(config)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, &artifacts); err != nil {
		return err
	}
	if err = os.Mkdir(out, 0755); err != nil {
		return err
	}
	for _, a := range artifacts {
		if !filepath.IsLocal(a.ProxyPath) || strings.ContainsAny(a.Version, "/\\") {
			return fmt.Errorf("invalid module proxy path: %q", a.ProxyPath)
		}
		path := filepath.Join(out, a.ProxyPath)
		if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		info, _ := json.Marshal(map[string]string{"Version": a.Version})
		if err = os.WriteFile(path+".info", info, 0644); err != nil {
			return err
		}
		if a.Kind == "mod" {
			if len(a.Members) != 1 || a.Members[0].Name != "go.mod" {
				return fmt.Errorf("invalid go.mod manifest")
			}
			contents, err := os.ReadFile(a.Members[0].File)
			if err != nil {
				return err
			}
			if err = os.WriteFile(path+".mod", contents, 0644); err != nil {
				return err
			}
			continue
		}
		if err = assembleZip(path+".zip", a.Members); err != nil {
			return err
		}
	}
	return nil
}
func assembleZip(path string, members []Member) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := zip.NewWriter(file)
	for _, m := range members {
		target, err := writer.CreateHeader(&zip.FileHeader{Name: m.Name, Method: zip.Store})
		if err != nil {
			return err
		}
		src, err := os.Open(m.File)
		if err != nil {
			return err
		}
		_, err = io.Copy(target, src)
		src.Close()
		if err != nil {
			return err
		}
	}
	return writer.Close()
}
