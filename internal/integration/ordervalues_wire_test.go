package integration

import (
	"bytes"
	"cmp"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/untyped"
)

// wireValues is vals as a cursor carries them: EncodeOrderValue, JSON out
// and back with numbers as json.Number, DecodeOrderValue.
func wireValues(t *testing.T, vals []any) []any {
	t.Helper()
	enc := make([]any, len(vals))
	for i, v := range vals {
		var err error
		if enc[i], err = untyped.EncodeOrderValue(v); err != nil {
			t.Fatal(err)
		}
	}
	b, err := json.Marshal(enc)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var back []any
	if err := dec.Decode(&back); err != nil {
		t.Fatal(err)
	}
	for i, v := range back {
		if back[i], err = untyped.DecodeOrderValue(v); err != nil {
			t.Fatalf("%#v from %#v: %v", v, vals[i], err)
		}
	}
	return back
}

// wireExpr is an expression ordering of the wire test: its term, and how two
// rows compare by it, each value NULL or not.
type wireExpr struct {
	name string
	term velox.Term[testmodel.Invoice]
	cmp  func(a, b testmodel.Invoice) (null [2]bool, c int)
}

// TestOrderValuesWire: rows ordered by an integer, a fraction, a decimal
// finer than a double, a text and a time expression, each with ties and
// NULLs, page forward and backward with every value carried through the
// cursor's JSON, with no row repeated or skipped and in the exact order the
// database sorts them. On PostgreSQL a numeric also holds NaN, which sorts
// after every number, and -Infinity; on MySQL a time holds the zero date,
// which sorts before every other, under the default sql_mode with
// NO_ZERO_DATE. The decimal's rows differ past a double's digits and
// are tie-broken by the key against the expression's direction, so a value
// compared as a double (MySQL's text against a decimal) repeats or skips
// rows; on SQLite, which keeps a double, they tie. On MySQL a time is also
// paged by a connection without parseTime, which reads it as text.
func TestOrderValuesWire(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		letters := "ABCABCCAABCBA"
		t1 := t0.Add(time.Hour + 123456*time.Microsecond)
		t2 := t0.Add(2*time.Hour + 654321*time.Microsecond)
		at := []*time.Time{nil, &t1, &t2, &t1}
		rows := make([]testmodel.Invoice, len(letters))
		for i := range rows {
			rows[i] = newInvoice(7, fmt.Sprintf("%c%02d", letters[i], i))
			rows[i].ID, rows[i].Total, rows[i].PaidAt = int64(700+i), float64(i%5)*0.375, at[i%4]
		}
		if err := tg.db.InsertAll(rows).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		exact := tg.d != velox.SQLite
		exprs := []wireExpr{
			{"integer", velox.Calc[testmodel.Invoice]("NULLIF(? % ?, ?)", I.ID, int64(3), int64(0)),
				func(a, b testmodel.Invoice) ([2]bool, int) {
					return [2]bool{a.ID%3 == 0, b.ID%3 == 0}, cmp.Compare(a.ID%3, b.ID%3)
				}},
			{"fraction", velox.Calc[testmodel.Invoice]("NULLIF(? * ?, ?)", I.Total, 0.5, 0.0),
				func(a, b testmodel.Invoice) ([2]bool, int) {
					return [2]bool{a.Total == 0, b.Total == 0}, cmp.Compare(a.Total, b.Total)
				}},
			{"decimal", velox.Calc[testmodel.Invoice]("CASE WHEN ? % ? = ? THEN NULL ELSE CAST(? AS DECIMAL(30,20)) + ? * CAST(? AS DECIMAL(30,20)) END",
				I.ID, int64(4), int64(0), I.Total, I.ID, "0.00000000000000000001"),
				func(a, b testmodel.Invoice) ([2]bool, int) {
					c := cmp.Compare(a.Total, b.Total)
					switch {
					case c != 0:
					case exact:
						c = cmp.Compare(a.ID, b.ID)
					default: // SQLite's REAL arithmetic
						c = cmp.Compare(a.Total+float64(a.ID)*1e-20, b.Total+float64(b.ID)*1e-20)
					}
					return [2]bool{a.ID%4 == 0, b.ID%4 == 0}, c
				}},
			{"text", velox.Calc[testmodel.Invoice]("NULLIF(SUBSTR(?, ?, ?), ?)", I.Number, int64(1), int64(1), "C"),
				func(a, b testmodel.Invoice) ([2]bool, int) {
					return [2]bool{a.Number[0] == 'C', b.Number[0] == 'C'}, cmp.Compare(a.Number[0], b.Number[0])
				}},
			// Text marks the Calc, so a cursor's value of it is checked
			// as text's (decision 0166's 2026-11-24 amendment).
			{"marked text", velox.Text(velox.Calc[testmodel.Invoice]("NULLIF(SUBSTR(?, ?, ?), ?)", I.Number, int64(1), int64(1), "C")),
				func(a, b testmodel.Invoice) ([2]bool, int) {
					return [2]bool{a.Number[0] == 'C', b.Number[0] == 'C'}, cmp.Compare(a.Number[0], b.Number[0])
				}},
			{"time", velox.Calc[testmodel.Invoice]("CASE WHEN ? > ? THEN ? END", I.ID, int64(0), I.PaidAt),
				func(a, b testmodel.Invoice) ([2]bool, int) {
					if a.PaidAt == nil || b.PaidAt == nil {
						return [2]bool{a.PaidAt == nil, b.PaidAt == nil}, 0
					}
					return [2]bool{}, a.PaidAt.Compare(*b.PaidAt)
				}},
		}
		// An unsigned value past int64: MySQL's BIGINT UNSIGNED, and
		// PostgreSQL's numeric(20,0), velox's uint64 column there.
		switch tg.d {
		case velox.MySQL:
			exprs = append(exprs, wireExpr{"unsigned", velox.Calc[testmodel.Invoice]("CAST(? % ? AS UNSIGNED) + CAST(? AS UNSIGNED)", I.ID, int64(3), uint64(18446744073709551000)),
				func(a, b testmodel.Invoice) ([2]bool, int) { return [2]bool{}, cmp.Compare(a.ID%3, b.ID%3) }})
		case velox.Postgres:
			exprs = append(exprs, wireExpr{"unsigned", velox.Calc[testmodel.Invoice]("CAST(? % ? AS NUMERIC(20,0)) + ?", I.ID, int64(3), uint64(18446744073709551000)),
				func(a, b testmodel.Invoice) ([2]bool, int) { return [2]bool{}, cmp.Compare(a.ID%3, b.ID%3) }})
		}
		if tg.d == velox.Postgres {
			// NaN where the key is a multiple of 5, else NULL where it is
			// 1 past a multiple of 4, else -Infinity where it is 2 past a
			// multiple of 6, else the total.
			rank := func(a testmodel.Invoice) (bool, float64) {
				switch {
				case a.ID%5 == 0:
					return false, 1e9
				case a.ID%4 == 1:
					return true, 0
				case a.ID%6 == 2:
					return false, -1e9
				}
				return false, a.Total
			}
			exprs = append(exprs, wireExpr{"nan", velox.Calc[testmodel.Invoice](
				"CASE WHEN ? % ? = ? THEN CAST(? AS NUMERIC) WHEN ? % ? = ? THEN NULL WHEN ? % ? = ? THEN CAST(? AS NUMERIC) ELSE CAST(? AS NUMERIC) END",
				I.ID, int64(5), int64(0), "NaN", I.ID, int64(4), int64(1), I.ID, int64(6), int64(2), "-Infinity", I.Total),
				func(a, b testmodel.Invoice) ([2]bool, int) {
					na, ra := rank(a)
					nb, rb := rank(b)
					return [2]bool{na, nb}, cmp.Compare(ra, rb)
				}})
		}
		if tg.d == velox.MySQL {
			var mode string
			var zero bool
			if err := tg.direct.QueryRowContext(ctx, "SELECT @@SESSION.sql_mode, YEAR(FROM_DAYS(0)) = 0").Scan(&mode, &zero); err != nil {
				t.Fatal(err)
			}
			if !zero || !strings.Contains(mode, "NO_ZERO_DATE") {
				t.Fatalf("sql_mode %q, FROM_DAYS(0) the zero date: %v", mode, zero)
			}
			// The zero date where the key is a multiple of 5, else the
			// time paid.
			exprs = append(exprs, wireExpr{"zero date", velox.Calc[testmodel.Invoice]("CASE WHEN ? % ? = ? THEN FROM_DAYS(?) ELSE ? END",
				I.ID, int64(5), int64(0), int64(0), I.PaidAt),
				func(a, b testmodel.Invoice) ([2]bool, int) {
					at := func(a testmodel.Invoice) *time.Time {
						if a.ID%5 == 0 {
							return &time.Time{}
						}
						return a.PaidAt
					}
					x, y := at(a), at(b)
					if x == nil || y == nil {
						return [2]bool{x == nil, y == nil}, 0
					}
					return [2]bool{}, x.Compare(*y)
				}})
		}
		dbs := []*velox.DB{tg.db}
		if tg.d == velox.MySQL {
			dbs = append(dbs, mysqlNoParseTime(t))
		}
		for k, db := range dbs {
			for _, e := range exprs {
				if k > 0 && e.name != "time" && e.name != "decimal" && e.name != "zero date" {
					continue
				}
				for _, o := range []struct {
					desc  bool
					nulls int // 0 the default, 1 first, 2 last
				}{{false, 0}, {true, 0}, {false, 1}, {true, 2}} {
					name := fmt.Sprintf("db %d %s desc %v nulls %d", k, e.name, o.desc, o.nulls)
					// The key against the expression's direction.
					want := wireOrder(rows, e, o.desc, o.nulls, !o.desc)
					got := pageWire(t, db, k > 0, e.term, o.desc, o.nulls, !o.desc, len(rows))
					if !slices.Equal(got, ids(want)) {
						t.Errorf("%s, forward:\n got %v\nwant %v", name, got, ids(want))
					}
					// Backward: every direction and NULL placement turned,
					// the rows read back reversed.
					back := pageWire(t, db, k > 0, e.term, !o.desc, [3]int{0, 2, 1}[o.nulls], o.desc, len(rows))
					slices.Reverse(back)
					if !slices.Equal(back, ids(want)) {
						t.Errorf("%s, backward:\n got %v\nwant %v", name, back, ids(want))
					}
				}
			}
		}
		t.Run("typed terms", func(t *testing.T) { wireTypedTerms(t, tg) })
		t.Run("mixed", func(t *testing.T) { wireMixed(t, tg, rows) })
	})
}

