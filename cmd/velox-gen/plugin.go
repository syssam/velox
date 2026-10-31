package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/syssam/velox/internal/buildinfo"
	"github.com/syssam/velox/internal/codegen"
	"github.com/syssam/velox/plugin"
)

// pluginSpec is one -plugin flag: name[:key=value[,key=value]...].
type pluginSpec struct {
	name    string            // the tool name
	options map[string]string // nil for none
	raw     string            // the flag as given, for the -check hint
}

// pluginFlag is the repeated -plugin flag, in command-line order.
type pluginFlag []pluginSpec

func (p *pluginFlag) String() string {
	raws := make([]string, len(*p))
	for i, s := range *p {
		raws[i] = s.raw
	}
	return strings.Join(raws, ",")
}

// Set parses name[:key=value[,key=value]...]. A key is not empty, has an =,
// and is given once; a value may be empty, or hold an =.
func (p *pluginFlag) Set(raw string) error {
	name, opts, hasOpts := strings.Cut(raw, ":")
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("a plugin needs a tool name")
	}
	spec := pluginSpec{name: name, raw: raw}
	if hasOpts {
		spec.options = map[string]string{}
		for opt := range strings.SplitSeq(opts, ",") {
			k, v, ok := strings.Cut(opt, "=")
			switch {
			case !ok:
				return fmt.Errorf("plugin %s: option %q is not key=value", name, opt)
			case k == "":
				return fmt.Errorf("plugin %s: option %q has no key", name, opt)
			}
			if _, dup := spec.options[k]; dup {
				return fmt.Errorf("plugin %s: option %s is given twice", name, k)
			}
			spec.options[k] = v
		}
	}
	*p = append(*p, spec)
	return nil
}

// generator is Request.Generator: velox-gen and the version of the velox
// module it is built from, "(devel)" when the build does not say.
func generator() string { return buildinfo.Generator() }

// runPlugins runs each plugin, in order, as "go tool name" in modDir with the
// request, and its options, on its standard input, and returns their files by
// path relative to the output directory, with the header of their kind in
// front, and the plugin that wrote each. Each plugin's warnings are printed to
// stderr as it answers. The run fails, naming the plugin, when a plugin cannot
// be run, exits non-zero, answers something that is not a response of
// protocol 2, reports an error, or names a path that checkPluginPath refuses,
// or a .go file that checkPluginGo refuses at goVersion, the go version of
// the output module's go.mod, or one that another plugin, or
// itself, has written. Paths that differ only
// in case are one file, or one directory, as they are on a case-insensitive
// file system; a path is not both a file and a directory. A plugin's stderr is
// shown only when it fails.
func runPlugins(specs []pluginSpec, modDir, goVersion string, req plugin.Request, env []string, stderr io.Writer) (files map[string][]byte, owners map[string]string, err error) {
	files, owners = map[string][]byte{}, map[string]string{}
	by := map[string]string{}   // folded file path → the path that took it
	dirs := map[string]string{} // folded directory path → the spelling that took it
	dirOwner := map[string]string{}
	for _, spec := range specs {
		name := spec.name
		req.Options = spec.options
		body, err := json.Marshal(req)
		if err != nil {
			return nil, nil, err
		}
		resp, err := runPlugin(name, modDir, body, env, stderr)
		if err != nil {
			return nil, nil, fmt.Errorf("plugin %s: %w", name, err)
		}
		for _, f := range resp.Files {
			if err := checkPluginPath(f.Path); err != nil {
				return nil, nil, fmt.Errorf("plugin %s: %w", name, err)
			}
			if err := checkPluginGo(f, goVersion); err != nil {
				return nil, nil, fmt.Errorf("plugin %s: %w", name, err)
			}
			if other, ok := by[fold(f.Path)]; ok {
				if other == f.Path {
					return nil, nil, fmt.Errorf("plugin %s: %s is also written by plugin %s", name, f.Path, owners[other])
				}
				return nil, nil, fmt.Errorf("plugin %s: %s is also written by plugin %s as %s: names that differ only in case are one file", name, f.Path, owners[other], other)
			}
			if other, ok := dirs[fold(f.Path)]; ok {
				return nil, nil, fmt.Errorf("plugin %s: %s: %s is a directory of plugin %s, not a file", name, f.Path, other, dirOwner[other])
			}
			for _, d := range parents(f.Path) {
				if other, ok := by[fold(d)]; ok {
					return nil, nil, fmt.Errorf("plugin %s: %s: %s is a file of plugin %s, not a directory", name, f.Path, other, owners[other])
				}
				if other, ok := dirs[fold(d)]; !ok {
					dirs[fold(d)], dirOwner[d] = d, name
				} else if other != d {
					return nil, nil, fmt.Errorf("plugin %s: %s: directory %s is also written by plugin %s as %s: names that differ only in case are one directory", name, f.Path, d, dirOwner[other], other)
				}
			}
			by[fold(f.Path)] = f.Path
			owners[f.Path] = name
			files[f.Path] = withHeader(f.Path, f.Content)
		}
	}
	return files, owners, nil
}

