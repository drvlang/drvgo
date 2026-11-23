package export

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/ryantm/drvgo/internal/nixstore"
)

// Keep graph.json as the complete diagnostic graph. Nix only needs these fields.
func writeEvaluationMetadata(dir string, g *Graph) error {
	type unit struct {
		ID              string   `json:"id"`
		Name            string   `json:"name"`
		Binary          bool     `json:"binary"`
		Native          bool     `json:"native"`
		InternalLink    bool     `json:"internalLink,omitempty"`
		StoreReferences []string `json:"storeReferences,omitempty"`
	}
	var units []unit
	sourceHashes := make(map[string]string)
	for _, u := range g.Units {
		// cgo actions invoke the native compiler; linking may use it too.
		units = append(units, unit{u.ID, u.Name, u.Binary, (u.Binary && g.NativeLink) || strings.Contains(u.Script, "/cgo "), u.Binary && g.InternalLink, u.StoreReferences})
		for _, id := range u.Sources {
			sourceHashes[id] = g.SourceHashes[id]
		}
	}
	data, err := json.Marshal(struct {
		*Graph
		Units        []unit            `json:"units"`
		SourceHashes map[string]string `json:"sourceHashes"`
	}{g, units, sourceHashes})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "evaluation.json"), data, 0644)
}

// Preserve the derivation output context rather than treating the toolchain as
// a plain store source (which changes every package derivation). A portable
// default export can recreate its pinned recipe. Custom toolchains must be
// supplied explicitly if their store paths are unavailable.
func writeResolvedToolchain(dir, tc string) error {
	paths := []string{tc}
	fallbacks := []string{"import ./toolchain.nix"}
	var names []string
	for _, name := range []string{"compile", "link", "native", "internalLink"} {
		file := name
		if name == "internalLink" {
			file = "internal-link"
		}
		if data, err := os.ReadFile(filepath.Join(tc, "nix-support", "drvgo-"+file+"-toolchain")); err == nil {
			paths = append(paths, strings.TrimSpace(string(data)))
			fallbacks = append(fallbacks, "(import ./toolchain.nix)."+name)
			names = append(names, name)
		}
	}
	resolved := resolvedToolchains(dir, paths, fallbacks)
	expression := resolved[0]
	if len(names) != 0 {
		expression = "{ outPath = (" + expression + ");"
		for i, name := range names {
			expression += " " + name + " = (" + resolved[i+1] + ");"
		}
		expression += " }\n"
	}
	return os.WriteFile(filepath.Join(dir, "resolved-toolchain.nix"), []byte(expression), 0644)
}

func resolvedToolchains(dir string, toolchains, fallbacks []string) []string {
	// Recursive Nix may not permit querying derivation bindings. Its caller
	// already supplies an explicit toolchain. Never silently use another one.
	expressions := make([]string, len(toolchains))
	for i, tc := range toolchains {
		expressions[i] = "throw " + nixString("Cannot recover selected drvgo toolchain "+tc+"; import this export with an explicit toolchain argument built by drvgo.lib.mkToolchain.") + "\n"
	}
	paths := make([]string, len(toolchains))
	for i, tc := range toolchains {
		path, err := filepath.EvalSymlinks(tc)
		if err != nil {
			return expressions
		}
		paths[i] = path
	}
	var drvs []string
	if remote := os.Getenv("NIX_REMOTE"); nixstore.IsUnixSocket(remote) {
		// The recursive daemon censors derivers. Read its authoritative view
		// directly, keeping the explicit-toolchain requirement when absent.
		var err error
		drvs, err = nixstore.Derivers(remote, paths)
		if err != nil {
			return expressions
		}
	} else {
		// Query all outputs together: extra toolchains must not add a pair of
		// Nix process startups to every export. Preserve other store transports.
		data, err := run("", os.Environ(), "nix-store", append([]string{"--query", "--deriver"}, paths...)...)
		drvs = strings.Fields(string(data))
		if err != nil || len(drvs) != len(paths) {
			return expressions
		}
	}
	for _, drv := range drvs {
		if !filepath.IsAbs(drv) || !strings.HasSuffix(drv, ".drv") {
			return expressions
		}
	}
	data, err := run("", os.Environ(), "nix-store", append([]string{"--query", "--binding", "out"}, drvs...)...)
	outputs := strings.Fields(string(data))
	if err != nil || len(outputs) != len(paths) {
		return expressions
	}
	// Floating outputs have placeholders in the recipe. Recover the original
	// pinned recipes when their realised outputs match the chosen bundles.
	var pinned map[string]string
	for i, path := range paths {
		if outputs[i] != path {
			if pinned == nil {
				pinned = pinnedRecipes(dir, fallbacks)
			}
			drv := drvs[i]
			if selected := pinned[path]; selected != "" {
				drv = selected
			}
			expressions[i] = "let path = " + nixString(path) + "; drv = " + nixString(drv) + "; in\n" +
				"if builtins.pathExists path && builtins.pathExists drv then\n" +
				"  builtins.outputOf (builtins.storePath drv) \"out\"\n" +
				"else let fallback = " + fallbacks[i] + "; in\n" +
				"  if fallback.drvPath == drv then fallback else throw " +
				nixString("Selected drvgo toolchain "+path+" is unavailable; pass the same toolchain explicitly. Refusing to substitute the default compiler.") + "\n"
			continue
		}
		expressions[i] = "let path = " + nixString(path) + "; drv = " + nixString(drvs[i]) + "; in\n" +
			"if builtins.pathExists path && builtins.pathExists drv then\n" +
			"  builtins.appendContext path { ${drv} = { outputs = [ \"out\" ]; }; }\n" +
			"else let fallback = " + fallbacks[i] + "; in\n" +
			"  if toString fallback == path then fallback else\n" +
			"  throw " + nixString("Selected drvgo toolchain "+path+" is unavailable; rebuild it or pass the same toolchain explicitly. Refusing to substitute the default compiler.") + "\n"
	}
	return expressions
}

// Compare recipes without building an unselected custom compiler or using IFD.
func pinnedRecipes(dir string, fallbacks []string) map[string]string {
	selected := make(map[string]string)
	var args []string
	for _, expr := range fallbacks {
		args = append(args, "("+expr+").drvPath")
	}
	data, err := run(dir, os.Environ(), "nix-instantiate", "--eval", "--strict", "--json", "--read-write-mode", "--expr", "[ "+strings.Join(args, " ")+" ]")
	var recipes []string
	if err != nil || json.Unmarshal(data, &recipes) != nil || len(recipes) != len(fallbacks) {
		return selected
	}
	// Toolchain, compile, link, native, and internal-link have distinct names.
	// Batch the realisation lookup without building an unselected toolchain.
	byName := make(map[string]string)
	query := []string{"path-info", "--offline", "--max-jobs", "0", "--option", "builders", ""}
	for _, drv := range recipes {
		_, name, ok := strings.Cut(filepath.Base(drv), "-")
		name = strings.TrimSuffix(name, ".drv")
		if !ok || byName[name] != "" {
			return selected
		}
		byName[name] = drv
		query = append(query, drv+"^out")
	}
	data, err = run(dir, os.Environ(), "nix", query...)
	if err == nil {
		for _, path := range strings.Fields(string(data)) {
			_, name, _ := strings.Cut(filepath.Base(path), "-")
			selected[path] = byName[name]
		}
	}
	return selected
}
