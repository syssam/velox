package softdelete_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/schema/mixin/softdelete"
	"github.com/syssam/velox/scope"
)

var (
	D = fixture.SDocs
	O = fixture.SOwners
)

// sqlLog records the SQL of every statement sent.
type sqlLog struct {
	mu  sync.Mutex
	sql []string
}

func (l *sqlLog) around(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
	l.mu.Lock()
	l.sql = append(l.sql, st.SQL)
	l.mu.Unlock()
	return next(ctx)
}

func (l *sqlLog) last() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.sql) == 0 {
		return ""
	}
	return l.sql[len(l.sql)-1]
}

// open returns a SQLite DB with owners 1 (pinned doc 2) and 2 (pinned doc
// 4), and docs, all live: (id: org, owner, title) 1: 1, 1, a; 2: 1, 1, b;
// 3: 1, 2, c; 4: 2, 2, d. raw reads the tables without velox.
func open(t *testing.T) (db *velox.DB, raw *sql.DB, log *sqlLog) {
	t.Helper()
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "soft.db"))
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	for _, s := range append(slices.Clone(fixture.SoftDeleteDDL),
		`INSERT INTO "s_owners" VALUES (1, 'o1', 2), (2, 'o2', 4)`,
		`INSERT INTO "s_docs" VALUES (1, 1, 1, 'a', NULL), (2, 1, 1, 'b', NULL), (3, 1, 2, 'c', NULL), (4, 2, 2, 'd', NULL)`,
	) {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	log = &sqlLog{}
	return velox.Open(velox.SQLite, raw, velox.WithAround(log.around)), raw, log
}

// org returns a context in organization id.
func org(id int64) context.Context { return testmodel.Org.With(context.Background(), id) }

