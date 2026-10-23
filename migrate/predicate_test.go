package migrate_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/migrate"
)

// A predicateCase is a block of testdata/predicates: a predicate as written,
// as PostgreSQL printed it ("" until inspected), its normal form, and a
// predicate of another meaning that the normaliser must keep apart from it.
type predicateCase struct{ input, printed, normal, differs string }

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
			case "differs":
				c.differs = val
			default:
				t.Fatalf("%s: unknown line %q", path, l)
			}
		}
		if c.input == "" || c.differs == "" {
			t.Fatalf("%s: a block without input or differs:\n%s", path, block)
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
		b.WriteString("input: " + c.input + "\nprinted: " + c.printed + "\nnormal: " + c.normal + "\ndiffers: " + c.differs + "\n")
	}
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(b.String(), ": \n", ":\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}

var predicatesPath = filepath.Join("testdata", "predicates", "postgres.txt")

// same reports whether the normaliser calls a and b one predicate.
func same(d velox.Dialect, a, b string) bool {
	s, _ := migrate.SamePredicate(d, a, b)
	return s
}

func TestNormalizePredicate(t *testing.T) {
	cs := readPredicates(t, predicatesPath)
	if *update {
		for i := range cs {
			cs[i].normal = migrate.NormalizePredicate(velox.Postgres, cs[i].input)
		}
		writePredicates(t, predicatesPath, cs)
		return
	}
	for _, c := range cs {
		if got := migrate.NormalizePredicate(velox.Postgres, c.input); got != c.normal {
			t.Errorf("%s: normal %q, want %q", c.input, got, c.normal)
		}
		if got := migrate.NormalizePredicate(velox.Postgres, c.normal); got != c.normal {
			t.Errorf("%s: the normal form %q normalises to %q", c.input, c.normal, got)
		}
		if s, ok := migrate.SamePredicate(velox.Postgres, c.input, c.input); !s || !ok {
			t.Errorf("%s: not itself, or not parsed", c.input)
		}
		if c.printed != "" && !same(velox.Postgres, c.printed, c.input) {
			t.Errorf("%s: printed %s is not the same predicate", c.input, c.printed)
		}
		for _, x := range []string{c.input, c.printed} {
			if x != "" && same(velox.Postgres, x, c.differs) {
				t.Errorf("%s and %s are called the same", x, c.differs)
			}
		}
	}
}

// TestPredicatesPostgres creates each golden predicate as a partial index
// and as a check on PostgreSQL, and reads them back: what PostgreSQL prints
// is the predicate written, and not its neighbour of another meaning. With
// -update it writes the printed lines.
func TestPredicatesPostgres(t *testing.T) {
	db, _ := freshDB(t, velox.Postgres)
	db.SetMaxOpenConns(1)
	pgExec(t, db, "SET TimeZone = 'UTC'")
	pgExec(t, db, `CREATE SEQUENCE g_seq`)
	pgExec(t, db, `CREATE TABLE g (id integer PRIMARY KEY DEFAULT nextval('g_seq'), status text, qty integer,
		price numeric, r real, deleted_at timestamptz, active boolean, created_on date, ts timestamp,
		tstz timestamptz, tm time, email text, name text, code varchar(10))`)
	read := func(q string, args ...any) string {
		t.Helper()
		var s string
		if err := db.QueryRow(q, args...).Scan(&s); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return s
	}
	cs := readPredicates(t, predicatesPath)
	for i, c := range cs {
		name := fmt.Sprintf("g_%02d", i)
		pgExec(t, db, "CREATE INDEX "+name+" ON g (id) WHERE "+c.input)
		pgExec(t, db, "ALTER TABLE g ADD CONSTRAINT "+name+"_check CHECK ("+c.input+")")
		printed := read(`SELECT pg_get_expr(indpred, indrelid) FROM pg_index WHERE indexrelid = $1::regclass`, name)
		def := read(`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = $1`, name+"_check")
		check, ok := strings.CutPrefix(def, "CHECK ")
		if !ok {
			t.Fatalf("%s: constraint %s", c.input, def)
		}
		for _, x := range []string{printed, check} {
			if !same(velox.Postgres, x, c.input) {
				t.Errorf("%s: PostgreSQL prints %s, not the same predicate", c.input, x)
			}
			if same(velox.Postgres, x, c.differs) {
				t.Errorf("%s: PostgreSQL's %s is called the same as %s", c.input, x, c.differs)
			}
		}
		if *update {
			cs[i].printed = printed
		} else if c.printed != printed {
			t.Errorf("%s: PostgreSQL prints %s, the golden %s (run with -update)", c.input, printed, c.printed)
		}
	}
	if *update {
		writePredicates(t, predicatesPath, cs)
	}
	// A serial default comes back with its regclass cast.
	def := read(`SELECT pg_get_expr(adbin, adrelid) FROM pg_attrdef WHERE adrelid = 'g'::regclass`)
	if !same(velox.Postgres, def, "nextval('g_seq')") || same(velox.Postgres, def, "nextval('g_other')") {
		t.Errorf("the default %s", def)
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
		{`(((qty * 2))::numeric > (price - (1)::numeric))`, `qty * 2 > price - 1`},
		{`(r < (1.5)::double precision)`, `r < 1.5`},
		{`(ts > '2020-01-01 00:00:00'::timestamp without time zone)`, `ts > '2020-01-01'`},
		{`(created_on > '2020-01-01'::date)`, `created_on > '2020-01-01'`},
		{`nextval('users_id_seq'::regclass)`, `nextval('users_id_seq')`},
		{`("Status" = 'a'::text)`, `"Status" = 'a'`},
	} {
		if !same(velox.Postgres, c.printed, c.written) {
			t.Errorf("%s is not %s", c.printed, c.written)
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
		if !same(c.d, c.printed, c.written) {
			t.Errorf("%v: %s is not %s", c.d, c.printed, c.written)
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
		if same(velox.Postgres, c[0], c[1]) {
			t.Errorf("%s and %s are called the same", c[0], c[1])
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
		if !same(velox.Postgres, c[0], c[1]) {
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
		// A cast the database adds itself matches no cast, but never
		// another cast, and a narrowing cast never matches none.
		{velox.Postgres, `x::numeric > 1`, `x::bigint > 1`},
		{velox.Postgres, `x::double precision > 1`, `x::real > 1`},
		{velox.Postgres, `qty::integer > 1`, `qty > 1`},
		{velox.Postgres, `qty::smallint > 1`, `qty > 1`},
		{velox.Postgres, `qty::numeric(3, 1) > 1`, `qty::numeric > 1`},
		{velox.Postgres, `qty::bigint[] = x`, `qty = x`},
		{velox.Postgres, `x::bigint > 1`, `x > 1`},
		{velox.Postgres, `x::int8 > 1`, `x > 1`},
		{velox.Postgres, `x::double precision > 1`, `x > 1`},
		{velox.Postgres, `x::real > 1`, `x > 1`},
		{velox.Postgres, `1.5::bigint > x`, `1.5 > x`},
		{velox.Postgres, `'1.5'::real > x`, `'1.5' > x`},
		// An elided cast keeps the value only where it sits: never under
		// division or in a function's arguments, and ::real never.
		{velox.Postgres, `qty::numeric / 3 > 1`, `qty / 3 > 1`},
		{velox.Postgres, `qty / 2::double precision > 1`, `qty / 2 > 1`},
		{velox.Postgres, `qty / 2::real > 1`, `qty / 2 > 1`},
		{velox.Postgres, `r > 1.1::real`, `r > 1.1`},
		{velox.Postgres, `qty / 2::numeric > 1`, `qty / 2 > 1`},
		{velox.Postgres, `qty / 2::bigint > 1`, `qty / 2 > 1`},
		{velox.Postgres, `(qty + 1::numeric) / 2 > 1`, `(qty + 1) / 2 > 1`},
		{velox.Postgres, `f(qty::numeric) > 1`, `f(qty) > 1`},
		{velox.Postgres, `round(2::numeric) > x`, `round(2) > x`},
		{velox.Postgres, `qty + 1.5::double precision > 1`, `qty + 1.5 > 1`},
		{velox.Postgres, `x::numeric`, `x`},
		// A zone is an offset or Z; an abbreviation keeps the cast.
		{velox.Postgres, `x = '2020-01-01 00:00 PST'::timestamptz`, `x = '2020-01-01 00:00 EST'`},
		{velox.Postgres, `x = '2020-01-01 00:00 PST'::timestamptz`, `x = '2020-01-01 00:00'`},
		{velox.Postgres, `x = '2020-01-01 00:00 PST'::timestamptz`, `x = '2020-01-01 00:00 EST'::timestamptz`},
		{velox.Postgres, `ts::date = '2020-01-01'`, `ts = '2020-01-01'`},
		{velox.Postgres, `x = '2020-01-01'::date`, `x = '2020-01-01'::timestamp`},
		{velox.Postgres, `x = '2020-01-01'::date`, `x = '2020-01-02'`},
		{velox.Postgres, `x = '2020-01-01 00:00:00'::timestamp`, `x = '2020-01-01 00:00:01'`},
		{velox.Postgres, `x = '2020-01-01 00:00:00+00'::timestamptz`, `x = '2020-01-01 00:00:00+01'`},
		{velox.Postgres, `x = '10:00:00'::time`, `x = '10:00:01'`},
		{velox.Postgres, `x = 'soon'::date`, `x = 'soon'`},
		{velox.Postgres, `x = '1 day'::interval`, `x = '1 day'`},
		{velox.Postgres, `x = '2020-01-01'::date`, `x = 2020`},
		{velox.Postgres, `nextval('s'::regclass)`, `nextval('t')`},
		{velox.Postgres, `nextval('s'::regclass)`, `nextval(s)`},
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
		if same(c.d, c.a, c.b) || same(c.d, c.b, c.a) {
			t.Errorf("%v: %s and %s are called the same", c.d, c.a, c.b)
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
		{velox.Postgres, `(qty * 2)::numeric > price - 1::numeric`, `qty * 2 > price - 1`},
		{velox.Postgres, `r < 1.5::double precision`, `r < 1.5`},
		{velox.Postgres, `r < 2::float8`, `r < 2`},
		{velox.Postgres, `price * 2::numeric > 1`, `price * 2 > 1`},
		{velox.Postgres, `price - qty::numeric > 1`, `price - qty > 1`},
		{velox.Postgres, `qty IN (1::numeric, 2)`, `qty IN (1, 2)`},
		{velox.Postgres, `qty + 1::bigint > 0`, `qty + 1 > 0`},
		{velox.Postgres, `price > '-1.5'::numeric`, `price > -1.5`},
		{velox.Postgres, `x = '2020-01-01 00:00Z'::timestamptz`, `x = '2020-01-01 00:00+00:00'`},
		{velox.Postgres, `f(x)::decimal > 1`, `f(x) > 1`},
		{velox.Postgres, `x = '2020-01-01 00:00:00'::timestamp without time zone`, `x = '2020-01-01'`},
		{velox.Postgres, `x = '2020-01-01'::date`, `x = '2020-01-01'`},
		{velox.Postgres, `x = '2020-01-01 10:30:00'::timestamp`, `x = '2020-01-01 10:30'`},
		{velox.Postgres, `x = '2020-01-01 00:00:00+00'::timestamp with time zone`, `x = '2020-01-01'`},
		{velox.Postgres, `x = '2020-01-01 01:00:00+01'::timestamptz`, `x = '2020-01-01 00:00:00+00'`},
		{velox.Postgres, `x = '10:00:00'::time without time zone`, `x = '10:00'`},
		{velox.Postgres, `x = '2020-01-01'::date`, `x = '2020-01-01'::date`},
		{velox.Postgres, `nextval('s'::regclass)`, `nextval('s')`},
		{velox.MySQL, `name = _utf8mb4'abc'`, `name = 'abc'`},
		{velox.MySQL, `name = _utf8mb3'abc'`, `name = 'abc'`},
		{velox.MySQL, `name = _UTF8'abc'`, `name = 'abc'`},
		{velox.MySQL, `code LIKE '\\_'`, `code LIKE '\_'`},
		{velox.MySQL, `code = 'it\'s'`, `code = 'it''s'`},
		{velox.MySQL, `code = '\q'`, `code = 'q'`},
	} {
		if !same(c.d, c.a, c.b) || !same(c.d, c.b, c.a) {
			t.Errorf("%v: %s and %s are not called the same", c.d, c.a, c.b)
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
		"-":                         "-",
		"- (":                       "- (",
		"E'a\\'  b'   = x":          "E'a\\'  b' = x",
		"$t$a  b$t$   = x":          "$t$a  b$t$ = x",
	} {
		if got := norm(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}
