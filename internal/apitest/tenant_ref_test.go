package apitest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

var refQuery = regexp.MustCompile(`^SELECT \S+ FROM \S+ WHERE \S+ IN \(.*\) AND \S+ = \S+$`)

// passRefs is a fakeconn.Exec's Answer for the tests of something else: every row a
// write's foreign keys name is the tenant's.
func passRefs(sql string, args []any) ([][]any, bool) {
	if !refQuery.MatchString(sql) {
		return nil, false
	}
	rows := make([][]any, len(args)-1)
	for i := range rows {
		rows[i] = []any{args[i]}
	}
	return rows, true
}

// refDB is SQLite with real foreign keys, off so that one test can seed another
// tenant's rows freely, then on. Tenant 1 has invoice 1 and ticket 1, tenant 2 has
// invoice 2 and ticket 2.
func refDB(t *testing.T) (*velox.DB, *sql.DB) {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+t.TempDir()+"/t.db?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	raw.SetMaxOpenConns(1)
	if _, err := raw.Exec(`
		CREATE TABLE invoices (created_at datetime NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at datetime NOT NULL DEFAULT CURRENT_TIMESTAMP, id integer PRIMARY KEY,
			org_id integer NOT NULL, number text NOT NULL, total real NOT NULL, paid boolean NOT NULL, paid_at datetime, note text NOT NULL, data blob NOT NULL);
		CREATE TABLE tickets (id integer PRIMARY KEY, created_by text NOT NULL, org_id integer NOT NULL, owner text NOT NULL, title text NOT NULL, done boolean NOT NULL,
			invoice_id integer NOT NULL REFERENCES invoices (id), parent_id integer REFERENCES tickets (id), UNIQUE (org_id, title));
		INSERT INTO invoices (id, org_id, number, total, paid, note, data) VALUES (1, 1, 'a', 0, false, '', x''), (2, 2, 'b', 0, false, '', x'');
		INSERT INTO tickets VALUES (1, '', 1, 'ann', 'one', false, 1, NULL), (2, '', 2, 'bob', 'two', true, 2, NULL)`); err != nil {
		t.Fatal(err)
	}
	return velox.Open(velox.SQLite, raw), raw
}

// wantRefused checks the one error of a foreign key that is not the tenant's.
func wantRefused(t *testing.T, err error, column string) {
	t.Helper()
	var ce *velox.ConstraintError
	if !errors.Is(err, velox.ErrTenantRef) || !errors.As(err, &ce) || ce.Kind != velox.ConstraintForeignKey || ce.Constraint != column {
		t.Errorf("%v, want ErrTenantRef on %s", err, column)
	}
}

