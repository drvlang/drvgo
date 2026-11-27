package export

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ryantm/drvgo/internal/nixstore"
	"github.com/ryantm/drvgo/internal/plan"
)

// ToolchainRef describes one Nix toolchain bundle supplied to the direct planner.
// Out is the store path (for input-addressed or plain store paths) or CA
// downstream placeholder. Drv is its derivation path, or empty for plain store paths.
type ToolchainRef struct {
	Out string
	Drv string
}

// DirectPlanOptions configures direct Nix daemon derivation registration without
// spawning nix-instantiate.
type DirectPlanOptions struct {
	Export       Options
	Remote       string
	ResultName   string
	ExportData   bool
	Drvgo        ToolchainRef
	Compile      ToolchainRef
	Link         ToolchainRef
	Native       ToolchainRef
	InternalLink ToolchainRef
}

var drvNameSanitizer = strings.NewReplacer("/", "-", "@", "-", "+", "-")

// PlanDirect discovers the Go package graph, registers all non-inlined sources
// and package derivations directly over the recursive Nix daemon socket, and
// returns the ATerm bytes of the final forwarding derivation.
func PlanDirect(o DirectPlanOptions) ([]byte, error) {
	if !nixstore.IsUnixSocket(o.Remote) {
		return nil, fmt.Errorf("direct planner requires a recursive Nix Unix socket, got %q", o.Remote)
	}
	if o.ResultName == "" || o.Drvgo.Out == "" || o.Compile.Out == "" {
		return nil, fmt.Errorf("direct planner: missing required toolchain or result name")
	}
	staging, err := os.MkdirTemp("", "drvgo-plan-sources-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)

	graph, err := discoverGraph(o.Export, staging)
	if err != nil {
		return nil, err
	}

	storeDir := nixstore.StoreDir()
	outPlaceholder := nixstore.HashPlaceholder("out")
	apiPlaceholder := nixstore.HashPlaceholder("api")

	var drvItems []nixstore.TextItem
	seenDrv := make(map[string]bool)
	recordDrv := func(d nixstore.Derivation) (string, []byte) {
		aterm := []byte(d.Format())
		refs := d.References()
		path := nixstore.MakeTextPath(storeDir, d.Env["name"]+".drv", aterm, refs)
		if !seenDrv[path] {
			seenDrv[path] = true
			drvItems = append(drvItems, nixstore.TextItem{StorePath: path, Name: d.Env["name"] + ".drv", Content: aterm, Refs: refs})
		}
		return path, aterm
	}

	addRef := func(ref ToolchainRef, inputDrvs map[string][]string, inputSrcs *[]string) {
		if ref.Drv != "" {
			inputDrvs[ref.Drv] = []string{"out"}
		} else {
			*inputSrcs = append(*inputSrcs, ref.Out)
		}
	}

	var sourceDirs []nixstore.DirItem
	var commandDrvPath string
	if graph.EmptyTests {
		inputDrvs := map[string][]string{}
		var inputSrcs []string
		addRef(o.Compile, inputDrvs, &inputSrcs)
		builder := o.Compile.Out + "/bin/drvgo-builder"
		d := nixstore.Derivation{
			Outputs:   []nixstore.DerivationOutput{{Name: "out", HashAlgo: "r:sha256"}},
			InputDrvs: inputDrvs,
			InputSrcs: inputSrcs,
			Platform:  graph.System,
			Builder:   builder,
			Env: map[string]string{
				"builder":        builder,
				"emptyTests":     "1",
				"name":           "go-empty-tests",
				"out":            outPlaceholder,
				"outputHashAlgo": "sha256",
				"outputHashMode": "recursive",
				"system":         graph.System,
			},
		}
		commandDrvPath, _ = recordDrv(d)
	} else {
		// Collect any non-inlined source snapshots to register alongside the DAG.
		neededSources := make(map[string]string) // id -> expected store path
		for _, u := range graph.Units {
			for _, id := range u.Sources {
				if _, ok := neededSources[id]; !ok {
					hash := graph.SourceHashes[id]
					if hash == "" {
						return nil, fmt.Errorf("missing source hash for %s", id)
					}
					p := nixstore.MakeFixedOutputPath(storeDir, "go-source", true, hash)
					neededSources[id] = p
					sourceDirs = append(sourceDirs, nixstore.DirItem{
						StorePath: p,
						LocalDir:  filepath.Join(staging, "sources", id),
					})
				}
			}
		}

		unitByID := make(map[string]plan.Unit, len(graph.Units))
		for _, u := range graph.Units {
			unitByID[u.ID] = u
		}
		unitDrvPath := make(map[string]string, len(graph.Units))
		visiting := make(map[string]bool, len(graph.Units))

		var emitUnit func(id string) (string, error)
		emitUnit = func(id string) (string, error) {
			if p, ok := unitDrvPath[id]; ok {
				return p, nil
			}
			if visiting[id] {
				return "", fmt.Errorf("dependency cycle involving unit %s", id)
			}
			u, ok := unitByID[id]
			if !ok {
				return "", fmt.Errorf("unknown dependency unit %s", id)
			}
			visiting[id] = true
			inputDrvs := make(map[string][]string, len(u.Deps)+1)
			pairs := make([]string, 0, 2*(len(u.Deps)+len(u.Sources)))
			for _, depID := range u.Deps {
				depDrv, err := emitUnit(depID)
				if err != nil {
					return "", err
				}
				outName := "out"
				if !u.Binary && o.ExportData {
					outName = "api"
				}
				inputDrvs[depDrv] = append(inputDrvs[depDrv], outName)
				pairs = append(pairs, "@DEP:"+depID+"@", nixstore.DownstreamPlaceholder(depDrv, outName))
			}
			var inputSrcs []string
			for _, srcID := range u.Sources {
				srcPath := neededSources[srcID]
				inputSrcs = append(inputSrcs, srcPath)
				pairs = append(pairs, "@SRC:"+srcID+"@", srcPath)
			}
			script := u.Script
			if len(pairs) > 0 {
				script = strings.NewReplacer(pairs...).Replace(script)
			}

			native := (u.Binary && graph.NativeLink) || strings.Contains(u.Script, "/cgo ")
			tc := o.Compile
			if native {
				tc = o.Native
			} else if u.Binary {
				if graph.InternalLink {
					tc = o.InternalLink
				} else {
					tc = o.Link
				}
			}
			addRef(tc, inputDrvs, &inputSrcs)

			isBinary := "0"
			binaryName := ""
			if u.Binary {
				isBinary = "1"
				binaryName = graph.BinaryName
			}
			builder := tc.Out + "/bin/drvgo-builder"
			env := map[string]string{
				"CGO_ENABLED":    graph.CGO,
				"GOARCH":         graph.GoArch,
				"GOENV":          "off",
				"GOOS":           graph.GoOS,
				"GOROOT":         tc.Out + "/share/go",
				"GOTOOLCHAIN":    "local",
				"PATH":           tc.Out + "/bin",
				"binaryName":     binaryName,
				"builder":        builder,
				"isBinary":       isBinary,
				"name":           "go-" + drvNameSanitizer.Replace(u.Name),
				"out":            outPlaceholder,
				"outputHashAlgo": "sha256",
				"outputHashMode": "recursive",
				"passAsFile":     "script",
				"script":         script,
				"system":         graph.System,
				"toolchainInput": tc.Out,
			}
			outputs := []nixstore.DerivationOutput{{Name: "out", HashAlgo: "r:sha256"}}
			if o.ExportData && !u.Binary {
				outputs = append(outputs, nixstore.DerivationOutput{Name: "api", HashAlgo: "r:sha256"})
				env["api"] = apiPlaceholder
				env["outputs"] = "out api"
			}
			if len(u.StoreReferences) > 0 {
				env["runtimeReferences"] = strings.Join(u.StoreReferences, " ")
				env["passAsFile"] = "script runtimeReferences"
				inputSrcs = append(inputSrcs, u.StoreReferences...)
			}
			d := nixstore.Derivation{
				Outputs:   outputs,
				InputDrvs: inputDrvs,
				InputSrcs: inputSrcs,
				Platform:  graph.System,
				Builder:   builder,
				Env:       env,
			}
			drvPath, _ := recordDrv(d)
			delete(visiting, id)
			unitDrvPath[id] = drvPath
			return drvPath, nil
		}

		commandDrvPath, err = emitUnit(graph.Root)
		if err != nil {
			return nil, err
		}
	}

	resultInputDrvs := map[string][]string{commandDrvPath: {"out"}}
	var resultInputSrcs []string
	addRef(o.Drvgo, resultInputDrvs, &resultInputSrcs)
	drvgoBuilder := o.Drvgo.Out + "/bin/drvgo"
	resultDrv := nixstore.Derivation{
		Outputs:   []nixstore.DerivationOutput{{Name: "out", HashAlgo: "r:sha256"}},
		InputDrvs: resultInputDrvs,
		InputSrcs: resultInputSrcs,
		Platform:  graph.System,
		Builder:   drvgoBuilder,
		Args:      []string{"internal-symlink", nixstore.DownstreamPlaceholder(commandDrvPath, "out"), outPlaceholder},
		Env: map[string]string{
			"builder":          drvgoBuilder,
			"name":             o.ResultName,
			"out":              outPlaceholder,
			"outputHashAlgo":   "sha256",
			"outputHashMode":   "recursive",
			"preferLocalBuild": "1",
			"system":           graph.System,
		},
	}
	resultATerm := []byte(resultDrv.Format())

	if err := nixstore.RegisterGraph(o.Remote, "go-source", sourceDirs, drvItems); err != nil {
		return nil, fmt.Errorf("register derivations: %w", err)
	}
	return resultATerm, nil
}
