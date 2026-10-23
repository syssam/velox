package migrate_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/migrate"
)

// A predicateCase is a block of testdata/predicates: a predicate as written,
// as PostgreSQL printed it ("" until inspected), and its normal form.
type predicateCase struct{ input, printed, normal string }

func readPredicates(t *testing.T, path string) []predicateCase {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []predicateCase
	for _, block := range strings.Split(strings.TrimSpace(string(b)), "\n\n") {
		var c predicateCase
		for _, l := range strings.Split(block, "\n") {
			key, val, _ := strings.Cut(l, ":")
			val = strings.TrimPrefix(val, " ")
			switch key {
			case "input":
				c.input = val
			case "printed":
				c.printed = val
			case "normal":
				c.normal = val
			default:
				t.Fatalf("%s: unknown line %q", path, l)
			}
		}
		if c.input == "" {
			t.Fatalf("%s: a block without input:\n%s", path, block)
		}
		out = append(out, c)
	}
	return out
}

func writePredicates(t *testing.T, path string, cs []predicateCase) {
	t.Helper()
	var b strings.Builder
	for i, c := range cs {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("input: " + c.input + "\nprinted: " + c.printed + "\nnormal: " + c.normal + "\n")
	}
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(b.String(), ": \n", ":\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizePredicate(t *testing.T) {
	path := filepath.Join("testdata", "predicates", "postgres.txt")
	cs := readPredicates(t, path)
	if *update {
		for i := range cs {
			cs[i].normal = migrate.NormalizePredicate(velox.Postgres, cs[i].input)
		}
		writePredicates(t, path, cs)
		return
	}
	for _, c := range cs {
		if got := migrate.NormalizePredicate(velox.Postgres, c.input); got != c.normal {
			t.Errorf("%s: normal %q, want %q", c.input, got, c.normal)
		}
		if c.printed != "" {
			if got := migrate.NormalizePredicate(velox.Postgres, c.printed); got != c.normal {
				t.Errorf("%s: printed %s normalises to %q, want %q", c.input, c.printed, got, c.normal)
			}
		}
		if got := migrate.NormalizePredicate(velox.Postgres, c.normal); got != c.normal {
			t.Errorf("%s: the normal form %q normalises to %q", c.input, c.normal, got)
		}
	}
}

// TestNormalizePredicatePrinted pins PostgreSQL's printed spellings of the
// common predicates, written by hand, apart from the goldens a real
// PostgreSQL fills.
func TestNormalizePredicatePrinted(t *testing.T) {
	for _, c := range []struct{ printed, written string }{
		{`(status = 'active'::text)`, `status = 'active'`},
		{`((code)::text = 'x'::text)`, `code = 'x'`},
		{`(qty >= '-1'::integer)`, `qty >= -1`},
		{`(qty >= 1.5)`, `qty >= 1.5`},
		{`(deleted_at IS NULL)`, `deleted_at IS NULL`},
		{`(NOT active)`, `NOT active`},
		{`(status = ANY (ARRAY['a'::text, 'b'::text]))`, `status IN ('a', 'b')`},
		{`((status)::text = ANY ((ARRAY['a'::character varying, 'b'::character varying])::text[]))`, `status IN ('a', 'b')`},
		{`(status <> ALL (ARRAY['a'::text, 'b'::text]))`, `status NOT IN ('a', 'b')`},
		{`(status = 'a'::text)`, `status IN ('a')`},
		{`((qty > 0) AND (status = 'a'::text))`, `qty > 0 AND status = 'a'`},
		{`(((qty > 0) OR (qty < '-5'::integer)) AND (deleted_at IS NULL))`, `(qty > 0 OR qty < -5) AND deleted_at IS NULL`},
		{`(created_on > '2020-01-01'::date)`, `created_on > '2020-01-01'::date`},
		{`(price < '-1.5'::numeric)`, `price < -1.5`},
		{`(price > (2)::numeric)`, `price > 2`},
		{`(qty > (1.9)::integer)`, `qty > 1.9::integer`},
		{`(lower((email)::text) = 'x@y'::text)`, `lower(email) = 'x@y'`},
		{`(COALESCE(qty, 0) > 1)`, `coalesce(qty, 0) > 1`},
		{`(name ~~ 'a%'::text)`, `name LIKE 'a%'`},
		{`(name !~~ 'a%'::text)`, `name NOT LIKE 'a%'`},
		{`(name ~~* 'a%'::text)`, `name ILIKE 'a%'`},
		{`(name !~~* 'a%'::text)`, `name NOT ILIKE 'a%'`},
		{`((qty >= 1) AND (qty <= 5))`, `qty BETWEEN 1 AND 5`},
		{`((qty < 1) OR (qty > 5))`, `qty NOT BETWEEN 1 AND 5`},
		{`((qty * 2) > (qty - 1))`, `qty * 2 > qty - 1`},
		{`("Status" = 'a'::text)`, `"Status" = 'a'`},
	} {
		got, want := migrate.NormalizePredicate(velox.Postgres, c.printed), migrate.NormalizePredicate(velox.Postgres, c.written)
		if got != want {
			t.Errorf("%s normalises to %q; %s to %q", c.printed, got, c.written, want)
		}
	}
	for _, c := range []struct {
		d       velox.Dialect
		printed string
		written string
	}{
		{velox.MySQL, "(`qty` > 0)", "qty > 0"},
		{velox.MySQL, "(`status` in (_utf8mb4'a',_utf8mb4'b'))", "`status` IN ('a', 'b')"},
		{velox.MySQL, `(name = 'it\'s')`, `name = 'it''s'`},
		{velox.SQLite, `[qty] == 0`, `"qty" = 0`},
	} {
		got, want := migrate.NormalizePredicate(c.d, c.printed), migrate.NormalizePredicate(c.d, c.written)
		if got != want {
			t.Errorf("%v: %s normalises to %q; %s to %q", c.d, c.printed, got, c.written, want)
		}
	}
}

func TestNormalizePredicateKeepsMeaning(t *testing.T) {
	norm := func(s string) string { return migrate.NormalizePredicate(velox.Postgres, s) }
	for _, c := range [][2]string{
		{`a AND (b OR c)`, `a AND b OR c`},
		{`(a OR b) AND c`, `a OR b AND c`},
		{`"Foo" = 1`, `foo = 1`},
		{`a - (b - c) > 0`, `a - b - c > 0`},
		{`a / (b * c) > 0`, `a / b * c > 0`},
		{`NOT (a AND b)`, `NOT a AND b`},
		{`"null" IS NULL`, `NULL IS NULL`},
		{`"and" = 1`, `x = 1`},
		{`qty IN (1, 2)`, `qty IN (2, 1)`},
		{`status = 'a'`, `status = 'A'`},
		{`qty = 5`, `qty = '5'`},
		{`(a = b) IS NULL`, `a = (b IS NULL)`},
		{`- (a + b) > 0`, `- a + b > 0`},
		{`qty > 0 OR status = 'a'`, `qty > 0 AND status = 'a'`},
	} {
		if norm(c[0]) == norm(c[1]) {
			t.Errorf("%s and %s both normalise to %q", c[0], c[1], norm(c[0]))
		}
	}
	for _, c := range [][2]string{
		{`(a)`, `a`},
		{`((a AND b))`, `a AND b`},
		{`a AND (b AND c)`, `a AND b AND c`},
		{`qty  >   0`, `qty > 0`},
		{`"qty" > 0`, `QTY > 0`},
		{`Lower(email) = 'x'`, `lower(email) = 'x'`},
		{`qty != 3`, `qty <> 3`},
		{`'5'::integer = qty`, `5 = qty`},
	} {
		if norm(c[0]) != norm(c[1]) {
			t.Errorf("%s normalises to %q and %s to %q", c[0], norm(c[0]), c[1], norm(c[1]))
		}
	}
	// Each pair differs in meaning on its dialect.
	for _, c := range []struct {
		d    velox.Dialect
		a, b string
	}{
		// A cast is part of the expression, but for the spellings of one
		// value the databases echo back.
		{velox.Postgres, `qty > 1.9::integer`, `qty > 1.9`},
		{velox.Postgres, `code = 'abc'::varchar(1)`, `code = 'abc'`},
		{velox.Postgres, `code = 'abc'::character varying(1)`, `code = 'abc'`},
		{velox.Postgres, `x::date = y`, `x::timestamp = y`},
		{velox.Postgres, `x::date = y`, `x = y`},
		{velox.Postgres, `x::timestamp = y`, `x = y`},
		{velox.Postgres, `created_at::date > '2020-01-01'`, `created_at > '2020-01-01'`},
		{velox.Postgres, `t > '2020-01-01 10:00'::date`, `t > '2020-01-01 10:00'::timestamp`},
		{velox.Postgres, `t > '2020-01-01 10:00'::date`, `t > '2020-01-01 10:00'`},
		{velox.Postgres, `qty::numeric(3, 1) > 1`, `qty > 1`},
		{velox.Postgres, `5::numeric(3, 1) > qty`, `5 > qty`},
		{velox.Postgres, `code::varchar(2) = 'ab'`, `code = 'ab'`},
		{velox.Postgres, `(a + b)::text = 'x'`, `a + b = 'x'`},
		{velox.Postgres, `'5'::bigint = qty`, `'5' = qty`},
		// MySQL's escapes: \% and \_ keep their backslash, \b and \Z are
		// characters of their own.
		{velox.MySQL, `code NOT LIKE '%\_%'`, `code NOT LIKE '%_%'`},
		{velox.MySQL, `code LIKE '\%'`, `code LIKE '%'`},
		{velox.MySQL, `code = '\b'`, `code = 'b'`},
		{velox.MySQL, `code = '\Z'`, `code = 'Z'`},
		// A chain of comparisons is read differently by each database, so
		// it is not read.
		{velox.SQLite, `a = b < c`, `(a = b) < c`},
		{velox.Postgres, `flag = b IN (true, false)`, `(flag = b) IN (true, false)`},
		{velox.Postgres, `name LIKE 'a' = flag`, `(name LIKE 'a') = flag`},
		{velox.Postgres, `a IS NULL IS NULL`, `(a IS NULL) IS NULL`},
		// A comment is not an operator.
		{velox.Postgres, `qty > 5 -- x`, `qty > 5 - -x`},
		{velox.Postgres, `qty > 5 /* x */`, `qty > 5 / * x * /`},
		// MySQL's other character sets change the comparison.
		{velox.MySQL, `name = _binary'abc'`, `name = 'abc'`},
		{velox.MySQL, `name = _latin1'abc'`, `name = 'abc'`},
		// The fallback keeps every literal's inside.
		{velox.Postgres, `E'a\'  b' = x`, `E'a\' b' = x`},
		{velox.Postgres, `$$a  b$$ = x`, `$$a b$$ = x`},
		{velox.Postgres, `$t$a  b$t$ = x`, `$t$a b$t$ = x`},
	} {
		if x, y := migrate.NormalizePredicate(c.d, c.a), migrate.NormalizePredicate(c.d, c.b); x == y {
			t.Errorf("%v: %s and %s both normalise to %q", c.d, c.a, c.b, x)
		}
	}
	// And each of these is one value spelled two ways.
	for _, c := range []struct {
		d    velox.Dialect
		a, b string
	}{
		{velox.Postgres, `status = 'a'::text`, `status = 'a'`},
		{velox.Postgres, `status = 'a'::varchar`, `status = 'a'`},
		{velox.Postgres, `status = 'a'::character varying`, `status = 'a'`},
		{velox.Postgres, `qty = 5::bigint`, `qty = 5`},
		{velox.Postgres, `qty >= '-1'::integer`, `qty >= -1`},
		{velox.Postgres, `price > 2::numeric`, `price > 2`},
		{velox.Postgres, `(code)::text = 'x'`, `code = 'x'`},
		{velox.Postgres, `code::character varying = 'x'`, `code = 'x'`},
		{velox.MySQL, `name = _utf8mb4'abc'`, `name = 'abc'`},
		{velox.MySQL, `name = _utf8mb3'abc'`, `name = 'abc'`},
		{velox.MySQL, `name = _UTF8'abc'`, `name = 'abc'`},
		{velox.MySQL, `code LIKE '\\_'`, `code LIKE '\_'`},
		{velox.MySQL, `code = 'it\'s'`, `code = 'it''s'`},
		{velox.MySQL, `code = '\q'`, `code = 'q'`},
	} {
		if x, y := migrate.NormalizePredicate(c.d, c.a), migrate.NormalizePredicate(c.d, c.b); x != y {
			t.Errorf("%v: %s normalises to %q and %s to %q", c.d, c.a, x, c.b, y)
		}
	}
	if got := norm(`name = 'A  B'`); got != `name = 'A  B'` {
		t.Errorf("a literal's case and spaces: got %q", got)
	}
	// Text it cannot parse comes back with its white space collapsed,
	// literals kept.
	for in, want := range map[string]string{
		"a ||  b = 'x  y'":          "a || b = 'x  y'",
		"  CASE WHEN a\tTHEN b END": "CASE WHEN a THEN b END",
		"a = ":                      "a =",
		"x::":                       "x::",
		"f(a,":                      "f(a,",
		"E'a\\'  b'   = x":          "E'a\\'  b' = x",
		"$t$a  b$t$   = x":          "$t$a  b$t$ = x",
	} {
		if got := norm(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}
