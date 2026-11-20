package apitest

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/engine"
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

// dAll has no column that must be written: its key is generated and its
// status has a declared database default.
type dAll struct {
	ID     int64
	Status string
}

var dAllMeta velox.MetaOnce[dAll]

func (*dAll) VeloxMeta() *velox.Meta[dAll] {
	return dAllMeta.Load(func() velox.Descriptor[dAll] {
		return velox.Descriptor[dAll]{
			Table:      "dalls",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Status", Name: "status", DBDefault: allDialects}},
			PrimaryKey: []int{0},
			FieldPtr: func(x *dAll, i int) any {
				if i == 0 {
					return &x.ID
				}
				return &x.Status
			},
		}
	})
}

// TestInsertAllEmptyUsesDefaults: an insert that leaves every column to the
// database sends DEFAULT VALUES (PostgreSQL, SQLite) or () VALUES ()
// (MySQL), so the declared default applies, and reads it back (ADR 0093).
func TestInsertAllEmptyUsesDefaults(t *testing.T) {
	db, raw := defDB(t)
	exec(t, raw, `CREATE TABLE dalls (id INTEGER PRIMARY KEY, status TEXT NOT NULL DEFAULT 'new')`)
	a := dAll{}
	got, _, err := db.Insert(&a).SQL(ctx)
	if want := `INSERT INTO "dalls" DEFAULT VALUES RETURNING "id", "status"`; err != nil || got != want {
		t.Fatalf("SQLite SQL:\n got %s, %v\nwant %s", got, err, want)
	}
	if err := db.Insert(&a).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	xs := []dAll{{}, {}}
	if err := db.InsertAll(xs).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var stored []string
	rows, err := raw.Query("SELECT status FROM dalls ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		stored = append(stored, s)
	}
	_ = rows.Close()
	if !slices.Equal(stored, []string{"new", "new", "new"}) || a.Status != "new" || a.ID == 0 || xs[0].Status != "new" || xs[1].Status != "new" || xs[1].ID == 0 {
		t.Errorf("stored %q; entities %+v %+v", stored, a, xs)
	}

	e := &fakeconn.Exec{Rows: [][]any{{int64(3), "new"}}}
	if err := openFake(velox.Postgres, e).Insert(&dAll{}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `INSERT INTO "dalls" DEFAULT VALUES RETURNING "id", "status"`; e.SQL != want {
		t.Errorf("PostgreSQL SQL:\n got %s\nwant %s", e.SQL, want)
	}
	e = &fakeconn.Exec{Rows: [][]any{{int64(3), "new"}, {int64(4), "new"}}}
	if err := openFake(velox.Postgres, e).InsertAll([]dAll{{}, {}}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if want := []string{`INSERT INTO "dalls" ("status") VALUES (DEFAULT), (DEFAULT) RETURNING "id", "status"`}; !slices.Equal(e.Log, want) {
		t.Errorf("PostgreSQL InsertAll:\n got %q\nwant %q", e.Log, want)
	}

	e = &fakeconn.Exec{Affected: 1, LastID: 9, HasLastID: true, Results: [][][]any{{{"new"}}}}
	m := dAll{}
	if err := openFake(velox.MySQL, e).Insert(&m).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{"INSERT INTO `dalls` () VALUES ()", "SELECT `status` FROM `dalls` WHERE `id` = ? LIMIT ?"}
	if !slices.Equal(e.Log, want) || m.ID != 9 || m.Status != "new" {
		t.Errorf("MySQL: %q, %+v", e.Log, m)
	}
	e = &fakeconn.Exec{Affected: 2, LastID: 9, HasLastID: true, Results: [][][]any{{{int64(9), "new"}, {int64(10), "new"}}}}
	if err := openFake(velox.MySQL, e).InsertAll([]dAll{{}, {}}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if want := "INSERT INTO `dalls` () VALUES (), ()"; len(e.Log) == 0 || e.Log[0] != want {
		t.Errorf("MySQL InsertAll: %q, want first %q", e.Log, want)
	}
}

// TestInsertAllOneStatementWithDefaultCells: on PostgreSQL and MySQL an
// InsertAll whose rows leave different columns to the database stays one
// statement: a cell left to its default is the DEFAULT keyword, which takes
// no parameter, and every row reads its values back. SQLite, which has no
// DEFAULT in VALUES, still splits (TestInsertAllDatabaseDefaults).
func TestInsertAllOneStatementWithDefaultCells(t *testing.T) {
	alternating := func() []dDef {
		xs := make([]dDef, 4)
		for i := range xs {
			xs[i].Name = "x"
			if i%2 == 0 {
				xs[i].Score = int64(i + 1)
			}
		}
		return xs
	}
	ret := [][]any{
		{int64(1), "new", int64(1), "n"}, {int64(2), "new", int64(7), "n"},
		{int64(3), "new", int64(3), "n"}, {int64(4), "new", int64(7), "n"},
	}
	e := &fakeconn.Exec{Rows: ret}
	xs := alternating()
	if err := openFake(velox.Postgres, e).InsertAll(xs).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{`INSERT INTO "ddefs" ("name", "score") VALUES ($1, $2), ($3, DEFAULT), ($4, $5), ($6, DEFAULT) RETURNING "id", "status", "score", "note"`}
	if !slices.Equal(e.Log, want) {
		t.Errorf("PostgreSQL:\n got %q\nwant %q", e.Log, want)
	}
	if len(e.Args) != 6 {
		t.Errorf("args %v, want 6: DEFAULT takes none", e.Args)
	}
	for i, w := range []int64{1, 7, 3, 7} {
		if xs[i].ID != int64(i+1) || xs[i].Score != w || xs[i].Status != "new" {
			t.Errorf("row %d: %+v", i, xs[i])
		}
	}

	// WithMaxParams still chunks, by the rows' parameters.
	e = &fakeconn.Exec{Results: [][][]any{ret[:2], ret[2:]}}
	if err := openFake(velox.Postgres, e, engine.WithMaxParams(6)).InsertAll(alternating()).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	ins := `INSERT INTO "ddefs" ("name", "score") VALUES ($1, $2), ($3, DEFAULT) RETURNING "id", "status", "score", "note"`
	if want := []string{"BEGIN", ins, ins, "COMMIT"}; !slices.Equal(e.Log, want) {
		t.Errorf("PostgreSQL chunked:\n got %q\nwant %q", e.Log, want)
	}

	e = &fakeconn.Exec{Affected: 4, LastID: 1, HasLastID: true, Results: [][][]any{ret}}
	xs = alternating()
	if err := openFake(velox.MySQL, e).InsertAll(xs).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	want = []string{
		"INSERT INTO `ddefs` (`name`, `score`) VALUES (?, ?), (?, DEFAULT), (?, ?), (?, DEFAULT)",
		"SELECT `id`, `status`, `score`, `note` FROM `ddefs` WHERE `id` IN (?, ?, ?, ?)",
	}
	if !slices.Equal(e.Log, want) {
		t.Errorf("MySQL:\n got %q\nwant %q", e.Log, want)
	}
	if xs[1].ID != 2 || xs[1].Score != 7 || xs[2].Score != 3 {
		t.Errorf("MySQL read back %+v", xs)
	}

	// The conflict and update columns are written as the rows hold them.
	e = &fakeconn.Exec{Rows: [][]any{{int64(1), "new", "n"}, {int64(2), "new", "n"}}}
	err := openFake(velox.Postgres, e).InsertAll(alternating()[:2]).OnConflict(dDefs.Name).Update(dDefs.Score).Exec(ctx)
	if want := `INSERT INTO "ddefs" ("name", "score") VALUES ($1, $2), ($3, $4) ON CONFLICT ("name") DO UPDATE SET "score" = EXCLUDED."score" RETURNING "id", "status", "note"`; err != nil || e.SQL != want {
		t.Errorf("upsert:\n got %s, %v\nwant %s", e.SQL, err, want)
	}
}

// TestUpsertConflictWhereFillsByKey: an upsert whose update branch has a
// condition (here a Rewrite hook's) returns only the rows it wrote, so the
// rows are placed by their conflict columns, never by position; a row that
// matches no entity fails the write, which rolls back.
func TestUpsertConflictWhereFillsByKey(t *testing.T) {
	rw := velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		if st.Conflict != nil {
			st.Conflict.Where = append(st.Conflict.Where, velox.Predicate(velox.Expr[dDef]("false")))
		}
		return nil
	})
	e := &fakeconn.Exec{Rows: [][]any{{int64(42), "new", int64(7), "n", "b"}}}
	xs := []dDef{{Name: "a"}, {Name: "b"}}
	if err := openFake(velox.Postgres, e, rw).InsertAll(xs).OnConflict(dDefs.Name).Update(dDefs.Name).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"BEGIN",
		`INSERT INTO "ddefs" ("name") VALUES ($1), ($2) ON CONFLICT ("name") DO UPDATE SET "name" = EXCLUDED."name" WHERE (false) RETURNING "id", "status", "score", "note", "name"`,
		"COMMIT",
	}
	if !slices.Equal(e.Log, want) {
		t.Errorf("statements:\n%s\nwant:\n%s", strings.Join(e.Log, "\n"), strings.Join(want, "\n"))
	}
	if xs[0].ID != 0 || xs[0].Status != "" || xs[1].ID != 42 || xs[1].Status != "new" || xs[1].Name != "b" {
		t.Errorf("filled %+v", xs)
	}

	// Every row back: by position, as without a condition.
	e = &fakeconn.Exec{Rows: [][]any{{int64(41), "new", int64(7), "n", "A"}, {int64(42), "new", int64(7), "n", "b"}}}
	xs = []dDef{{Name: "a"}, {Name: "b"}}
	if err := openFake(velox.Postgres, e, rw).InsertAll(xs).OnConflict(dDefs.Name).Update(dDefs.Name).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if xs[0].ID != 41 || xs[1].ID != 42 || xs[0].Name != "a" {
		t.Errorf("every row back: %+v", xs)
	}

	// A row the database returned in another form (a collation's) is not
	// guessed at.
	e = &fakeconn.Exec{Rows: [][]any{{int64(42), "new", int64(7), "n", "B"}}}
	xs = []dDef{{Name: "a"}, {Name: "b"}}
	err := openFake(velox.Postgres, e, rw).InsertAll(xs).OnConflict(dDefs.Name).Update(dDefs.Name).Exec(ctx)
	if err == nil || !strings.Contains(err.Error(), "cannot place upserted row") {
		t.Fatalf("got %v, want the placement's refusal", err)
	}
	if last := e.Log[len(e.Log)-1]; last != "ROLLBACK" || xs[0].ID != 0 || xs[1].ID != 0 {
		t.Errorf("last statement %q, entities %+v", last, xs)
	}

	// A recorded upsert, which returns every column, places its rows alike.
	e = &fakeconn.Exec{Results: [][][]any{{}, {{int64(42), "b", "new", int64(7), "n", nil}}}}
	xs = []dDef{{Name: "a"}, {Name: "b"}}
	var rec velox.Recorder
	if err := openFake(velox.Postgres, e, rw).Record(&rec).InsertAll(xs).OnConflict(dDefs.Name).Update(dDefs.Name).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if xs[0].ID != 0 || xs[1].ID != 42 || xs[1].Status != "new" || rec.Len() != 1 {
		t.Errorf("recorded: %+v, %d Changes", xs, rec.Len())
	}
}

