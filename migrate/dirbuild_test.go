package migrate_test

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// A step is one addition to a test directory.
type step struct {
	to    *migrate.Schema     // nil: no plan, only files
	opts  migrate.PlanOptions // LockTimeout 0 takes the Config's
	name  string
	files map[string]string // hand-written files added after the plan's, by path
}

// oneTable is a schema of one table t, with an integer key and the columns.
func oneTable(d velox.Dialect, cols ...migrate.Column) *migrate.Schema {
	all := append([]migrate.Column{{Name: "id", Type: "integer"}}, cols...)
	t := migrate.Table{Name: "t", Columns: all, PrimaryKey: []string{"id"}}
	switch d {
	case velox.Postgres:
		t.PrimaryKeyName = "t_pkey"
	case velox.MySQL:
		t.PrimaryKeyName = "PRIMARY"
		t.Collation = "utf8mb4_bin"
	}
	return &migrate.Schema{Dialect: d, Tables: []migrate.Table{t}}
}

// allowAll allows code for every target.
func allowAll(code string) migrate.PlanOptions {
	return migrate.PlanOptions{Allow: []migrate.Allow{{Code: code}}}
}

// rewrite is fsys with path's content edited by f and velox.sum made again.
func rewrite(t *testing.T, fsys fstest.MapFS, path string, f func(string) string) fstest.MapFS {
	t.Helper()
	out := cloneFS(fsys)
	out[path].Data = []byte(f(string(out[path].Data)))
	sum, err := migrate.Sum(out)
	if err != nil {
		t.Fatal(err)
	}
	out["velox.sum"] = &fstest.MapFile{Data: sum}
	return out
}

// cloneFS is a copy of fsys.
func cloneFS(fsys fstest.MapFS) fstest.MapFS {
	out := fstest.MapFS{}
	for p, f := range fsys {
		out[p] = &fstest.MapFile{Data: slices.Clone(f.Data)}
	}
	return out
}

// extend is a copy of fsys (nil: a new directory of dialect d) with s's plan
// and files added and velox.sum rewritten. A step whose plan has no change
// writes no plan file (Add is not called). A step's hand files are written
// with schema.json of the current model (the last plan's to, or the empty
// schema), so the next ReadDir accepts the directory.
func extend(t *testing.T, fsys fstest.MapFS, d velox.Dialect, s step) fstest.MapFS {
	t.Helper()
	var dir *migrate.Dir
	var err error
	out := cloneFS(fsys)
	if fsys == nil {
		out = fstest.MapFS{}
		dir, err = migrate.NewDir(migrate.Config{Dialect: d})
		if err == nil {
			out["velox.json"] = &fstest.MapFile{Data: migrate.ConfigJSON(dir.Config)}
		}
	} else {
		dir, err = migrate.ReadDir(out)
	}
	if err != nil {
		t.Fatal(err)
	}
	model := dir.Snapshot
	if s.to != nil {
		opts := s.opts
		if opts.LockTimeout == 0 {
			opts.LockTimeout = dir.Config.LockTimeout
		}
		plan, err := migrate.NewPlan(model, s.to, opts)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Scripts) > 0 {
			files, err := dir.Add(s.name, plan, s.to)
			if err != nil {
				t.Fatal(err)
			}
			for p, b := range files {
				out[p] = &fstest.MapFile{Data: b}
			}
			model = s.to
		}
	}
	if len(s.files) > 0 {
		for _, p := range slices.Sorted(maps.Keys(s.files)) {
			out[p] = &fstest.MapFile{Data: []byte(s.files[p])}
		}
		b, err := json.MarshalIndent(model, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		out["schema.json"] = &fstest.MapFile{Data: append(b, '\n')}
	}
	sum, err := migrate.Sum(out)
	if err != nil {
		t.Fatal(err)
	}
	out["velox.sum"] = &fstest.MapFile{Data: sum}
	return out
}

// dirOf is extend from nil, once per step.
func dirOf(t *testing.T, d velox.Dialect, steps ...step) fstest.MapFS {
	t.Helper()
	if len(steps) == 0 {
		return extend(t, nil, d, step{})
	}
	var fsys fstest.MapFS
	for _, s := range steps {
		fsys = extend(t, fsys, d, s)
	}
	return fsys
}
