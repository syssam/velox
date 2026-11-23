package codegen

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Cache holds what one velox-gen run reads more than once: the output of
// the go list and go env commands, by directory, environment and
// arguments, and the user's .go files, read and parsed once each. The
// passes of a run share one Cache, so that the schema package is listed and
// parsed once, not once per pass. A Cache is for one run: the files and
// packages it holds do not change while the run reads them. It is safe for
// concurrent use.
type Cache struct {
	mu    sync.Mutex
	fset  *token.FileSet // every file the Cache parses
	goOut map[string]goResult
	files map[string]*cachedFile // by path
	dirs  map[string]*cachedDir  // by directory
	ran   []string               // the arguments of each go command run, for tests
}

// NewCache returns an empty Cache.
func NewCache() *Cache {
	return &Cache{fset: token.NewFileSet(), goOut: map[string]goResult{}, files: map[string]*cachedFile{}, dirs: map[string]*cachedDir{}}
}

// cacheKey is the context key of a run's Cache.
type cacheKey struct{}

// WithCache returns ctx carrying a new Cache, which CacheOf returns, or ctx
// itself when it carries one already. The passes given the context read
// through one Cache.
func WithCache(ctx context.Context) context.Context {
	if c, ok := ctx.Value(cacheKey{}).(*Cache); ok && c != nil {
		return ctx
	}
	return context.WithValue(ctx, cacheKey{}, NewCache())
}

// CacheOf returns the Cache ctx carries, or a new one when it carries none.
func CacheOf(ctx context.Context) *Cache {
	if c, ok := ctx.Value(cacheKey{}).(*Cache); ok && c != nil {
		return c
	}
	return NewCache()
}

// goResult is what one go command wrote, and its error.
type goResult struct {
	stdout, stderr string
	err            error
}

// run runs go with args in dir and returns what it wrote to stdout and
// stderr. A nil env means os.Environ().
func (c *Cache) run(dir string, env []string, args ...string) (stdout, stderr string, err error) {
	return c.runContext(context.Background(), dir, env, args...)
}

// runContext is run, whose command ctx stops. The output of go list and go
// env is kept: the same command, in the same directory and environment, is
// run once. A command ctx stopped is not kept.
func (c *Cache) runContext(ctx context.Context, dir string, env []string, args ...string) (stdout, stderr string, err error) {
	memo := len(args) > 0 && (args[0] == "list" || args[0] == "env")
	key := dir + "\x00" + strings.Join(env, "\x00") + "\x01" + strings.Join(args, "\x00")
	if memo {
		c.mu.Lock()
		r, ok := c.goOut[key]
		c.mu.Unlock()
		if ok {
			return r.stdout, r.stderr, r.err
		}
	}
	var so, se bytes.Buffer
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, &so, &se
	if env != nil {
		cmd.Env = env
	}
	err = cmd.Run()
	c.mu.Lock()
	c.ran = append(c.ran, strings.Join(args, " "))
	if memo && ctx.Err() == nil {
		c.goOut[key] = goResult{so.String(), se.String(), err}
	}
	c.mu.Unlock()
	return so.String(), se.String(), err
}

// listedModuleDir is the module directory of the package pattern names,
// from a go list of it the Cache holds that succeeded, and "" when it holds
// none.
func (c *Cache) listedModuleDir(dir, pattern string, env []string) string {
	key := dir + "\x00" + strings.Join(env, "\x00") + "\x01" + strings.Join([]string{"list", listFields, pattern}, "\x00")
	c.mu.Lock()
	r, ok := c.goOut[key]
	c.mu.Unlock()
	if !ok || r.err != nil {
		return ""
	}
	pkg, err := decodeListed(r.stdout, pattern)
	if err != nil || pkg.Module == nil {
		return ""
	}
	return pkg.Module.Dir
}

// ModuleDir is the directory of the module that holds the package at
// pattern, when the Cache holds a go list of it, run in dir with env, that
// succeeded; ok is false when it holds none.
func (c *Cache) ModuleDir(dir, pattern string, env []string) (mod string, ok bool) {
	mod = c.listedModuleDir(dir, pattern, env)
	return mod, mod != ""
}

// cachedDir is a directory's entries and the go version of its module.
type cachedDir struct {
	entries   []os.DirEntry
	err       error
	goVersion string
}

// cachedFile is one .go file: its source, read once, the file go/parser
// makes of it with comments, and, when that fails, what each narrower mode
// makes of it.
type cachedFile struct {
	read    bool
	src     []byte
	readErr error
	parsed  bool
	full    *ast.File
	fullErr error
	narrow  map[parser.Mode]parsedFile
	systems map[string]systemsResult // GoFileSystems by go version
}

// parsedFile is one parse's file and error.
type parsedFile struct {
	f   *ast.File
	err error
}

