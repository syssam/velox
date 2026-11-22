package apitest

import (
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// TestExprKeepsPolicy: an Expr is one more condition ANDed with the Read
// policy and the scope, so the query still carries both.
func TestExprKeepsPolicy(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openPolicied(velox.Postgres, e)
	q := db.Query[testmodel.Ticket]().
		Where(velox.Expr[testmodel.Ticket]("LOWER(?) = LOWER(?)", Tk.Title, "Fix")).
		Limit(5)
	if _, err := q.All(asUser("ann")); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT `+ticketCols+` FROM "tickets" WHERE (LOWER("title") = LOWER($1)) AND "org_id" = $2 AND "owner" = $3 LIMIT $4`,
		"Fix", int64(1), "ann", 5)

	// Inside Or and Not, and with the policy's own error for no user.
	or := velox.Or(
		velox.Expr[testmodel.Ticket]("LENGTH(?) > ?", Tk.Title, 10),
		velox.Not(velox.Expr[testmodel.Ticket]("? IS NULL", Tk.Owner)),
	)
	if _, err := db.Query[testmodel.Ticket]().Where(or).Limit(1).All(asUser("ann")); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT `+ticketCols+` FROM "tickets" WHERE ((LENGTH("title") > $1) OR NOT (("owner" IS NULL))) AND "org_id" = $2 AND "owner" = $3 LIMIT $4`,
		10, int64(1), "ann", 1)

	calls := e.Calls
	if _, err := db.Query[testmodel.Ticket]().Where(velox.Expr[testmodel.Ticket]("? = ?", Tk.Title, "x")).Limit(1).All(asUser("")); err == nil {
		t.Fatal("no user: want the policy's refusal")
	}
	if e.Calls != calls {
		t.Errorf("%d denied statements sent", e.Calls-calls)
	}
}

// TestExprDialects: the placeholders are the dialect's and columns are
// quoted as the dialect quotes them, in a count as in a select.
func TestExprDialects(t *testing.T) {
	for _, tc := range []struct {
		d   velox.Dialect
		sql string
	}{
		{velox.Postgres, `SELECT COUNT(*) FROM "tickets" WHERE ("title" || $1 = $2)`},
		{velox.MySQL, "SELECT COUNT(*) FROM `tickets` WHERE (`title` || ? = ?)"},
		{velox.SQLite, `SELECT COUNT(*) FROM "tickets" WHERE ("title" || ? = ?)`},
	} {
		e := &fakeconn.Exec{Rows: [][]any{{int64(0)}}}
		db := openFake(tc.d, e)
		p := velox.Expr[testmodel.Ticket]("? || ? = ?", Tk.Title, "!", "a!")
		if _, err := db.Query[testmodel.Ticket]().Where(p).Count(ctx); err != nil {
			t.Fatalf("%v: %v", tc.d, err)
		}
		wantSent(t, e, tc.sql, "!", "a!")
	}
}

