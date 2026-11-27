package gqlgraph_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
)

// money stands in for a decimal library's type, as shopspring's
// decimal.Decimal is: a struct, written to the database by Value, read by
// Scan, and held in a cursor as its text.
type money struct{ r *big.Rat }

func (m money) rat() *big.Rat {
	if m.r == nil {
		return new(big.Rat)
	}
	return m.r
}

func (m money) Value() (driver.Value, error) { return m.rat().FloatString(4), nil }

func (m *money) Scan(src any) error {
	var s string
	switch v := src.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	case int64:
		s = strconv.FormatInt(v, 10)
	case float64:
		s = strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Errorf("money: cannot scan a %T", src)
	}
	return m.UnmarshalText([]byte(s))
}

func (m money) MarshalText() ([]byte, error) { return []byte(m.rat().FloatString(4)), nil }

func (m *money) UnmarshalText(b []byte) error {
	r, ok := new(big.Rat).SetString(string(b))
	if !ok {
		return fmt.Errorf("money: %q is not a number", b)
	}
	m.r = r
	return nil
}

// ledgerRow is a hand-written entity with a decimal column, as velox-gen
// writes one for an Other field of a decimal Go type.
type ledgerRow struct {
	ID     int64
	Amount money
}

var ledgerMeta velox.MetaOnce[ledgerRow]

func (*ledgerRow) VeloxMeta() *velox.Meta[ledgerRow] { return ledgerMeta.Load(ledgerDescriptor) }

func ledgerDescriptor() velox.Descriptor[ledgerRow] {
	return velox.Descriptor[ledgerRow]{
		Table:      "ledgers",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Amount", Name: "amount"}},
		PrimaryKey: []int{0},
		FieldPtr: func(x *ledgerRow, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.Amount
			}
			return nil
		},
	}
}

