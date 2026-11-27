package nixstore

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
)

const nixBase32Chars = "0123456789abcdfghijklmnpqrsvwxyz"

// StoreDir returns the active Nix store directory, defaulting to /nix/store.
func StoreDir() string {
	if dir := os.Getenv("NIX_STORE"); filepath.IsAbs(dir) {
		return filepath.Clean(dir)
	}
	return "/nix/store"
}

func nixBase32(data []byte) string {
	outLen := (len(data)*8-1)/5 + 1
	out := make([]byte, outLen)
	for n := 0; n < outLen; n++ {
		b := uint(outLen-1-n) * 5
		i := b / 8
		j := b % 8
		c := data[i] >> j
		if int(i+1) < len(data) {
			c |= data[i+1] << (8 - j)
		}
		out[n] = nixBase32Chars[c&0x1f]
	}
	return string(out)
}

func compressHash(h []byte, size int) []byte {
	out := make([]byte, size)
	for i, b := range h {
		out[i%size] ^= b
	}
	return out
}

func makeStorePath(storeDir, typ, sha256Hex, name string) string {
	fingerprint := typ + ":sha256:" + sha256Hex + ":" + storeDir + ":" + name
	sum := sha256.Sum256([]byte(fingerprint))
	return storeDir + "/" + nixBase32(compressHash(sum[:], 20)) + "-" + name
}

// MakeTextPath computes the exact Nix store path for a text-addressed object
// (including .drv files and builtins.toFile outputs).
func MakeTextPath(storeDir, name string, text []byte, refs []string) string {
	sum := sha256.Sum256(text)
	sorted := dedupSorted(refs)
	var typ strings.Builder
	typ.WriteString("text")
	for _, ref := range sorted {
		typ.WriteByte(':')
		typ.WriteString(ref)
	}
	return makeStorePath(storeDir, typ.String(), hex.EncodeToString(sum[:]), name)
}

// MakeFixedOutputPath computes the exact Nix store path for a fixed-output
// SHA-256 object (flat when recursive is false, NAR source when true).
func MakeFixedOutputPath(storeDir, name string, recursive bool, sha256Hex string) string {
	if recursive {
		return makeStorePath(storeDir, "source", sha256Hex, name)
	}
	inner := sha256.Sum256([]byte("fixed:out:sha256:" + sha256Hex + ":"))
	return makeStorePath(storeDir, "output:out", hex.EncodeToString(inner[:]), name)
}

// HashPlaceholder returns Nix's builtins.placeholder value for outputName.
func HashPlaceholder(outputName string) string {
	sum := sha256.Sum256([]byte("nix-output:" + outputName))
	return "/" + nixBase32(sum[:])
}

// DownstreamPlaceholder returns the Nix placeholder used when a derivation
// references a floating content-addressed output of drvPath.
func DownstreamPlaceholder(drvPath, outputName string) string {
	base := filepath.Base(drvPath)
	hashPart := base[:32]
	drvName := strings.TrimSuffix(base[33:], ".drv")
	outPathName := drvName
	if outputName != "out" {
		outPathName = drvName + "-" + outputName
	}
	sum := sha256.Sum256([]byte("nix-upstream-output:" + hashPart + ":" + outPathName))
	return "/" + nixBase32(sum[:])
}

// DerivationOutput represents one entry in a Nix ATerm Derive(...) outputs list.
type DerivationOutput struct {
	Name     string
	Path     string
	HashAlgo string
	Hash     string
}

// Derivation represents a Nix ATerm derivation.
type Derivation struct {
	Outputs   []DerivationOutput
	InputDrvs map[string][]string
	InputSrcs []string
	Platform  string
	Builder   string
	Args      []string
	Env       map[string]string
}

