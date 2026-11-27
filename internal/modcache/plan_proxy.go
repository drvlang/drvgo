package modcache

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ryantm/drvgo/internal/nixstore"
)

type lockEntry struct {
	ProxyPath string `json:"proxyPath"`
	Version   string `json:"version"`
	Kind      string `json:"kind"`
	URL       string `json:"url"`
	Checksum  string `json:"checksum"`
}

// PlanProxy registers the verified module-recovery, per-file, archive, proxy,
// and forwarding derivations directly over the recursive Nix daemon socket,
// matching nix/module-members.nix without spawning nix-instantiate or a shell.
func PlanProxy() error {
	out := os.Getenv("out")
	system := os.Getenv("system")
	drvgo := os.Getenv("drvgoOut")
	cacert := os.Getenv("cacert")
	impureEnvVars := os.Getenv("drvgoImpureEnvVars")
	entriesPath := os.Getenv("entries")
	manifestsPath := os.Getenv("manifests")
	membersPath := os.Getenv("members")
	remote := os.Getenv("NIX_REMOTE")
	if out == "" || system == "" || drvgo == "" || cacert == "" || entriesPath == "" {
		return fmt.Errorf("internal-proxy-plan: missing required environment")
	}
	entriesData, err := os.ReadFile(entriesPath)
	if err != nil {
		return err
	}
	var artifacts []lockEntry
	if err := json.Unmarshal(entriesData, &artifacts); err != nil {
		return err
	}
	var manifests [][]memberHash
	if manifestsPath != "" {
		pathsData, err := os.ReadFile(manifestsPath)
		if err != nil {
			return err
		}
		for _, path := range strings.Fields(string(pathsData)) {
			m, err := readMembers(path)
			if err != nil {
				return err
			}
			manifests = append(manifests, m)
		}
	} else if membersPath != "" {
		membersData, err := os.ReadFile(membersPath)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(membersData, &manifests); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("internal-proxy-plan: missing manifests or members")
	}
	if len(manifests) != len(artifacts) {
		return fmt.Errorf("internal-proxy-plan: %d manifests for %d artifacts", len(manifests), len(artifacts))
	}

	storeDir := nixstore.StoreDir()
	builder := drvgo + "/bin/drvgo"
	certFile := cacert + "/etc/ssl/certs/ca-bundle.crt"
	outPlaceholder := nixstore.HashPlaceholder("out")

	var items []nixstore.TextItem
	seenItem := make(map[string]bool)
	addItem := func(name string, content []byte, refs []string) string {
		path := nixstore.MakeTextPath(storeDir, name, content, refs)
		if !seenItem[path] {
			seenItem[path] = true
			items = append(items, nixstore.TextItem{StorePath: path, Name: name, Content: content, Refs: refs})
		}
		return path
	}
	addDrv := func(d nixstore.Derivation) (string, []byte) {
		aterm := []byte(d.Format())
		path := addItem(d.Env["name"]+".drv", aterm, d.References())
		return path, aterm
	}

	// Deduplicate member files by hash, matching builtins.listToAttrs (first wins).
	type fileInfo struct {
		drvPath string
		outPath string
	}
	files := make(map[string]fileInfo)
	for i, a := range artifacts {
		members := manifests[i]
		recoveryDrv := ""
		recoveryOut := ""
		if a.Kind == "zip" && len(members) > 1 {
			if !strings.HasPrefix(a.Checksum, "h1:") {
				return fmt.Errorf("invalid checksum %q", a.Checksum)
			}
			raw, err := base64.StdEncoding.DecodeString(a.Checksum[3:])
			if err != nil || len(raw) != 32 {
				return fmt.Errorf("invalid checksum %q", a.Checksum)
			}
			hashHex := hex.EncodeToString(raw)
			recoveryOut = nixstore.MakeFixedOutputPath(storeDir, "drvgo-module-recovery", false, hashHex)
			d := nixstore.Derivation{
				Outputs:   []nixstore.DerivationOutput{{Name: "out", Path: recoveryOut, HashAlgo: "sha256", Hash: hashHex}},
				InputSrcs: []string{cacert, drvgo},
				Platform:  system,
				Builder:   builder,
				Args:      []string{"internal-module-fetch", a.URL, a.Kind, a.Checksum, outPlaceholder},
				Env: map[string]string{
					"SSL_CERT_FILE":          certFile,
					"allowSubstitutes":       "",
					"builder":                builder,
					"impureEnvVars":          impureEnvVars,
					"name":                   "drvgo-module-recovery",
					"out":                    recoveryOut,
					"outputHash":             "sha256-" + a.Checksum[3:],
					"outputHashMode":         "flat",
					"requiredSystemFeatures": "recursive-nix",
					"system":                 system,
				},
			}
			recoveryDrv, _ = addDrv(d)
		}
		for _, m := range members {
			if _, exists := files[m.Hash]; exists {
				continue
			}
			fileOut := nixstore.MakeFixedOutputPath(storeDir, "drvgo-module-file", false, m.Hash)
			env := map[string]string{
				"SSL_CERT_FILE":  certFile,
				"builder":        builder,
				"impureEnvVars":  impureEnvVars,
				"name":           "drvgo-module-file",
				"out":            fileOut,
				"outputHash":     m.Hash,
				"outputHashAlgo": "sha256",
				"outputHashMode": "flat",
				"system":         system,
			}
			var inputDrvs map[string][]string
			if recoveryDrv != "" {
				env["bulkRecovery"] = recoveryOut
				inputDrvs = map[string][]string{recoveryDrv: {"out"}}
			}
			d := nixstore.Derivation{
				Outputs:   []nixstore.DerivationOutput{{Name: "out", Path: fileOut, HashAlgo: "sha256", Hash: m.Hash}},
				InputDrvs: inputDrvs,
				InputSrcs: []string{cacert, drvgo},
				Platform:  system,
				Builder:   builder,
				Args:      []string{"internal-module-fetch", a.URL, a.Kind, a.Checksum, outPlaceholder, m.Name},
				Env:       env,
			}
			drvPath, _ := addDrv(d)
			files[m.Hash] = fileInfo{drvPath: drvPath, outPath: fileOut}
		}
	}

	type planEntry struct {
		File      string `json:"file"`
		Kind      string `json:"kind"`
		ProxyPath string `json:"proxyPath"`
		Version   string `json:"version"`
	}
	plan := make([]planEntry, 0, len(artifacts))
	proxyInputDrvs := make(map[string][]string)

	for i, a := range artifacts {
		members := manifests[i]
		if a.Kind == "zip" {
			type archiveMember struct {
				File string `json:"file"`
				Name string `json:"name"`
			}
			cfg := make([]archiveMember, len(members))
			archiveInputDrvs := make(map[string][]string, len(members))
			for j, m := range members {
				fi := files[m.Hash]
				cfg[j] = archiveMember{File: fi.outPath, Name: m.Name}
				archiveInputDrvs[fi.drvPath] = []string{"out"}
			}
			cfgJSON, err := json.Marshal(cfg)
			if err != nil {
				return err
			}
			serializedPath := addItem("drvgo-archive-inputs", cfgJSON, nil)
			archiveDrv := nixstore.Derivation{
				Outputs:   []nixstore.DerivationOutput{{Name: "out", HashAlgo: "r:sha256"}},
				InputDrvs: archiveInputDrvs,
				InputSrcs: []string{drvgo, serializedPath},
				Platform:  system,
				Builder:   builder,
				Args:      []string{"internal-module-archive", serializedPath, outPlaceholder},
				Env: map[string]string{
					"builder":        builder,
					"memberInputs":   "",
					"name":           "drvgo-module.zip",
					"out":            outPlaceholder,
					"outputHashAlgo": "sha256",
					"outputHashMode": "recursive",
					"system":         system,
				},
			}
			archiveDrvPath, _ := addDrv(archiveDrv)
			proxyInputDrvs[archiveDrvPath] = []string{"out"}
			plan = append(plan, planEntry{
				File:      nixstore.DownstreamPlaceholder(archiveDrvPath, "out"),
				Kind:      a.Kind,
				ProxyPath: a.ProxyPath,
				Version:   a.Version,
			})
		} else {
			if len(members) != 1 || members[0].Name != "go.mod" {
				return fmt.Errorf("invalid go.mod manifest for %s", a.ProxyPath)
			}
			fi := files[members[0].Hash]
			proxyInputDrvs[fi.drvPath] = []string{"out"}
			plan = append(plan, planEntry{
				File:      fi.outPath,
				Kind:      a.Kind,
				ProxyPath: a.ProxyPath,
				Version:   a.Version,
			})
		}
	}

	planJSON, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	lockedProxyDrv := nixstore.Derivation{
		Outputs:   []nixstore.DerivationOutput{{Name: "out", HashAlgo: "r:sha256"}},
		InputDrvs: proxyInputDrvs,
		InputSrcs: []string{drvgo},
		Platform:  system,
		Builder:   builder,
		Args:      []string{"internal-module-proxy", outPlaceholder},
		Env: map[string]string{
			"builder":        builder,
			"manifest":       string(planJSON),
			"name":           "drvgo-locked-proxy",
			"out":            outPlaceholder,
			"outputHashAlgo": "sha256",
			"outputHashMode": "recursive",
			"passAsFile":     "manifest",
			"system":         system,
		},
	}
	lockedProxyPath, _ := addDrv(lockedProxyDrv)

	resultDrv := nixstore.Derivation{
		Outputs:   []nixstore.DerivationOutput{{Name: "out", HashAlgo: "r:sha256"}},
		InputDrvs: map[string][]string{lockedProxyPath: {"out"}},
		InputSrcs: []string{drvgo},
		Platform:  system,
		Builder:   builder,
		Args:      []string{"internal-symlink", nixstore.DownstreamPlaceholder(lockedProxyPath, "out"), outPlaceholder},
		Env: map[string]string{
			"allowSubstitutes": "1",
			"builder":          builder,
			"name":             "drvgo-proxy-result",
			"out":              outPlaceholder,
			"outputHashAlgo":   "sha256",
			"outputHashMode":   "recursive",
			"preferLocalBuild": "1",
			"system":           system,
		},
	}
	resultATerm := []byte(resultDrv.Format())

	if err := nixstore.AddTextWaves(remote, items); err != nil {
		return fmt.Errorf("register proxy derivations: %w", err)
	}
	return os.WriteFile(out, resultATerm, 0644)
}