// deleted returns the ids of the rows whose deleted_at is set, read without
// velox.
func deleted(t *testing.T, raw *sql.DB) []int64 {
	t.Helper()
	rows, err := raw.Query(`SELECT "id" FROM "s_docs" WHERE "deleted_at" IS NOT NULL ORDER BY "id"`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}

// total is the number of rows in s_docs, read without velox.
func total(t *testing.T, raw *sql.DB) int64 {
	t.Helper()
	var n int64
	if err := raw.QueryRow(`SELECT COUNT(*) FROM "s_docs"`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func docIDs(ds []fixture.SDoc) []int64 {
	out := []int64{}
	for _, d := range ds {
		out = append(out, d.ID)
	}
	return out
}

func wantIDs(t *testing.T, what string, got []int64, want ...int64) {
	t.Helper()
	if want == nil {
		want = []int64{}
	}
	if !slices.Equal(got, want) {
		t.Errorf("%s: %v, want %v", what, got, want)
	}
}

func TestDeleteSetsDeletedAt(t *testing.T) {
	db, raw, log := open(t)
	n, err := db.Delete[fixture.SDoc]().Where(D.ID.EQ(1)).Exec(org(1))
	if err != nil || n != 1 {
		t.Fatalf("Delete: %d, %v; want 1", n, err)
	}
	if want := `UPDATE "s_docs" SET "deleted_at" = ? WHERE "id" = ? AND "deleted_at" IS NULL AND "org_id" = ?`; log.last() != want {
		t.Errorf("SQL\n got %s\nwant %s", log.last(), want)
	}
	wantIDs(t, "deleted", deleted(t, raw), 1)
	if total(t, raw) != 4 {
		t.Error("the delete removed a row")
	}
}

func TestDeleteAll(t *testing.T) {
	db, raw, log := open(t)
	n, err := db.Delete[fixture.SDoc]().All().Exec(org(1))
	if err != nil || n != 3 {
		t.Fatalf("Delete all: %d, %v; want org 1's 3", n, err)
	}
	if want := `UPDATE "s_docs" SET "deleted_at" = ? WHERE "deleted_at" IS NULL AND "org_id" = ?`; log.last() != want {
		t.Errorf("SQL\n got %s\nwant %s", log.last(), want)
	}
	wantIDs(t, "deleted", deleted(t, raw), 1, 2, 3)
	if _, err := db.Delete[fixture.SDoc]().Exec(org(1)); !errors.Is(err, velox.ErrNoWhere) {
		t.Errorf("a delete with neither Where nor All: %v", err)
	}
}

func TestRedeleteAffectsZero(t *testing.T) {
	db, raw, _ := open(t)
	if _, err := db.Delete[fixture.SDoc]().Where(D.ID.EQ(1)).Exec(org(1)); err != nil {
		t.Fatal(err)
	}
	var first string
	if err := raw.QueryRow(`SELECT "deleted_at" FROM "s_docs" WHERE "id" = 1`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if n, err := db.Delete[fixture.SDoc]().Where(D.ID.EQ(1)).Exec(org(1)); err != nil || n != 0 {
		t.Errorf("the second delete: %d, %v; want 0", n, err)
	}
	var again string
	if err := raw.QueryRow(`SELECT "deleted_at" FROM "s_docs" WHERE "id" = 1`).Scan(&again); err != nil {
		t.Fatal(err)
	}
	if again != first {
		t.Errorf("the second delete moved deleted_at from %s to %s", first, again)
	}
}

func TestUpdateSkipsDeleted(t *testing.T) {
	db, raw, _ := open(t)
	if _, err := db.Delete[fixture.SDoc]().Where(D.ID.EQ(1)).Exec(org(1)); err != nil {
		t.Fatal(err)
	}
	if n, err := db.Update[fixture.SDoc]().Set(D.Title.Set("x")).Where(D.ID.In(1, 2)).Exec(org(1)); err != nil || n != 1 {
		t.Errorf("Update of 1 and 2: %d, %v; want the live 2", n, err)
	}
	var title string
	if err := raw.QueryRow(`SELECT "title" FROM "s_docs" WHERE "id" = 1`).Scan(&title); err != nil || title != "a" {
		t.Errorf("the deleted row's title: %q, %v", title, err)
	}
	d := fixture.SDoc{ID: 1, OrgID: 1, OwnerID: 1, Title: "y"}
	if err := db.Save(&d, D.Title).Exec(org(1)); !velox.IsNotFound(err) {
		t.Errorf("Save of the deleted row: %v; want not found", err)
	}
}

func TestReadsHideDeleted(t *testing.T) {
	db, _, _ := open(t)
	ctx := org(1)
	if _, err := db.Delete[fixture.SDoc]().Where(D.ID.EQ(2)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	ds, err := db.Query[fixture.SDoc]().OrderBy(D.ID.Asc()).AllowUnbounded().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "Query", docIDs(ds), 1, 3)
	if n, err := db.Query[fixture.SDoc]().Count(ctx); err != nil || n != 2 {
		t.Errorf("Count: %d, %v", n, err)
	}
	if _, err := db.Query[fixture.SDoc]().Where(D.ID.EQ(2)).One(ctx); !velox.IsNotFound(err) {
		t.Errorf("One of the deleted row: %v", err)
	}

	os, err := db.Query[fixture.SOwner]().With(O.Docs.OrderBy(D.ID.Asc())).With(O.Pinned).OrderBy(O.ID.Asc()).AllowUnbounded().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	docs, _ := os[0].Docs.Get()
	wantIDs(t, "With(Docs) of owner 1", docIDs(docs), 1)
	if p, ok := os[0].Pinned.Get(); ok && p != nil {
		t.Errorf("With(Pinned) of owner 1 loaded the deleted doc %d", p.ID)
	}
	if n, err := db.Query[fixture.SOwner]().Where(O.Pinned.Has()).Count(ctx); err != nil || n != 0 {
		t.Errorf("Pinned.Has: %d, %v; want 0: owner 1's pin is deleted and owner 2's is in org 2", n, err)
	}
	if n, err := db.Query[fixture.SOwner]().Where(O.Docs.HasWith(D.Title.EQ("b"))).Count(ctx); err != nil || n != 0 {
		t.Errorf("Docs.HasWith(b): %d, %v; want 0", n, err)
	}
	joined, err := db.Query[fixture.SOwner]().Join(O.Pinned).AllowUnbounded().All(ctx)
	if err != nil || len(joined) != 0 {
		t.Errorf("Join(Pinned): %v, %v; want none", joined, err)
	}
}

func TestSkipShowsDeletedAndHardDeletes(t *testing.T) {
	db, raw, log := open(t)
	ctx := org(1)
	if _, err := db.Delete[fixture.SDoc]().Where(D.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	skip := softdelete.Skip(ctx)
	if !softdelete.Skipped(skip) || softdelete.Skipped(ctx) {
		t.Error("Skipped does not tell Skip's context")
	}
	if n, err := db.Query[fixture.SDoc]().Count(skip); err != nil || n != 3 {
		t.Errorf("Count under Skip: %d, %v; want org 1's 3", n, err)
	}
	if n, err := db.Delete[fixture.SDoc]().Where(D.ID.In(1, 2)).Exec(skip); err != nil || n != 2 {
		t.Errorf("Delete under Skip: %d, %v; want 2, the deleted row too", n, err)
	}
	if want := `DELETE FROM "s_docs" WHERE "id" IN (?, ?) AND "org_id" = ?`; log.last() != want {
		t.Errorf("SQL\n got %s\nwant %s", log.last(), want)
	}
	if total(t, raw) != 2 {
		t.Errorf("rows left: %d, want 2", total(t, raw))
	}
}

func TestRestoreUnderSkip(t *testing.T) {
	db, raw, _ := open(t)
	ctx := org(1)
	if _, err := db.Delete[fixture.SDoc]().Where(D.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := db.Update[fixture.SDoc]().Where(D.ID.EQ(1)).Set(D.DeletedAt.SetNull()).Exec(ctx); err != nil || n != 0 {
		t.Errorf("restore without Skip: %d, %v; want 0", n, err)
	}
	if n, err := db.Update[fixture.SDoc]().Where(D.ID.EQ(1)).Set(D.DeletedAt.SetNull()).Exec(softdelete.Skip(ctx)); err != nil || n != 1 {
		t.Errorf("restore under Skip: %d, %v; want 1", n, err)
	}
	wantIDs(t, "deleted", deleted(t, raw))
}

func TestSkipKeepsTenancy(t *testing.T) {
	db, _, _ := open(t)
	if _, err := db.Query[fixture.SDoc]().Count(softdelete.Skip(context.Background())); !errors.Is(err, scope.ErrNoScope) {
		t.Errorf("Skip without an org: %v; want ErrNoScope", err)
	}
	if n, err := db.Delete[fixture.SDoc]().Where(D.ID.EQ(4)).Exec(softdelete.Skip(org(1))); err != nil || n != 0 {
		t.Errorf("hard delete of org 2's doc from org 1: %d, %v; want 0", n, err)
	}
}

func TestWithoutPolicyKeepsSoftDelete(t *testing.T) {
	db, raw, _ := open(t)
	if _, err := db.Delete[fixture.SDoc]().Where(D.ID.EQ(1)).Exec(org(1)); err != nil {
		t.Fatal(err)
	}
	op, free := context.Background(), db.WithoutPolicy("audit")
	if n, err := free.Query[fixture.SDoc]().Count(op); err != nil || n != 3 {
		t.Errorf("Count under WithoutPolicy: %d, %v; want every org's 3 live rows", n, err)
	}
	if n, err := free.Delete[fixture.SDoc]().Where(D.ID.EQ(4)).Exec(op); err != nil || n != 1 {
		t.Errorf("Delete under WithoutPolicy: %d, %v", n, err)
	}
	wantIDs(t, "deleted", deleted(t, raw), 1, 4)
	if n, err := free.Query[fixture.SDoc]().Count(softdelete.Skip(op)); err != nil || n != 4 {
		t.Errorf("Count under both: %d, %v; want 4", n, err)
	}
}

func TestSoftDeleteDryRunIsErrDryRun(t *testing.T) {
	db, raw, _ := open(t)
	if _, _, err := db.Delete[fixture.SDoc]().Where(D.ID.EQ(1)).SQL(org(1)); !errors.Is(err, velox.ErrDryRun) {
		t.Errorf("SQL of a soft delete: %v; want ErrDryRun", err)
	}
	q, _, err := db.Delete[fixture.SDoc]().Where(D.ID.EQ(1)).SQL(softdelete.Skip(org(1)))
	if err != nil || !strings.HasPrefix(q, `DELETE FROM "s_docs"`) {
		t.Errorf("SQL of a hard delete: %s, %v", q, err)
	}
	wantIDs(t, "deleted", deleted(t, raw))
}

func TestSkipSplitsBatcher(t *testing.T) {
	ctx := org(1)
	a, b := velox.RowScopeFromContext(ctx), velox.RowScopeFromContext(softdelete.Skip(ctx))
	if a == b {
		t.Errorf("Skip leaves the row scope as it was: %+v", a)
	}
	if !strings.Contains(b.Values, "softdelete") {
		t.Errorf("Skip's row scope %q does not name softdelete", b.Values)
	}
}

func TestPartialUniqueIndexReinsert(t *testing.T) {
	db, _, _ := open(t)
	ctx := org(1)
	if err := db.Insert(&fixture.SDoc{OwnerID: 1, Title: "a"}).Exec(ctx); err == nil {
		t.Fatal("a second live doc titled a was inserted")
	}
	if _, err := db.Delete[fixture.SDoc]().Where(D.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	d := fixture.SDoc{OwnerID: 1, Title: "a"}
	if err := db.Insert(&d).Exec(ctx); err != nil {
		t.Fatalf("re-insert after the delete: %v", err)
	}
	if d.OrgID != 1 {
		t.Errorf("the tenancy hook left OrgID %d", d.OrgID)
	}
}

func TestFields(t *testing.T) {
	fs := softdelete.Mixin{}.Fields()
	if len(fs) != 1 {
		t.Fatalf("%d fields", len(fs))
	}
	d := fs[0].Descriptor()
	if d.Name != softdelete.Column || !d.Nillable || !d.Optional || len(d.Errs) != 0 {
		t.Errorf("deleted_at: %+v", d)
	}
}