// Format serializes the derivation in Nix's exact canonical ATerm format.
func (d Derivation) Format() string {
	var b strings.Builder
	b.Grow(1024)
	b.WriteString("Derive([")
	outputs := slices.Clone(d.Outputs)
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].Name < outputs[j].Name })
	for i, o := range outputs {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('(')
		writeATermString(&b, o.Name)
		b.WriteByte(',')
		writeATermString(&b, o.Path)
		b.WriteByte(',')
		writeATermString(&b, o.HashAlgo)
		b.WriteByte(',')
		writeATermString(&b, o.Hash)
		b.WriteByte(')')
	}
	b.WriteString("],[")
	drvPaths := make([]string, 0, len(d.InputDrvs))
	for p := range d.InputDrvs {
		drvPaths = append(drvPaths, p)
	}
	sort.Strings(drvPaths)
	for i, p := range drvPaths {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('(')
		writeATermString(&b, p)
		b.WriteString(",[")
		outs := dedupSorted(d.InputDrvs[p])
		for j, o := range outs {
			if j > 0 {
				b.WriteByte(',')
			}
			writeATermString(&b, o)
		}
		b.WriteString("])")
	}
	b.WriteString("],[")
	srcs := dedupSorted(d.InputSrcs)
	for i, s := range srcs {
		if i > 0 {
			b.WriteByte(',')
		}
		writeATermString(&b, s)
	}
	b.WriteString("],")
	writeATermString(&b, d.Platform)
	b.WriteByte(',')
	writeATermString(&b, d.Builder)
	b.WriteString(",[")
	for i, a := range d.Args {
		if i > 0 {
			b.WriteByte(',')
		}
		writeATermString(&b, a)
	}
	b.WriteString("],[")
	keys := make([]string, 0, len(d.Env))
	for k := range d.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('(')
		writeATermString(&b, k)
		b.WriteByte(',')
		writeATermString(&b, d.Env[k])
		b.WriteByte(')')
	}
	b.WriteString("])")
	return b.String()
}

// References returns the sorted store references required when registering this
// derivation via wopAddTextToStore (all inputDrvs keys plus inputSrcs).
func (d Derivation) References() []string {
	refs := make([]string, 0, len(d.InputDrvs)+len(d.InputSrcs))
	for p := range d.InputDrvs {
		refs = append(refs, p)
	}
	refs = append(refs, d.InputSrcs...)
	return dedupSorted(refs)
}

// Path computes the exact store path for this derivation given its ATerm bytes.
func (d Derivation) Path(storeDir string, aterm []byte) string {
	return MakeTextPath(storeDir, d.Env["name"]+".drv", aterm, d.References())
}

func writeATermString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(s[i])
		}
	}
	b.WriteByte('"')
}

func dedupSorted(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := slices.Clone(items)
	sort.Strings(out)
	return slices.Compact(out)
}

// Client maintains a persistent protocol-1.21 Nix daemon session so a planner
// can batch validity checks and store registrations over one handshake.
type Client struct {
	conn *os.File
	bw   *bufio.Writer
	p    *protocol
}

