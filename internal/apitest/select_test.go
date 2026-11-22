package apitest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

type inv = testmodel.Invoice

// report is a grouped report on invoices: the org, the sum and the count of
// the paid ones, over 100 in all, biggest first.
func report(db *velox.DB) velox.Query[inv] {
	return db.Query[inv]().Where(I.Paid.EQ(true)).
		Select(I.OrgID, I.Total.Sum(), velox.CountAll[inv]()).
		GroupBy(I.OrgID).
		Having(velox.Expr[inv]("? > ?", I.Total.Sum(), 100)).
		OrderBy(I.Total.Sum().Desc()).
		Limit(10)
}

// TestSelectSQL: the select list, GROUP BY, HAVING and ORDER BY of a
// projection, in each dialect, with the arguments in the order they are
// written. An ordering by an expression puts NULL last, ascending, as every
// ordering does: where the database sorts it first, a term of IS NULL goes
// before.
func TestSelectSQL(t *testing.T) {
	for _, tc := range []struct {
		d    velox.Dialect
		sql  string
		args []any
	}{
		{velox.Postgres,
			`SELECT "org_id", SUM("total"), COUNT(*) FROM "invoices" WHERE "paid" GROUP BY "org_id" HAVING (SUM("total") > $1) ORDER BY SUM("total") DESC LIMIT $2`,
			[]any{100, 10}},
		{velox.MySQL,
			"SELECT `org_id`, SUM(`total`), COUNT(*) FROM `invoices` WHERE `paid` = 1 GROUP BY `org_id` HAVING (SUM(`total`) > ?) ORDER BY SUM(`total`) IS NULL DESC, SUM(`total`) DESC LIMIT ?",
			[]any{100, 10}},
		{velox.SQLite,
			`SELECT "org_id", SUM("total"), COUNT(*) FROM "invoices" WHERE "paid" = 1 GROUP BY "org_id" HAVING (SUM("total") > ?) ORDER BY SUM("total") IS NULL DESC, SUM("total") DESC LIMIT 10`,
			[]any{100}},
	} {
		db := openFake(tc.d, &fakeconn.Exec{})
		text, args, err := report(db).SQL(ctx)
		if err != nil || text != tc.sql {
			t.Errorf("%v:\n got %s %v\nwant %s", tc.d, text, err, tc.sql)
		}
		if len(args) != len(tc.args) {
			t.Errorf("%v: args %v, want %v", tc.d, args, tc.args)
			continue
		}
		for i := range args {
			if args[i] != tc.args[i] {
				t.Errorf("%v: arg %d is %v, want %v", tc.d, i, args[i], tc.args[i])
			}
		}
	}
}

// TestSelectHavingByName: a condition by name in Having is resolved against
// the entity, as one in Where is, and renders as the typed one does.
func TestSelectHavingByName(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	q := db.Query[inv]().Select(I.OrgID).GroupBy(I.OrgID).Limit(5)
	byName, _, err := q.Having(velox.Pred[inv](velox.FieldEQ("org_id", int64(1)))).SQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	typed, _, err := q.Having(I.OrgID.EQ(1)).SQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if byName != typed || !strings.Contains(byName, `HAVING "org_id" = $1`) {
		t.Errorf("by name:\n%s\ntyped:\n%s", byName, typed)
	}
}

// TestSelectTerms: each kind of term renders, aggregates over expressions
// and DISTINCT too, and an Expr takes a term in place of a column.
func TestSelectTerms(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	month := velox.Calc[inv]("EXTRACT(MONTH FROM ?)", I.CreatedAt)
	net := velox.Calc[inv]("? * ?", I.Total, 0.9)
	text, args, err := db.Query[inv]().
		Select(month, net.Sum(), I.OrgID.CountDistinct(), I.Note.Count(), net.Avg(), I.Total.Min(), I.Total.Max()).
		GroupBy(month).
		Limit(5).SQL(ctx)
	want := `SELECT (EXTRACT(MONTH FROM "created_at")), SUM(("total" * $1)), COUNT(DISTINCT "org_id"), COUNT("note"), AVG(("total" * $2)), MIN("total"), MAX("total") ` +
		`FROM "invoices" GROUP BY (EXTRACT(MONTH FROM "created_at")) LIMIT $3`
	if err != nil || text != want {
		t.Errorf("\n got %s %v\nwant %s", text, err, want)
	}
	if len(args) != 3 || args[0] != 0.9 || args[1] != 0.9 || args[2] != 5 {
		t.Errorf("args %v", args)
	}
}