// parents are the directories of the slash path p, outermost first: "a" and
// "a/b" for "a/b/c.go".
func parents(p string) []string {
	var ds []string
	for i := range len(p) {
		if p[i] == '/' {
			ds = append(ds, p[:i])
		}
	}
	return ds
}

// fold is the key two paths share when a case-insensitive file system takes
// them for one.
func fold(path string) string { return strings.ToLower(path) }

// runPlugin runs one plugin and decodes its response, and prints its
// warnings. When go tool finds no tool of that name, the error says how to
// add it.
func runPlugin(name, modDir string, req []byte, env []string, stderr io.Writer) (*plugin.Response, error) {
	cmd := exec.Command("go", "tool", name)
	cmd.Dir, cmd.Env = modDir, env
	var so, se bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = bytes.NewReader(req), &so, &se
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(se.String()); msg != "" {
			text := oneLines(msg)
			if strings.Contains(msg, "no such tool") {
				text += fmt.Sprintf("\nthe module has no tool %s: add it with go get -tool %s", oneLine(name), toolPath(name))
			}
			return nil, fmt.Errorf("%w\n%s", err, text)
		}
		return nil, err
	}
	var resp plugin.Response
	if err := json.Unmarshal(so.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("malformed response: %w", err)
	}
	// Protocol 1's responses had no plugin_protocol: whatever such a plugin
	// answers, its files or its refusal of this request, it is told to update.
	switch n := resp.Protocol; {
	case n < plugin.ProtocolVersion:
		return nil, fmt.Errorf("it speaks plugin protocol %d (or does not say); velox-gen speaks %d: update the plugin", max(n, 1), plugin.ProtocolVersion)
	case n > plugin.ProtocolVersion:
		return nil, fmt.Errorf("it speaks plugin protocol %d; velox-gen speaks %d: update velox-gen", n, plugin.ProtocolVersion)
	}
	for _, w := range resp.Warnings {
		_, _ = fmt.Fprintf(stderr, "velox-gen: plugin %s: warning: %s\n", name, oneLine(w))
	}
	if resp.Error != "" {
		// A plugin's error is a line, but for the stack that follows a panic
		// under VELOX_PLUGIN_TRACE=1.
		return nil, fmt.Errorf("%s", oneLines(strings.TrimRight(resp.Error, "\r\n")))
	}
	return &resp, nil
}

// knownTools are the package paths of the plugins velox ships, by tool
// name, for the hint of a module that lacks one.
var knownTools = map[string]string{
	"velox-graphql": "github.com/syssam/velox/contrib/graphql/cmd/velox-graphql",
}

// toolPath is what to go get -tool for the plugin name: its package path when
// velox ships it, else a placeholder.
func toolPath(name string) string {
	if p, ok := knownTools[name]; ok {
		return p
	}
	return "<its package path>"
}

// oneLines is each line of s through oneLine, a carriage return before its
// newline dropped, joined by newlines, but with its tabs kept, as a stack's
// lines indent with them.
func oneLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = escape(strings.TrimSuffix(l, "\r"), true)
	}
	return strings.Join(lines, "\n")
}

