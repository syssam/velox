package apitest

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// wgBig has an unsigned key, which the fake can scan only by converting an
// int64 row value.
type wgBig struct {
	ID   uint64
	Name string
}

var wgBigMeta velox.MetaOnce[wgBig]

func (*wgBig) VeloxMeta() *velox.Meta[wgBig] {
	return wgBigMeta.Load(func() velox.Descriptor[wgBig] {
		return velox.Descriptor[wgBig]{
			Table:      "bigs",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"}},
			PrimaryKey: []int{0},
			FieldPtr: func(x *wgBig, i int) any {
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
	return wgPairMeta.Load(func() velox.Descriptor[wgPair] {
		return velox.Descriptor[wgPair]{
			Table:      "pairs",
			Columns:    []velox.ColumnDescriptor{{Field: "A", Name: "a"}, {Field: "B", Name: "b"}, {Field: "Price", Name: "price", Default: wgCents(250)}},
			PrimaryKey: []int{0, 1},
			FieldPtr: func(x *wgPair, i int) any {
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
	return wgPairVMeta.Load(func() velox.Descriptor[wgPairV] {
		return velox.Descriptor[wgPairV]{
			Table:      "pairs",
			Columns:    []velox.ColumnDescriptor{{Field: "A", Name: "a"}, {Field: "B", Name: "b"}, {Field: "Price", Name: "price", Default: wgCents(250)}},
			PrimaryKey: []int{0, 1},
			FieldPtr: func(x *wgPairV, i int) any {
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

// wgFlag has a bool whose Default is true and a number whose Default is
// 250, as ent's field.Bool("on").Default(true) and field.Int("price").Default(250).
type wgFlag struct {
	ID    int64
	On    bool
	Price int64
}

var wgFlagMeta velox.MetaOnce[wgFlag]

func (*wgFlag) VeloxMeta() *velox.Meta[wgFlag] {
	return wgFlagMeta.Load(func() velox.Descriptor[wgFlag] {
		return velox.Descriptor[wgFlag]{
			Table:      "flags",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "On", Name: "on", Default: true}, {Field: "Price", Name: "price", Default: int64(250)}},
			PrimaryKey: []int{0},
			FieldPtr: func(x *wgFlag, i int) any {
				switch i {
				case 0:
					return &x.ID
				case 1:
					return &x.On
				}
				return &x.Price
			},
		}
	})
}

var (
	wgFlagOn    = velox.Col[wgFlag, bool]{I: 1}
	wgFlagPrice = velox.Col[wgFlag, int64]{I: 2}
)

// A bool or number with a Default takes it when the struct leaves the field
// at its zero value and nothing writes it after. The defaults are filled
// before the setters and the hooks, as ent's builder fills them before its
// hooks, so a setter's false or 0, a hook's Mutation.Set and a Row's Set are
// stored over the default (ADR 0082).
func TestDefaultUnderExplicitZero(t *testing.T) {
	insert := func(t *testing.T, db *velox.DB, f func(q velox.InsertQuery[wgFlag]) velox.InsertQuery[wgFlag], x *wgFlag) {
		t.Helper()
		if err := f(db.Insert(x)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	same := func(q velox.InsertQuery[wgFlag]) velox.InsertQuery[wgFlag] { return q }
	want := func(t *testing.T, x wgFlag, on bool, price int64) {
		t.Helper()
		if x.On != on || x.Price != price {
			t.Errorf("On %v, Price %d; want %v, %d", x.On, x.Price, on, price)
		}
	}
	t.Run("unset takes the default", func(t *testing.T) {
		x := wgFlag{ID: 1}
		insert(t, openFake(velox.Postgres, &fakeconn.Exec{}), same, &x)
		want(t, x, true, 250)
	})
	t.Run("a value given is kept", func(t *testing.T) {
		x := wgFlag{ID: 1, On: true, Price: 7}
		insert(t, openFake(velox.Postgres, &fakeconn.Exec{}), same, &x)
		want(t, x, true, 7)
	})
	t.Run("a setter stores the zero", func(t *testing.T) {
		x := wgFlag{ID: 1}
		insert(t, openFake(velox.Postgres, &fakeconn.Exec{}), func(q velox.InsertQuery[wgFlag]) velox.InsertQuery[wgFlag] {
			return q.Set(wgFlagOn.Set(false), wgFlagPrice.Set(0))
		}, &x)
		want(t, x, false, 0)
	})
	t.Run("an InsertAll's setter stores the zero in every row", func(t *testing.T) {
		xs := []wgFlag{{ID: 1}, {ID: 2}}
		if err := openFake(velox.Postgres, &fakeconn.Exec{}).InsertAll(xs).Set(wgFlagOn.Set(false)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		for _, x := range xs {
			want(t, x, false, 250)
		}
	})
	t.Run("a hook's Set stores the zero, and sees the default", func(t *testing.T) {
		var seen bool
		hook := velox.Before(func(_ context.Context, m *velox.Mutation) error {
			for r := range m.Rows() {
				seen, _ = r.Value("on").(bool)
			}
			return m.Set("on", false)
		}, velox.OpInsert)
		x := wgFlag{ID: 1}
		insert(t, openFake(velox.Postgres, &fakeconn.Exec{}, velox.WithHooks[wgFlag](hook)), same, &x)
		want(t, x, false, 250)
		if !seen {
			t.Error("the hook saw on unset; the default is filled before the hooks")
		}
	})
	t.Run("a hook's Fill does not replace a default", func(t *testing.T) {
		hook := velox.Before(func(_ context.Context, m *velox.Mutation) error { return m.Fill("price", int64(9)) }, velox.OpInsert)
		x := wgFlag{ID: 1}
		insert(t, openFake(velox.Postgres, &fakeconn.Exec{}, velox.WithHooks[wgFlag](hook)), same, &x)
		want(t, x, true, 250)
	})
	t.Run("SQL renders the setter's zero", func(t *testing.T) {
		x := wgFlag{ID: 1}
		_, args, err := openFake(velox.Postgres, &fakeconn.Exec{}).Insert(&x).Set(wgFlagOn.Set(false)).SQL(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(args) != "[1 false 250]" {
			t.Errorf("args %v, want [1 false 250]", args)
		}
	})
}

// wgScore has a nullable number whose Default is 1.5, as ent's
// field.Float("score").Nillable().Default(1.5).
type wgScore struct {
	ID    int64
	Score *float64
}

var wgScoreMeta velox.MetaOnce[wgScore]

func (*wgScore) VeloxMeta() *velox.Meta[wgScore] {
	return wgScoreMeta.Load(func() velox.Descriptor[wgScore] {
		return velox.Descriptor[wgScore]{
			Table:      "scores",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Score", Name: "score", Default: 1.5}},
			PrimaryKey: []int{0},
			FieldPtr: func(x *wgScore, i int) any {
				if i == 0 {
					return &x.ID
				}
				return &x.Score
			},
		}
	})
}

// A nil pointer field with a Default takes it in an insert, as velox-gen's
// doc comment of such a field says.
func TestDefaultUnderNil(t *testing.T) {
	x := wgScore{ID: 1}
	_, args, err := openFake(velox.Postgres, &fakeconn.Exec{}).Insert(&x).SQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 2 || fmt.Sprint(velox.Show(args[1].(*float64))) != "1.5" {
		t.Errorf("args %v, want 1 and a pointer to 1.5", args)
	}
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

	// The same Default on a field that is no pointer fills its zero.
	v := wgPairV{A: 1, B: 2}
	if err := openFake(velox.Postgres, &fakeconn.Exec{}).Insert(&v).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if v.Price != 250 {
		t.Errorf("Price %v, want 250", v.Price)
	}
}
