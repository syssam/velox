package apitest

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// ticketsOf is the condition of ADR 0157's case on the test model: the
// invoice's note, text, names a ticket by its integer id.
func ticketsOf(raw bool, ps ...velox.Predicate) velox.Pred[testmodel.Invoice] {
	if raw {
		return velox.Pred[testmodel.Invoice](velox.FieldInSelectRaw("notes name tickets", "note", "Ticket", "id", ps...))
	}
	return velox.Pred[testmodel.Invoice](velox.FieldInSelect("note", "Ticket", "id", ps...))
}

// noDoneTickets is a WithFilter filter that hides done tickets.
func noDoneTickets(_ context.Context, _ *velox.DB, fe velox.FilterEntity, _ velox.Op) (velox.Predicate, error) {
	if fe.Name() == "Ticket" {
		return velox.FieldEQ("done", false), nil
	}
	return velox.Allow(), nil
}

// TestFieldInSelectRawSkipsInner: inside FieldInSelectRaw's subquery only
// its own conditions apply, not the target's Read policy, its scope or a
// WithFilter filter, which FieldInSelect adds; the outer statement keeps
// its own policy. The key, an integer, is made text to meet the column.
func TestFieldInSelectRawSkipsInner(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openPolicied(velox.Postgres, e, velox.WithFilter(noDoneTickets))
	sqlOf := func(p velox.Pred[testmodel.Invoice]) string {
		t.Helper()
		text, _, err := db.Query[testmodel.Invoice]().Columns(I.ID).Where(p).Limit(1).SQL(asUser("ann"))
		if err != nil {
			t.Fatal(err)
		}
		return text
	}
	title := velox.FieldEQ("title", "x")
	raw := sqlOf(ticketsOf(true, title))
	want := `SELECT "id" FROM "invoices" WHERE EXISTS (SELECT 1 FROM "tickets" AS "vx1" WHERE CAST("vx1"."id" AS TEXT) = "invoices"."note" AND "title" = $1) AND "org_id" = $2 LIMIT 1`
	if raw != want {
		t.Errorf("raw:\n got %s\nwant %s", raw, want)
	}
	plain := sqlOf(ticketsOf(false, title))
	for _, s := range []string{`CAST("vx1"."id" AS TEXT) = "invoices"."note" AND "title" = $1 AND`, `"owner" = `, `NOT "done"`} {
		if !strings.Contains(plain, s) {
			t.Errorf("FieldInSelect %s lacks %s", plain, s)
		}
	}
	// Or, And and Not hold it; the outer policy stays.
	or := sqlOf(velox.Or(I.Paid.EQ(true), velox.Not(velox.And(ticketsOf(true), I.Total.GT(1)))))
	if !strings.Contains(or, `("paid" OR NOT ((EXISTS (SELECT 1 FROM "tickets" AS "vx1" WHERE CAST("vx1"."id" AS TEXT) = "invoices"."note") AND "total" > $1))) AND "org_id" = $2`) {
		t.Errorf("Or, And, Not: %s", or)
	}
	// Writes: the Write policy's scope stays on the outer rows.
	if _, err := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(ticketsOf(true)).Exec(asUser("ann")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.SQL, `WHERE EXISTS (SELECT 1 FROM "tickets" AS "vx1" WHERE CAST("vx1"."id" AS TEXT) = "invoices"."note") AND "org_id" = `) {
		t.Errorf("update: %s", e.SQL)
	}
	if _, err := db.Delete[testmodel.Invoice]().Where(ticketsOf(true)).Exec(asUser("ann")); err != nil {
		t.Fatal(err)
	}
	if e.SQL != `DELETE FROM "invoices" WHERE EXISTS (SELECT 1 FROM "tickets" AS "vx1" WHERE CAST("vx1"."id" AS TEXT) = "invoices"."note") AND "org_id" = $1` {
		t.Errorf("delete: %s", e.SQL)
	}
}