// systemsResult is GoFileSystems' result.
type systemsResult struct {
	sys  Systems
	line int
}

// fullMode is the mode the Cache parses a file with first: every mode a
// pass asks for is served from it.
const fullMode = parser.ParseComments | parser.SkipObjectResolution

// dir returns the entries of the directory dir, read once.
func (c *Cache) dir(dir string) *cachedDir {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d, ok := c.dirs[dir]; ok {
		return d
	}
	des, err := os.ReadDir(dir)
	d := &cachedDir{entries: des, err: err}
	if err == nil {
		d.goVersion = ModuleGoVersion(dir)
	}
	c.dirs[dir] = d
	return d
}

// file returns the cached file at path, which it creates.
func (c *Cache) file(path string) *cachedFile {
	f, ok := c.files[path]
	if !ok {
		f = &cachedFile{}
		c.files[path] = f
	}
	return f
}

// readFile returns the source of the file at path, read once.
func (c *Cache) readFile(path string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f := c.file(path)
	if !f.read {
		f.src, f.readErr = os.ReadFile(path)
		f.read = true
	}
	return f.src, f.readErr
}

// fileSystems is GoFileSystems of the file at path, named name, which holds
// src, kept by go version.
func (c *Cache) fileSystems(path, name string, src []byte, goVersion string) (Systems, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f := c.file(path)
	if r, ok := f.systems[goVersion]; ok {
		return r.sys, r.line
	}
	sys, line := GoFileSystems(name, src, goVersion)
	if f.systems == nil {
		f.systems = map[string]systemsResult{}
	}
	f.systems[goVersion] = systemsResult{sys, line}
	return sys, line
}

// parseFile parses the file at path, whose source is src, as go/parser
// does with mode and SkipObjectResolution, into the Cache's file set. A file
// is parsed once with comments, which serves every mode: a narrower mode
// reads less of the file, but nothing the passes read differs. A mode that
// stops at the imports or the package clause is parsed as itself until the
// file is parsed whole: reading a package's name does not parse its bodies.
// When the whole parse fails the file is parsed with mode itself, whose
// result, error or not, is the one go/parser gives for mode.
func (c *Cache) parseFile(path string, src []byte, mode parser.Mode) (*ast.File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f := c.file(path)
	if !f.parsed && mode&(parser.ImportsOnly|parser.PackageClauseOnly) == 0 {
		f.full, f.fullErr = parser.ParseFile(c.fset, path, src, fullMode)
		f.parsed = true
	}
	if f.parsed && f.fullErr == nil {
		return f.full, nil
	}
	mode |= parser.SkipObjectResolution
	if r, ok := f.narrow[mode]; ok {
		return r.f, r.err
	}
	pf, err := parser.ParseFile(c.fset, path, src, mode)
	if f.narrow == nil {
		f.narrow = map[parser.Mode]parsedFile{}
	}
	f.narrow[mode] = parsedFile{pf, err}
	return pf, err
}

// parsePath reads and parses the file at path (parseFile).
func (c *Cache) parsePath(path string, mode parser.Mode) (*ast.File, error) {
	src, err := c.readFile(path)
	if err != nil {
		return nil, err
	}
	return c.parseFile(path, src, mode)
}

// userFiles parses, with mode, each non-test .go file in dir that does not
// start with velox-gen's header, and calls fn with it. Whatever system
// velox-gen runs on, it leaves out a file that no build includes
// (GoFileSystems, at the go version of dir's go.mod): //go:build ignore, or a
// name that starts with _ or .. It refuses, as a *FileError, one that only
// some builds include, such as one behind a GOOS or a custom tag: the
// generated code, built on another system or with other tags, would bind what
// the package there does not have, or would leave a policy unbound in a build
// that has it. A missing dir is no files. Each file is read and parsed once
// per Cache (parseFile); fn is given the Cache's file set.
func (c *Cache) userFiles(dir string, mode parser.Mode, fn func(fset *token.FileSet, path string, f *ast.File) error) error {
	d := c.dir(dir)
	if errors.Is(d.err, os.ErrNotExist) {
		return nil
	}
	if d.err != nil {
		return d.err
	}
	for _, de := range d.entries {
		name := de.Name()
		if de.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		src, err := c.readFile(path)
		if err != nil {
			return err
		}
		if bytes.HasPrefix(src, []byte(strings.TrimSuffix(header, "\n\n")+"\n")) {
			continue
		}
		switch sys, line := c.fileSystems(path, name, src, d.goVersion); sys {
		case NoSystem:
			continue
		case SomeSystems:
			return &FileError{Path: path, Line: line, Reason: "go leaves it out of some builds; velox-gen reads hooks and policies only from files that every build includes"}
		}
		f, err := c.parseFile(path, src, mode)
		if err != nil {
			return err
		}
		if err := fn(c.fset, path, f); err != nil {
			return err
		}
	}
	return nil
}
