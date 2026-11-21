package softdelete_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/mixin/softdelete"
)

// adoc lists softdelete.Mixin with Assign, which sets deleted_by from the
// context; udoc lists it with Assign and Reads: Unfiltered. Both bind it as
// velox-gen does: the Descriptor names a schema value whose Mixin method
// returns the configured mixin.
type adoc struct {
	ID        int64
	Title     string
	DeletedBy *int64
	DeletedAt *time.Time
}

type udoc adoc

type userKey struct{}

var errNoUser = errors.New("no user in the context")

// deletedBy is the Assign of both: override when set, else the user in
// ctx, or errNoUser. calls counts its runs.
var (
	calls    int
	override []softdelete.Set
)

func deletedBy(ctx context.Context) ([]softdelete.Set, error) {
	calls++
	if override != nil {
		return override, nil
	}
	u, ok := ctx.Value(userKey{}).(int)
	if !ok {
		return nil, errNoUser
	}
	return []softdelete.Set{{Column: "deleted_by", Value: u}}, nil // an int, converted to int64
}

type adocSchema struct{}

func (adocSchema) Mixin() []schema.Mixin {
	return []schema.Mixin{softdelete.Mixin{Assign: deletedBy}}
}

type udocSchema struct{}

func (udocSchema) Mixin() []schema.Mixin {
	return []schema.Mixin{softdelete.Mixin{Assign: deletedBy, Reads: softdelete.Unfiltered}}
}

var (
	adocMeta velox.MetaOnce[adoc]
	udocMeta velox.MetaOnce[udoc]
	aID      = velox.NumberCol[adoc, int64]{I: 0}
	uID      = velox.NumberCol[udoc, int64]{I: 0}
	uTitle   = velox.StringCol[udoc, string]{I: 1}
	uDelAt   = velox.NullTimeCol[udoc, time.Time]{I: 3}
)

var docColumns = []velox.ColumnDescriptor{
	{Field: "ID", Name: "id"}, {Field: "Title", Name: "title"},
	{Field: "DeletedBy", Name: "deleted_by"}, {Field: "DeletedAt", Name: "deleted_at"},
}

func (*adoc) VeloxMeta() *velox.Meta[adoc] {
	return adocMeta.Load(func() velox.Descriptor[adoc] {
		return velox.Descriptor[adoc]{
			Table: "a_docs", Columns: docColumns, PrimaryKey: []int{0}, Schema: adocSchema{},
			FieldPtr: func(x *adoc, i int) any { return [...]any{&x.ID, &x.Title, &x.DeletedBy, &x.DeletedAt}[i] },
		}
	})
}

func (*udoc) VeloxMeta() *velox.Meta[udoc] {
	return udocMeta.Load(func() velox.Descriptor[udoc] {
		return velox.Descriptor[udoc]{
			Table: "u_docs", Columns: docColumns, PrimaryKey: []int{0}, Schema: udocSchema{},
			FieldPtr: func(x *udoc, i int) any { return [...]any{&x.ID, &x.Title, &x.DeletedBy, &x.DeletedAt}[i] },
		}
	})
}

// openDocs returns a SQLite DB with a_docs and u_docs, each with live rows
// 1 and 2.
func openDocs(t *testing.T) (*velox.DB, *sql.DB, *sqlLog) {
	t.Helper()
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "opts.db"))
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	for _, tbl := range []string{"a_docs", "u_docs"} {
		for _, s := range []string{
			`CREATE TABLE "` + tbl + `" ("id" INTEGER PRIMARY KEY, "title" TEXT NOT NULL, "deleted_by" INTEGER, "deleted_at" DATETIME)`,
			`INSERT INTO "` + tbl + `" ("id", "title") VALUES (1, 'a'), (2, 'b')`,
		} {
			if _, err := raw.Exec(s); err != nil {
				t.Fatal(err)
			}
		}
	}
	log := &sqlLog{}
	return velox.Open(velox.SQLite, raw, velox.WithAround(log.around)), raw, log
}