// TestFieldInSelectRawRows: on SQLite, an owner whose name is the id of a
// note that INote's interceptor hides (2) or its Read policy hides (3)
// matches FieldInSelectRaw, and not FieldInSelect.
func TestFieldInSelectRawRows(t *testing.T) {
	db := interceptorDB(t)
	O := fixture.IOwners
	for id, name := range map[int64]string{1: "3", 2: "2"} {
		if _, err := db.Update[fixture.IOwner]().Set(O.Name.Set(name)).Where(O.ID.EQ(id)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	owners := func(p velox.Predicate) []int64 {
		t.Helper()
		os, err := db.Query[fixture.IOwner]().Where(velox.Pred[fixture.IOwner](p)).OrderBy(O.ID.Asc()).AllowUnbounded().All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return ownerIDs(os)
	}
	if got := owners(velox.FieldInSelect("name", "INote", "id")); len(got) != 0 {
		t.Errorf("FieldInSelect: %v, want none", got)
	}
	if got := owners(velox.FieldInSelectRaw("notes keep their owners", "name", "INote", "id")); !slices.Equal(got, []int64{1, 2}) {
		t.Errorf("FieldInSelectRaw: %v, want [1 2]", got)
	}
	if got := owners(velox.FieldInSelectRaw("r", "name", "INote", "id", velox.FieldEQ("hidden", true))); !slices.Equal(got, []int64{2}) {
		t.Errorf("FieldInSelectRaw with a condition: %v, want [2]", got)
	}
}

// TestFieldInSelectRawReasons: an Around hook reads FieldInSelectRaw's
// reason as it reads ExprRaw's, in order, inside Or and Not and in writes;
// FieldInSelect has none, and PolicyBypass stays empty.
func TestFieldInSelectRawReasons(t *testing.T) {
	var reasons [][]string
	var bypass []string
	e := &fakeconn.Exec{Rows: [][]any{{int64(0)}}}
	db := openPolicied(velox.Postgres, e, velox.WithAround(func(c context.Context, st *velox.Statement, next func(context.Context) error) error {
		reasons = append(reasons, st.RawReasons())
		bypass = append(bypass, st.PolicyBypass)
		return next(c)
	}))
	other := velox.ExprRaw[testmodel.Invoice]("bills are text-keyed", "? IN (SELECT b.n FROM b)", I.Number)
	c := asUser("ann")
	if _, err := db.Query[testmodel.Invoice]().Where(other, velox.Or(I.Paid.EQ(true), velox.Not(ticketsOf(true)))).Count(c); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Query[testmodel.Invoice]().Where(ticketsOf(false)).Count(c); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(ticketsOf(true)).Exec(c); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Delete[testmodel.Invoice]().Where(ticketsOf(true)).Exec(c); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"bills are text-keyed", "notes name tickets"}, nil, {"notes name tickets"}, {"notes name tickets"}}
	if !slices.EqualFunc(reasons, want, slices.Equal) {
		t.Errorf("reasons %q, want %q", reasons, want)
	}
	for i, b := range bypass {
		if b != "" {
			t.Errorf("statement %d: PolicyBypass %q", i, b)
		}
	}
	// The reason is required.
	if _, err := db.Query[testmodel.Invoice]().Where(velox.Pred[testmodel.Invoice](velox.FieldInSelectRaw(" ", "note", "Ticket", "id"))).Count(c); err == nil || !strings.Contains(err.Error(), "needs a reason") {
		t.Errorf("blank reason: %v", err)
	}
}

// TestFieldInSelectRawRenderCache: the render cache keys the structure,
// not the values nor the reason.
func TestFieldInSelectRawRenderCache(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	sqlOf := func(reason, title string) (string, []any) {
		t.Helper()
		p := velox.Pred[testmodel.Invoice](velox.FieldInSelectRaw(reason, "note", "Ticket", "id", velox.FieldEQ("title", title)))
		text, args, err := db.Query[testmodel.Invoice]().Columns(I.ID).Where(p).Limit(1).SQL(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return text, args
	}
	a, argsA := sqlOf("one", "a")
	b, argsB := sqlOf("two", "b")
	if a != b || argsA[0] != "a" || argsB[0] != "b" {
		t.Errorf("same shape: %s %v / %s %v", a, argsA, b, argsB)
	}
	if strings.Contains(a, "one") || strings.Contains(b, "two") {
		t.Errorf("the reason is in the SQL: %s", a)
	}
}