// oneLine is s with each control character (newlines and tabs among them),
// each line or paragraph separator (U+2028, U+2029) and each bidirectional
// control (U+202E and its kin) escaped as in a Go string literal, so that a
// plugin's text is one line that cannot pass for velox-gen's own output,
// reorder what the terminal shows, or move its cursor.
func oneLine(s string) string { return escape(s, false) }

// escape is oneLine's, leaving tabs as they are when tabs is true.
func escape(s string, tabs bool) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\t' && tabs {
			b.WriteRune(r)
			continue
		}
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' || unicode.Is(unicode.Bidi_Control, r) {
			q := strconv.QuoteRune(r)
			b.WriteString(q[1 : len(q)-1])
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// pluginExts are the extensions of the files a plugin writes.
var pluginExts = []string{".go", ".graphqls", ".proto"}

// checkPluginPath accepts a slash path below the output directory: not
// absolute, no empty, "." or ".." segment, no backslash or NUL, no directory
// that starts with "." or "_" (the stale-file sweep skips those, and go
// ignores them) or is testdata or vendor (which go's ./... leaves out), and a
// file name ending in .go, .graphqls or .proto that is
// not velox.go in a directory below the output directory (which marks another
// run's output, which the sweep leaves alone). The path is ASCII, and a .go
// file's name is not one goIgnored reports.
func checkPluginPath(p string) error {
	bad := func(why string) error { return fmt.Errorf("invalid path %q: %s", p, why) }
	switch {
	case p == "":
		return bad("a path is not empty")
	case strings.ContainsFunc(p, func(r rune) bool { return r >= 0x80 }):
		return bad("a path is ASCII")
	case strings.HasPrefix(p, "/"):
		return bad("a path is relative to the output directory")
	case strings.ContainsAny(p, "\\\x00"):
		return bad("a path holds no backslash or NUL; it is slash-separated")
	}
	segs := strings.Split(p, "/")
	for i, seg := range segs {
		switch {
		case seg == "" || seg == "." || seg == "..":
			return bad("a path has no empty, . or .. segment; it stays below the output directory")
		case i < len(segs)-1 && (seg[0] == '.' || seg[0] == '_'):
			return bad("a directory of a path does not start with . or _")
		case i < len(segs)-1 && (seg == "testdata" || seg == "vendor"):
			return bad("a directory of a path is not testdata or vendor")
		}
	}
	base := segs[len(segs)-1]
	ext := path.Ext(base)
	switch {
	case !slices.Contains(pluginExts, ext):
		return bad("a path ends in .go, .graphqls or .proto")
	case ext == base:
		return bad("a path needs a name before the extension")
	case ext == ".go" && goIgnored(base):
		return bad("a Go file's name is not one go build ignores or builds for some systems only")
	case !filepath.IsLocal(filepath.FromSlash(p)):
		return bad("a path is a local file name")
	case len(segs) > 1 && strings.EqualFold(base, "velox.go"):
		return bad("a velox.go below the output directory marks another velox-gen run's output; name the file otherwise")
	}
	return nil
}

// goIgnored reports whether go build leaves out a .go file named base, or
// builds it only for some systems: a name that starts with _ or ., a
// _test.go file, or a _GOOS, _GOARCH or _GOOS_GOARCH suffix.
func goIgnored(base string) bool { return !goBuilds(base, "package p\n", "") }

// goBuilds reports whether go build builds, in every build, a .go file named
// base that holds src, in a module of go version goVersion
// (codegen.GoFileSystems, which also decides the user's files in the output
// directory).
func goBuilds(base, src, goVersion string) bool {
	sys, _ := codegen.GoFileSystems(base, []byte(src), goVersion)
	return sys == codegen.EverySystem
}

// checkPluginGo refuses a plugin's .go file whose build constraints, such as
// //go:build ignore, //go:build linux or //go:build prod, leave it out of
// some builds of the output module, of go version goVersion: velox-gen's
// header is a line comment, so the constraints apply. Its name is
// checkPluginPath's.
func checkPluginGo(f plugin.File, goVersion string) error {
	if path.Ext(f.Path) != ".go" || goBuilds(path.Base(f.Path), f.Content, goVersion) {
		return nil
	}
	return fmt.Errorf("invalid path %q: a Go file's name is not one go build ignores or builds for some systems only; its build constraints leave it out", f.Path)
}