// TestMySQLUpsertReadsBackDefaults: MySQL has no RETURNING, so a single-row
// upsert reads the stored key and the columns it left to the database by
// its conflict values, in one transaction with the upsert: the stored
// row's, whether inserted or updated.
func TestMySQLUpsertReadsBackDefaults(t *testing.T) {
	e := &fakeconn.Exec{Affected: 2, Results: [][][]any{{{int64(8), "done", int64(3), "z"}}}}
	a := dDef{Name: "a"}
	if err := openFake(velox.MySQL, e).Insert(&a).OnConflict(dDefs.Name).Update(dDefs.Name).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"BEGIN",
		"INSERT INTO `ddefs` (`name`) VALUES (?) AS new ON DUPLICATE KEY UPDATE `name` = new.`name`",
		"SELECT `id`, `status`, `score`, `note` FROM `ddefs` WHERE `name` = ? LIMIT ?",
		"COMMIT",
	}
	if !slices.Equal(e.Log, want) {
		t.Errorf("statements:\n%s\nwant:\n%s", strings.Join(e.Log, "\n"), strings.Join(want, "\n"))
	}
	if a.ID != 8 || a.Status != "done" || a.Score != 3 || a.Note == nil || *a.Note != "z" {
		t.Errorf("read back %+v", a)
	}

	// A key the caller set is not where an upsert on another column finds
	// its row: the read is by the conflict values. The key is not generated,
	// so it is not read, as PostgreSQL's RETURNING then names none.
	e = &fakeconn.Exec{Affected: 2, Results: [][][]any{{{"done", int64(3), "z"}}}}
	b := dDef{ID: 5, Name: "a"}
	if err := openFake(velox.MySQL, e).Insert(&b).OnConflict(dDefs.Name).Update(dDefs.Name).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.Log) != 4 || e.Log[2] != "SELECT `status`, `score`, `note` FROM `ddefs` WHERE `name` = ? LIMIT ?" || b.Status != "done" || b.ID != 5 {
		t.Errorf("caller's key: %q, %+v", e.Log, b)
	}
}

