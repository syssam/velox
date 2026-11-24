package apitest

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/untyped"
)

// audited is the correlated condition of ADR 0155's third case: no audit row
// names the outer ticket. The text reads audit_log, which no policy guards.
func audited() velox.Pred[testmodel.Ticket] {
	return velox.ExprRaw[testmodel.Ticket]("audit trail",
		"NOT EXISTS (SELECT 1 FROM audit_log m WHERE m.org_id = ? AND m.entity_id = CAST(? AS TEXT))",
		Tk.OrgID, Tk.ID)
}

// TestExprRawKeepsOuterPolicy: the text is written as given, with nothing
// added inside it, while the outer statement keeps its Read policy and
// scope; the handles are qualified by the outer table.
func TestExprRawKeepsOuterPolicy(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openPolicied(velox.Postgres, e)
	if _, err := db.Query[testmodel.Ticket]().Where(audited()).Limit(5).All(asUser("ann")); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT `+ticketCols+` FROM "tickets" WHERE (NOT EXISTS (SELECT 1 FROM audit_log m WHERE m.org_id = "tickets"."org_id" AND m.entity_id = CAST("tickets"."id" AS TEXT))) AND "org_id" = $1 AND "owner" = $2 LIMIT 5`,
		int64(1), "ann")

	// A text key against a cast integer key, with values bound in order.
	inBill := velox.ExprRaw[testmodel.Ticket]("bills are text-keyed",
		"? IN (SELECT CAST(b.id AS TEXT) FROM invoices b WHERE b.org_id IN (?, ?))", Tk.Title, int64(1), int64(2))
	if _, err := db.Query[testmodel.Ticket]().Where(Tk.Done.EQ(true), inBill).Limit(1).All(asUser("ann")); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT `+ticketCols+` FROM "tickets" WHERE "done" AND ("tickets"."title" IN (SELECT CAST(b.id AS TEXT) FROM invoices b WHERE b.org_id IN ($1, $2))) AND "org_id" = $3 AND "owner" = $4 LIMIT 1`,
		int64(1), int64(2), int64(1), "ann")

	// The policy's refusal still sends nothing.
	calls := e.Calls
	if _, err := db.Query[testmodel.Ticket]().Where(audited()).Limit(1).All(asUser("")); err == nil {
		t.Fatal("no user: want the policy's refusal")
	}
	if e.Calls != calls {
		t.Errorf("%d denied statements sent", e.Calls-calls)
	}
}

