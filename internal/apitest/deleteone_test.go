package apitest

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testbind/bindtype"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/schema/mixin/softdelete"
)

// byKeyTickets seeds policyDB with tickets of org 1: ann's done, bob's done,
// ann's open one and carl's done, in that order, and returns their keys.
func byKeyTickets(t *testing.T, db *velox.DB) [4]int64 {
	t.Helper()
	seed, sdb := context.Background(), db.WithoutPolicy("seed")
	inv := testmodel.Invoice{OrgID: 1, Number: "N1", Data: []byte("d")}
	if err := sdb.Insert(&inv).Exec(seed); err != nil {
		t.Fatal(err)
	}
	var ids [4]int64
	for i, tk := range []testmodel.Ticket{
		{OrgID: 1, Owner: "ann", Title: "T-1", Done: true, InvoiceID: inv.ID},
		{OrgID: 1, Owner: "bob", Title: "T-2", Done: true, InvoiceID: inv.ID},
		{OrgID: 1, Owner: "ann", Title: "T-3", Done: false, InvoiceID: inv.ID},
		{OrgID: 1, Owner: "carl", Title: "T-4", Done: true, InvoiceID: inv.ID},
	} {
		if err := sdb.Insert(&tk).Exec(seed); err != nil {
			t.Fatal(err)
		}
		ids[i] = tk.ID
	}
	return ids
}

