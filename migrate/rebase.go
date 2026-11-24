package migrate

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"testing/fstest"
)

// Rebase moves the migration files paths of the directory fsys to the
// versions after its other files, in the order of their versions, keeping
// their names and their content, hand-written statements and all: what two
// branches that each took the next version need once they meet. It returns
// the directory as it is then, each file velox.sum covers by path with its
// content, schema.json being to and velox.sum over them, and each moved
// file's new path by its old one.
//
// velox.sum is not read, so a merge's conflict in it does not matter.
// schema.json is read for the constraints of its indexes, which Dir.Add
// carries: keep the branch's that the moved files follow, the main branch's.
// to is the schema the directory ends at, as velox-gen builds it from the
// merged schema package; the files are not compared with it, which Verify
// does on the result. When velox.json says "foreign_keys": false, to's
// foreign keys are left out, as Dir.Add needs. A path that is not a migration file of fsys, a moved
// file that does not parse, a schema.json that does not read, as one with a
// merge's conflict in it, and a result ReadDir refuses, as one whose other
// files still share a version, are an *Error of Code "directory" or the
// reader's own.
func Rebase(fsys fs.FS, paths []string, to *Schema) (map[string][]byte, map[string]string, error) {
	refuse := func(format string, args ...any) error {
		return &Error{Code: "directory", Reason: fmt.Sprintf(format, args...)}
	}
	if len(paths) == 0 {
		return nil, nil, refuse("Rebase needs a file to move")
	}
	if to == nil {
		return nil, nil, refuse("the schema is nil")
	}
	files, err := topFiles(fsys)
	if err != nil {
		return nil, nil, err
	}
	raw, ok := files["velox.json"]
	if !ok {
		return nil, nil, &Error{Code: "config", Pos: "velox.json", Reason: "the file is missing"}
	}
	cfg, err := parseConfig(raw)
	if err != nil {
		return nil, nil, err
	}
	if to.Dialect != cfg.Dialect {
		return nil, nil, refuse("the schema is %s's; the directory is %s's", to.Dialect, cfg.Dialect)
	}
	if cfg.NoForeignKeys {
		to = to.WithoutForeignKeys()
	}
	var snap Schema
	if err := snap.UnmarshalJSON(bytes.TrimPrefix(files["schema.json"], bom)); err != nil {
		return nil, nil, &Error{Code: "directory", Pos: "schema.json", Reason: "the file does not read, as after a merge's conflict: keep the main branch's and rebase again (" + err.Error() + ")"}
	}

	// The moved files, by version, and the last version of the others.
	type moving struct {
		path    string
		version uint64
		name    string
	}
	var move []moving
	for _, p := range paths {
		data, ok := files[p]
		if !ok || !fileName.MatchString(p) {
			return nil, nil, refuse("%s is not a migration file of the directory", p)
		}
		f, err := ParseFile(cfg.Dialect, p, data)
		if err != nil {
			return nil, nil, err
		}
		if slices.ContainsFunc(move, func(m moving) bool { return m.path == p }) {
			return nil, nil, refuse("%s is named twice", p)
		}
		move = append(move, moving{p, f.Version, f.Name})
	}
	slices.SortFunc(move, func(a, b moving) int { return cmp.Or(cmp.Compare(a.version, b.version), cmp.Compare(a.name, b.name)) })
	var last uint64
	for p := range files {
		if v, _, err := parseFileName(p); err == nil && !slices.ContainsFunc(move, func(m moving) bool { return m.path == p }) {
			last = max(last, v)
		}
	}

	out := maps.Clone(files)
	moved := map[string]string{}
	for i, m := range move {
		np := versionFile(last+uint64(i)+1, m.name)
		data := out[m.path]
		delete(out, m.path)
		out[np] = data
		moved[m.path] = np
	}
	snapshot, err := json.MarshalIndent(carryConstraints(&snap, to, nil), "", "  ")
	if err != nil {
		return nil, nil, err
	}
	out["schema.json"] = append(snapshot, '\n')
	result := fstest.MapFS{}
	for p, data := range out {
		result[p] = &fstest.MapFile{Data: data}
	}
	sum, err := Sum(result)
	if err != nil {
		return nil, nil, err
	}
	out["velox.sum"] = sum
	result["velox.sum"] = &fstest.MapFile{Data: sum}
	if _, err := ReadDir(result); err != nil {
		return nil, nil, err
	}
	return out, moved, nil
}
