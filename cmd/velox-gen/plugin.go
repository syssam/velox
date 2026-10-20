package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/syssam/velox2/internal/gen"
	"github.com/syssam/velox2/plugin"
)

// pluginFlag is the repeated -plugin flag: tool names, in command-line order.
type pluginFlag []string

func (p *pluginFlag) String() string { return strings.Join(*p, ",") }

func (p *pluginFlag) Set(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("a plugin needs a tool name")
	}
	*p = append(*p, name)
	return nil
}

// runPlugins runs each plugin, in order, as "go tool name" in modDir with the
// request on its standard input, and returns their files by path relative to
// the output directory, with the header of their kind in front, and the plugin
// that wrote each. The run fails, naming the plugin, when a plugin cannot be
// run, exits non-zero, answers something that is not a response, reports an
// error, or names a path that is not a file of the output directory (a bare
// name ending in .go or .graphqls), or one that another plugin has written;
// two names that differ only in case are one file, as they are on a
// case-insensitive file system. A plugin's stderr is shown only when it fails.
func runPlugins(names []string, modDir string, req plugin.Request, env []string) (files map[string][]byte, owners map[string]string, err error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, nil, err
	}
	files, owners = map[string][]byte{}, map[string]string{}
	by := map[string]string{} // folded name → the path that took it
	for _, name := range names {
		resp, err := runPlugin(name, modDir, body, env)
		if err != nil {
			return nil, nil, fmt.Errorf("plugin %s: %w", name, err)
		}
		for _, f := range resp.Files {
			if err := checkPluginPath(f.Path); err != nil {
				return nil, nil, fmt.Errorf("plugin %s: %w", name, err)
			}
			if other, ok := by[fold(f.Path)]; ok {
				if other == f.Path {
					return nil, nil, fmt.Errorf("plugin %s: %s is also written by plugin %s", name, f.Path, owners[other])
				}
				return nil, nil, fmt.Errorf("plugin %s: %s is also written by plugin %s as %s: names that differ only in case are one file", name, f.Path, owners[other], other)
			}
			by[fold(f.Path)] = f.Path
			owners[f.Path] = name
			files[f.Path] = withHeader(f.Path, f.Content)
		}
	}
	return files, owners, nil
}

// fold is the key two file names share when a case-insensitive file system
// takes them for one.
func fold(path string) string { return strings.ToLower(filepath.Base(path)) }

// runPlugin runs one plugin and decodes its response.
func runPlugin(name, modDir string, req []byte, env []string) (*plugin.Response, error) {
	cmd := exec.Command("go", "tool", name)
	cmd.Dir, cmd.Env = modDir, env
	var so, se bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = bytes.NewReader(req), &so, &se
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(se.String()); msg != "" {
			return nil, fmt.Errorf("%w\n%s", err, msg)
		}
		return nil, err
	}
	var resp plugin.Response
	if err := json.Unmarshal(so.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("malformed response: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return &resp, nil
}

// checkPluginPath accepts a bare file name, ending in .go or .graphqls, that
// stays in the output directory.
func checkPluginPath(path string) error {
	ext := filepath.Ext(path)
	switch {
	case strings.ContainsAny(path, `/\`) || path == "." || path == ".." || strings.Contains(path, "\x00"):
		return fmt.Errorf("invalid path %q: a path is a file name, with no directory", path)
	case ext != ".go" && ext != ".graphqls":
		return fmt.Errorf("invalid path %q: a path ends in .go or .graphqls", path)
	case ext == path:
		return fmt.Errorf("invalid path %q: a path needs a name before the extension", path)
	}
	return nil
}

// withHeader puts the header of path's kind in front of content.
func withHeader(path, content string) []byte {
	return []byte(headerFor(path) + "\n\n" + content)
}

// pluginFiles runs f's plugins on the model of gm and returns their files by
// absolute path, and the plugin that wrote each. It runs them in the module of
// the schema package at pattern.
func pluginFiles(f genFlags, gm *genModel, pattern, dir, pkg string, env []string) (files map[string][]byte, owners map[string]string, err error) {
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
	req := plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: pkg, OutPath: outPath, Model: gen.PluginModel(gm.graph, gm.out)}
	rel, byPlugin, err := runPlugins(f.plugins, mod, req, env)
	if err != nil {
		return nil, nil, err
	}
	files, owners = make(map[string][]byte, len(rel)), make(map[string]string, len(rel))
	for name, data := range rel {
		files[filepath.Join(out, name)] = data
		owners[filepath.Join(out, name)] = byPlugin[name]
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
// velox-gen writes itself, whatever the case of its name. owners names the
// plugin that wrote each file.
func mergePluginFiles(dir string, want, files map[string][]byte, owners map[string]string) error {
	mine := make(map[string]string, len(want)) // folded path → the path velox-gen writes
	for p := range want {
		mine[fold(p)] = p
	}
	for _, p := range slices.Sorted(maps.Keys(files)) {
		if own, ok := mine[fold(p)]; ok {
			return fmt.Errorf("plugin %s: %s collides with %s, which velox-gen writes", owners[p], relPath(dir, p), relPath(dir, own))
		}
		want[p] = files[p]
	}
	return nil
}