// by is the deleted_by and whether deleted_at is set of row id of tbl.
func by(t *testing.T, raw *sql.DB, tbl string, id int64) (deletedBy sql.NullInt64, deleted bool) {
	t.Helper()
	var at sql.NullString
	if err := raw.QueryRow(`SELECT "deleted_by", "deleted_at" FROM "`+tbl+`" WHERE "id" = ?`, id).Scan(&deletedBy, &at); err != nil {
		t.Fatal(err)
	}
	return deletedBy, at.Valid
}

func asUser(id int) context.Context { return context.WithValue(context.Background(), userKey{}, id) }

// TestAssignInTheSameUpdate: Assign's columns go into the one UPDATE that
// sets deleted_at, with the value converted to the column's type.
func TestAssignInTheSameUpdate(t *testing.T) {
	db, raw, log := openDocs(t)
	sent := len(log.sql)
	n, err := db.Delete[adoc]().Where(aID.EQ(1)).Exec(asUser(7))
	if err != nil || n != 1 {
		t.Fatalf("Delete: %d, %v", n, err)
	}
	if len(log.sql)-sent != 1 {
		t.Errorf("%d statements, want one UPDATE: %q", len(log.sql)-sent, log.sql[sent:])
	}
	if want := `UPDATE "a_docs" SET "deleted_at" = ?, "deleted_by" = ? WHERE "id" = ? AND "deleted_at" IS NULL`; log.last() != want {
		t.Errorf("SQL\n got %s\nwant %s", log.last(), want)
	}
	if u, del := by(t, raw, "a_docs", 1); !del || !u.Valid || u.Int64 != 7 {
		t.Errorf("row 1: deleted_by %v, deleted %v; want 7, true", u, del)
	}
	// A filtered read hides it, as without Assign.
	if c, err := db.Query[adoc]().Count(context.Background()); err != nil || c != 1 {
		t.Errorf("Count: %d, %v; want 1", c, err)
	}
}