// TestExprRawQualifies: a handle is the outer row's column under every
// dialect's quoting and placeholder, under a self-join (the main table keeps
// its name, the join an alias), inside a relation's EXISTS (velox's alias),
// inside Or and Not, and from untyped's Col.
func TestExprRawQualifies(t *testing.T) {
	for _, tc := range []struct {
		d   velox.Dialect
		sql string
	}{
		{velox.Postgres, `SELECT COUNT(*) FROM "tickets" WHERE (EXISTS (SELECT 1 FROM t2 x WHERE x.id = "tickets"."id" AND x.n > $1))`},
		{velox.MySQL, "SELECT COUNT(*) FROM `tickets` WHERE (EXISTS (SELECT 1 FROM t2 x WHERE x.id = `tickets`.`id` AND x.n > ?))"},
		{velox.SQLite, `SELECT COUNT(*) FROM "tickets" WHERE (EXISTS (SELECT 1 FROM t2 x WHERE x.id = "tickets"."id" AND x.n > ?))`},
	} {
		e := &fakeconn.Exec{Rows: [][]any{{int64(0)}}}
		db := openFake(tc.d, e)
		p := velox.ExprRaw[testmodel.Ticket]("r", "EXISTS (SELECT 1 FROM t2 x WHERE x.id = ? AND x.n > ?)", Tk.ID, 3)
		if _, err := db.Query[testmodel.Ticket]().Where(p).Count(ctx); err != nil {
			t.Fatalf("%v: %v", tc.d, err)
		}
		wantSent(t, e, tc.sql, 3)
	}

	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	sqlOf := func(q velox.Query[testmodel.Ticket]) string {
		t.Helper()
		text, _, err := q.SQL(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return text
	}
	joined := sqlOf(db.Query[testmodel.Ticket]().Join(Tk.Parent).Where(audited()).Limit(1))
	if !strings.Contains(joined, `m.org_id = "tickets"."org_id" AND m.entity_id = CAST("tickets"."id" AS TEXT)`) {
		t.Errorf("under a join: %s", joined)
	}
	inv := velox.ExprRaw[testmodel.Invoice]("r", "NOT EXISTS (SELECT 1 FROM audit_log m WHERE m.entity_id = CAST(? AS TEXT))", I.ID)
	nested := sqlOf(db.Query[testmodel.Ticket]().Where(Tk.Invoice.HasWith(inv)).Limit(1))
	if !strings.Contains(nested, `AS "vx1" WHERE "vx1"."id" = "tickets"."invoice_id" AND (NOT EXISTS (SELECT 1 FROM audit_log m WHERE m.entity_id = CAST("vx1"."id" AS TEXT)))`) {
		t.Errorf("inside a relation's EXISTS: %s", nested)
	}
	or := sqlOf(db.Query[testmodel.Ticket]().Where(velox.Or(Tk.Done.EQ(true), velox.Not(audited()))).Limit(1))
	if !strings.Contains(or, `("done" OR NOT ((NOT EXISTS (SELECT 1 FROM audit_log m WHERE m.org_id = "tickets"."org_id"`) {
		t.Errorf("inside Or and Not: %s", or)
	}
	tq := untyped.Query(db, untyped.TableOf[testmodel.Ticket]()).
		Where(untyped.ExprRaw("r", "EXISTS (SELECT 1 FROM t2 x WHERE x.id = ?)", untyped.Col(0))).Limit(1)
	text, _, err := tq.SQL(ctx)
	if err != nil || !strings.Contains(text, `(EXISTS (SELECT 1 FROM t2 x WHERE x.id = "tickets"."id"))`) {
		t.Errorf("untyped: %s, %v", text, err)
	}
}

// TestExprRawWrites: in an UPDATE's and a DELETE's Where the Write policy
// still applies to the outer rows, and the handles name the outer table.
func TestExprRawWrites(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openPolicied(velox.Postgres, e)
	if _, err := db.Update[testmodel.Ticket]().Set(Tk.Done.Set(true)).Where(audited()).Exec(asUser("ann")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.SQL, `WHERE (NOT EXISTS (SELECT 1 FROM audit_log m WHERE m.org_id = "tickets"."org_id" AND m.entity_id = CAST("tickets"."id" AS TEXT))) AND "org_id" = `) ||
		!strings.Contains(e.SQL, `"owner" = `) {
		t.Errorf("update: %s", e.SQL)
	}
	if _, err := db.Delete[testmodel.Ticket]().Where(audited()).Exec(asUser("ann")); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(e.SQL, `DELETE FROM "tickets" WHERE (NOT EXISTS (SELECT 1 FROM audit_log m WHERE m.org_id = "tickets"."org_id"`) ||
		!strings.Contains(e.SQL, `"owner" = `) {
		t.Errorf("delete: %s", e.SQL)
	}
}

// TestExprRawHaving: a raw condition in a projection's HAVING, where an
// Expr may not hold a subquery.
func TestExprRawHaving(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	text, args, err := db.Query[testmodel.Ticket]().Select(Tk.Owner).GroupBy(Tk.Owner).
		Having(velox.ExprRaw[testmodel.Ticket]("r", "? IN (SELECT u.name FROM users u WHERE u.active = ?)", Tk.Owner, true)).
		Limit(5).SQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, `HAVING ("tickets"."owner" IN (SELECT u.name FROM users u WHERE u.active = $1))`) || len(args) == 0 || args[0] != true {
		t.Errorf("having: %s %v", text, args)
	}
}