// ticketCount is the rows of tickets.
func ticketCount(t *testing.T, raw *sql.DB) int {
	t.Helper()
	var n int
	if err := raw.QueryRow("SELECT COUNT(*) FROM tickets").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestTenantRefRefusesAnotherTenantsRow is the finding: under tenant 1, a write
// that names tenant 2's invoice succeeded, tenant 2 could then not delete its own
// invoice, for a row it cannot see, and success against a foreign key failure told
// tenant 1 which of tenant 2's IDs exist. Every write refuses it, unsent, with one
// error for a row of another tenant and for no row.
func TestTenantRefRefusesAnotherTenantsRow(t *testing.T) {
	t1 := tenant(int64(1))
	theirs, none := int64(2), int64(99)
	for _, id := range []int64{theirs, none} {
		for name, write := range map[string]func(db *velox.DB) error{
			"Insert": func(db *velox.DB) error {
				return db.Insert(&testmodel.Ticket{Owner: "ann", Title: "n", InvoiceID: id}).Exec(t1)
			},
			"InsertAll": func(db *velox.DB) error {
				return db.InsertAll([]testmodel.Ticket{{Owner: "ann", Title: "n", InvoiceID: 1}, {Owner: "ann", Title: "m", InvoiceID: id}}).Exec(t1)
			},
			"upsert": func(db *velox.DB) error {
				return db.Insert(&testmodel.Ticket{ID: 1, Owner: "ann", Title: "one", InvoiceID: id}).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.InvoiceID).Exec(t1)
			},
			"Update": func(db *velox.DB) error {
				_, err := db.Update[testmodel.Ticket]().Set(Tk.InvoiceID.Set(id)).Where(Tk.ID.EQ(1)).Exec(t1)
				return err
			},
			"Save": func(db *velox.DB) error {
				return db.Save(&testmodel.Ticket{ID: 1, Owner: "ann", InvoiceID: id}, Tk.InvoiceID).Exec(t1)
			},
			"in a Tx": func(db *velox.DB) error {
				return db.Tx(t1, func(ctx context.Context, tx *velox.DB) error {
					return tx.Insert(&testmodel.Ticket{Owner: "ann", Title: "n", InvoiceID: id}).Exec(ctx)
				})
			},
		} {
			t.Run(fmt.Sprint(name, " to ", id), func(t *testing.T) {
				db, raw := refDB(t)
				wantRefused(t, write(db), "invoice_id")
				var invoice int64
				if err := raw.QueryRow("SELECT invoice_id FROM tickets WHERE id = 1").Scan(&invoice); err != nil || invoice != 1 {
					t.Errorf("ticket 1's invoice is %d, %v", invoice, err)
				}
				if n := ticketCount(t, raw); n != 2 {
					t.Errorf("%d tickets", n)
				}
			})
		}
	}
	t.Run("the message does not tell another tenant's row from none", func(t *testing.T) {
		db, _ := refDB(t)
		e1 := db.Insert(&testmodel.Ticket{Owner: "ann", Title: "n", InvoiceID: theirs}).Exec(t1)
		e2 := db.Insert(&testmodel.Ticket{Owner: "ann", Title: "n", InvoiceID: none}).Exec(t1)
		if e1 == nil || e2 == nil || e1.Error() != e2.Error() {
			t.Errorf("%v\n%v", e1, e2)
		}
	})
	t.Run("the other tenant's delete is not blocked", func(t *testing.T) {
		db, _ := refDB(t)
		_ = db.Insert(&testmodel.Ticket{Owner: "ann", Title: "n", InvoiceID: theirs}).Exec(t1)
		if n, err := db.Delete[testmodel.Ticket]().Where(Tk.ID.EQ(2)).Exec(tenant(int64(2))); err != nil || n != 1 {
			t.Fatal(n, err)
		}
		if _, err := db.Delete[testmodel.Invoice]().Where(I.ID.EQ(2)).Exec(tenant(int64(2))); err != nil {
			t.Errorf("tenant 2 cannot delete its own invoice: %v", err)
		}
	})
	t.Run("a nullable reference", func(t *testing.T) {
		db, _ := refDB(t)
		wantRefused(t, db.Insert(&testmodel.Ticket{Owner: "ann", Title: "n", InvoiceID: 1, ParentID: &theirs}).Exec(t1), "parent_id")
		_, err := db.Update[testmodel.Ticket]().Set(Tk.ParentID.Set(theirs)).Where(Tk.ID.EQ(1)).Exec(t1)
		wantRefused(t, err, "parent_id")
	})
}

// TestTenantRefAllowsTheTenantsOwn: the tenant's own rows pass, NULL asks nothing,
// and an insert may refer to a row it inserts itself, or to one earlier in its Tx.
func TestTenantRefAllowsTheTenantsOwn(t *testing.T) {
	t1 := tenant(int64(1))
	db, raw := refDB(t)
	one := int64(1)
	tk := testmodel.Ticket{Owner: "ann", Title: "own", InvoiceID: 1, ParentID: &one}
	if err := db.Insert(&tk).Exec(t1); err != nil {
		t.Fatal(err)
	}
	if n, err := db.Update[testmodel.Ticket]().Set(Tk.InvoiceID.Set(1), Tk.ParentID.Set(one)).Where(Tk.ID.EQ(tk.ID)).Exec(t1); err != nil || n != 1 {
		t.Errorf("%d, %v", n, err)
	}
	if n, err := db.Update[testmodel.Ticket]().Set(Tk.ParentID.SetNull()).Where(Tk.ID.EQ(tk.ID)).Exec(t1); err != nil || n != 1 {
		t.Errorf("clearing: %d, %v", n, err)
	}
	// Row 10 is the parent of row 11 in one statement, and of itself.
	ten, eleven := int64(10), int64(11)
	if err := db.InsertAll([]testmodel.Ticket{
		{ID: ten, Owner: "ann", Title: "ten", InvoiceID: 1, ParentID: &ten},
		{ID: eleven, Owner: "ann", Title: "eleven", InvoiceID: 1, ParentID: &ten},
	}).Exec(t1); err != nil {
		t.Errorf("rows of one statement: %v", err)
	}
	self := int64(12)
	if err := db.Insert(&testmodel.Ticket{ID: self, Owner: "ann", Title: "self", InvoiceID: 1, ParentID: &self}).Exec(t1); err != nil {
		t.Errorf("its own row: %v", err)
	}
	if err := db.Tx(t1, func(ctx context.Context, tx *velox.DB) error {
		parent := testmodel.Ticket{Owner: "ann", Title: "parent", InvoiceID: 1}
		if err := tx.Insert(&parent).Exec(ctx); err != nil {
			return err
		}
		return tx.Insert(&testmodel.Ticket{Owner: "ann", Title: "child", InvoiceID: 1, ParentID: &parent.ID}).Exec(ctx)
	}); err != nil {
		t.Errorf("a row of the Tx: %v", err)
	}
	// A value in another row's key does not excuse itself across tenants.
	two := int64(2)
	wantRefused(t, db.InsertAll([]testmodel.Ticket{
		{ID: 20, Owner: "ann", Title: "twenty", InvoiceID: 1, ParentID: &two},
	}).Exec(t1), "parent_id")
	if n := ticketCount(t, raw); n != 8 {
		t.Errorf("%d tickets", n)
	}
}

