package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
	"github.com/syssam/velox2/veloxpgx"
)

var Tk = testmodel.Tickets

// seedTicket is one row setup writes to the tickets table.
type seedTicket struct {
	id, org      int64
	owner, title string
	done         bool
	invoice      int64
	parent       *int64
}

func ptr(v int64) *int64 { return &v }

// seedTickets are two tenants' tickets, each owned by ann or bob. Ticket 5
// names org 2's invoice 5 and bob's ticket 3 as its parent; invoice 0 does not
// exist. Done: 2, 3 and 8.
var seedTickets = []seedTicket{
	{1, 1, "ann", "A1", false, 1, nil},
	{2, 1, "ann", "A2", true, 0, ptr(1)},
	{3, 1, "bob", "B1", true, 2, nil},
	{4, 1, "bob", "B2", false, 1, ptr(3)},
	{5, 1, "ann", "A3", false, 5, ptr(3)},
	{6, 2, "ann", "A1", false, 5, nil},
	{7, 2, "bob", "B1", false, 5, ptr(6)},
	{8, 2, "ann", "A2", true, 0, ptr(6)},
}

// who is a caller the scope tests act as.
type who struct {
	name string
	ctx  context.Context
}

// asIn1 acts as user in org 1.
func asIn1(user string) context.Context {
	return testmodel.WithUser(velox.WithTenant(context.Background(), int64(1)), user)
}

var (
	ann   = who{"ann", asIn1("ann")}
	admin = who{"admin", asIn1("admin")}
	// anyone sees both orgs and skips the policy.
	anyone  = who{"anyone", velox.WithoutPolicy(ctx, "velox tests")}
	callers = []who{ann, admin, anyone}
)

// ticketRow is a tickets row as read back without velox.
type ticketRow struct {
	id, org                 int64
	createdBy, owner, title string
	done                    bool
}

// ticketRows reads every tickets row, ordered by id, straight from the
// database.
func ticketRows(t *testing.T, tg target) []ticketRow {
	t.Helper()
	rs, err := tg.direct.QueryContext(ctx, "SELECT id, org_id, created_by, owner, title, done FROM tickets ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rs.Close() }()
	var out []ticketRow
	for rs.Next() {
		var r ticketRow
		if err := rs.Scan(&r.id, &r.org, &r.createdBy, &r.owner, &r.title, &r.done); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rs.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// rowIDs returns the ids of the rows for which keep is true.
func rowIDs(rows []ticketRow, keep func(ticketRow) bool) []int64 {
	var out []int64
	for _, r := range rows {
		if keep(r) {
			out = append(out, r.id)
		}
	}
	return out
}

func ticketIDs(xs []testmodel.Ticket) []int64 {
	var out []int64
	for i := range xs {
		out = append(out, xs[i].ID)
	}
	return out
}

// eachCaller runs f on every target as every caller, with freshly set-up
// tables for each.
func eachCaller(t *testing.T, f func(t *testing.T, tg target, c who)) {
	for _, tg := range targets(t, nil) {
		t.Run(tg.name, func(t *testing.T) {
			for _, c := range callers {
				t.Run(c.name, func(t *testing.T) {
					setup(t, tg)
					f(t, tg, c)
				})
			}
		})
	}
}

// visible are the tickets each caller may see and change: ann's own in org 1,
// all of org 1, and every ticket.
var visible = map[string][]int64{"ann": {1, 2, 5}, "admin": {1, 2, 3, 4, 5}, "anyone": {1, 2, 3, 4, 5, 6, 7, 8}}

func TestScopeReads(t *testing.T) {
	eachCaller(t, func(t *testing.T, tg target, c who) {
		q := tg.db.Query[testmodel.Ticket]().OrderBy(Tk.ID.Asc()).Unbounded()
		all, err := q.All(c.ctx)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, ticketIDs(all), visible[c.name])
		if n, err := tg.db.Query[testmodel.Ticket]().Count(c.ctx); err != nil || n != int64(len(visible[c.name])) {
			t.Errorf("Count: %d, %v", n, err)
		}
		var iter []int64
		for x, err := range q.Iter(c.ctx) {
			if err != nil {
				t.Fatal(err)
			}
			iter = append(iter, x.ID)
		}
		wantIDs(t, iter, visible[c.name])

		// A1 is ticket 1 in org 1 and ticket 6 in org 2; B2 is bob's ticket 4.
		a1, err := tg.db.Query[testmodel.Ticket]().Where(Tk.Title.EQ("A1")).One(c.ctx)
		switch c.name {
		case "anyone":
			if !errors.Is(err, velox.ErrNotSingular) {
				t.Errorf("One A1: %v", err)
			}
		default:
			if err != nil || a1.ID != 1 || a1.OrgID != 1 {
				t.Errorf("One A1: %+v, %v", a1, err)
			}
		}
		b2, err := tg.db.Query[testmodel.Ticket]().Where(Tk.Title.EQ("B2")).One(c.ctx)
		switch c.name {
		case "ann":
			if !errors.Is(err, velox.ErrNotFound) {
				t.Errorf("One B2: %+v, %v", b2, err)
			}
		default:
			if err != nil || b2.ID != 4 {
				t.Errorf("One B2: %+v, %v", b2, err)
			}
		}
	})
}

// TestScopeNeedsTenantAndUser: no tenant is an error, not every row; no user
// is the policy's denial.
func TestScopeNeedsTenantAndUser(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		_, err := tg.db.Query[testmodel.Ticket]().Unbounded().All(testmodel.WithUser(context.Background(), "ann"))
		if !errors.Is(err, velox.ErrNoTenant) {
			t.Errorf("no tenant: %v", err)
		}
		_, err = tg.db.Query[testmodel.Ticket]().Unbounded().All(velox.WithTenant(context.Background(), int64(1)))
		var pe *velox.PolicyError
		if !errors.As(err, &pe) || pe.Op != velox.OpSelect {
			t.Errorf("no user: %v", err)
		}
	})
}

