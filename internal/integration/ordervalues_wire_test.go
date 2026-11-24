package integration

import (
	"bytes"
	"cmp"
	"database/sql"
	"encoding/json"
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
			{"time", velox.Calc[testmodel.Invoice]("CASE WHEN ? > ? THEN ? END", I.ID, int64(0), I.PaidAt),
				func(a, b testmodel.Invoice) ([2]bool, int) {
					if a.PaidAt == nil || b.PaidAt == nil {
						return [2]bool{a.PaidAt == nil, b.PaidAt == nil}, 0
					}
					return [2]bool{}, a.PaidAt.Compare(*b.PaidAt)
				}},
		}
		if tg.d == velox.MySQL {
			// An unsigned value past int64.
			exprs = append(exprs, wireExpr{"unsigned", velox.Calc[testmodel.Invoice]("CAST(? % ? AS UNSIGNED) + CAST(? AS UNSIGNED)", I.ID, int64(3), uint64(18446744073709551000)),
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
	})
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
