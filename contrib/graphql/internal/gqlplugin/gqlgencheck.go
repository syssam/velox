package gqlplugin

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// gqlgenxPath is the import path of gqlgen's adapter, whose package
// registers the reader generated code needs (decision 0173).
const gqlgenxPath = "github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"

// gqlgenConfigs are the names gqlgen reads its configuration from, in its
// order.
var gqlgenConfigs = []string{".gqlgen.yml", "gqlgen.yml", "gqlgen.yaml"}

// checkGQLGenConfig refuses a gqlgen configuration that binds nothing from
// gqlgenx, so that a server built from it would not link gqlgenx and its
// reader: there generated code would read every column and batch nothing
// (decision 0173). The configuration is the first file of gqlgenConfigs in
// the generated package's directory or a directory above it, up to its
// module's root: the module is the one that holds dir, the directory
// velox-graphql runs in, and the package's directory is outPath below it.
// With no module above dir, an outPath outside its module, or no
// configuration found, it checks nothing.
func checkGQLGenConfig(dir, outPath string) error {
	root, modPath, ok := moduleOf(dir)
	if !ok {
		return nil
	}
	var rel string
	switch {
	case outPath == modPath:
	case strings.HasPrefix(outPath, modPath+"/"):
		rel = strings.TrimPrefix(outPath, modPath+"/")
	default:
		return nil
	}
	for d := filepath.Join(root, filepath.FromSlash(rel)); ; d = filepath.Dir(d) {
		for _, name := range gqlgenConfigs {
			path := filepath.Join(d, name)
			src, err := os.ReadFile(path)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return fmt.Errorf("velox-graphql: %w", err)
			}
			if bindsGQLGenx(src) {
				return nil
			}
			return fmt.Errorf("velox-graphql: %s binds nothing from %s, so a server built from it does not link the reader that gives generated code its fields and batches its edges, and every edge would read one row at a time: bind ID to %s.Int64ID (or Time to %s.Time), or import %q blank in the server's package and give velox-graphql the option gqlgenx=blank", path, gqlgenxPath, gqlgenxPath, gqlgenxPath, gqlgenxPath)
		}
		if d == root || filepath.Dir(d) == d {
			return nil
		}
	}
}

// moduleOf is the root and the module path of the module that holds dir.
func moduleOf(dir string) (root, path string, ok bool) {
	for d := dir; ; d = filepath.Dir(d) {
		if src, err := os.ReadFile(filepath.Join(d, "go.mod")); err == nil {
			p := modulePath(src)
			return d, p, p != ""
		}
		if filepath.Dir(d) == d {
			return "", "", false
		}
	}
}

// bindsGQLGenx reports whether a gqlgen configuration names gqlgenx's
// package, or a name in it, outside a comment: a model, an autobind entry
// or a resolver's package.
func bindsGQLGenx(src []byte) bool {
	sc := bufio.NewScanner(bytes.NewReader(src))
	sc.Buffer(nil, len(src)+1)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, "#"); i >= 0 && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			line = line[:i]
		}
		for rest := line; ; {
			i := strings.Index(rest, gqlgenxPath)
			if i < 0 {
				break
			}
			rest = rest[i+len(gqlgenxPath):]
			if rest == "" || rest[0] != '/' && !isIdent(rest[0]) {
				return true
			}
		}
	}
	return false
}

// isIdent reports whether c may continue an import path's last element.
func isIdent(c byte) bool {
	return c == '_' || c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// modulePath is the path a go.mod's module directive names, or "".
func modulePath(src []byte) string {
	for line := range strings.Lines(string(src)) {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "module" {
			continue
		}
		if p, err := strconv.Unquote(f[1]); err == nil {
			return p
		}
		return f[1]
	}
	return ""
}
