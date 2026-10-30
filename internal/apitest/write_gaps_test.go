package apitest

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/fakeconn"
	"github.com/syssam/velox2/internal/testmodel"
)

// wgBig has an unsigned key, which the fake can scan only by converting an
// int64 row value.
type wgBig struct {
	ID   uint64
	Name string
}

var wgBigMeta velox.MetaOnce[wgBig]

func (*wgBig) VeloxMeta() *velox.Meta[wgBig] {
	return wgBigMeta.Load(func() velox.Desc[wgBig] {
		return velox.Desc[wgBig]{
			Table:      "bigs",
			Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"}},
			PrimaryKey: []int{0},
			Tenant:     -1,
			Addr: func(x *wgBig, i int) any {
				if i == 0 {
					return &x.ID
				}
				return &x.Name
			},
		}
	})
}

// wgCents is a named numeric type.
type wgCents int64

// wgPair has a composite key and an optional price whose column has a
// Default of a named numeric type.
type wgPair struct {
	A, B  int64
	Price *wgCents
}

var wgPairs = struct {
	A, B  velox.NumberCol[wgPair, int64]
	Price velox.NullNumberCol[wgPair, wgCents]
}{velox.NumberCol[wgPair, int64]{I: 0}, velox.NumberCol[wgPair, int64]{I: 1}, velox.NullNumberCol[wgPair, wgCents]{I: 2}}

var wgPairMeta velox.MetaOnce[wgPair]

func (*wgPair) VeloxMeta() *velox.Meta[wgPair] {
	return wgPairMeta.Load(func() velox.Desc[wgPair] {
		return velox.Desc[wgPair]{
			Table:      "pairs",
			Columns:    []velox.ColumnDesc{{Field: "A", Name: "a"}, {Field: "B", Name: "b"}, {Field: "Price", Name: "price", Default: wgCents(250)}},
			PrimaryKey: []int{0, 1},
			Tenant:     -1,
			Addr: func(x *wgPair, i int) any {
				switch i {
				case 0:
					return &x.A
				case 1:
					return &x.B
				}
				return &x.Price
			},
		}
	})
}

// wgPairV is wgPair with a plain price: a Default on it would overwrite an
// explicit 0.
type wgPairV struct {
	A, B  int64
	Price wgCents
}

var wgPairVMeta velox.MetaOnce[wgPairV]

func (*wgPairV) VeloxMeta() *velox.Meta[wgPairV] {
	return wgPairVMeta.Load(func() velox.Desc[wgPairV] {
		return velox.Desc[wgPairV]{
			Table:      "pairs",
			Columns:    []velox.ColumnDesc{{Field: "A", Name: "a"}, {Field: "B", Name: "b"}, {Field: "Price", Name: "price", Default: wgCents(250)}},
			PrimaryKey: []int{0, 1},
			Tenant:     -1,
			Addr: func(x *wgPairV, i int) any {
				switch i {
				case 0:
					return &x.A
				case 1:
					return &x.B
				}
				return &x.Price
			},
		}
	})
}

// TestFakeScanErrors: a row value of the wrong kind is an error from the
// fake, as database/sql's Scan returns one, not a panic.
func TestFakeScanErrors(t *testing.T) {
	for _, c := range []struct {
		name string
		col  velox.Column[testmodel.Invoice]
		row  any
		want string
	}{
		{"string into int64", I.ID, "x", "fake: cannot scan string into *int64"},
		{"nil into float64", I.Total, nil, "fake: cannot scan <nil> into *float64"},
	} {
		db := openFake(velox.Postgres, &fakeconn.Exec{Results: [][][]any{{{c.row}}}})
		_, err := db.Query[testmodel.Invoice]().Columns(c.col).Limit(1).All(ctx)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error containing %q", c.name, err, c.want)
		}
	}
}