// withHeader puts the header of path's kind in front of content.
func withHeader(path, content string) []byte {
	return []byte(headerFor(path) + "\n\n" + content)
}

// pluginFiles runs f's plugins on the model of gm and returns their files by
// absolute path, and the plugin that wrote each. It runs them in the module of
// the schema package at pattern.
func pluginFiles(f genFlags, gm *genModel, pattern, dir, pkg string, env []string, stderr io.Writer) (files map[string][]byte, owners map[string]string, err error) {
	if len(f.plugins) == 0 {
		return nil, nil, nil
	}
	mod, err := moduleDir(dir, pattern, env)
	if err != nil {
		return nil, nil, err
	}
	out := f.outDir(dir)
	outPath, err := dirImportPath(out, env)
	if err != nil {
		return nil, nil, err
	}
	req := plugin.Request{Protocol: plugin.ProtocolVersion, Generator: generator(), OutPackage: pkg, OutPath: outPath, Model: codegen.PluginModel(gm.graph, gm.out)}
	rel, byPlugin, err := runPlugins(f.plugins, mod, codegen.ModuleGoVersion(out), req, env, stderr)
	if err != nil {
		return nil, nil, err
	}
	files, owners = make(map[string][]byte, len(rel)), make(map[string]string, len(rel))
	for name, data := range rel {
		files[filepath.Join(out, filepath.FromSlash(name))] = data
		owners[filepath.Join(out, filepath.FromSlash(name))] = byPlugin[name]
	}
	return files, owners, nil
}

// dirImportPath is the import path of the directory out, which need not exist
// yet: its path below the module that holds it, joined to the module's path.
func dirImportPath(out string, env []string) (string, error) {
	base, rest := out, ""
	for {
		if fi, err := os.Stat(base); err == nil && fi.IsDir() {
			break
		}
		parent := filepath.Dir(base)
		if parent == base {
			return "", fmt.Errorf("the import path of %s: no directory above it exists", out)
		}
		rest, base = filepath.Join(filepath.Base(base), rest), parent
	}
	if r, err := filepath.EvalSymlinks(base); err == nil {
		base = r
	}
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Path}}\t{{.Dir}}")
	cmd.Dir = base
	if env != nil {
		cmd.Env = env
	}
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("the import path of %s: go list -m: %w\n%s", out, err, strings.TrimRight(se.String(), "\n"))
	}
	// A workspace lists several modules: the one that holds out is the
	// deepest one above it.
	best, path := "", ""
	for line := range strings.SplitSeq(so.String(), "\n") {
		mp, md, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || md == "" {
			continue
		}
		if r, err := filepath.EvalSymlinks(md); err == nil {
			md = r
		}
		if within(md, base) && len(md) > len(best) {
			best, path = md, mp
		}
	}
	if best == "" {
		return "", fmt.Errorf("the import path of %s: it is not in a module", out)
	}
	sub, err := filepath.Rel(best, filepath.Join(base, rest))
	if err != nil {
		return "", err
	}
	if sub == "." {
		return path, nil
	}
	return path + "/" + filepath.ToSlash(sub), nil
}

// within reports whether dir is root or below it.
func within(root, dir string) bool {
	rel, err := filepath.Rel(root, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// mergePluginFiles adds the plugins' files to want and refuses one at a path
// velox-gen writes itself, or below it, whatever the case of its name. owners
// names the plugin that wrote each file.
func mergePluginFiles(dir, out string, want, files map[string][]byte, owners map[string]string) error {
	mine := make(map[string]string, len(want)) // folded path → the path velox-gen writes
	for p := range want {
		mine[fold(p)] = p
	}
	for _, p := range slices.Sorted(maps.Keys(files)) {
		for q := p; within(out, q) && q != out; q = filepath.Dir(q) {
			if own, ok := mine[fold(q)]; ok {
				return fmt.Errorf("plugin %s: %s collides with %s, which velox-gen writes", owners[p], relPath(dir, p), relPath(dir, own))
			}
		}
	}
	maps.Copy(want, files)
	return nil
}