// TestMySQLUpsertSetsKey: a single-row upsert on MySQL that leaves no column
// to the database still reads the stored key by its conflict values, in
// one transaction with the upsert, as RETURNING gives it on PostgreSQL and
// SQLite; inside the caller's transaction it opens none.
func TestMySQLUpsertSetsKey(t *testing.T) {
	note := "n"
	row := func() dDef { return dDef{Name: "a", Status: "s", Score: 1, Note: &note} }
	e := &fakeconn.Exec{Affected: 1, Rows: [][]any{{int64(42)}}}
	a := row()
	if err := openFake(velox.MySQL, e).Insert(&a).OnConflict(dDefs.Name).Update(dDefs.Name).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	wantLog(t, e, []string{
		"BEGIN",
		"INSERT INTO `ddefs` (`name`, `status`, `score`, `note`) VALUES (?, ?, ?, ?) AS new ON DUPLICATE KEY UPDATE `name` = new.`name`",
		"SELECT `id` FROM `ddefs` WHERE `name` = ? LIMIT ?",
		"COMMIT",
	})
	if a.ID != 42 {
		t.Errorf("the entity's key %d, want 42", a.ID)
	}

	e = &fakeconn.Exec{Affected: 1, Rows: [][]any{{int64(43)}}}
	b := row()
	err := openFake(velox.MySQL, e).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		return tx.Insert(&b).OnConflict(dDefs.Name).Update(dDefs.Name).Exec(ctx)
	})
	if err != nil {
		t.Fatal(err)
	}
	wantLog(t, e, []string{
		"BEGIN",
		"INSERT INTO `ddefs` (`name`, `status`, `score`, `note`) VALUES (?, ?, ?, ?) AS new ON DUPLICATE KEY UPDATE `name` = new.`name`",
		"SELECT `id` FROM `ddefs` WHERE `name` = ? LIMIT ?",
		"COMMIT",
	})
	if b.ID != 43 {
		t.Errorf("in a transaction: the entity's key %d, want 43", b.ID)
	}
}