// Open connects to a Unix-socket Nix daemon and completes the 1.21 handshake.
func Open(remote string) (*Client, error) {
	conn, err := connect(remote)
	if err != nil {
		return nil, err
	}
	c, err := openClient(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	c.conn = conn
	return c, nil
}

type bufferedRW struct {
	io.Reader
	*bufio.Writer
}

// openClient performs the handshake on the raw connection, then wraps writes.
func openClient(rw io.ReadWriter) (*Client, error) {
	p := handshake(rw)
	if p.err != nil {
		return nil, p.err
	}
	bw := bufio.NewWriterSize(rw, 64*1024)
	p.conn = bufferedRW{rw, bw}
	return &Client{bw: bw, p: p}, nil
}

func (c *Client) flush() {
	if c.p.err == nil {
		c.p.err = c.bw.Flush()
	}
}

// Close closes the underlying daemon socket.
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// ValidPaths queries which of paths currently exist in the daemon's view.
func (c *Client) ValidPaths(paths []string) (map[string]bool, error) {
	if len(paths) == 0 {
		return map[string]bool{}, nil
	}
	p := c.p
	p.writeUint(31) // wopQueryValidPaths
	p.writeUint(uint64(len(paths)))
	requested := make(map[string]bool, len(paths))
	for _, path := range paths {
		p.writeString(path)
		requested[path] = true
	}
	c.flush()
	p.stderr()
	valid := make(map[string]bool)
	for n := p.readCount(uint64(len(requested))); n > 0 && p.err == nil; n-- {
		path := p.readString()
		if p.err == nil && (!requested[path] || valid[path]) {
			p.err = fmt.Errorf("unexpected valid path from Nix daemon: %q", path)
		}
		valid[path] = true
	}
	if p.err != nil {
		return nil, p.err
	}
	return valid, nil
}

// AddText registers a text-addressed store object (such as a .drv or toFile).
func (c *Client) AddText(name string, content []byte, refs []string) (string, error) {
	p := c.p
	sorted := dedupSorted(refs)
	p.writeUint(8) // wopAddTextToStore
	p.writeString(name)
	p.writeBytes(content)
	p.writeUint(uint64(len(sorted)))
	for _, ref := range sorted {
		p.writeString(ref)
	}
	c.flush()
	p.stderr()
	path := p.readString()
	if p.err != nil {
		return "", p.err
	}
	return path, nil
}

// AddFixedFile registers a flat fixed-output SHA-256 regular file (equivalent
// to nix-store --add-fixed sha256).
func (c *Client) AddFixedFile(name string, data []byte) (string, error) {
	return c.addNAR(name, false, func(w io.Writer) error {
		return WriteRegularNAR(w, data, false)
	})
}

// AddDirectory registers a recursive SHA-256 directory tree (equivalent to
// builtins.path { inherit path name; }).
func (c *Client) AddDirectory(name, dir string) (string, error) {
	return c.addNAR(name, true, func(w io.Writer) error {
		return WritePathNAR(w, dir)
	})
}

// AddRecursiveNAR registers a recursive SHA-256 store object streamed by writeNAR.
func (c *Client) AddRecursiveNAR(name string, writeNAR func(io.Writer) error) (string, error) {
	return c.addNAR(name, true, writeNAR)
}

func (c *Client) addNAR(name string, recursive bool, writeNAR func(io.Writer) error) (string, error) {
	p := c.p
	p.writeUint(7) // wopAddToStore (protocol 1.21)
	p.writeString(name)
	p.writeUint(1) // fixed = true
	if recursive {
		p.writeUint(1)
	} else {
		p.writeUint(0)
	}
	p.writeString("sha256")
	if p.err == nil {
		p.err = writeNAR(c.bw)
	}
	c.flush()
	p.stderr()
	path := p.readString()
	if p.err != nil {
		return "", p.err
	}
	return path, nil
}

func (p *protocol) writeBytes(b []byte) {
	p.writeUint(uint64(len(b)))
	if p.err != nil {
		return
	}
	if _, p.err = p.conn.Write(b); p.err != nil {
		return
	}
	if pad := (-len(b)) & 7; pad != 0 {
		var zeros [8]byte
		_, p.err = p.conn.Write(zeros[:pad])
	}
}

// WriteRegularNAR encodes a single regular file as a Nix Archive (NAR).
func WriteRegularNAR(w io.Writer, data []byte, executable bool) error {
	nw := &narWriter{w: w}
	nw.str("nix-archive-1")
	nw.str("(")
	nw.str("type")
	nw.str("regular")
	if executable {
		nw.str("executable")
		nw.str("")
	}
	nw.str("contents")
	nw.bytes(data)
	nw.str(")")
	return nw.err
}

// WritePathNAR encodes a filesystem path (regular file or directory tree) as a NAR.
func WritePathNAR(w io.Writer, root string) error {
	nw := &narWriter{w: w, buf: make([]byte, 32*1024)}
	nw.str("nix-archive-1")
	nw.node(root)
	return nw.err
}

type narWriter struct {
	w   io.Writer
	buf []byte
	err error
}

func (nw *narWriter) size(n uint64) {
	if nw.err != nil {
		return
	}
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], n)
	_, nw.err = nw.w.Write(b[:])
}

