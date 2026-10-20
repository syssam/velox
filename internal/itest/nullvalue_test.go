package itest

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox2"
)

// The model of an Optional field that is not Nillable, written by hand as
// velox-gen writes its Desc: a NULL-able column whose Go field is a value
// (ColumnDesc.Nullable), which reads NULL as the zero value.

type nvCode string

type nvrec struct {
	ID   int64
	S    string
	N    int64
	F    float64
	B    bool
	T    time.Time
	Code nvCode
}

var NV = struct {
	ID velox.NumberCol[nvrec, int64]
	S  velox.StringCol[nvrec, string]
}{velox.NumberCol[nvrec, int64]{I: 0}, velox.StringCol[nvrec, string]{I: 1}}

var nvrecMeta velox.MetaOnce[nvrec]

func (*nvrec) VeloxMeta() *velox.Meta[nvrec] { return nvrecMeta.Load(nvrecDesc) }

func nvrecDesc() velox.Desc[nvrec] {
	return velox.Desc[nvrec]{
		Table: "null_values",
		Columns: []velox.ColumnDesc{
			{Field: "ID", Name: "id"},
			{Field: "S", Name: "s", Nullable: true},
			{Field: "N", Name: "n", Nullable: true},
			{Field: "F", Name: "f", Nullable: true},
			{Field: "B", Name: "b", Nullable: true},
			{Field: "T", Name: "t", Nullable: true},
			{Field: "Code", Name: "code", Nullable: true},
		},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Addr: func(x *nvrec, i int) any {
			return [...]any{&x.ID, &x.S, &x.N, &x.F, &x.B, &x.T, &x.Code}[i]
		},
	}
}

func nullValuesDDL(d velox.Dialect) string {
	switch d {
	case velox.Postgres:
		return "CREATE TABLE null_values (id bigint PRIMARY KEY, s text, n bigint, f double precision, b boolean, t timestamptz, code text)"
	case velox.MySQL:
		return "CREATE TABLE null_values (id BIGINT PRIMARY KEY, s TEXT NULL, n BIGINT NULL, f DOUBLE NULL, b BOOLEAN NULL, t DATETIME(6) NULL, code TEXT NULL)"
	}
	return "CREATE TABLE null_values (id INTEGER PRIMARY KEY, s TEXT, n INTEGER, f REAL, b BOOLEAN, t DATETIME, code TEXT)"
}

// An Optional value field reads NULL as its zero value, and a value as
// itself, through every query path that scans rows: All, One, Iter, Columns
// and Raw.
func TestNullIntoValue(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		tg.exec(t, "DROP TABLE IF EXISTS null_values")
		tg.exec(t, nullValuesDDL(tg.d))
		t.Cleanup(func() { tg.exec(t, "DROP TABLE IF EXISTS null_values") })
		tg.exec(t, "INSERT INTO null_values (id) VALUES (1)")
		full := nvrec{ID: 2, S: "s", N: 7, F: 1.5, B: true, T: t0, Code: "c"}
		if err := tg.db.Insert(&full).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		check := func(what string, got nvrec, want nvrec) {
			t.Helper()
			got.T, want.T = got.T.UTC(), want.T.UTC()
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("%s: got %+v, want %+v", what, got, want)
			}
		}
		xs, err := tg.db.Query[nvrec]().OrderBy(NV.ID.Asc()).Limit(2).All(ctx)
		if err != nil || len(xs) != 2 {
			t.Fatalf("All: %v, %v", xs, err)
		}
		check("All NULL", xs[0], nvrec{ID: 1})
		check("All values", xs[1], full)
		one, err := tg.db.Query[nvrec]().Where(NV.ID.EQ(1)).One(ctx)
		if err != nil {
			t.Fatal(err)
		}
		check("One", *one, nvrec{ID: 1})
		for x, err := range tg.db.Query[nvrec]().Where(NV.ID.EQ(1)).Limit(1).Iter(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			check("Iter", x, nvrec{ID: 1})
		}
		cols, err := tg.db.Query[nvrec]().Columns(NV.S, NV.ID).Where(NV.ID.EQ(1)).Limit(1).All(ctx)
		if err != nil || len(cols) != 1 {
			t.Fatalf("Columns: %v, %v", cols, err)
		}
		check("Columns", cols[0], nvrec{ID: 1})
		raw, err := tg.db.Raw[nvrec]("itest: NULL into values", "SELECT id, s, n, f, b, t, code FROM null_values WHERE id = 1").All(ctx)
		if err != nil || len(raw) != 1 {
			t.Fatalf("Raw: %v, %v", raw, err)
		}
		check("Raw", raw[0], nvrec{ID: 1})
		// The column is NULL-able, so keyset paging over it is refused, as
		// over a pointer field's.
		if _, err := tg.db.Query[nvrec]().OrderBy(NV.S.Asc(), NV.ID.Asc()).After(&full).Limit(1).All(ctx); err == nil || !strings.Contains(err.Error(), "nullable column s") {
			t.Errorf("After over an Optional value: %v", err)
		}
	})
}