// TestAssignFailsTheDelete: an error from Assign, an unknown column, and
// deleted_at or the key among its columns each fail the delete before anything is
// sent. Under Skip, Assign does not run and the row goes.
func TestAssignFailsTheDelete(t *testing.T) {
	db, raw, log := openDocs(t)
	sent := len(log.sql)
	if _, err := db.Delete[adoc]().Where(aID.EQ(1)).Exec(context.Background()); !errors.Is(err, errNoUser) {
		t.Errorf("no user: %v, want errNoUser", err)
	}
	if len(log.sql) != sent {
		t.Errorf("sent %q", log.sql[sent:])
	}
	if _, del := by(t, raw, "a_docs", 1); del {
		t.Error("row 1 deleted after Assign failed")
	}
	for _, c := range []struct {
		set  softdelete.Set
		want string
	}{
		{softdelete.Set{Column: "nope", Value: 1}, `no column "nope"`},
		{softdelete.Set{Column: softdelete.Column, Value: time.Now()}, "which the mixin sets"},
		{softdelete.Set{Column: "title", Value: nil}, "not nullable"},
		{softdelete.Set{Column: "id", Value: 99}, "a primary-key column"},
	} {
		override = []softdelete.Set{c.set}
		_, err := db.Delete[adoc]().Where(aID.EQ(1)).Exec(asUser(7))
		override = nil
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v, want %q", c.set, err, c.want)
		}
	}
	before := calls
	if n, err := db.Delete[adoc]().Where(aID.EQ(2)).Exec(softdelete.Skip(context.Background())); err != nil || n != 1 {
		t.Errorf("Skip: %d, %v", n, err)
	}
	if calls != before {
		t.Error("Assign ran under Skip")
	}
	var left int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM "a_docs"`).Scan(&left); err != nil || left != 1 {
		t.Errorf("%d rows left, %v; want 1", left, err)
	}
}

// TestUnfiltered: reads see deleted rows, an update reaches them (a restore
// is a plain update), and a delete stays soft and marks live rows only, so
// a second delete affects 0 rows and keeps the first one's time and user.
func TestUnfiltered(t *testing.T) {
	db, raw, log := openDocs(t)
	if n, err := db.Delete[udoc]().Where(uID.EQ(1)).Exec(asUser(7)); err != nil || n != 1 {
		t.Fatalf("Delete: %d, %v", n, err)
	}
	if want := `UPDATE "u_docs" SET "deleted_at" = ?, "deleted_by" = ? WHERE "id" = ? AND "deleted_at" IS NULL`; log.last() != want {
		t.Errorf("SQL\n got %s\nwant %s", log.last(), want)
	}
	ctx := context.Background()
	if c, err := db.Query[udoc]().Count(ctx); err != nil || c != 2 {
		t.Errorf("Count: %d, %v; want both rows", c, err)
	}
	if c, err := db.Query[udoc]().Where(uDelAt.NotNull()).Count(ctx); err != nil || c != 1 {
		t.Errorf("deleted: %d, %v; want 1", c, err)
	}
	var first string
	if err := raw.QueryRow(`SELECT "deleted_at" FROM "u_docs" WHERE "id" = 1`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if n, err := db.Delete[udoc]().Where(uID.EQ(1)).Exec(asUser(8)); err != nil || n != 0 {
		t.Errorf("second delete: %d, %v; want 0", n, err)
	}
	var again string
	if err := raw.QueryRow(`SELECT "deleted_at" FROM "u_docs" WHERE "id" = 1`).Scan(&again); err != nil || again != first {
		t.Errorf("deleted_at moved from %s to %s (%v)", first, again, err)
	}
	if u, _ := by(t, raw, "u_docs", 1); u.Int64 != 7 {
		t.Errorf("deleted_by %v, want the first delete's 7", u)
	}
	// All marks the live row only.
	if n, err := db.Delete[udoc]().All().Exec(asUser(9)); err != nil || n != 1 {
		t.Errorf("Delete All: %d, %v; want the live row", n, err)
	}
	// A restore is a plain update.
	if n, err := db.Update[udoc]().Where(uID.EQ(1)).Set(uDelAt.SetNull(), uTitle.Set("back")).Exec(ctx); err != nil || n != 1 {
		t.Errorf("restore: %d, %v", n, err)
	}
	if _, del := by(t, raw, "u_docs", 1); del {
		t.Error("row 1 still deleted after the restore")
	}
}

// TestRecordAssign: through a recording handle, a soft delete's Change, a
// delete marked Soft, holds the row after the update, whose Assign columns
// differ from the row before it beside deleted_at. Change.Diff is nil for a
// delete, so velox.Diff compares the two rows.
func TestRecordAssign(t *testing.T) {
	db, _, _ := openDocs(t)
	var rec velox.Recorder
	if n, err := db.Record(&rec).Delete[adoc]().Where(aID.EQ(1)).Exec(asUser(7)); err != nil || n != 1 {
		t.Fatalf("Delete: %d, %v", n, err)
	}
	cs := slices.Collect(rec.All())
	if len(cs) != 1 || cs[0].Op != velox.OpDelete || !cs[0].Soft || cs[0].Key != int64(1) {
		t.Fatalf("Changes %+v, want one soft delete of row 1", cs)
	}
	old, cur, ok := velox.RowsOf[adoc](cs[0])
	if !ok || old == nil || cur == nil {
		t.Fatalf("rows %v, %v, %v; want both", old, cur, ok)
	}
	var names []string
	for _, f := range velox.Diff(old, cur) {
		names = append(names, f.Name)
		if f.Name == "deleted_by" && (f.Old != nil || f.New != int64(7)) {
			t.Errorf("deleted_by: %v -> %v, want nil -> 7", f.Old, f.New)
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"deleted_at", "deleted_by"}) {
		t.Errorf("changed fields %v, want deleted_at and deleted_by", names)
	}
}