// ticketExists reports whether the ticket with key id is in the table,
// whatever the policies say.
func ticketExists(t *testing.T, db *velox.DB, id int64) bool {
	t.Helper()
	ok, err := db.WithoutPolicy("check").Query[testmodel.Ticket]().Where(Tk.ID.EQ(id)).Exists(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// TestByKeyHonoursPolicies: a read, an update and a delete of one row by its
// key find a row the policies hide no more than Where(ID.EQ) with Exec does:
// each is ErrNotFound, and the row is left as it was. A delete hook's
// condition counts too.
func TestByKeyHonoursPolicies(t *testing.T) {
	db := policyDB(t)
	ids := byKeyTickets(t, db)
	ann := asUser("ann")

	if _, err := db.Query[testmodel.Ticket]().Where(Tk.ID.EQ(ids[1])).One(ann); !velox.IsNotFound(err) {
		t.Errorf("read of bob's ticket: %v, want not found", err)
	}
	if _, err := db.Update[testmodel.Ticket]().Where(Tk.ID.EQ(ids[1])).Set(Tk.Title.Set("x")).One(ann); !velox.IsNotFound(err) {
		t.Errorf("update of bob's ticket: %v, want not found", err)
	}
	if err := db.Delete[testmodel.Ticket]().Where(Tk.ID.EQ(ids[1])).One(ann); !velox.IsNotFound(err) {
		t.Errorf("delete of bob's ticket: %v, want not found", err)
	}
	if !ticketExists(t, db, ids[1]) {
		t.Error("bob's ticket was deleted")
	}
	// onlyDone, a Before hook, adds done = true: the open ticket is not found.
	if err := db.Delete[testmodel.Ticket]().Where(Tk.ID.EQ(ids[2])).One(ann); !velox.IsNotFound(err) {
		t.Errorf("delete of an open ticket: %v, want not found", err)
	}
	if !ticketExists(t, db, ids[2]) {
		t.Error("the open ticket was deleted")
	}

	if err := db.Delete[testmodel.Ticket]().Where(Tk.ID.EQ(ids[0])).One(ann); err != nil {
		t.Fatalf("delete of ann's ticket: %v", err)
	}
	if ticketExists(t, db, ids[0]) {
		t.Error("ann's ticket is still there")
	}
	if _, err := db.Query[testmodel.Ticket]().Where(Tk.ID.EQ(ids[0])).One(ann); !velox.IsNotFound(err) {
		t.Errorf("read after the delete: %v, want not found", err)
	}
	err := db.Delete[testmodel.Ticket]().Where(Tk.ID.EQ(ids[0])).One(ann)
	if !velox.IsNotFound(err) || !errors.Is(err, velox.ErrNotFound) {
		t.Errorf("second delete: %v, want not found", err)
	}
}

// TestDeleteOneNotSingular: a delete One whose conditions do not pin the key
// and match two rows removes neither, and inside a Tx undoes only its own
// savepoint; one that matches one row removes it.
func TestDeleteOneNotSingular(t *testing.T) {
	db := policyDB(t)
	ids := byKeyTickets(t, db)
	err := db.Tx(asUser("admin"), func(ctx context.Context, tx *velox.DB) error {
		if err := tx.Delete[testmodel.Ticket]().Where(Tk.ID.EQ(ids[1])).One(ctx); err != nil {
			return err
		}
		// ann's and carl's done tickets; onlyDone leaves out the open one.
		if err := tx.Delete[testmodel.Ticket]().Where(Tk.Title.NEQ("")).One(ctx); !errors.Is(err, velox.ErrNotSingular) {
			t.Errorf("a delete of two rows: %v, want ErrNotSingular", err)
		}
		return tx.Delete[testmodel.Ticket]().Where(Tk.Owner.EQ("carl")).One(ctx)
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []bool{true, false, true, false} {
		if got := ticketExists(t, db, ids[i]); got != want {
			t.Errorf("ticket %d exists: %v, want %v", i, got, want)
		}
	}
}

// TestDeleteOneStatements: by key, One is the DELETE alone, as Exec sends
// it; otherwise it is the DELETE in a transaction, rolled back when it
// matches two rows.
func TestDeleteOneStatements(t *testing.T) {
	for _, c := range []struct {
		name     string
		affected int64
		where    velox.Pred[testmodel.Invoice]
		want     []string
		err      error
	}{
		{"by key", 1, I.ID.EQ(7), []string{`DELETE FROM "invoices" WHERE "id" = $1`}, nil},
		{"by key, none", 0, I.ID.EQ(7), []string{`DELETE FROM "invoices" WHERE "id" = $1`}, velox.ErrNotFound},
		{"by key, unknown count", -1, I.ID.EQ(7), []string{`DELETE FROM "invoices" WHERE "id" = $1`}, nil},
		{"by a column", 1, I.Number.EQ("n"), []string{"BEGIN", `DELETE FROM "invoices" WHERE "number" = $1`, "COMMIT"}, nil},
		{"by a column, two", 2, I.Number.EQ("n"), []string{"BEGIN", `DELETE FROM "invoices" WHERE "number" = $1`, "ROLLBACK"}, velox.ErrNotSingular},
	} {
		e := &fakeconn.Exec{Affected: c.affected}
		err := openFake(velox.Postgres, e).Delete[testmodel.Invoice]().Where(c.where).One(ctx)
		if c.err == nil && err != nil || c.err != nil && !errors.Is(err, c.err) {
			t.Errorf("%s: %v, want %v", c.name, err, c.err)
		}
		if !slices.Equal(e.Log, c.want) {
			t.Errorf("%s: sent %q, want %q", c.name, e.Log, c.want)
		}
	}
	// The guard runs first: no Where is ErrNoWhere, and nothing is sent.
	e := &fakeconn.Exec{Affected: 1}
	if err := openFake(velox.Postgres, e).Delete[testmodel.Invoice]().One(ctx); !errors.Is(err, velox.ErrNoWhere) || len(e.Log) != 0 {
		t.Errorf("no Where: %v; sent %q", err, e.Log)
	}
	// A condition's error is returned unsent.
	if err := openFake(velox.Postgres, e).Delete[testmodel.Invoice]().Where(velox.Pred[testmodel.Invoice]{}).One(ctx); err == nil || len(e.Log) != 0 {
		t.Errorf("zero Pred: %v; sent %q", err, e.Log)
	}
}

// softDocs opens a SQLite database with the softdelete fixture's tables:
// docs 1 and 2 of org 1, doc 3 of org 2.
func softDocs(t *testing.T) *velox.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "soft.db"))
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	stmts := append(slices.Clone(fixture.SoftDeleteDDL),
		`INSERT INTO s_owners (id, name) VALUES (1, 'o1'), (2, 'o2')`,
		`INSERT INTO s_docs (id, org_id, owner_id, title) VALUES (1, 1, 1, 'a'), (2, 1, 1, 'b'), (3, 2, 2, 'c')`)
	for _, s := range stmts {
		exec(t, raw, s)
	}
	return velox.Open(velox.SQLite, raw)
}

// TestByKeySoftDelete: a delete One by key of a softdelete entity sets
// deleted_at; then the row is not found by a read, an update or another
// delete by key, and is under softdelete.Skip. A row of another tenant is
// not found either.
func TestByKeySoftDelete(t *testing.T) {
	db := softDocs(t)
	D := fixture.SDocs
	in1 := testmodel.Org.With(context.Background(), int64(1))

	if err := db.Delete[fixture.SDoc]().Where(D.ID.EQ(2)).One(in1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := db.Query[fixture.SDoc]().Where(D.ID.EQ(2)).One(in1); !velox.IsNotFound(err) {
		t.Errorf("read of the deleted doc: %v, want not found", err)
	}
	if _, err := db.Update[fixture.SDoc]().Where(D.ID.EQ(2)).Set(D.Title.Set("z")).One(in1); !velox.IsNotFound(err) {
		t.Errorf("update of the deleted doc: %v, want not found", err)
	}
	if err := db.Delete[fixture.SDoc]().Where(D.ID.EQ(2)).One(in1); !velox.IsNotFound(err) {
		t.Errorf("second delete: %v, want not found", err)
	}
	got, err := db.Query[fixture.SDoc]().Where(D.ID.EQ(2)).One(softdelete.Skip(in1))
	if err != nil || got.DeletedAt == nil || got.Title != "b" {
		t.Errorf("read under Skip: %+v, %v", got, err)
	}
	if err := db.Delete[fixture.SDoc]().Where(D.ID.EQ(3)).One(in1); !velox.IsNotFound(err) {
		t.Errorf("delete of another org's doc: %v, want not found", err)
	}
	if _, err := db.Query[fixture.SDoc]().Where(D.ID.EQ(3)).One(in1); !velox.IsNotFound(err) {
		t.Errorf("read of another org's doc: %v, want not found", err)
	}
}

// TestByKeyUUID: an entity keyed by a UUID, with a Version column, is read,
// updated and deleted by its key, and then not found by any of the three.
func TestByKeyUUID(t *testing.T) {
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "papers.db"))
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	exec(t, raw, `CREATE TABLE papers (id TEXT PRIMARY KEY, title TEXT NOT NULL, version INTEGER NOT NULL)`)
	db := velox.Open(velox.SQLite, raw)
	P := testmodel.Papers

	id := bindtype.UUID{0x12, 0x3e, 0x45, 0x67, 0xe8, 0x9b, 0x42, 0xd3, 0xa4, 0x56, 0x42, 0x66, 0x14, 0x17, 0x40, 0x00}
	other := testmodel.Paper{Title: "other"}
	for _, p := range []*testmodel.Paper{{ID: id, Title: "A"}, &other} {
		if err := db.Insert(p).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.Query[testmodel.Paper]().Where(P.ID.EQ(id)).One(ctx)
	if err != nil || got.ID != id || got.Title != "A" {
		t.Fatalf("read: %+v, %v", got, err)
	}
	got, err = db.Update[testmodel.Paper]().Where(P.ID.EQ(id)).Set(P.Title.Set("B")).One(ctx)
	if err != nil || got.ID != id || got.Title != "B" {
		t.Fatalf("update: %+v, %v", got, err)
	}
	if err := db.Delete[testmodel.Paper]().Where(P.ID.EQ(id)).One(ctx); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := db.Query[testmodel.Paper]().Where(P.ID.EQ(id)).One(ctx); !velox.IsNotFound(err) {
		t.Errorf("read after the delete: %v", err)
	}
	if _, err := db.Update[testmodel.Paper]().Where(P.ID.EQ(id)).Set(P.Title.Set("C")).One(ctx); !velox.IsNotFound(err) {
		t.Errorf("update after the delete: %v", err)
	}
	err = db.Delete[testmodel.Paper]().Where(P.ID.EQ(id)).One(ctx)
	if !velox.IsNotFound(err) || errors.Is(err, velox.ErrStale) || !strings.Contains(err.Error(), "testmodel.Paper") {
		t.Errorf("delete after the delete: %v, want not found, not stale", err)
	}
	if n, err := db.Query[testmodel.Paper]().Count(ctx); err != nil || n != 1 {
		t.Errorf("%d papers left, %v; want the other", n, err)
	}
}
