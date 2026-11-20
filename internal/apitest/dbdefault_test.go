package apitest

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
)

// dDef has columns whose database default the schema declared (DBDefault):
// status, score and note; tag is nullable without one.
type dDef struct {
	ID     int64
	Name   string
	Status string
	Score  int64
	Note   *string
	Tag    *string
}

var dDefs = struct {
	ID    velox.NumberCol[dDef, int64]
	Name  velox.StringCol[dDef, string]
	Score velox.NumberCol[dDef, int64]
}{velox.NumberCol[dDef, int64]{I: 0}, velox.StringCol[dDef, string]{I: 1}, velox.NumberCol[dDef, int64]{I: 3}}

var dDefMeta velox.MetaOnce[dDef]

var allDialects = []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite}

func (*dDef) VeloxMeta() *velox.Meta[dDef] {
	return dDefMeta.Load(func() velox.Descriptor[dDef] {
		return velox.Descriptor[dDef]{
			Table: "ddefs",
			Columns: []velox.ColumnDescriptor{
				{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"},
				{Field: "Status", Name: "status", DBDefault: allDialects},
				{Field: "Score", Name: "score", DBDefault: allDialects},
				{Field: "Note", Name: "note", DBDefault: allDialects},
				{Field: "Tag", Name: "tag"},
			},
			PrimaryKey: []int{0},
			FieldPtr: func(x *dDef, i int) any {
				return [...]any{&x.ID, &x.Name, &x.Status, &x.Score, &x.Note, &x.Tag}[i]
			},
		}
	})
}

// dKey has a text key that the database's default fills.
type dKey struct {
	ID   string
	Name string
}

var dKeyMeta velox.MetaOnce[dKey]

func (*dKey) VeloxMeta() *velox.Meta[dKey] {
	return dKeyMeta.Load(func() velox.Descriptor[dKey] {
		return velox.Descriptor[dKey]{
			Table:      "dkeys",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id", DBDefault: allDialects}, {Field: "Name", Name: "name"}},
			PrimaryKey: []int{0},
			FieldPtr: func(x *dKey, i int) any {
				if i == 0 {
					return &x.ID
				}
				return &x.Name
			},
		}
	})
}

func defDB(t *testing.T, opts ...velox.Option) (*velox.DB, *sql.DB) {
	t.Helper()
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "defs.db"))
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	exec(t, raw, `CREATE TABLE ddefs (id INTEGER PRIMARY KEY, name TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'new',
		score INTEGER NOT NULL DEFAULT 7, note TEXT DEFAULT 'n', tag TEXT DEFAULT 'undeclared')`)
	return velox.Open(velox.SQLite, raw, opts...), raw
}

// TestInsertDatabaseDefaults: a zero value in a column with a declared
// database default leaves the column to the database, and the stored value
// is read back into the entity with RETURNING; a value, a non-nil pointer to
// a zero included, is written.
func TestInsertDatabaseDefaults(t *testing.T) {
	db, raw := defDB(t)
	a := dDef{Name: "a"}
	q := db.Insert(&a)
	got, _, err := q.SQL(ctx)
	if want := `INSERT INTO "ddefs" ("name") VALUES (?) RETURNING "id", "status", "score", "note"`; err != nil || got != want {
		t.Fatalf("SQL:\n got %s, %v\nwant %s", got, err, want)
	}
	if err := q.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if a.ID == 0 || a.Status != "new" || a.Score != 7 || a.Note == nil || *a.Note != "n" || a.Tag != nil {
		t.Errorf("read back: %+v", a)
	}

	b := dDef{Name: "b", Status: "done", Score: 3, Note: new("")}
	got, _, err = db.Insert(&b).SQL(ctx)
	if want := `INSERT INTO "ddefs" ("name", "status", "score", "note") VALUES (?, ?, ?, ?) RETURNING "id"`; err != nil || got != want {
		t.Fatalf("SQL with values:\n got %s, %v\nwant %s", got, err, want)
	}
	if err := db.Insert(&b).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var status, note string
	var score int64
	if err := raw.QueryRow("SELECT status, score, note FROM ddefs WHERE id = ?", b.ID).Scan(&status, &score, &note); err != nil {
		t.Fatal(err)
	}
	if status != "done" || score != 3 || note != "" {
		t.Errorf("stored %q %d %q, want the values given, the empty note included", status, score, note)
	}
}

// TestInsertAllDatabaseDefaults: InsertAll splits its rows into runs that
// leave the same columns to the database, so each row stores what it would
// alone, in one transaction; every row gets its key and its defaults back.
func TestInsertAllDatabaseDefaults(t *testing.T) {
	var stmts []string
	db, raw := defDB(t, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		if st.Op == velox.OpInsert {
			stmts = append(stmts, strings.Join(colNames(st), ","))
		}
		return nil
	}))
	xs := []dDef{{Name: "c"}, {Name: "d", Score: 5}, {Name: "e", Score: 6}, {Name: "f"}}
	if err := db.InsertAll(xs).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if want := []string{"name", "name,score", "name"}; !slices.Equal(stmts, want) {
		t.Errorf("statements' columns %q, want %q", stmts, want)
	}
	for i, want := range []int64{7, 5, 6, 7} {
		if xs[i].ID == 0 || xs[i].Score != want || xs[i].Status != "new" {
			t.Errorf("row %d: %+v, want its key and score %d", i, xs[i], want)
		}
	}
	if got := int64s(t, raw, "SELECT score FROM ddefs ORDER BY id"); !slices.Equal(got, []int64{7, 5, 6, 7}) {
		t.Errorf("stored scores %v", got)
	}
}