// TestRecordedMySQLUpsertReadsBackDefaults: a recorded single-row upsert on
// MySQL that leaves columns to their declared defaults reads them back by
// its conflict values, after the INSERT and before the read that reports
// the Change, all in one transaction; the entity and the Change's new row
// hold the stored values.
func TestRecordedMySQLUpsertReadsBackDefaults(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1, Results: [][][]any{
		{}, // the locked read: no conflicting row
		{{int64(1), "done", int64(3), "z"}},
		{{int64(1), "a", "done", int64(3), "z", nil}},
	}}
	var rec velox.Recorder
	a := dDef{Name: "a"}
	if err := openFake(velox.MySQL, e).Record(&rec).Insert(&a).OnConflict(dDefs.Name).Update(dDefs.Name).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	wantLog(t, e, []string{
		"BEGIN",
		"SELECT `id`, `name`, `status`, `score`, `note`, `tag` FROM `ddefs` WHERE `name` IN (?) FOR UPDATE",
		"INSERT INTO `ddefs` (`name`) VALUES (?) AS new ON DUPLICATE KEY UPDATE `name` = new.`name`",
		"SELECT `id`, `status`, `score`, `note` FROM `ddefs` WHERE `name` = ? LIMIT ?", // by the conflict values
		"SELECT `id`, `name`, `status`, `score`, `note`, `tag` FROM `ddefs` WHERE `name` IN (?)",
		"COMMIT",
	})
	if a.ID != 1 || a.Status != "done" || a.Score != 3 || a.Note == nil || *a.Note != "z" {
		t.Errorf("read back %+v", a)
	}
	cs := slices.Collect(rec.All())
	if len(cs) != 1 || cs[0].Op != velox.OpInsert {
		t.Fatalf("Changes %+v, want one insert", cs)
	}
	if _, cur, _ := velox.RowsOf[dDef](cs[0]); cur == nil || cur.Status != "done" || cur.Score != 3 {
		t.Errorf("the Change's new row: %+v", cur)
	}
}