// wireTypedTerms: an ordering by a term whose kind velox knows, a JSON
// path's Number, Time or Text, pages through the cursor's JSON both ways
// as one read orders the rows; and a cursor's value of another kind (text
// for Number, text for Time off SQLite, a number for Text) or text holding
// a NUL byte on PostgreSQL fails with ErrOrderValue before the database
// sees it, never with a database error or a page.
func wireTypedTerms(t *testing.T, tg target) {
	jsonParsedSetup(t, tg)
	num, when, text := JR.Attrs.Text("n").Number(), JR.Attrs.Text("t").Time(), JR.Attrs.Text("n")
	for _, c := range []struct {
		name     string
		asc, dsc velox.Order[jrec]
	}{
		{"number", num.Asc(), num.Desc()},
		{"time", when.Asc(), when.Desc()},
		{"text", text.Asc(), text.Desc()},
	} {
		for _, by := range []velox.Order[jrec]{c.asc, c.dsc} {
			q := untyped.FromQuery(tg.db.Query[jrec]().OrderBy(by, JR.ID.Asc())).OrderValues()
			all, err := q.Limit(100).All(ctx)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			want := jrecIDs(untyped.RowsOf[jrec](all))
			for n := 1; n <= 4; n++ {
				var got []int64
				page, err := q.Limit(n).All(ctx)
				for err == nil && page.Len() > 0 && len(got) <= len(want) {
					got = append(got, jrecIDs(untyped.RowsOf[jrec](page))...)
					last := page.Len() - 1
					page, err = q.AfterValues(page.At(last), wireValues(t, page.OrderValues(last))).Limit(n).All(ctx)
				}
				if err != nil {
					t.Fatalf("%s by %d: %v", c.name, n, err)
				}
				if !slices.Equal(got, want) {
					t.Errorf("%s by %d:\n got %v\nwant %v", c.name, n, got, want)
				}
			}
		}
	}
	row, err := tg.db.Query[jrec]().Where(JR.ID.EQ(1)).One(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		by   velox.Order[jrec]
		v    any // a decoded cursor's value
		ok   func(velox.Dialect) bool
	}{
		{"text for Number", num.Asc(), "s:abc", func(velox.Dialect) bool { return false }},
		{"numeric text for Number", num.Asc(), "s:10", func(velox.Dialect) bool { return false }},
		{"a number for Number", num.Asc(), json.Number("2.5"), func(velox.Dialect) bool { return true }},
		{"text for Time", when.Asc(), "s:2026-03-01 02:00:00", func(d velox.Dialect) bool { return d == velox.SQLite }},
		{"a number for Text", text.Asc(), json.Number("7"), func(velox.Dialect) bool { return false }},
		{"NUL in Text", text.Asc(), "s:a\x00b", func(d velox.Dialect) bool { return d != velox.Postgres }},
		{"text for Text", text.Asc(), "s:abc", func(velox.Dialect) bool { return true }},
	} {
		v, err := untyped.DecodeOrderValue(c.v)
		if err != nil {
			t.Fatal(err)
		}
		q := untyped.FromQuery(tg.db.Query[jrec]().OrderBy(c.by, JR.ID.Asc()).Limit(5)).OrderValues()
		_, err = q.AfterValues(row, []any{v}).All(ctx)
		if c.ok(tg.d) {
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
			continue
		}
		if !errors.Is(err, untyped.ErrOrderValue) {
			t.Errorf("%s: %v, want ErrOrderValue", c.name, err)
		}
	}
}