// TestOrderByExpression: an entity query orders by an expression, and the
// same shape with other values renders alike.
func TestOrderByExpression(t *testing.T) {
	for _, tc := range []struct {
		d   velox.Dialect
		sql string
	}{
		{velox.Postgres, `SELECT "id" FROM "invoices" ORDER BY (LOWER("number")), "id" DESC LIMIT $1`},
		{velox.MySQL, "SELECT `id` FROM `invoices` ORDER BY (LOWER(`number`)) IS NULL, (LOWER(`number`)), `id` DESC LIMIT ?"},
		{velox.SQLite, `SELECT "id" FROM "invoices" ORDER BY (LOWER("number")) IS NULL, (LOWER("number")), "id" DESC LIMIT 5`},
	} {
		db := openFake(tc.d, &fakeconn.Exec{})
		text, _, err := db.Query[inv]().Columns(I.ID).
			OrderBy(velox.Calc[inv]("LOWER(?)", I.Number).Asc(), I.ID.Desc()).Limit(5).SQL(ctx)
		if err != nil || text != tc.sql {
			t.Errorf("%v:\n got %s %v\nwant %s", tc.d, text, err, tc.sql)
		}
	}
	// After reads the ordering's columns from the last row: not an expression.
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	_, err := db.Query[inv]().Columns(I.ID).After(&inv{ID: 1}).
		OrderBy(velox.Calc[inv]("LOWER(?)", I.Number).Asc(), I.ID.Asc()).Limit(5).All(ctx)
	if err == nil || !strings.Contains(err.Error(), "expression") {
		t.Errorf("After with an expression order: %v", err)
	}
}

// TestSelectKeepsPolicy: a projection reads under the Read policy, the
// scope and the interceptors, as every query does, and a refused caller
// sends nothing.
func TestSelectKeepsPolicy(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openPolicied(velox.Postgres, e)
	q := db.Query[testmodel.Ticket]().
		Select(Tk.Owner, velox.CountAll[testmodel.Ticket]()).
		GroupBy(Tk.Owner).Limit(10)
	for row, err := range q.Rows(asUser("ann")) {
		_, _ = row, err
	}
	wantSent(t, e, `SELECT "owner", COUNT(*) FROM "tickets" WHERE "org_id" = $1 AND "owner" = $2 GROUP BY "owner" LIMIT $3`, int64(1), "ann", 10)

	calls := e.Calls
	var got error
	for _, err := range q.Rows(asUser("")) {
		got = err
	}
	if !errors.Is(got, velox.ErrDenied) || e.Calls != calls {
		t.Errorf("no user: %v, %d statements sent", got, e.Calls-calls)
	}
}

// TestSelectRows: the rows come back by position, as the database reports
// them, on a real SQLite database.
func TestSelectRows(t *testing.T) {
	db := connectInvoices(t, ":memory:")
	type row struct {
		org     int64
		n       int64
		longest string
	}
	var got []row
	q := db.Query[inv]().
		Select(I.OrgID, velox.CountAll[inv](), velox.Calc[inv]("MAX(?)", I.Number)).
		GroupBy(I.OrgID).Limit(10)
	for r, err := range q.Rows(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		var x row
		if err := r.Scan(&x.org, &x.n, &x.longest); err != nil {
			t.Fatal(err)
		}
		got = append(got, x)
	}
	if len(got) != 1 || got[0] != (row{1, 3, "c"}) {
		t.Errorf("got %v, want [{1 3 c}]", got)
	}
	// A break ends the read and frees the connection.
	for range q.Rows(ctx) {
		break
	}
	if n, err := db.Query[inv]().Count(ctx); err != nil || n != 3 {
		t.Errorf("after a break: %d, %v", n, err)
	}
}