// TestTenantRefBypass: WithAllTenants is the deliberate cross-tenant write. It sends
// no check, and the database's foreign key is what remains.
func TestTenantRefBypass(t *testing.T) {
	db, _ := refDB(t)
	all := velox.WithAllTenants(velox.WithoutPolicy(ctx, "test"), "the migration")
	if err := db.Insert(&testmodel.Ticket{OrgID: 1, Owner: "ann", Title: "n", InvoiceID: 2}).Exec(all); err != nil {
		t.Error(err)
	}
	err := db.Insert(&testmodel.Ticket{OrgID: 1, Owner: "ann", Title: "m", InvoiceID: 99}).Exec(all)
	var ce *velox.ConstraintError
	if !errors.As(err, &ce) || errors.Is(err, velox.ErrTenantRef) {
		t.Errorf("the database's refusal: %v", err)
	}
}

// TestTenantRefStatement: one SELECT per reference set, of distinct values, before
// the write, with the final values, and nothing for what is not set.
func TestTenantRefStatement(t *testing.T) {
	t7 := tenant(int64(7))
	const invoices, tickets = `SELECT "id" FROM "invoices" WHERE "id" IN ($1) AND "org_id" = $2`, `SELECT "id" FROM "tickets" WHERE "id" IN ($1) AND "org_id" = $2`
	t.Run("an update sets what it sets", func(t *testing.T) {
		e := &fakeconn.Exec{Affected: 1, Answer: passRefs}
		db := openFake(velox.Postgres, e)
		if _, err := db.Update[testmodel.Ticket]().Set(Tk.Title.Set("x")).Where(Tk.ID.EQ(1)).Exec(t7); err != nil {
			t.Fatal(err)
		}
		if len(e.Log) != 1 {
			t.Errorf("no reference set: %q", e.Log)
		}
		e.Log = nil
		if _, err := db.Update[testmodel.Ticket]().Set(Tk.ParentID.SetNull()).Where(Tk.ID.EQ(1)).Exec(t7); err != nil {
			t.Fatal(err)
		}
		if len(e.Log) != 1 {
			t.Errorf("NULL: %q", e.Log)
		}
		e.Log, e.ArgLog = nil, nil
		if _, err := db.Update[testmodel.Ticket]().Set(Tk.InvoiceID.Set(5)).Where(Tk.ID.EQ(1)).Exec(t7); err != nil {
			t.Fatal(err)
		}
		if len(e.Log) != 2 || e.Log[0] != invoices || !reflect.DeepEqual(e.ArgLog[0], []any{int64(5), int64(7)}) {
			t.Errorf("%q %v", e.Log, e.ArgLog)
		}
	})
	t.Run("a batch checks distinct values with one statement", func(t *testing.T) {
		e := &fakeconn.Exec{Affected: 3, Answer: passRefs}
		db := openFake(velox.Postgres, e)
		p := int64(4)
		rows := []testmodel.Ticket{{Owner: "a", Title: "a", InvoiceID: 1}, {Owner: "a", Title: "b", InvoiceID: 2, ParentID: &p}, {Owner: "a", Title: "c", InvoiceID: 1, ParentID: &p}}
		if err := db.InsertAll(rows).Exec(t7); err != nil {
			t.Fatal(err)
		}
		if len(e.Log) != 3 || e.Log[0] != `SELECT "id" FROM "invoices" WHERE "id" IN ($1, $2) AND "org_id" = $3` || e.Log[1] != tickets {
			t.Errorf("%q", e.Log)
		}
		if !reflect.DeepEqual(e.ArgLog[0], []any{int64(1), int64(2), int64(7)}) || !reflect.DeepEqual(e.ArgLog[1], []any{int64(4), int64(7)}) {
			t.Errorf("%v", e.ArgLog)
		}
	})
	t.Run("a Rewrite hook's value is the one checked", func(t *testing.T) {
		e := &fakeconn.Exec{Affected: 1, Answer: passRefs}
		db := openFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
			st.Set[0].Value = int64(8)
			return nil
		}))
		if _, err := db.Update[testmodel.Ticket]().Set(Tk.InvoiceID.Set(5)).Where(Tk.ID.EQ(1)).Exec(t7); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(e.ArgLog[0], []any{int64(8), int64(7)}) {
			t.Errorf("%v", e.ArgLog)
		}
	})
	t.Run("an entity hook's value is the one checked", func(t *testing.T) {
		e := &fakeconn.Exec{Affected: 1, Answer: passRefs}
		db := openFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
			if st.Op == velox.OpInsert {
				st.Rows[0][slices.Index(st.Columns, 6)] = int64(9)
			}
			return nil
		}))
		if err := db.Insert(&testmodel.Ticket{ID: 1, Owner: "a", Title: "a", InvoiceID: 1}).Exec(t7); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(e.ArgLog[0], []any{int64(9), int64(7)}) {
			t.Errorf("%v", e.ArgLog)
		}
	})
	t.Run("a refused write is not sent, and SQL(ctx) sends no check", func(t *testing.T) {
		e := &fakeconn.Exec{Affected: 1}
		db := openFake(velox.Postgres, e)
		q := db.Update[testmodel.Ticket]().Set(Tk.InvoiceID.Set(5)).Where(Tk.ID.EQ(1))
		if text, _, err := q.SQL(t7); err != nil || !strings.HasPrefix(text, "UPDATE") || e.Calls != 0 {
			t.Errorf("%s, %v, %d sent", text, err, e.Calls)
		}
		_, err := q.Exec(t7)
		wantRefused(t, err, "invoice_id")
		if len(e.Log) != 1 || e.Log[0] != invoices {
			t.Errorf("%q", e.Log)
		}
	})
	t.Run("the check's failure is the statement's", func(t *testing.T) {
		boom := errors.New("boom")
		e := &fakeconn.Exec{Errs: []error{boom}}
		_, err := openFake(velox.Postgres, e).Update[testmodel.Ticket]().Set(Tk.InvoiceID.Set(5)).Where(Tk.ID.EQ(1)).Exec(t7)
		if !errors.Is(err, boom) || errors.Is(err, velox.ErrTenantRef) || len(e.Log) != 1 {
			t.Errorf("%v, %q", err, e.Log)
		}
		e = &fakeconn.Exec{RowsErr: boom, Rows: [][]any{{int64(5)}}}
		_, err = openFake(velox.Postgres, e).Update[testmodel.Ticket]().Set(Tk.InvoiceID.Set(5)).Where(Tk.ID.EQ(1)).Exec(t7)
		if !errors.Is(err, boom) || len(e.Log) != 1 {
			t.Errorf("rows: %v, %q", err, e.Log)
		}
	})
	t.Run("arithmetic on a reference cannot be checked", func(t *testing.T) {
		e := &fakeconn.Exec{Affected: 1}
		db := openFake(velox.Postgres, e)
		q := db.Update[testmodel.Ticket]().Set(Tk.InvoiceID.Add(1)).Where(Tk.ID.EQ(1))
		if _, err := q.Exec(t7); !errors.Is(err, velox.ErrTenantRef) || e.Calls != 0 {
			t.Errorf("%v, %d sent", err, e.Calls)
		}
		if _, _, err := q.SQL(t7); !errors.Is(err, velox.ErrTenantRef) {
			t.Errorf("SQL: %v", err)
		}
		if _, err := q.Exec(velox.WithAllTenants(velox.WithoutPolicy(ctx, "test"), "test")); err != nil || e.Calls != 1 {
			t.Errorf("WithAllTenants: %v, %d sent", err, e.Calls)
		}
	})
	t.Run("in a SQLite Tx a refusal sends no probe", func(t *testing.T) {
		e := &fakeconn.Exec{Affected: 1}
		err := openFake(velox.SQLite, e).Tx(t7, func(ctx context.Context, tx *velox.DB) error {
			_, err := tx.Update[testmodel.Ticket]().Set(Tk.InvoiceID.Set(5)).Where(Tk.ID.EQ(1)).Exec(ctx)
			wantRefused(t, err, "invoice_id")
			return nil
		})
		if err != nil || slices.Contains(e.Log[1:], "BEGIN") {
			t.Errorf("%v, %q", err, e.Log)
		}
	})
}

