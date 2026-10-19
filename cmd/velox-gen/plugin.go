package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
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
// the output directory, with the header of their kind in front. The run fails,
// naming the plugin, when a plugin cannot be run, exits non-zero, answers
// something that is not a response, reports an error, or names a path that is
// not a file of the output directory (a bare name ending in .go or .graphqls),
// or one that another plugin has written. A plugin's stderr is shown only
// when it fails.
func runPlugins(names []string, modDir, outPkg string, m *plugin.Model, env []string) (map[string][]byte, error) {
	req, err := json.Marshal(plugin.Request{Protocol: plugin.Version, OutPackage: outPkg, Model: m})
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	by := map[string]string{} // path → the plugin that wrote it
	for _, name := range names {
		resp, err := runPlugin(name, modDir, req, env)
		if err != nil {
			return nil, fmt.Errorf("plugin %s: %w", name, err)
		}
		for _, f := range resp.Files {
			if err := checkPluginPath(f.Path); err != nil {
				return nil, fmt.Errorf("plugin %s: %w", name, err)
			}
			if other, ok := by[f.Path]; ok {
				return nil, fmt.Errorf("plugin %s: %s is also written by plugin %s", name, f.Path, other)
			}
			by[f.Path] = name
			files[f.Path] = withHeader(f.Path, f.Content)
		}
	}
	return files, nil
}

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
// absolute path. It runs them in the module of the schema package at pattern.
func pluginFiles(f genFlags, gm *genModel, pattern, dir, pkg string, env []string) (map[string][]byte, error) {
	if len(f.plugins) == 0 {
		return nil, nil
	}
	mod, err := moduleDir(dir, pattern, env)
	if err != nil {
		return nil, err
	}
	files, err := runPlugins(f.plugins, mod, pkg, gen.PluginModel(gm.graph, gm.out), env)
	if err != nil {
		return nil, err
	}
	out := f.outDir(dir)
	abs := make(map[string][]byte, len(files))
	for name, data := range files {
		abs[filepath.Join(out, name)] = data
	}
	return abs, nil
}

// mergePluginFiles adds the plugins' files to want and refuses one at a path
// velox-gen writes itself.
func mergePluginFiles(dir string, want, files map[string][]byte) error {
	for _, p := range slices.Sorted(maps.Keys(files)) {
		if _, ok := want[p]; ok {
			return fmt.Errorf("plugin file %s collides with a file velox-gen writes", relPath(dir, p))
		}
		want[p] = files[p]
	}
	return nil
}