// TestSelectRefused: a projection does not mix with what reads entities, and
// a malformed one fails before anything is sent.
func TestSelectRefused(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	base := func() velox.Query[inv] {
		return db.Query[inv]().Select(I.OrgID).Limit(5)
	}
	for _, tc := range []struct {
		name string
		run  func() error
		want string
	}{
		{"All", func() error { _, err := base().All(ctx); return err }, "Rows"},
		{"One", func() error { _, err := base().One(ctx); return err }, "Rows"},
		{"First", func() error { _, err := base().First(ctx); return err }, "Rows"},
		{"Iter", func() error {
			for _, err := range base().Iter(ctx) {
				return err
			}
			return nil
		}, "Rows"},
		{"Count", func() error { _, err := base().Count(ctx); return err }, "Rows"},
		{"Exists", func() error { _, err := base().Exists(ctx); return err }, "Rows"},
		{"Sum", func() error { _, err := velox.Sum(ctx, base(), I.Total); return err }, "Rows"},
		{"CountBy", func() error { _, err := velox.CountBy(ctx, base().AllowUnbounded(), I.OrgID.Key()); return err }, "Rows"},
		{"Columns", func() error { return rowsErr(base().Columns(I.ID)) }, "Columns"},
		{"Omit", func() error { return rowsErr(base().Omit(I.Note)) }, "Omit"},
		{"With", func() error { return rowsErr(base().With(I.Items)) }, "With"},
		{"Join", func() error {
			return rowsErr(db.Query[testmodel.Ticket]().Select(Tk.Owner).Join(Tk.Invoice).Limit(5))
		}, "Join"},
		{"After", func() error { return rowsErr(base().After(&inv{ID: 1})) }, "After"},
		{"ForUpdate", func() error { return rowsErr(base().ForUpdate()) }, "lock"},
		{"no terms", func() error { return rowsErr(db.Query[inv]().Select().Limit(5)) }, "no terms"},
		{"GroupBy without Select", func() error { return rowsErr(db.Query[inv]().GroupBy(I.OrgID).Limit(5)) }, "Select"},
		{"Having without Select", func() error {
			return rowsErr(db.Query[inv]().Having(velox.Expr[inv]("? > 1", I.Total)).Limit(5))
		}, "Select"},
		{"a relation in Having", func() error {
			return rowsErr(base().GroupBy(I.OrgID).Having(I.Items.Has()))
		}, "Having"},
		{"a relation by name in Having", func() error {
			return rowsErr(base().GroupBy(I.OrgID).Having(velox.Pred[inv](velox.HasEdge("items"))))
		}, "Having"},
		{"a relation inside Not in Having", func() error {
			return rowsErr(base().GroupBy(I.OrgID).Having(velox.Not(I.Items.Has())))
		}, "Having"},
		{"a column subquery in Having", func() error {
			return rowsErr(base().GroupBy(I.OrgID).Having(velox.Pred[inv](velox.FieldInSelect("id", "Item", "invoice_id"))))
		}, "Having"},
		{"an unknown column by name in Having", func() error {
			return rowsErr(base().GroupBy(I.OrgID).Having(velox.Pred[inv](velox.FieldEQ("nope", 1))))
		}, "nope"},
		{"no Limit", func() error { return rowsErr(db.Query[inv]().Select(I.OrgID)) }, "Limit"},
		{"aggregate of an aggregate", func() error {
			return rowsErr(db.Query[inv]().Select(I.Total.Sum().Max()).Limit(5))
		}, "aggregate"},
		{"a bad expression", func() error {
			return rowsErr(db.Query[inv]().Select(velox.Calc[inv]("(SELECT 1)")).Limit(5))
		}, "SELECT"},
		{"a column of another entity", func() error {
			return rowsErr(db.Query[inv]().Select(velox.Calc[inv]("?", testmodel.Tickets.ID)).Limit(5))
		}, "another entity"},
		{"a term of another entity", func() error {
			return rowsErr(db.Query[inv]().Select(velox.Calc[inv]("?", Tk.ID)).Limit(5))
		}, "another entity"},
		{"an encoded column", func() error {
			return rowsErr(db.Query[fixture.JDoc]().Select(velox.Calc[fixture.JDoc]("?", fixture.JDocs.Attrs)).Limit(5))
		}, "encoded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := e.Calls
			err := tc.run()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error naming %q", err, tc.want)
			}
			if e.Calls != before {
				t.Errorf("%d statements sent", e.Calls-before)
			}
		})
	}
}

// rowsErr runs q's Rows and returns the error it yields.
func rowsErr[E any](q velox.Query[E]) error {
	for _, err := range q.Rows(ctx) {
		return err
	}
	return nil
}

