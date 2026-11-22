package migrate_test

import (
	"errors"
	"maps"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// TestRebase: the files named move, in their order, to the versions after the
// others, as they are; velox.sum is rebuilt though it was in conflict, and
// the result reads. A path that is not a file of the directory, and a
// schema.json that does not read, are refused.
func TestRebase(t *testing.T) {
	t.Parallel()
	d := velox.SQLite
	fsys := dirOf(t, d,
		files("0001_a.sql", "CREATE TABLE a (id integer PRIMARY KEY);\n"),
		files("0002_main.sql", "CREATE TABLE m (id integer PRIMARY KEY);\n"))
	fsys["0002_mine_enum.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE e (id integer PRIMARY KEY);\n")}
	fsys["0003_mine.sql"] = &fstest.MapFile{Data: []byte("-- by hand\nCREATE TABLE x (id integer PRIMARY KEY);\n")}
	fsys["velox.sum"] = &fstest.MapFile{Data: []byte("<<<<<<< HEAD\n")}
	to := mustTables(d, nil)
	out, moved, err := migrate.Rebase(fsys, []string{"0003_mine.sql", "0002_mine_enum.sql"}, to)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"0002_mine_enum.sql": "0003_mine_enum.sql", "0003_mine.sql": "0004_mine.sql"}; !maps.Equal(moved, want) {
		t.Errorf("moved %v, want %v", moved, want)
	}
	if string(out["0004_mine.sql"]) != string(fsys["0003_mine.sql"].Data) {
		t.Errorf("0004_mine.sql is %q", out["0004_mine.sql"])
	}
	result := fstest.MapFS{}
	for p, data := range out {
		result[p] = &fstest.MapFile{Data: data}
	}
	dir, err := migrate.ReadDir(result)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range dir.Files {
		got = append(got, f.Path)
	}
	if want := []string{"0001_a.sql", "0002_main.sql", "0003_mine_enum.sql", "0004_mine.sql"}; !slices.Equal(got, want) {
		t.Errorf("files %v, want %v", got, want)
	}

	for _, c := range []struct {
		paths []string
		fsys  fstest.MapFS
	}{
		{[]string{"0009_nope.sql"}, fsys},
		{nil, fsys},
		{[]string{"0003_mine.sql", "0003_mine.sql"}, fsys},
	} {
		if _, _, err := migrate.Rebase(c.fsys, c.paths, to); err == nil {
			t.Errorf("%v: no error", c.paths)
		}
	}
	conflicted := maps.Clone(fsys)
	conflicted["schema.json"] = &fstest.MapFile{Data: []byte("<<<<<<< HEAD\n")}
	_, _, err = migrate.Rebase(conflicted, []string{"0003_mine.sql"}, to)
	if e, ok := errors.AsType[*migrate.Error](err); !ok || e.Pos != "schema.json" {
		t.Errorf("a conflicted schema.json: %v", err)
	}
	// The others still share a version: the result is refused as ReadDir refuses it.
	if _, _, err := migrate.Rebase(fsys, []string{"0003_mine.sql"}, to); err == nil {
		t.Error("two files of version 2 left: no error")
	}
}