func TestInsertReadsBackAUint64Key(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{Rows: [][]any{{int64(1) << 40}}})
	x := wgBig{Name: "x"}
	if err := db.Insert(&x).Exec(ctx); err != nil || x.ID != 1<<40 {
		t.Errorf("ID %d, %v", x.ID, err)
	}
	db = openFake(velox.Postgres, &fakeconn.Exec{Rows: [][]any{{int64(-1)}}})
	y := wgBig{Name: "y"}
	if err := db.Insert(&y).Exec(ctx); err == nil || !strings.Contains(err.Error(), "fake: -1 overflows *uint64") {
		t.Errorf("a negative key: %v", err)
	}
}

// TestWriteQueriesAreSafeToExtendConcurrently is
// TestBaseQueryIsSafeToExtendConcurrently for Update and Delete; run it with
// -race.
func TestWriteQueriesAreSafeToExtendConcurrently(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{Affected: 1})
	upBase := db.Update[testmodel.Invoice]().Set(I.Total.Set(1)).Where(I.OrgID.EQ(1))
	delBase := db.Delete[testmodel.Invoice]().Where(I.OrgID.EQ(1))
	baseSQL := func() (string, string) {
		up, _, err := upBase.SQL(ctx)
		if err != nil {
			t.Fatal(err)
		}
		del, _, err := delBase.SQL(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return up, del
	}
	upBefore, delBefore := baseSQL()

	const upWant = `UPDATE "invoices" SET "total" = $1, "updated_at" = $2 WHERE "org_id" = $3 AND "id" = $4`
	const delWant = `DELETE FROM "invoices" WHERE "org_id" = $1 AND "id" = $2`
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 100 {
				id := int64(g*1000 + i)
				up := upBase.Where(I.ID.EQ(id))
				text, args, err := up.SQL(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				if len(args) != 4 || text != upWant || !reflect.DeepEqual(args[2:], []any{int64(1), id}) {
					t.Errorf("goroutine %d: update %s %v", g, text, args)
					return
				}
				if _, err := up.Exec(ctx); err != nil {
					t.Error(err)
					return
				}
				del := delBase.Where(I.ID.EQ(id))
				text, args, err = del.SQL(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				if text != delWant || !reflect.DeepEqual(args, []any{int64(1), id}) {
					t.Errorf("goroutine %d: delete %s %v", g, text, args)
					return
				}
				if _, err := del.Exec(ctx); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	if up, del := baseSQL(); up != upBefore || del != delBefore {
		t.Errorf("a base changed:\n%s\n%s\nwas\n%s\n%s", up, del, upBefore, delBefore)
	}
}

func TestSaveCompositeKey(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	p := wgPair{A: 1, B: 2}
	if err := db.Save(&p, wgPairs.B).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `UPDATE "pairs" SET "b" = $1 WHERE "a" = $2 AND "b" = $3`; e.SQL != want {
		t.Errorf("\n got %s\nwant %s", e.SQL, want)
	}
	if want := []any{int64(2), int64(1), int64(2)}; !reflect.DeepEqual(e.Args, want) {
		t.Errorf("args %#v, want %#v", e.Args, want)
	}
}

func TestNamedNumericDefault(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	p := wgPair{A: 1, B: 2}
	if err := db.Insert(&p).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `INSERT INTO "pairs" ("a", "b", "price") VALUES ($1, $2, $3)`; e.SQL != want {
		t.Errorf("\n got %s\nwant %s", e.SQL, want)
	}
	if p.Price == nil || *p.Price != 250 {
		t.Errorf("Price %v, want 250", p.Price)
	}

	e = &fakeconn.Exec{}
	db = openFake(velox.Postgres, e)
	v := wgPairV{A: 1, B: 2}
	want := "velox: apitest.wgPairV.Price: Default on bool/numeric field Price would overwrite an explicit false or 0 on insert; make the field a pointer (nil uses the default)"
	if err := db.Insert(&v).Exec(ctx); err == nil || err.Error() != want || e.Calls != 0 {
		var se *velox.SchemaError
		t.Errorf("%v (SchemaError %v), calls %d", err, errors.As(err, &se), e.Calls)
	}
}