func TestScopeRelations(t *testing.T) {
	// The parent each visible ticket loads; 0 is none. Ann's ticket 5 has bob's
	// ticket 3 as parent, which ann cannot see.
	parents := map[string]map[int64]int64{
		"ann":    {1: 0, 2: 1, 5: 0},
		"admin":  {1: 0, 2: 1, 3: 0, 4: 3, 5: 3},
		"anyone": {1: 0, 2: 1, 3: 0, 4: 3, 5: 3, 6: 0, 7: 6, 8: 6},
	}
	// The tickets with a visible parent, and with a visible invoice: invoice 5
	// is org 2's, invoice 0 does not exist.
	hasParent := map[string][]int64{"ann": {2}, "admin": {2, 4, 5}, "anyone": {2, 4, 5, 7, 8}}
	hasInvoice := map[string][]int64{"ann": {1}, "admin": {1, 3, 4}, "anyone": {1, 3, 4, 5, 6, 7}}
	eachCaller(t, func(t *testing.T, tg target, c who) {
		xs, err := tg.db.Query[testmodel.Ticket]().With(Tk.Parent).OrderBy(Tk.ID.Asc()).Unbounded().All(c.ctx)
		if err != nil {
			t.Fatal(err)
		}
		got := map[int64]int64{}
		for _, x := range xs {
			p, loaded := x.Parent.Get()
			if !loaded {
				t.Fatalf("ticket %d: parent not loaded", x.ID)
			}
			got[x.ID] = 0
			if p != nil {
				got[x.ID] = p.ID
			}
		}
		if fmt.Sprint(got) != fmt.Sprint(parents[c.name]) {
			t.Errorf("With(Parent): %v, want %v", got, parents[c.name])
		}
		has := func(p velox.Pred[testmodel.Ticket]) []int64 {
			xs, err := tg.db.Query[testmodel.Ticket]().Where(p).OrderBy(Tk.ID.Asc()).Unbounded().All(c.ctx)
			if err != nil {
				t.Fatal(err)
			}
			return ticketIDs(xs)
		}
		wantIDs(t, has(Tk.Parent.Has()), hasParent[c.name])
		wantIDs(t, has(Tk.Invoice.Has()), hasInvoice[c.name])
	})
}