// TestExprRawRefused: the guard on structure stays, and the reason is
// required; words such as SELECT and CAST pass.
func TestExprRawRefused(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	raw := func(reason, text string, args ...any) velox.Pred[testmodel.Ticket] {
		return velox.ExprRaw[testmodel.Ticket](reason, text, args...)
	}
	for _, tc := range []struct {
		name string
		p    velox.Pred[testmodel.Ticket]
		want string
	}{
		{"no reason", raw("", "? = 1", Tk.ID), "reason"},
		{"blank reason", raw(" \t", "? = 1", Tk.ID), "reason"},
		{"semicolon", raw("r", "? = 1; DROP TABLE tickets", Tk.ID), ";"},
		{"line comment", raw("r", "? IN (SELECT 1 -- x\n)", Tk.ID), "--"},
		{"block comment", raw("r", "? IN (SELECT /* x */ 1)", Tk.ID), "/*"},
		{"hash comment", raw("r", "? = 1 # x", Tk.ID), "#"},
		{"closes early", raw("r", "? IN (SELECT 1)) OR (1 = 1", Tk.ID), "parenthes"},
		{"never closes", raw("r", "? IN (SELECT 1", Tk.ID), "parenthes"},
		{"unterminated literal", raw("r", "? = 'abc", Tk.Title), "literal"},
		{"unclosed quoted name", raw("r", `? IN (SELECT "id FROM t)`, Tk.ID), "quoted identifier"},
		{"unclosed backtick", raw("r", "? IN (SELECT `id FROM t)", Tk.ID), "quoted identifier"},
		{"doubled quote in a quoted name", raw("r", `? IN (SELECT "a""b" FROM t)`, Tk.ID), "quoted identifier"},
		{"question mark in a quoted name", raw("r", `? IN (SELECT "a?" FROM t)`, Tk.ID), "quoted identifier"},
		{"semicolon after a quoted name", raw("r", `? IN (SELECT "id" FROM t); DELETE FROM t`, Tk.ID), ";"},
		{"dollar placeholder", raw("r", "? = $1", Tk.ID), "$"},
		{"backslash", raw("r", `? = \`, Tk.ID), `\`},
		{"empty", raw("r", " "), "empty"},
		{"too few arguments", raw("r", "? = ?", Tk.ID), "2"},
		{"another entity's column", raw("r", "? = 1", testmodel.Invoices.ID), "another entity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := e.Calls
			_, err := db.Query[testmodel.Ticket]().Where(tc.p).Limit(1).All(ctx)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "Expr") {
				t.Fatalf("got %v, want an ExprRaw error naming %q", err, tc.want)
			}
			if e.Calls != before {
				t.Errorf("%d statements sent", e.Calls-before)
			}
		})
	}
	for _, text := range []string{
		"? IN (SELECT CAST(b.id AS TEXT) FROM invoices b WHERE b.org_id IN (?))",
		"NOT EXISTS (SELECT 1 FROM audit_log m WHERE m.id = ? AND m.x = ?)",
		`? IN (SELECT "id" FROM "order" WHERE "n" > ?)`,
		"? IN (SELECT `id` FROM `order` WHERE `n` > ?)",
		"? IN (WITH x AS (SELECT 1 AS id) SELECT id FROM x UNION SELECT ?)",
		"? = (SELECT MAX(n) OVER () FROM t LIMIT ?)",
	} {
		args := []any{Tk.ID}
		for range strings.Count(text, "?") - 1 {
			args = append(args, 1)
		}
		if _, _, err := db.Query[testmodel.Ticket]().Where(raw("r", text, args...)).Limit(1).SQL(ctx); err != nil {
			t.Errorf("%s: %v", text, err)
		}
	}
}

// TestExprRawReasons: an Around hook reads every raw condition's reason
// from the statement, in order, inside Or and Not and in writes too; a
// statement without one reports none, and PolicyBypass stays empty.
func TestExprRawReasons(t *testing.T) {
	var reasons [][]string
	var bypass []string
	e := &fakeconn.Exec{Rows: [][]any{{int64(0)}}}
	db := openPolicied(velox.Postgres, e, velox.WithAround(func(c context.Context, st *velox.Statement, next func(context.Context) error) error {
		reasons = append(reasons, st.RawReasons())
		bypass = append(bypass, st.PolicyBypass)
		return next(c)
	}))
	other := velox.ExprRaw[testmodel.Ticket]("bills are text-keyed", "? IN (SELECT CAST(b.id AS TEXT) FROM invoices b)", Tk.Title)
	c := asUser("ann")
	if _, err := db.Query[testmodel.Ticket]().Where(audited(), velox.Or(Tk.Done.EQ(true), velox.Not(other))).Count(c); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Query[testmodel.Ticket]().Where(velox.Expr[testmodel.Ticket]("? = 1", Tk.ID)).Count(c); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Update[testmodel.Ticket]().Set(Tk.Done.Set(true)).Where(other).Exec(c); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Delete[testmodel.Ticket]().Where(audited()).Exec(c); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"audit trail", "bills are text-keyed"}, nil, {"bills are text-keyed"}, {"audit trail"}}
	if !slices.EqualFunc(reasons, want, slices.Equal) {
		t.Errorf("reasons %q, want %q", reasons, want)
	}
	for i, b := range bypass {
		if b != "" {
			t.Errorf("statement %d: PolicyBypass %q", i, b)
		}
	}
}

// TestExprRawRenderCache: the cache keys the text and what each slot
// holds, not the values, nor the reason; an Expr of the same text renders
// apart, as its columns are not qualified.
func TestExprRawRenderCache(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	sqlOf := func(p velox.Pred[testmodel.Ticket]) (string, []any) {
		t.Helper()
		text, args, err := db.Query[testmodel.Ticket]().Columns(Tk.ID).Where(p).Limit(1).SQL(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return text, args
	}
	plain, _ := sqlOf(velox.Expr[testmodel.Ticket]("? = ?", Tk.Title, "a"))
	a, argsA := sqlOf(velox.ExprRaw[testmodel.Ticket]("one", "? = ?", Tk.Title, "a"))
	b, argsB := sqlOf(velox.ExprRaw[testmodel.Ticket]("two", "? = ?", Tk.Title, "b"))
	if a != b || argsA[0] != "a" || argsB[0] != "b" {
		t.Errorf("same shape: %s %v / %s %v", a, argsA, b, argsB)
	}
	if a == plain || !strings.Contains(a, `("tickets"."title" = $1)`) || !strings.Contains(plain, `("title" = $1)`) {
		t.Errorf("Expr and ExprRaw of one text: %s / %s", plain, a)
	}
	if strings.Contains(a, "one") || strings.Contains(b, "two") {
		t.Errorf("the reason is in the SQL: %s", a)
	}
}