// TestInsertDatabaseDefaultsMySQL: MySQL has no RETURNING, so the columns
// the database filled are read by key after the insert, and only then.
func TestInsertDatabaseDefaultsMySQL(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1, LastID: 9, HasLastID: true, Results: [][][]any{{{"new", int64(7), "n"}}}}
	db := openFake(velox.MySQL, e)
	a := dDef{Name: "a"}
	if err := db.Insert(&a).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"INSERT INTO `ddefs` (`name`) VALUES (?)",
		"SELECT `status`, `score`, `note` FROM `ddefs` WHERE `id` = ? LIMIT ?",
	}
	if !slices.Equal(e.Log, want) {
		t.Errorf("statements:\n%s\nwant:\n%s", strings.Join(e.Log, "\n"), strings.Join(want, "\n"))
	}
	if a.ID != 9 || a.Status != "new" || a.Score != 7 || a.Note == nil || *a.Note != "n" {
		t.Errorf("read back: %+v", a)
	}

	e = &fakeconn.Exec{Affected: 1, LastID: 10, HasLastID: true}
	b := dDef{Name: "b", Status: "s", Score: 1, Note: new("x")}
	if err := openFake(velox.MySQL, e).Insert(&b).Exec(ctx); err != nil || len(e.Log) != 1 {
		t.Errorf("an insert with every value sends one statement: %v, %q", err, e.Log)
	}
}

// TestInsertDatabaseKey: a key the database's default fills comes back with
// RETURNING; MySQL, which cannot return it, refuses the insert before
// sending anything.
func TestInsertDatabaseKey(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{"k1"}}}
	k := dKey{Name: "a"}
	if err := openFake(velox.Postgres, e).Insert(&k).Exec(ctx); err != nil || k.ID != "k1" {
		t.Fatalf("Insert: %+v, %v", k, err)
	}
	if want := `INSERT INTO "dkeys" ("name") VALUES ($1) RETURNING "id"`; e.SQL != want {
		t.Errorf("SQL:\n got %s\nwant %s", e.SQL, want)
	}
	e = &fakeconn.Exec{Affected: 1}
	err := openFake(velox.MySQL, e).Insert(&dKey{Name: "a"}).Exec(ctx)
	if err == nil || !strings.Contains(err.Error(), "MySQL cannot return the key id") || len(e.Log) != 0 {
		t.Errorf("MySQL: %v, sent %q", err, e.Log)
	}
}

// TestInsertAllDoNothingReturnsNothing: an InsertAll that keeps existing
// rows on a conflict reads nothing back, as RETURNING would leave out the
// rows kept and so not match the rows sent.
func TestInsertAllDoNothingReturnsNothing(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	xs := []dDef{{ID: 1, Name: "a"}, {ID: 2, Name: "b"}}
	if err := db.InsertAll(xs).OnConflict(dDefs.ID).DoNothing().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.SQL, "RETURNING") {
		t.Errorf("SQL %s", e.SQL)
	}
}

// colNames is the names of st's columns.
func colNames(st *velox.Statement) []string {
	out := make([]string, len(st.Columns))
	for i, c := range st.Columns {
		out[i] = st.Entity.Columns[c].Name
	}
	return out
}

// TestInsertAllDatabaseDefaultsMySQL: on MySQL an InsertAll's rows take
// consecutive auto-increment keys from the first, and the columns the
// database filled come back in one SELECT by those keys.
func TestInsertAllDatabaseDefaultsMySQL(t *testing.T) {
	e := &fakeconn.Exec{Affected: 2, LastID: 20, HasLastID: true,
		Results: [][][]any{{{int64(21), "new", int64(7), "n"}, {int64(20), "new", int64(7), "m"}}}}
	xs := []dDef{{Name: "a"}, {Name: "b"}}
	if err := openFake(velox.MySQL, e).InsertAll(xs).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"INSERT INTO `ddefs` (`name`) VALUES (?), (?)",
		"SELECT `id`, `status`, `score`, `note` FROM `ddefs` WHERE `id` IN (?, ?)",
	}
	if !slices.Equal(e.Log, want) {
		t.Errorf("statements:\n%s\nwant:\n%s", strings.Join(e.Log, "\n"), strings.Join(want, "\n"))
	}
	if xs[0].ID != 20 || xs[1].ID != 21 || *xs[0].Note != "m" || *xs[1].Note != "n" || xs[1].Score != 7 {
		t.Errorf("read back: %+v %+v", xs[0], xs[1])
	}
}

// TestUpsertKeepsTheColumnsItUpdates: an upsert's update branch copies its
// columns from the inserted row, so they are written even when empty: a nil
// note overwrites the stored one with NULL, not with the column's DEFAULT.
func TestUpsertKeepsTheColumnsItUpdates(t *testing.T) {
	db, raw := defDB(t)
	exec(t, raw, "INSERT INTO ddefs (id, name, note) VALUES (1, 'a', 'x')")
	dNote := velox.NullStringCol[dDef, string]{I: 4}
	q := db.Insert(&dDef{ID: 1, Name: "a"}).OnConflict(dDefs.ID).Update(dNote)
	got, _, err := q.SQL(ctx)
	if want := `INSERT INTO "ddefs" ("id", "name", "note") VALUES (?, ?, NULL) ON CONFLICT ("id") DO UPDATE SET "note" = EXCLUDED."note" RETURNING "status", "score"`; err != nil || got != want {
		t.Fatalf("SQL:\n got %s, %v\nwant %s", got, err, want)
	}
	if err := q.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var note sql.NullString
	if err := raw.QueryRow("SELECT note FROM ddefs WHERE id = 1").Scan(&note); err != nil || note.Valid {
		t.Errorf("note %v, %v; want NULL", note, err)
	}
}