// TestScopeCrossOrgParent: a ticket whose parent is in another org. Ticket 1
// (org 1) is given ticket 6 (org 2) as its parent. A caller who cannot see
// org 2 loads no parent for it, and Parent.Has() does not count it; a caller
// who sees both orgs loads ticket 6. The seed itself has no cross-org parent.
func TestScopeCrossOrgParent(t *testing.T) {
	parents := map[string]map[int64]int64{
		"ann":    {1: 0, 2: 1, 5: 0},
		"admin":  {1: 0, 2: 1, 3: 0, 4: 3, 5: 3},
		"anyone": {1: 6, 2: 1, 3: 0, 4: 3, 5: 3, 6: 0, 7: 6, 8: 6},
	}
	hasParent := map[string][]int64{"ann": {2}, "admin": {2, 4, 5}, "anyone": {1, 2, 4, 5, 7, 8}}
	eachCaller(t, func(t *testing.T, tg target, c who) {
		tg.exec(t, "UPDATE tickets SET parent_id = 6 WHERE id = 1")
		xs, err := tg.db.Query[testmodel.Ticket]().With(Tk.Parent).OrderBy(Tk.ID.Asc()).Unbounded().All(c.ctx)
		if err != nil {
			t.Fatal(err)
		}
		got := map[int64]int64{}
		for _, x := range xs {
			p, loaded := x.Parent.Get()
			if !loaded {
				t.Fatalf("ticket %d: parent not loaded", x.ID)
			}
			got[x.ID] = 0
			if p != nil {
				got[x.ID] = p.ID
			}
		}
		if fmt.Sprint(got) != fmt.Sprint(parents[c.name]) {
			t.Errorf("With(Parent): %v, want %v", got, parents[c.name])
		}
		has, err := tg.db.Query[testmodel.Ticket]().Where(Tk.Parent.Has()).OrderBy(Tk.ID.Asc()).Unbounded().All(c.ctx)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, ticketIDs(has), hasParent[c.name])
	})
}

func TestScopeUpdate(t *testing.T) {
	eachCaller(t, func(t *testing.T, tg target, c who) {
		before := rowIDs(ticketRows(t, tg), func(r ticketRow) bool { return r.done })
		n, err := tg.db.Update[testmodel.Ticket]().Set(Tk.Done.Set(true)).Where(Tk.Done.EQ(false)).Exec(c.ctx)
		if err != nil {
			t.Fatal(err)
		}
		// Every visible ticket is now done, and only those changed.
		want := slices.Compact(slices.Sorted(slices.Values(append(slices.Clone(before), visible[c.name]...))))
		got := rowIDs(ticketRows(t, tg), func(r ticketRow) bool { return r.done })
		wantIDs(t, got, want)
		if int(n) != len(want)-len(before) {
			t.Errorf("updated %d rows, want %d", n, len(want)-len(before))
		}
	})
}

func TestScopeDelete(t *testing.T) {
	// Ticket's delete hook deletes only done tickets: 2, 3 and 8.
	deleted := map[string][]int64{"ann": {2}, "admin": {2, 3}, "anyone": {2, 3, 8}}
	eachCaller(t, func(t *testing.T, tg target, c who) {
		n, err := tg.db.Delete[testmodel.Ticket]().Where(Tk.Owner.In("ann", "bob")).Exec(c.ctx)
		if err != nil || int(n) != len(deleted[c.name]) {
			t.Errorf("delete: %d, %v", n, err)
		}
		got := rowIDs(ticketRows(t, tg), func(r ticketRow) bool { return true })
		want := rowIDs(seededRows(), func(r ticketRow) bool { return !slices.Contains(deleted[c.name], r.id) })
		wantIDs(t, got, want)
	})
}

// seededRows is seedTickets as ticketRows.
func seededRows() []ticketRow {
	out := make([]ticketRow, len(seedTickets))
	for i, s := range seedTickets {
		out[i] = ticketRow{id: s.id, org: s.org, createdBy: "seed", owner: s.owner, title: s.title, done: s.done}
	}
	return out
}

func TestScopeSave(t *testing.T) {
	eachCaller(t, func(t *testing.T, tg target, c who) {
		for _, s := range seedTickets {
			x := testmodel.Ticket{ID: s.id, OrgID: s.org, Owner: s.owner, Title: fmt.Sprintf("S%d", s.id)}
			err := tg.db.Save(&x, Tk.Title).Exec(c.ctx)
			if slices.Contains(visible[c.name], s.id) {
				if err != nil {
					t.Errorf("ticket %d: %v", s.id, err)
				}
			} else if !errors.Is(err, velox.ErrNotFound) {
				t.Errorf("ticket %d: %v, want ErrNotFound", s.id, err)
			}
		}
		saved := rowIDs(ticketRows(t, tg), func(r ticketRow) bool { return r.title == fmt.Sprintf("S%d", r.id) })
		wantIDs(t, saved, visible[c.name])
		// Saving the tenant column with another org is refused.
		if c.name != "anyone" {
			x := testmodel.Ticket{ID: 1, OrgID: 2, Owner: "ann", Title: "S1"}
			if err := tg.db.Save(&x, Tk.OrgID).Exec(c.ctx); !errors.Is(err, velox.ErrTenantImmutable) {
				t.Errorf("moving ticket 1 to org 2: %v", err)
			}
			if rows := ticketRows(t, tg); rows[0].org != 1 {
				t.Errorf("ticket 1 moved to org %d", rows[0].org)
			}
		}
	})
}