// TestPaginateDecimal: a connection ordered by a decimal of a struct type
// pages exactly, forward and backward, at every page size and from every
// cursor read back from the wire, in numeric order: 9 comes before 100, and
// -3 before both. The cursor holds the decimal as its text.
func TestPaginateDecimal(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec(`CREATE TABLE ledgers (id INTEGER PRIMARY KEY, amount NUMERIC NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for i, a := range []string{"9", "100", "25.5", "100", "-3", "0.5", "1000", "9.0001"} {
		if _, err := sqlDB.Exec(`INSERT INTO ledgers (id, amount) VALUES (?, ?)`, i+1, a); err != nil {
			t.Fatal(err)
		}
	}
	db := velox.Open(velox.SQLite, sqlDB)
	for _, c := range []struct {
		dir  veloxgql.OrderDirection
		want []int64
	}{
		{veloxgql.OrderDirectionAsc, []int64{5, 6, 1, 8, 3, 2, 4, 7}},
		{veloxgql.OrderDirectionDesc, []int64{7, 4, 2, 3, 8, 1, 6, 5}},
	} {
		t.Run(string(c.dir), func(t *testing.T) {
			page := func(args gqlgraph.Args) *gqlgraph.PageOf[ledgerRow] {
				t.Helper()
				terms := []gqlgraph.OrderTerm[ledgerRow]{gqlgraph.TermOf[ledgerRow]("AMOUNT", c.dir, 1)}
				key := []gqlgraph.OrderTerm[ledgerRow]{gqlgraph.TermOf[ledgerRow]("id", c.dir, 0)}
				conn, err := gqlgraph.NewConnectionOf(ctx, db.Query[ledgerRow](), "Ledger", args, terms, key)
				if err != nil {
					t.Fatalf("%+v: %v", args, err)
				}
				p, err := conn.Page(ctx)
				if err != nil {
					t.Fatalf("%+v: %v", args, err)
				}
				return p
			}
			ids := func(rows []*ledgerRow) []int64 {
				out := make([]int64, len(rows))
				for i, r := range rows {
					out[i] = r.ID
				}
				return out
			}
			all := page(gqlgraph.Args{First: new(len(c.want))})
			if got := ids(all.Rows); !slices.Equal(got, c.want) {
				t.Fatalf("all: %v, want %v", got, c.want)
			}
			for i := range c.want {
				cur := wire(t, all.Cursors[i])
				if _, ok := cur.Terms[0].Value.(string); !ok {
					t.Fatalf("cursor of %d holds %T, want the decimal's text", c.want[i], cur.Terms[0].Value)
				}
				for n := 1; n <= 4; n++ {
					after := page(gqlgraph.Args{First: new(n), After: cur})
					end := min(i+1+n, len(c.want))
					if got := ids(after.Rows); !slices.Equal(got, c.want[i+1:end]) || after.Info.HasNextPage != (end < len(c.want)) {
						t.Errorf("first %d after %d: %v next %v, want %v", n, c.want[i], got, after.Info.HasNextPage, c.want[i+1:end])
					}
					before := page(gqlgraph.Args{Last: new(n), Before: cur})
					start := max(i-n, 0)
					if got := ids(before.Rows); !slices.Equal(got, c.want[start:i]) || before.Info.HasPreviousPage != (start > 0) {
						t.Errorf("last %d before %d: %v prev %v, want %v", n, c.want[i], got, before.Info.HasPreviousPage, c.want[start:i])
					}
				}
			}
		})
	}
}

// opaque is a struct that has no text form and no Value: a cursor cannot
// hold it.
type opaque struct{ A, B int }

type opaqueRow struct {
	ID int64
	V  opaque
}

var opaqueMeta velox.MetaOnce[opaqueRow]

func (*opaqueRow) VeloxMeta() *velox.Meta[opaqueRow] { return opaqueMeta.Load(opaqueDescriptor) }

func opaqueDescriptor() velox.Descriptor[opaqueRow] {
	return velox.Descriptor[opaqueRow]{
		Table:      "opaques",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "V", Name: "v"}},
		PrimaryKey: []int{0},
		FieldPtr: func(x *opaqueRow, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.V
			}
			return nil
		},
	}
}

// TestPaginateOpaqueStruct: a struct with no text form and no Value is still
// refused, with the reason, when its connection is made.
func TestPaginateOpaqueStruct(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	db := velox.Open(velox.SQLite, sqlDB)
	terms := []gqlgraph.OrderTerm[opaqueRow]{gqlgraph.TermOf[opaqueRow]("V", veloxgql.OrderDirectionAsc, 1)}
	key := []gqlgraph.OrderTerm[opaqueRow]{gqlgraph.TermOf[opaqueRow]("id", veloxgql.OrderDirectionAsc, 0)}
	_, err = gqlgraph.NewConnectionOf(context.Background(), db.Query[opaqueRow](), "Opaque", gqlgraph.Args{First: new(1)}, terms, key)
	if err == nil || !strings.Contains(err.Error(), "keyset paging cannot continue after a gqlgraph_test.opaque") || !strings.Contains(err.Error(), "encoding.TextMarshaler") {
		t.Fatalf("got %v, want a refusal that names the missing text form", err)
	}
}

// feeRow is a hand-written entity with a nullable decimal, as velox-gen
// writes a Nillable Other field.
type feeRow struct {
	ID  int64
	Fee *money
}

var feeMeta velox.MetaOnce[feeRow]

func (*feeRow) VeloxMeta() *velox.Meta[feeRow] { return feeMeta.Load(feeDescriptor) }

func feeDescriptor() velox.Descriptor[feeRow] {
	return velox.Descriptor[feeRow]{
		Table:      "fees",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Fee", Name: "fee"}},
		PrimaryKey: []int{0},
		FieldPtr: func(x *feeRow, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.Fee
			}
			return nil
		},
	}
}

// TestPaginateNullableDecimal: a connection ordered by a nullable decimal
// pages exactly, forward and backward, with the NULLs after every value
// ascending and before them descending; a cursor of a NULL row holds null.
func TestPaginateNullableDecimal(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec(`CREATE TABLE fees (id INTEGER PRIMARY KEY, fee NUMERIC)`); err != nil {
		t.Fatal(err)
	}
	fees := []any{nil, "100", "9", nil, "100", "-0.5", nil, "9.25"}
	for i, f := range fees {
		if _, err := sqlDB.Exec(`INSERT INTO fees (id, fee) VALUES (?, ?)`, i+1, f); err != nil {
			t.Fatal(err)
		}
	}
	db := velox.Open(velox.SQLite, sqlDB)
	for _, c := range []struct {
		dir  veloxgql.OrderDirection
		want []int64
	}{
		{veloxgql.OrderDirectionAsc, []int64{6, 3, 8, 2, 5, 1, 4, 7}},
		{veloxgql.OrderDirectionDesc, []int64{7, 4, 1, 5, 2, 8, 3, 6}},
	} {
		t.Run(string(c.dir), func(t *testing.T) {
			page := func(args gqlgraph.Args) *gqlgraph.PageOf[feeRow] {
				t.Helper()
				terms := []gqlgraph.OrderTerm[feeRow]{gqlgraph.TermOf[feeRow]("FEE", c.dir, 1)}
				key := []gqlgraph.OrderTerm[feeRow]{gqlgraph.TermOf[feeRow]("id", c.dir, 0)}
				conn, err := gqlgraph.NewConnectionOf(ctx, db.Query[feeRow](), "Fee", args, terms, key)
				if err != nil {
					t.Fatalf("%+v: %v", args, err)
				}
				p, err := conn.Page(ctx)
				if err != nil {
					t.Fatalf("%+v: %v", args, err)
				}
				return p
			}
			ids := func(rows []*feeRow) []int64 {
				out := make([]int64, len(rows))
				for i, r := range rows {
					out[i] = r.ID
				}
				return out
			}
			all := page(gqlgraph.Args{First: new(len(c.want))})
			if got := ids(all.Rows); !slices.Equal(got, c.want) {
				t.Fatalf("all: %v, want %v", got, c.want)
			}
			for i := range c.want {
				cur := wire(t, all.Cursors[i])
				if null := fees[c.want[i]-1] == nil; null != (cur.Terms[0].Value == nil) {
					t.Fatalf("cursor of %d: %+v", c.want[i], cur)
				}
				for n := 1; n <= 4; n++ {
					after := page(gqlgraph.Args{First: new(n), After: cur})
					end := min(i+1+n, len(c.want))
					if got := ids(after.Rows); !slices.Equal(got, c.want[i+1:end]) {
						t.Errorf("first %d after %d: %v, want %v", n, c.want[i], got, c.want[i+1:end])
					}
					before := page(gqlgraph.Args{Last: new(n), Before: cur})
					start := max(i-n, 0)
					if got := ids(before.Rows); !slices.Equal(got, c.want[start:i]) {
						t.Errorf("last %d before %d: %v, want %v", n, c.want[i], got, c.want[start:i])
					}
				}
			}
		})
	}
}