// wireMixed: an ordering that mixes expressions and columns, an Integer
// Calc, then the nullable time column paid_at descending, then the string
// column number, each NullsLast, then the key, pages forward and backward
// through the cursor's JSON as a reference sort orders the rows, NULLs and
// ties included. A forged value for the Integer Calc and for a Text Calc
// fails with ErrOrderValue.
func wireMixed(t *testing.T, tg target, rows []testmodel.Invoice) {
	num := velox.Integer(velox.Calc[testmodel.Invoice]("NULLIF(? % ?, ?)", I.ID, int64(3), int64(0)))
	orders := func(back bool) []velox.Order[testmodel.Invoice] {
		if back {
			return []velox.Order[testmodel.Invoice]{num.Desc().NullsFirst(), I.PaidAt.Asc().NullsFirst(), I.Number.Desc().NullsFirst(), I.ID.Desc()}
		}
		return []velox.Order[testmodel.Invoice]{num.Asc().NullsLast(), I.PaidAt.Desc().NullsLast(), I.Number.Asc().NullsLast(), I.ID.Asc()}
	}
	want := slices.Clone(rows)
	slices.SortFunc(want, func(a, b testmodel.Invoice) int {
		if c := placed(a.ID%3 == 0, b.ID%3 == 0, false, func() int { return cmp.Compare(a.ID%3, b.ID%3) }, false); c != 0 {
			return c
		}
		if c := placed(a.PaidAt == nil, b.PaidAt == nil, false, func() int { return a.PaidAt.Compare(*b.PaidAt) }, true); c != 0 {
			return c
		}
		return cmp.Or(cmp.Compare(a.Number, b.Number), cmp.Compare(a.ID, b.ID))
	})
	for _, back := range []bool{false, true} {
		q := untyped.FromQuery(tg.db.Query[testmodel.Invoice]().Where(I.ID.GTE(700)).OrderBy(orders(back)...)).OrderValues()
		for n := 1; n <= 4; n++ {
			var got []int64
			page, err := q.Limit(n).All(ctx)
			for err == nil && page.Len() > 0 && len(got) <= len(rows) {
				got = append(got, ids(untyped.RowsOf[testmodel.Invoice](page))...)
				last := page.Len() - 1
				page, err = q.AfterValues(page.At(last), wireValues(t, page.OrderValues(last))).Limit(n).All(ctx)
			}
			if err != nil {
				t.Fatalf("back %v, %d a page: %v", back, n, err)
			}
			if back {
				slices.Reverse(got)
			}
			if !slices.Equal(got, ids(want)) {
				t.Errorf("back %v, %d a page:\n got %v\nwant %v", back, n, got, ids(want))
			}
		}
	}
	text := velox.Text(velox.Calc[testmodel.Invoice]("LOWER(?)", I.Number))
	for _, c := range []struct {
		name string
		by   velox.Order[testmodel.Invoice]
		v    any
	}{
		{"text for Integer", num.Asc(), "abc"},
		{"a float for Integer", num.Asc(), 2.5},
		{"a number for Text", text.Asc(), int64(7)},
	} {
		q := untyped.FromQuery(tg.db.Query[testmodel.Invoice]().OrderBy(c.by, I.ID.Asc()).Limit(5)).OrderValues()
		if _, err := q.AfterValues(&rows[0], []any{c.v}).All(ctx); !errors.Is(err, untyped.ErrOrderValue) {
			t.Errorf("%s: %v, want ErrOrderValue", c.name, err)
		}
	}
}