func TestScopeInsert(t *testing.T) {
	eachCaller(t, func(t *testing.T, tg target, c who) {
		// Ann's tickets: the tenant is filled from the context, and CreatedBy
		// from its user (anyone has none); under WithAllTenants the row names
		// its org.
		one := testmodel.Ticket{Owner: "ann", Title: "N1"}
		two := []testmodel.Ticket{{Owner: "ann", Title: "N2"}, {Owner: "ann", Title: "N3"}}
		org, by := int64(1), c.name
		if c.name == "anyone" {
			org, by = 2, ""
			one.OrgID = 2
			for i := range two {
				two[i].OrgID = 2
			}
		}
		if err := tg.db.Insert(&one).Exec(c.ctx); err != nil {
			t.Fatal(err)
		}
		if one.ID <= 8 || one.OrgID != org || one.CreatedBy != by {
			t.Errorf("inserted %+v", one)
		}
		if err := tg.db.InsertAll(two).Exec(c.ctx); err != nil {
			t.Fatal(err)
		}
		for _, x := range two {
			if x.OrgID != org || x.CreatedBy != by {
				t.Errorf("inserted %+v", x)
			}
		}
		// Bob's ticket, alone and among others: ann may not create it.
		bob := testmodel.Ticket{OrgID: org, Owner: "bob", Title: "N4"}
		mixed := []testmodel.Ticket{{OrgID: org, Owner: "ann", Title: "N5"}, {OrgID: org, Owner: "bob", Title: "N6"}}
		errOne, errAll := tg.db.Insert(&bob).Exec(c.ctx), tg.db.InsertAll(mixed).Exec(c.ctx)
		news := []string{"N1", "N2", "N3", "N4", "N5", "N6"}
		if c.name == "ann" {
			for _, err := range []error{errOne, errAll} {
				var pe *velox.PolicyError
				if !errors.As(err, &pe) || pe.Op != velox.OpInsert {
					t.Errorf("bob's ticket: %v", err)
				}
			}
			news = news[:3]
			// Another org's ticket.
			other := testmodel.Ticket{OrgID: 2, Owner: "ann", Title: "N7"}
			if err := tg.db.Insert(&other).Exec(c.ctx); !errors.Is(err, velox.ErrTenantMismatch) {
				t.Errorf("org 2's ticket: %v", err)
			}
		} else if errOne != nil || errAll != nil {
			t.Errorf("bob's ticket: %v, %v", errOne, errAll)
		}
		var got []string
		for _, r := range ticketRows(t, tg)[len(seedTickets):] {
			if r.org != org || r.createdBy != by {
				t.Errorf("row %+v", r)
			}
			got = append(got, r.title)
		}
		wantStrings(t, got, news)
	})
}