func (nw *narWriter) pad(n int64) {
	if nw.err != nil {
		return
	}
	if rem := (8 - n%8) % 8; rem != 0 {
		var zeros [8]byte
		_, nw.err = nw.w.Write(zeros[:rem])
	}
}

func (nw *narWriter) str(s string) {
	nw.size(uint64(len(s)))
	if nw.err == nil {
		_, nw.err = io.WriteString(nw.w, s)
	}
	nw.pad(int64(len(s)))
}

func (nw *narWriter) bytes(b []byte) {
	nw.size(uint64(len(b)))
	if nw.err == nil {
		_, nw.err = nw.w.Write(b)
	}
	nw.pad(int64(len(b)))
}

func (nw *narWriter) node(path string) {
	if nw.err != nil {
		return
	}
	st, err := os.Lstat(path)
	if err != nil {
		nw.err = err
		return
	}
	nw.str("(")
	nw.str("type")
	switch {
	case st.IsDir():
		nw.str("directory")
		entries, err := os.ReadDir(path)
		if err != nil {
			nw.err = err
			return
		}
		for _, e := range entries {
			nw.str("entry")
			nw.str("(")
			nw.str("name")
			nw.str(e.Name())
			nw.str("node")
			nw.node(filepath.Join(path, e.Name()))
			nw.str(")")
		}
	case st.Mode().IsRegular():
		nw.str("regular")
		if st.Mode()&0100 != 0 {
			nw.str("executable")
			nw.str("")
		}
		nw.str("contents")
		nw.size(uint64(st.Size()))
		f, err := os.Open(path)
		if err != nil {
			nw.err = err
			return
		}
		n, copyErr := io.CopyBuffer(nw.w, struct{ io.Reader }{f}, nw.buf)
		closeErr := f.Close()
		if copyErr != nil {
			nw.err = copyErr
			return
		}
		if closeErr != nil {
			nw.err = closeErr
			return
		}
		if n != st.Size() {
			nw.err = fmt.Errorf("path changed while writing NAR: %s", path)
			return
		}
		nw.pad(n)
	default:
		nw.err = fmt.Errorf("unsupported NAR entry: %s", path)
		return
	}
	nw.str(")")
}

// FixedFile describes a flat fixed-output file to seed into the Nix store.
type FixedFile struct {
	StorePath string
	LocalPath string
}

// DirItem describes a recursive directory snapshot to register in the Nix store.
type DirItem struct {
	StorePath string
	LocalDir  string
}

// TextItem describes a text-addressed store object (such as a .drv or toFile).
type TextItem struct {
	StorePath string
	Name      string
	Content   []byte
	Refs      []string
}

func daemonWorkers(count int) int {
	n := min(4, runtime.GOMAXPROCS(0), count)
	if n < 1 {
		return 1
	}
	return n
}

func openPool(remote string, count int) ([]*Client, error) {
	if count <= 1 {
		c, err := Open(remote)
		if err != nil {
			return nil, err
		}
		return []*Client{c}, nil
	}
	clients := make([]*Client, count)
	errs := make([]error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			clients[i], errs[i] = Open(remote)
		}(i)
	}
	wg.Wait()
	var firstErr error
	for _, err := range errs {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		for _, c := range clients {
			if c != nil {
				c.Close()
			}
		}
		return nil, firstErr
	}
	return clients, nil
}

func closePool(clients []*Client) {
	for _, c := range clients {
		if c != nil {
			c.Close()
		}
	}
}