// jrecIDs is the keys of rs, in order.
func jrecIDs(rs []jrec) []int64 {
	out := make([]int64, len(rs))
	for i := range rs {
		out[i] = rs[i].ID
	}
	return out
}

// wireOrder is rows sorted by e, descending when desc, NULL placed by nulls
// (0 the default: last ascending, first descending), ties by the key,
// descending when keyDesc.
func wireOrder(rows []testmodel.Invoice, e wireExpr, desc bool, nulls int, keyDesc bool) []testmodel.Invoice {
	first := [3]bool{desc, true, false}[nulls]
	want := slices.Clone(rows)
	slices.SortFunc(want, func(a, b testmodel.Invoice) int {
		null, c := e.cmp(a, b)
		c = placed(null[0], null[1], first, func() int { return c }, desc)
		if c == 0 {
			c = cmp.Compare(a.ID, b.ID)
			if keyDesc {
				c = -c
			}
		}
		return c
	})
	return want
}

// pageWire pages db's invoices from 700 by term and then the key, by each
// page size from 1 to 4, through the cursor's JSON, and returns the keys in
// the order read; every page size must read the same. idOnly reads the key
// column alone.
func pageWire(t *testing.T, db *velox.DB, idOnly bool, term velox.Term[testmodel.Invoice], desc bool, nulls int, keyDesc bool, most int) []int64 {
	t.Helper()
	by, key := term.Asc(), I.ID.Asc()
	if desc {
		by = term.Desc()
	}
	switch nulls {
	case 1:
		by = by.NullsFirst()
	case 2:
		by = by.NullsLast()
	}
	if keyDesc {
		key = I.ID.Desc()
	}
	q := untyped.FromQuery(db.Query[testmodel.Invoice]().Where(I.ID.GTE(700)).OrderBy(by, key)).OrderValues()
	if idOnly {
		q = q.Columns(int(I.ID.I))
	}
	var first []int64
	for n := 1; n <= 4; n++ {
		var got []int64
		page, err := q.Limit(n).All(ctx)
		for err == nil && page.Len() > 0 && len(got) <= most {
			got = append(got, ids(untyped.RowsOf[testmodel.Invoice](page))...)
			last := page.Len() - 1
			page, err = q.AfterValues(page.At(last), wireValues(t, page.OrderValues(last))).Limit(n).All(ctx)
		}
		if err != nil {
			t.Fatalf("%d a page: %v", n, err)
		}
		if first != nil && !slices.Equal(got, first) {
			t.Errorf("%d a page: %v, but 1 a page: %v", n, got, first)
		}
		if first == nil {
			first = got
		}
	}
	return first
}

