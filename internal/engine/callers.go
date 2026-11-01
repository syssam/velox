package engine

import (
	"cmp"
	"path"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

// WithCallers records each statement's call site in Statement.Caller. It
// costs one runtime.Callers per batch of up to 8 frames between velox and the
// call site, usually one per statement, and nothing more once a call site's
// program counters are cached: each is resolved once per DB, for up to 4,096
// of them.
func WithCallers() Option {
	return Option{func(c *config) {
		if c.Callers == nil {
			c.Callers = &callers{}
		}
	}}
}

// WithCallerSkip names packages, by import path, whose frames are not the
// caller either, such as the application's own repository layer: the call
// site is then the frame that called into them. It implies WithCallers.
func WithCallerSkip(pkgPath ...string) Option {
	return Option{func(c *config) {
		if c.Callers == nil {
			c.Callers = &callers{}
		}
		c.Callers.skip = append(c.Callers.skip, pkgPath...)
	}}
}

// callers finds statements' call sites for one DB, and caches what each
// program counter resolves to.
type callers struct {
	skip []string // package paths that are not the caller, besides velox's own

	once sync.Once
	self string   // the root package's path, from a frame of velox's own
	mods []string // module paths from the build info, longest first
	main string   // BuildInfo.Path: the import path of package main

	// depth is how many frames the last call site was down the stack, so the
	// next unwinding reads about that many; unwinding costs about 60 ns a
	// frame, skipped ones included. 0: not yet known.
	depth atomic.Int32

	mu  sync.RWMutex
	pcs map[uintptr]pcFrame // at most maxPCs entries
}

// pcFrame is what a program counter resolves to.
type pcFrame struct {
	caller Caller
	app    bool // a frame at this PC (inlined ones included) is outside velox and skip
}

// maxPCs bounds a DB's cache of program counters. Past it, a counter is
// resolved each time, as the render cache renders past its bound.
var maxPCs = 4096

// load reads, once, what resolving frames needs: velox's package path and
// the modules in the build.
func (c *callers) load() {
	pc, _, _, _ := runtime.Caller(0)
	c.self = strings.TrimSuffix(pkgOf(runtime.FuncForPC(pc).Name()), "/internal/engine")
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	c.main = bi.Path
	if bi.Main.Path != "" {
		c.mods = append(c.mods, bi.Main.Path)
	}
	for _, d := range bi.Deps {
		c.mods = append(c.mods, d.Path)
	}
	// Longest first, so that a nested module wins over the one around it.
	slices.SortStableFunc(c.mods, func(a, b string) int { return cmp.Compare(len(b), len(a)) })
}

// capture returns the call site of the statement being run: the first frame,
// within 64, outside velox and skip; zero when there is none. The first batch
// is as many frames as the last call site was down (4 to 8, 4 at first), the
// next ones 8, the last cut to end at the 64th.
func (c *callers) Capture() Caller {
	c.once.Do(c.load)
	var pcs [8]uintptr
	size := int(c.depth.Load())
	if size < 4 || size > len(pcs) {
		size = 4
	}
	// Skip runtime.Callers and capture; the frames above are velox's.
	for skip := 2; skip < 2+64; {
		n := runtime.Callers(skip, pcs[:size])
		if i, caller, ok := c.find(pcs[:n]); ok {
			if d := max(min(skip-2+i+1, len(pcs)), 4); d != int(c.depth.Load()) {
				c.depth.Store(int32(d))
			}
			return caller
		}
		if n < size {
			break
		}
		skip += size
		// The last batch stops at the 64th frame.
		size = min(len(pcs), 2+64-skip)
	}
	return Caller{}
}

// find returns the index and call site of the first of pcs outside velox and
// skip, taking the read lock once for all the counters already cached.
func (c *callers) find(pcs []uintptr) (int, Caller, bool) {
	i := 0
	c.mu.RLock()
	for ; i < len(pcs); i++ {
		f, ok := c.pcs[pcs[i]]
		if !ok {
			break
		}
		if f.app {
			c.mu.RUnlock()
			return i, f.caller, true
		}
	}
	c.mu.RUnlock()
	for ; i < len(pcs); i++ { // from the first counter not cached
		if f := c.frame(pcs[i]); f.app {
			return i, f.caller, true
		}
	}
	return 0, Caller{}, false
}

// frame returns what pc resolves to, from the cache when it is there.
func (c *callers) frame(pc uintptr) pcFrame {
	c.mu.RLock()
	f, ok := c.pcs[pc]
	c.mu.RUnlock()
	if ok {
		return f
	}
	f = c.resolve(pc)
	c.mu.Lock()
	if len(c.pcs) < maxPCs {
		if c.pcs == nil {
			c.pcs = make(map[uintptr]pcFrame)
		}
		c.pcs[pc] = f
	}
	c.mu.Unlock()
	return f
}

// resolve finds the first of pc's frames, inlined ones included, that is
// outside velox and skip.
func (c *callers) resolve(pc uintptr) pcFrame {
	frames := runtime.CallersFrames([]uintptr{pc})
	for {
		fr, more := frames.Next()
		if fr.Function != "" && !c.ours(fr.Function) {
			return pcFrame{caller: Caller{File: relFile(fr.File, fr.Function, c.main, c.mods), Line: fr.Line, Func: fr.Function}, app: true}
		}
		if !more {
			return pcFrame{}
		}
	}
}

// ours reports whether fn is velox's or in a package skip names.
func (c *callers) ours(fn string) bool {
	return inPkg(fn, c.self) || inPkg(fn, c.self+"/internal/engine") || slices.ContainsFunc(c.skip, func(pkg string) bool { return inPkg(fn, pkg) })
}

// pkgOf returns the package path of a function name as the runtime gives it:
// up to the first '.' after the last '/', ignoring a generic instantiation's
// brackets, whose type names hold both. The linker writes a '.' in the last
// path element as "%2e"; pkgOf unescapes it.
func pkgOf(fn string) string {
	if i := strings.IndexByte(fn, '['); i >= 0 {
		fn = fn[:i]
	}
	slash := strings.LastIndexByte(fn, '/')
	if dot := strings.IndexByte(fn[slash+1:], '.'); dot >= 0 {
		fn = fn[:slash+1+dot]
	}
	if strings.Contains(fn, "%2e") {
		fn = strings.ReplaceAll(fn, "%2e", ".")
	}
	return fn
}

// inPkg reports whether fn is a function of package pkg.
func inPkg(fn, pkg string) bool { return pkgOf(fn) == pkg }

// relFile returns file, the source of a frame in function fn, relative to
// the root of the module that holds fn's package: the package's directory in
// the module, then file's base name. That needs no part of file but the base,
// so it serves -trimpath names (module path, @version) and full paths alike.
// mainPkg is the import path of package main, and mods the module paths,
// longest first. Without a module, it is the base name.
func relFile(file, fn, mainPkg string, mods []string) string {
	base := path.Base(file)
	pkg := pkgOf(fn)
	if pkg == "main" {
		pkg = mainPkg
	}
	// An external test package is in the directory of the one it tests.
	pkg = strings.TrimSuffix(pkg, "_test")
	for _, m := range mods {
		if pkg == m {
			return base
		}
		if dir, ok := strings.CutPrefix(pkg, m+"/"); ok {
			return dir + "/" + base
		}
	}
	return base
}