// BenchmarkTenantInsert is an insert under a tenant, of an entity with and without a
// foreign key to check, on real SQLite with the check's one SELECT. The fake's
// sub-benchmarks are the engine's own cost, with no driver or database.
func BenchmarkTenantInsert(b *testing.B) {
	raw, err := sql.Open("sqlite", "file:"+b.TempDir()+"/t.db?_pragma=foreign_keys(1)&_pragma=synchronous(off)")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	raw.SetMaxOpenConns(1)
	if _, err := raw.Exec(`
		CREATE TABLE invoices (created_at datetime NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at datetime NOT NULL DEFAULT CURRENT_TIMESTAMP, id integer PRIMARY KEY,
			org_id integer NOT NULL, number text NOT NULL, total real NOT NULL, paid boolean NOT NULL, paid_at datetime, note text NOT NULL, data blob NOT NULL);
		CREATE TABLE tickets (id integer PRIMARY KEY, created_by text NOT NULL, org_id integer NOT NULL, owner text NOT NULL, title text NOT NULL, done boolean NOT NULL,
			invoice_id integer NOT NULL REFERENCES invoices (id), parent_id integer REFERENCES tickets (id));
		INSERT INTO invoices (id, org_id, number, total, paid, note, data) VALUES (1, 7, 'a', 0, false, '', x'')`); err != nil {
		b.Fatal(err)
	}
	db := velox.Open(velox.SQLite, raw)
	t7 := tenant(int64(7))
	all := velox.WithAllTenants(velox.WithoutPolicy(context.Background(), "the benchmark"), "the benchmark")
	b.Run("under a tenant, one foreign key", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if err := db.Insert(&testmodel.Ticket{Owner: "ann", Title: "t", InvoiceID: 1}).Exec(t7); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("under a tenant, no foreign key", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if err := db.Insert(&testmodel.Invoice{Number: "n", Data: []byte{}}).Exec(t7); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("under WithAllTenants, no check", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if err := db.Insert(&testmodel.Ticket{OrgID: 7, Owner: "ann", Title: "t", InvoiceID: 1}).Exec(all); err != nil {
				b.Fatal(err)
			}
		}
	})
	e := &fakeconn.Exec{Affected: 1, Rows: [][]any{{int64(1)}}}
	fake := openFake(velox.Postgres, e)
	b.Run("fake, one foreign key", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			e.Log, e.ArgLog = e.Log[:0], e.ArgLog[:0]
			if err := fake.Insert(&testmodel.Ticket{Owner: "ann", Title: "t", InvoiceID: 1}).Exec(t7); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("fake, WithAllTenants", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			e.Log, e.ArgLog = e.Log[:0], e.ArgLog[:0]
			if err := fake.Insert(&testmodel.Ticket{OrgID: 7, Owner: "ann", Title: "t", InvoiceID: 1}).Exec(all); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// TestTenantRefFollowsARewrite: a Rewrite that moves the statement to another schema,
// as one schema to a tenant does, moves its check there too, so it reads the rows the
// write will refer to.
func TestTenantRefFollowsARewrite(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1, Answer: passRefs}
	db := openFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		in := *st.Entity
		in.Schema = "t7"
		st.Entity = &in
		return nil
	}))
	if _, err := db.Update[testmodel.Ticket]().Set(Tk.InvoiceID.Set(5)).Where(Tk.ID.EQ(1)).Exec(tenant(int64(7))); err != nil {
		t.Fatal(err)
	}
	if want := `SELECT "id" FROM "t7"."invoices" WHERE "id" IN ($1) AND "org_id" = $2`; len(e.Log) != 2 || e.Log[0] != want {
		t.Errorf("%q, want first %q", e.Log, want)
	}
}
