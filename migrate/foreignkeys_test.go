package migrate_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// keyedSchema is users and posts, whose author_id references users with
// ON DELETE SET NULL and has an index of its own, on SQLite.
func keyedSchema() *migrate.Schema {
	return &migrate.Schema{Dialect: velox.SQLite, Tables: []migrate.Table{
		{
			Name:       "posts",
			Columns:    []migrate.Column{{Name: "id", Type: "integer", Identity: true}, {Name: "author_id", Type: "integer", Nullable: true}},
			PrimaryKey: []string{"id"},
			Indexes:    []migrate.Index{{Name: "posts_author_id", Columns: []string{"author_id"}}},
			ForeignKeys: []migrate.ForeignKey{{Name: "posts_users_author", Columns: []string{"author_id"},
				RefTable: migrate.TableRef{Name: "users"}, RefColumns: []string{"id"}, OnDelete: "SET NULL"}},
		},
		{
			Name:       "users",
			Columns:    []migrate.Column{{Name: "id", Type: "integer", Identity: true}},
			PrimaryKey: []string{"id"},
		},
	}}
}

// TestConfigForeignKeys: velox.json's "foreign_keys" reads and writes as
// Config.NoForeignKeys; it is written only when false, so a directory
// without it is byte for byte as before.
func TestConfigForeignKeys(t *testing.T) {
	t.Parallel()
	c := migrate.Config{Dialect: velox.Postgres, Table: "velox_migrations", LockTimeout: 5 * time.Second, NoForeignKeys: true}
	const off = "{\n  \"version\": 1,\n  \"dialect\": \"postgres\",\n  \"table\": \"velox_migrations\",\n  \"lock_timeout\": \"5s\",\n  \"foreign_keys\": false\n}\n"
	if got := string(migrate.ConfigJSON(c)); got != off {
		t.Errorf("velox.json = %q, want %q", got, off)
	}
	on := c
	on.NoForeignKeys = false
	if got := string(migrate.ConfigJSON(on)); strings.Contains(got, "foreign_keys") {
		t.Errorf("velox.json of the default names foreign_keys: %q", got)
	}

	// What Add writes reads back.
	d, err := migrate.NewDir(c)
	if err != nil {
		t.Fatal(err)
	}
	files, err := d.Add("x", mainPlan(), &migrate.Schema{Dialect: velox.Postgres})
	if err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{}
	for p, b := range files {
		fsys[p] = &fstest.MapFile{Data: b}
	}
	got, err := migrate.ReadDir(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if got.Config != c {
		t.Errorf("read back %+v, want %+v", got.Config, c)
	}

	for _, k := range []struct {
		json string
		off  bool
	}{
		{`{"version":1,"dialect":"postgres","foreign_keys":false}`, true},
		{`{"version":1,"dialect":"postgres","foreign_keys":true}`, false},
		{`{"version":1,"dialect":"postgres"}`, false},
	} {
		got, err := migrate.ReadDir(sumFS(t, map[string]string{"velox.json": k.json}))
		if err != nil {
			t.Fatalf("%s: %v", k.json, err)
		}
		if got.Config.NoForeignKeys != k.off {
			t.Errorf("%s: NoForeignKeys = %v", k.json, got.Config.NoForeignKeys)
		}
	}
	for _, bad := range []string{
		`{"version":1,"dialect":"postgres","foreign_keys":"false"}`,
		`{"version":1,"dialect":"postgres","foreign_keys":0}`,
		`{"version":1,"dialect":"postgres","foreign_keys":null}`,
	} {
		_, err := migrate.ReadDir(sumFS(t, map[string]string{"velox.json": bad}))
		if e := asError(t, err, "config"); !strings.Contains(e.Reason, "foreign_keys") {
			t.Errorf("%s: %v", bad, err)
		}
	}
}

// TestWithoutForeignKeys: the copy has no foreign key and keeps every
// index; s is not changed.
func TestWithoutForeignKeys(t *testing.T) {
	t.Parallel()
	s := keyedSchema()
	got := s.WithoutForeignKeys()
	for _, tb := range got.Tables {
		if len(tb.ForeignKeys) != 0 {
			t.Errorf("%s keeps %v", tb.Name, tb.ForeignKeys)
		}
	}
	want := keyedSchema()
	if !s.Equal(want) {
		t.Error("WithoutForeignKeys changed its receiver")
	}
	want.Tables[0].ForeignKeys = nil
	if !got.Equal(want) {
		t.Errorf("WithoutForeignKeys = %+v, want %+v", got, want)
	}
	if (*migrate.Schema)(nil).WithoutForeignKeys() != nil {
		t.Error("nil.WithoutForeignKeys() is not nil")
	}
}

// TestDirAddRefusesForeignKeys: a directory of "foreign_keys": false
// refuses a schema that has one, and takes it without.
func TestDirAddRefusesForeignKeys(t *testing.T) {
	t.Parallel()
	d, err := migrate.NewDir(migrate.Config{Dialect: velox.SQLite, NoForeignKeys: true})
	if err != nil {
		t.Fatal(err)
	}
	s := keyedSchema()
	plan, err := migrate.NewPlan(d.Snapshot, s.WithoutForeignKeys(), migrate.PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Add("x", plan, s)
	if e := asError(t, err, "directory"); !strings.Contains(e.Reason, "posts.posts_users_author") || !strings.Contains(e.Reason, `"foreign_keys": false`) {
		t.Errorf("Add: %v", err)
	}
	files, err := d.Add("x", plan, s.WithoutForeignKeys())
	if err != nil {
		t.Fatal(err)
	}
	if sql := string(files["0001_x.sql"]); strings.Contains(sql, "REFERENCES") || !strings.Contains(sql, "CREATE INDEX") {
		t.Errorf("0001_x.sql:\n%s", sql)
	}
	if strings.Contains(string(files["schema.json"]), "foreign_keys") {
		t.Errorf("schema.json:\n%s", files["schema.json"])
	}
	if !strings.Contains(string(files["velox.json"]), `"foreign_keys": false`) {
		t.Errorf("velox.json:\n%s", files["velox.json"])
	}
}

// TestDriftWithoutForeignKeys: on SQLite, a database made without foreign
// keys has no drift from a snapshot without them, and one made with them
// shows each as drift, to be dropped.
func TestDriftWithoutForeignKeys(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	open := func(s *migrate.Schema) *sql.DB {
		db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "app.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := migrate.Create(ctx, db, s); err != nil {
			t.Fatal(err)
		}
		return db
	}
	snap := keyedSchema().WithoutForeignKeys()
	r, err := migrate.Drift(ctx, open(snap), snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changes) != 0 {
		t.Errorf("drift of a database without keys: %v", r.Changes)
	}
	r, err = migrate.Drift(ctx, open(keyedSchema()), snap)
	if err != nil {
		t.Fatal(err)
	}
	if got := changeStrings(r.Changes); !slices.Equal(got, []string{"drop-foreign-key posts.posts_users_author"}) {
		t.Errorf("drift of a database with keys: %q", got)
	}
}