// TestSelectRenderCache: the same shape with other values renders alike, and
// another aggregate or column renders apart.
func TestSelectRenderCache(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	sqlOf := func(term velox.Term[inv], v any) (string, []any) {
		t.Helper()
		text, args, err := db.Query[inv]().Select(term).
			Having(velox.Expr[inv]("? > ?", term, v)).Limit(1).SQL(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return text, args
	}
	a, argsA := sqlOf(I.Total.Sum(), 1)
	b, argsB := sqlOf(I.Total.Sum(), 2)
	if a != b || argsA[0] != 1 || argsB[0] != 2 {
		t.Errorf("same shape: %s %v / %s %v", a, argsA, b, argsB)
	}
	seen := map[string]bool{a: true}
	for _, other := range []velox.Term[inv]{I.Total.Avg(), I.OrgID.Sum(), I.Total.Max(), velox.CountAll[inv]()} {
		o, _ := sqlOf(other, 1)
		if seen[o] {
			t.Errorf("two shapes render alike: %s", o)
		}
		seen[o] = true
	}
}

// TestRewriteHookCannotChangeSelect: the scan reads the terms' columns by
// position, so a Rewrite hook that changes them is refused.
func TestRewriteHookCannotChangeSelect(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		if st.Project != nil {
			st.Project.Terms = st.Project.Terms[:0]
		}
		return nil
	}))
	var got error
	for _, err := range report(db).Rows(ctx) {
		got = err
	}
	if got == nil || !strings.Contains(got.Error(), "Project") || e.Calls != 0 {
		t.Errorf("a hook that empties the terms: %v, %d statements sent", got, e.Calls)
	}
}

// TestSelectHandleAggregates: a column handle is a term as it is, and its
// aggregates are the ones its type allows: Count on every scalar handle, Min
// and Max on an ordered, number or text one, Sum and Avg on a number one.
func TestSelectHandleAggregates(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	text, _, err := db.Query[inv]().Limit(1).Select(
		I.ID, I.Total.Sum(), I.Total.Avg(), I.Total.Min(), I.Number.Max(),
		I.Paid.Count(), I.Number.CountDistinct(), I.PaidAt.Max(), I.PaidAt.Count(),
	).SQL(ctx)
	want := `SELECT "id", SUM("total"), AVG("total"), MIN("total"), MAX("number"), COUNT("paid"), COUNT(DISTINCT "number"), MAX("paid_at"), COUNT("paid_at") FROM "invoices" LIMIT $1`
	if err != nil || text != want {
		t.Errorf("got %s %v\nwant %s", text, err, want)
	}
}

// TestSelectRaw: SelectRaw is Select of one Calc, and keeps the policy
// like it: the text is guarded, and the statement's conditions are kept.
func TestSelectRaw(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	got, _, err := db.Query[inv]().Where(I.Paid.EQ(true)).Limit(3).
		SelectRaw("LOWER(?) || ?", I.Number, "!").SQL(ctx)
	want := `SELECT (LOWER("number") || $1) FROM "invoices" WHERE "paid" LIMIT $2`
	if err != nil || got != want {
		t.Errorf("got %s %v\nwant %s", got, err, want)
	}
	// With Select before it, it adds a term.
	got, _, err = db.Query[inv]().Limit(3).Select(I.ID).SelectRaw("? * 2", I.Total).SQL(ctx)
	want = `SELECT "id", ("total" * 2) FROM "invoices" LIMIT $1`
	if err != nil || got != want {
		t.Errorf("got %s %v\nwant %s", got, err, want)
	}
	// A subquery in the text is refused, as Expr refuses it.
	_, _, err = db.Query[inv]().Limit(3).SelectRaw("(SELECT 1)").SQL(ctx)
	if err == nil || !strings.Contains(err.Error(), "SELECT") {
		t.Errorf("a subquery in SelectRaw: %v", err)
	}
}

// TestSelectRefusesWhatIsNoTerm: Select and GroupBy take a handle or a Term
// of the query's entity; anything else fails the statement before it is
// sent.
func TestSelectRefusesWhatIsNoTerm(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	for name, q := range map[string]velox.Query[inv]{
		"a string":          db.Query[inv]().Limit(1).Select("org_id"),
		"a nil":             db.Query[inv]().Limit(1).Select(nil),
		"a foreign handle":  db.Query[inv]().Limit(1).Select(Tk.Owner),
		"a foreign term":    db.Query[inv]().Limit(1).Select(Tk.ID.Sum()),
		"a foreign GroupBy": db.Query[inv]().Limit(1).Select(I.ID).GroupBy(Tk.Owner),
	} {
		if _, _, err := q.SQL(ctx); err == nil {
			t.Errorf("%s: no error", name)
		}
		if err := rowsErr(q); err == nil || e.Calls != 0 {
			t.Errorf("%s: Rows: %v after %d statements", name, err, e.Calls)
		}
	}
}