func runParallel[T any](remote string, items []T, fn func(*Client, T) error) error {
	if len(items) == 0 {
		return nil
	}
	workers := daemonWorkers(len(items))
	clients, err := openPool(remote, workers)
	if err != nil {
		return err
	}
	defer closePool(clients)
	if workers == 1 {
		for _, item := range items {
			if err := fn(clients[0], item); err != nil {
				return err
			}
		}
		return nil
	}
	jobs := make(chan T)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Add(1)
		go func(c *Client) {
			defer wg.Done()
			for item := range jobs {
				if err := fn(c, item); err != nil {
					errs <- err
					return
				}
			}
		}(c)
	}
	var firstErr error
	for _, item := range items {
		select {
		case err := <-errs:
			if firstErr == nil {
				firstErr = err
			}
		case jobs <- item:
		}
		if firstErr != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
	close(errs)
	for err := range errs {
		if firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// AddFixedFiles seeds any missing flat fixed-output files over concurrent
// Nix daemon connections after a single ValidPaths query.
func AddFixedFiles(remote, name string, files []FixedFile) error {
	if len(files) == 0 {
		return nil
	}
	want := make([]string, len(files))
	for i, f := range files {
		want[i] = f.StorePath
	}
	valid, err := ValidPaths(remote, want)
	if err != nil {
		return fmt.Errorf("query fixed files: %w", err)
	}
	var missing []FixedFile
	for _, f := range files {
		if !valid[f.StorePath] {
			missing = append(missing, f)
		}
	}
	return runParallel(remote, missing, func(c *Client, f FixedFile) error {
		data, err := os.ReadFile(f.LocalPath)
		if err != nil {
			return err
		}
		got, err := c.AddFixedFile(name, data)
		if err != nil {
			return fmt.Errorf("seed %s: %w", name, err)
		}
		if got != f.StorePath {
			return fmt.Errorf("store path mismatch for %s: got %s, computed %s", name, got, f.StorePath)
		}
		return nil
	})
}

// AddDirectories registers any missing recursive directory snapshots over
// concurrent Nix daemon connections after a single ValidPaths query.
func AddDirectories(remote, name string, dirs []DirItem) error {
	return RegisterGraph(remote, name, dirs, nil)
}

// AddTextWaves registers any missing text-addressed objects (including .drv
// DAGs) dynamically over a persistent pool of concurrent Nix daemon connections.
func AddTextWaves(remote string, items []TextItem) error {
	return RegisterGraph(remote, "", nil, items)
}

type graphPlan struct {
	inDegree   []int
	dependents [][]int
	order      []int
}

func buildGraph(dirs []DirItem, items []TextItem) (graphPlan, error) {
	numDirs := len(dirs)
	total := numDirs + len(items)
	byPath := make(map[string]int, total)
	for i, d := range dirs {
		byPath[d.StorePath] = i
	}
	for i, it := range items {
		byPath[it.StorePath] = numDirs + i
	}
	inDegree := make([]int, total)
	dependents := make([][]int, total)
	for i, it := range items {
		node := numDirs + i
		seen := make(map[int]bool)
		for _, ref := range it.Refs {
			if dep, ok := byPath[ref]; ok && !seen[dep] {
				seen[dep] = true
				inDegree[node]++
				dependents[dep] = append(dependents[dep], node)
			}
		}
	}
	checkDeg := slices.Clone(inDegree)
	order := make([]int, 0, total)
	for i, deg := range checkDeg {
		if deg == 0 {
			order = append(order, i)
		}
	}
	for head := 0; head < len(order); head++ {
		idx := order[head]
		for _, dep := range dependents[idx] {
			checkDeg[dep]--
			if checkDeg[dep] == 0 {
				order = append(order, dep)
			}
		}
	}
	if len(order) != total {
		return graphPlan{}, fmt.Errorf("reference cycle among %d store items", total-len(order))
	}
	return graphPlan{inDegree: inDegree, dependents: dependents, order: order}, nil
}

// RegisterGraph queries validity once for all dirs and text items, then
// registers every missing item over a single persistent pool of Nix daemon
// connections as soon as all of its dependencies have been registered.
func RegisterGraph(remote, dirName string, dirs []DirItem, items []TextItem) error {
	if len(dirs) == 0 && len(items) == 0 {
		return nil
	}
	want := make([]string, 0, len(dirs)+len(items))
	for _, d := range dirs {
		want = append(want, d.StorePath)
	}
	for _, it := range items {
		want = append(want, it.StorePath)
	}
	// Open the first client to run ValidPaths, then reuse it in the pool.
	first, err := Open(remote)
	if err != nil {
		return err
	}
	valid, err := first.ValidPaths(want)
	if err != nil {
		first.Close()
		return fmt.Errorf("query store paths: %w", err)
	}
	var missingDirs []DirItem
	for _, d := range dirs {
		if !valid[d.StorePath] {
			missingDirs = append(missingDirs, d)
		}
	}
	var missingItems []TextItem
	for _, it := range items {
		if !valid[it.StorePath] {
			missingItems = append(missingItems, it)
		}
	}
	numDirs := len(missingDirs)
	total := numDirs + len(missingItems)
	if total == 0 {
		first.Close()
		return nil
	}

	gp, err := buildGraph(missingDirs, missingItems)
	if err != nil {
		first.Close()
		return err
	}

	registerNode := func(c *Client, idx int) error {
		if idx < numDirs {
			d := missingDirs[idx]
			got, err := c.AddDirectory(dirName, d.LocalDir)
			if err != nil {
				return fmt.Errorf("register %s: %w", dirName, err)
			}
			if got != d.StorePath {
				return fmt.Errorf("source path mismatch for %s: got %s, computed %s", dirName, got, d.StorePath)
			}
			return nil
		}
		it := missingItems[idx-numDirs]
		got, err := c.AddText(it.Name, it.Content, it.Refs)
		if err != nil {
			return fmt.Errorf("register %s: %w", it.Name, err)
		}
		if got != it.StorePath {
			return fmt.Errorf("store path mismatch for %s: got %s, computed %s", it.Name, got, it.StorePath)
		}
		return nil
	}

	workers := daemonWorkers(total)
	if workers == 1 {
		defer first.Close()
		for _, idx := range gp.order {
			if err := registerNode(first, idx); err != nil {
				return err
			}
		}
		return nil
	}

	extra, err := openPool(remote, workers-1)
	if err != nil {
		first.Close()
		return err
	}
	clients := append([]*Client{first}, extra...)
	defer closePool(clients)

	inDegree := gp.inDegree
	dependents := gp.dependents
	ready := make([]int, 0, total)
	for i, deg := range inDegree {
		if deg == 0 {
			ready = append(ready, i)
		}
	}
	type doneMsg struct {
		idx int
		err error
	}
	jobs := make(chan int, workers)
	done := make(chan doneMsg, workers)
	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Add(1)
		go func(c *Client) {
			defer wg.Done()
			for idx := range jobs {
				done <- doneMsg{idx: idx, err: registerNode(c, idx)}
			}
		}(c)
	}

	inFlight := 0
	var firstErr error
	for firstErr == nil && (len(ready) > 0 || inFlight > 0) {
		for len(ready) > 0 && inFlight < workers {
			idx := ready[0]
			ready = ready[1:]
			jobs <- idx
			inFlight++
		}
		if inFlight == 0 {
			break
		}
		msg := <-done
		inFlight--
		if msg.err != nil {
			firstErr = msg.err
			break
		}
		for _, dep := range dependents[msg.idx] {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				ready = append(ready, dep)
			}
		}
	}
	close(jobs)
	wg.Wait()
	return firstErr
}