// TestExprRefused: text that could read another table, end the condition or
// hide text, a wrong number of arguments and another entity's column all
// fail the statement before anything is sent.
func TestExprRefused(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	for _, tc := range []struct {
		name string
		p    velox.Pred[testmodel.Ticket]
		want string
	}{
		{"subselect", velox.Expr[testmodel.Ticket]("EXISTS (SELECT 1 FROM invoices WHERE ? = 1)", Tk.ID), "SELECT"},
		{"subselect lower case", velox.Expr[testmodel.Ticket]("? IN (select id from invoices)", Tk.ID), "select"},
		{"with", velox.Expr[testmodel.Ticket]("? IN (WITH x AS (SELECT 1) SELECT 1)", Tk.ID), "WITH"},
		{"values", velox.Expr[testmodel.Ticket]("? IN (VALUES (1))", Tk.ID), "VALUES"},
		{"table", velox.Expr[testmodel.Ticket]("? IN (TABLE invoices)", Tk.ID), "TABLE"},
		{"union", velox.Expr[testmodel.Ticket]("? = 1 UNION ?", Tk.ID, 1), "UNION"},
		{"table_to_xml", velox.Expr[testmodel.Ticket]("? = 1 OR table_to_xml('invoices', true, false, '') IS NOT NULL", Tk.ID), "table_to_xml"},
		{"query_to_xml", velox.Expr[testmodel.Ticket]("? = 1 OR Query_To_Xml('x', true, false, '') IS NOT NULL", Tk.ID), "Query_To_Xml"},
		{"pg_read_file", velox.Expr[testmodel.Ticket]("? = pg_read_file('/etc/passwd')", Tk.Title), "pg_read_file"},
		{"pg_catalog", velox.Expr[testmodel.Ticket]("? = pg_catalog.current_user", Tk.Title), "pg_catalog"},
		{"dblink", velox.Expr[testmodel.Ticket]("? = dblink_exec('x', 'y')", Tk.Title), "dblink_exec"},
		{"large object", velox.Expr[testmodel.Ticket]("? = lo_import('/x')", Tk.ID), "lo_import"},
		{"nextval", velox.Expr[testmodel.Ticket]("? = nextval('s')", Tk.ID), "nextval"},
		{"load_file", velox.Expr[testmodel.Ticket]("? = LOAD_FILE('/x')", Tk.Title), "LOAD_FILE"},
		{"sleep", velox.Expr[testmodel.Ticket]("? = 1 OR SLEEP(5) = 0", Tk.ID), "SLEEP"},
		{"benchmark", velox.Expr[testmodel.Ticket]("? = BENCHMARK(1, 1)", Tk.ID), "BENCHMARK"},
		{"ts_rewrite", velox.Expr[testmodel.Ticket]("? = 1 OR ts_rewrite('a'::tsquery, 'x') IS NULL", Tk.ID), "ts_rewrite"},
		{"ts_stat", velox.Expr[testmodel.Ticket]("? = 1 OR TS_STAT('x') IS NULL", Tk.ID), "TS_STAT"},
		{"mysql system variable", velox.Expr[testmodel.Ticket]("? = @@version", Tk.Title), "@"},
		{"mysql user variable", velox.Expr[testmodel.Ticket]("? = @x", Tk.Title), "@"},
		{"load_extension", velox.Expr[testmodel.Ticket]("? = load_extension('x')", Tk.ID), "load_extension"},
		{"semicolon", velox.Expr[testmodel.Ticket]("? = 1; DROP TABLE tickets", Tk.ID), ";"},
		{"line comment", velox.Expr[testmodel.Ticket]("? = 1 -- x", Tk.ID), "--"},
		{"block comment", velox.Expr[testmodel.Ticket]("? = 1 /* x */", Tk.ID), "/*"},
		{"hash comment", velox.Expr[testmodel.Ticket]("? = 1 # x", Tk.ID), "#"},
		{"unterminated literal", velox.Expr[testmodel.Ticket]("? = 'abc", Tk.Title), "literal"},
		{"backslash in a literal", velox.Expr[testmodel.Ticket](`? = 'a\'`, Tk.Title), "literal"},
		{"doubled quote in a literal", velox.Expr[testmodel.Ticket]("? = 'a''b'", Tk.Title), "literal"},
		{"question mark in a literal", velox.Expr[testmodel.Ticket]("? = '?'", Tk.Title), "literal"},
		{"newline in a literal", velox.Expr[testmodel.Ticket]("? = 'a\nb'", Tk.Title), "literal"},
		{"word right after a literal", velox.Expr[testmodel.Ticket]("? IN ('x'select 1)", Tk.ID), "select"},
		{"subselect after a literal", velox.Expr[testmodel.Ticket]("? = 'x' OR ? IN (SELECT 1)", Tk.Title, Tk.ID), "SELECT"},
		{"comment after a literal", velox.Expr[testmodel.Ticket]("? = 'x' -- y", Tk.Title), "--"},
		{"double quote", velox.Expr[testmodel.Ticket](`? = "a"`, Tk.Title), `"`},
		{"backtick", velox.Expr[testmodel.Ticket]("? = `a`", Tk.Title), "`"},
		{"dollar", velox.Expr[testmodel.Ticket]("? = $$a$$", Tk.Title), "$"},
		{"backslash", velox.Expr[testmodel.Ticket](`? = \`, Tk.Title), `\`},
		{"closes early", velox.Expr[testmodel.Ticket]("? = 1) OR (1 = 1", Tk.ID), "parenthes"},
		{"never closes", velox.Expr[testmodel.Ticket]("(? = 1", Tk.ID), "parenthes"},
		{"empty", velox.Expr[testmodel.Ticket]("  ", Tk.ID), "empty"},
		{"too few arguments", velox.Expr[testmodel.Ticket]("? = ?", Tk.ID), "2"},
		{"too many arguments", velox.Expr[testmodel.Ticket]("? = 1", Tk.ID, 1), "1"},
		{"another entity's column", velox.Expr[testmodel.Ticket]("? = 1", testmodel.Invoices.ID), "another entity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := e.Calls
			_, err := db.Query[testmodel.Ticket]().Where(tc.p).Limit(1).All(ctx)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "Expr") {
				t.Fatalf("got %v, want an Expr error naming %q", err, tc.want)
			}
			if e.Calls != before {
				t.Errorf("%d statements sent", e.Calls-before)
			}
		})
	}
}

// TestExprAllowedText: what a real expression needs is not refused: EXTRACT
// and TRIM use FROM, casts use ::, and operators are free.
func TestExprAllowedText(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	for _, text := range []string{
		"EXTRACT(YEAR FROM ?) = ?",
		"TRIM(BOTH ? FROM ?) = ?",
		"? % 2 = ?",
		"CASE WHEN ? > ? THEN 1 ELSE 0 END = 1",
		// String literals: what nextapp's real fragments write.
		"COALESCE(?, '') = ?",
		"REPLACE(LOWER(?), '_', '') = ?",
		"? LIKE ? ESCAPE '!'",
		"? = 'select from where with'",
		"? = '(( ; -- /* # $.a.b \"x\" `y`'",
		"COALESCE(?, '[]'::jsonb) = '[]'::jsonb",
		"? = 'caf\u00e9'",
		// PostgreSQL's @ operators, and an @ inside a literal.
		"to_tsvector(?) @@ to_tsquery(?)",
		"? @> ?",
		"? <@ ?",
		"? = 'a@b'",
	} {
		args := []any{}
		for range strings.Count(text, "?") {
			args = append(args, 1)
		}
		args[0] = Tk.ID
		if _, _, err := db.Query[testmodel.Ticket]().Where(velox.Expr[testmodel.Ticket](text, args...)).Limit(1).SQL(ctx); err != nil {
			t.Errorf("%s: %v", text, err)
		}
	}
}

// TestExprRenderCache: the same text and columns with other values render
// alike; another column or text renders apart.
func TestExprRenderCache(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	sqlOf := func(p velox.Pred[testmodel.Ticket]) (string, []any) {
		t.Helper()
		text, args, err := db.Query[testmodel.Ticket]().Columns(Tk.ID).Where(p).Limit(1).SQL(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return text, args
	}
	a, argsA := sqlOf(velox.Expr[testmodel.Ticket]("LOWER(?) = ?", Tk.Title, "a"))
	b, argsB := sqlOf(velox.Expr[testmodel.Ticket]("LOWER(?) = ?", Tk.Title, "b"))
	if a != b || argsA[0] != "a" || argsB[0] != "b" {
		t.Errorf("same shape: %s %v / %s %v", a, argsA, b, argsB)
	}
	other, _ := sqlOf(velox.Expr[testmodel.Ticket]("LOWER(?) = ?", Tk.Owner, "a"))
	if !strings.Contains(other, `"owner"`) || other == a {
		t.Errorf("another column: %s", other)
	}
	swapped, _ := sqlOf(velox.Expr[testmodel.Ticket]("? = LOWER(?)", "a", Tk.Title))
	if swapped == a {
		t.Errorf("another text renders alike: %s", swapped)
	}
	if !strings.Contains(swapped, `$1 = LOWER("title")`) {
		t.Errorf("a value before a column: %s", swapped)
	}
}