func TestScopeUpsert(t *testing.T) {
	eachCaller(t, func(t *testing.T, tg target, c who) {
		upsert := func(x *testmodel.Ticket) error {
			return tg.db.Insert(x).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(c.ctx)
		}
		done := func() []int64 { return rowIDs(ticketRows(t, tg), func(r ticketRow) bool { return r.done }) }
		// A1 is ticket 1 in org 1 and ticket 6 in org 2, neither done.
		a1 := testmodel.Ticket{Owner: "ann", Title: "A1", Done: true}
		id, want := int64(1), []int64{1, 2, 3, 8}
		if c.name == "anyone" {
			a1.OrgID = 2
			id, want = 6, []int64{2, 3, 6, 8}
		}
		err := upsert(&a1)
		if tg.d == velox.MySQL && c.name != "anyone" {
			// ON DUPLICATE KEY UPDATE also fires on the primary key, which
			// does not include the tenant.
			if !errors.Is(err, velox.ErrTenantUpsert) {
				t.Errorf("MySQL: %v", err)
			}
			wantIDs(t, done(), []int64{2, 3, 8})
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if tg.d != velox.MySQL && a1.ID != id {
			t.Errorf("RETURNING: ID %d", a1.ID)
		}
		wantIDs(t, done(), want)

		// B1 in org 1 is bob's ticket 3, done. Ann's upsert must not touch it.
		b1 := testmodel.Ticket{Owner: "ann", Title: "B1", Done: false}
		if c.name == "anyone" {
			b1.OrgID = 1
		}
		err = upsert(&b1)
		if c.name == "ann" {
			var pe *velox.PolicyError
			if !errors.As(err, &pe) || pe.Op != velox.OpUpdate || !errors.Is(err, velox.ErrDenied) {
				t.Errorf("bob's row: %v", err)
			}
			rows := ticketRows(t, tg)
			if len(rows) != len(seedTickets) || rows[2] != seededRows()[2] {
				t.Errorf("bob's row changed: %+v", rows)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		want = slices.DeleteFunc(want, func(id int64) bool { return id == 3 })
		wantIDs(t, done(), want)
	})
}

// TestUpsertSchemaQualifiedPostgres: a Rewrite hook that moves Ticket to the
// schema vx_billing makes the whole upsert schema-qualified, DO UPDATE's WHERE
// (the scope and the policy) included: ann's upsert updates vx_billing's row
// and leaves public's alone, and bob's row is refused as in TestScopeUpsert.
func TestUpsertSchemaQualifiedPostgres(t *testing.T) {
	for _, tg := range targets(t, nil) {
		t.Run(tg.name, func(t *testing.T) {
			if tg.d != velox.Postgres {
				t.Skip("schema-qualified DO UPDATE … WHERE is PostgreSQL's")
			}
			setup(t, tg)
			tg.exec(t, "DROP SCHEMA IF EXISTS vx_billing CASCADE")
			tg.exec(t, "CREATE SCHEMA vx_billing")
			t.Cleanup(func() { tg.exec(t, "DROP SCHEMA vx_billing CASCADE") })
			tg.exec(t, "CREATE TABLE vx_billing.tickets (LIKE public.tickets INCLUDING ALL)")
			for _, r := range seedTickets {
				tg.exec(t, "INSERT INTO vx_billing.tickets (created_by, id, org_id, owner, title, done, invoice_id, parent_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
					"seed", r.id, r.org, r.owner, r.title, r.done, r.invoice, r.parent)
			}
			move := velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
				e := *st.Entity
				e.Schema = "vx_billing"
				st.Entity = &e
				return nil
			})
			var db *velox.DB
			if tg.name == "postgres-pgx" {
				pool, err := pgxpool.New(ctx, os.Getenv("VELOX_TEST_POSTGRES"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(pool.Close)
				db = veloxpgx.Open(pool, move)
			} else {
				db = velox.Open(velox.Postgres, tg.direct, move)
			}
			done := func(table string) []int64 {
				rs, err := tg.direct.QueryContext(ctx, "SELECT id FROM "+table+" WHERE done ORDER BY id")
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = rs.Close() }()
				var out []int64
				for rs.Next() {
					var id int64
					if err := rs.Scan(&id); err != nil {
						t.Fatal(err)
					}
					out = append(out, id)
				}
				if err := rs.Err(); err != nil {
					t.Fatal(err)
				}
				return out
			}
			upsert := func(x *testmodel.Ticket) error {
				return db.Insert(x).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(ann.ctx)
			}
			a1 := testmodel.Ticket{Owner: "ann", Title: "A1", Done: true}
			if err := upsert(&a1); err != nil {
				t.Fatal(err)
			}
			if a1.ID != 1 {
				t.Errorf("RETURNING: ID %d, want 1", a1.ID)
			}
			wantIDs(t, done("vx_billing.tickets"), []int64{1, 2, 3, 8})
			wantIDs(t, done("public.tickets"), []int64{2, 3, 8})

			// B1 is bob's ticket 3: ann's upsert is refused, and nothing changes.
			b1 := testmodel.Ticket{Owner: "ann", Title: "B1", Done: false}
			err := upsert(&b1)
			var pe *velox.PolicyError
			if !errors.As(err, &pe) || pe.Op != velox.OpUpdate || !strings.Contains(err.Error(), "the existing row is hidden from this upsert") {
				t.Errorf("bob's row: %v", err)
			}
			wantIDs(t, done("vx_billing.tickets"), []int64{1, 2, 3, 8})
			wantIDs(t, done("public.tickets"), []int64{2, 3, 8})
		})
	}
}