// mysqlNoParseTime is the MySQL test database opened without parseTime, so
// that the driver reads a DATETIME as text.
func mysqlNoParseTime(t *testing.T) *velox.DB {
	t.Helper()
	cfg, err := mysql.ParseDSN(os.Getenv("VELOX_TEST_MYSQL"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ParseTime = false
	conn, err := mysql.NewConnector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(conn)
	t.Cleanup(func() { _ = db.Close() })
	return velox.Open(velox.MySQL, db).WithoutPolicy("velox tests")
}

// TestOrderValuesForged: a cursor's value that no read on the dialect gives
// fails with ErrOrderValue before the database sees it: a NaN or infinite
// decimal off PostgreSQL (MySQL would cast it to 0), the zero date off
// MySQL (PostgreSQL would fail to read it as a time), an unsigned integer
// past int64 on SQLite, whose integers are signed.
func TestOrderValuesForged(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		row := newInvoice(7, "F01")
		row.ID = 790
		if err := tg.db.Insert(&row).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		only := func(d velox.Dialect) func(velox.Dialect) bool {
			return func(e velox.Dialect) bool { return e == d }
		}
		for _, c := range []struct {
			v    string
			term velox.Term[testmodel.Invoice]
			on   func(velox.Dialect) bool // the dialects a read gives it on
		}{
			{"d:NaN", velox.Calc[testmodel.Invoice]("? * ?", I.Total, int64(2)), only(velox.Postgres)},
			{"d:-Infinity", velox.Calc[testmodel.Invoice]("? * ?", I.Total, int64(2)), only(velox.Postgres)},
			{"dt:0000-00-00 00:00:00", velox.Calc[testmodel.Invoice]("CASE WHEN ? > ? THEN ? END", I.ID, int64(0), I.PaidAt), only(velox.MySQL)},
			{"u:18446744073709551001", velox.Calc[testmodel.Invoice]("CAST(? AS DECIMAL(20,0))", I.ID), func(d velox.Dialect) bool { return d != velox.SQLite }},
		} {
			v, err := untyped.DecodeOrderValue(c.v)
			if err != nil {
				t.Fatal(err)
			}
			q := untyped.FromQuery(tg.db.Query[testmodel.Invoice]().Where(I.ID.GTE(790)).OrderBy(c.term.Asc(), I.ID.Asc()).Limit(5)).OrderValues()
			_, err = q.AfterValues(&row, []any{v}).All(ctx)
			if c.on(tg.d) {
				if err != nil {
					t.Errorf("%s: %v", c.v, err)
				}
				continue
			}
			if !errors.Is(err, untyped.ErrOrderValue) {
				t.Errorf("%s: %v, want ErrOrderValue", c.v, err)
			}
		}
	})
}
