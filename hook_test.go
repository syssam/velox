package velox_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox2"
)

// trace records the order hooks and the core ran in, per test.
var trace []string

func (*dBook) Hooks() []velox.Hook[dBook] {
	return []velox.Hook[dBook]{
		func(next velox.Mutator[dBook]) velox.Mutator[dBook] {
			return velox.MutateFunc[dBook](func(ctx context.Context, m *velox.Mutation[dBook]) error {
				trace = append(trace, "outer before "+m.Op().String())
				if m.Op() == velox.OpInsert {
					for _, b := range m.Rows() {
						b.Title = strings.TrimSpace(b.Title)
					}
				}
				err := next.Mutate(ctx, m)
				trace = append(trace, "outer after")
				return err
			})
		},
		velox.On(func(next velox.Mutator[dBook]) velox.Mutator[dBook] {
			return velox.MutateFunc[dBook](func(ctx context.Context, m *velox.Mutation[dBook]) error {
				trace = append(trace, "delete only")
				if hookRefuses {
					return errRefused
				}
				return next.Mutate(ctx, m)
			})
		}, velox.OpDelete),
	}
}

var (
	errRefused  = errors.New("refused")
	hookRefuses bool
)

func TestHookOrderAndRows(t *testing.T) {
	trace = nil
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{{int64(5)}}})
	b := dBook{AuthorID: 1, Title: "  dune  "}
	if err := db.Insert(&b).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if b.Title != "dune" || b.ID != 5 {
		t.Fatalf("the hook trims before next, and next writes back the key: %+v", b)
	}
	if strings.Join(trace, "|") != "outer before insert|outer after" {
		t.Fatalf("trace %v", trace)
	}
}

func TestOnSelectsOps(t *testing.T) {
	trace = nil
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Affected: 2})
	n, err := db.Delete[dBook]().Where(dBooks.AuthorID.EQ(1)).Exec(ctx)
	if err != nil || n != 2 || strings.Join(trace, "|") != "outer before delete|delete only|outer after" {
		t.Fatalf("%d %v %v", n, err, trace)
	}
}

func TestHookErrorIsWrapped(t *testing.T) {
	hookRefuses = true
	defer func() { hookRefuses = false }()
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	_, err := db.Delete[dBook]().Where(dBooks.ID.EQ(1)).Exec(ctx)
	if !errors.Is(err, errRefused) || err.Error() != "velox: velox_test.dBook: hook: refused" {
		t.Fatalf("got %v", err)
	}
}

func TestHookPassesVeloxErrorsThrough(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	_, err := db.Delete[dBook]().Exec(ctx) // no Where: the guard refuses inside next
	if !errors.Is(err, velox.ErrNoWhere) || strings.Count(err.Error(), "velox:") != 1 {
		t.Fatalf("got %v", err)
	}
}

func TestHooksUnderSQL(t *testing.T) {
	trace = nil
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	q, _, err := db.Insert(&dBook{AuthorID: 1, Title: " x "}).SQL(ctx)
	if err != nil || !strings.HasPrefix(q, "INSERT") || strings.Join(trace, "|") != "outer before insert|outer after" {
		t.Fatalf("%q %v %v", q, err, trace)
	}
}

// TestInsertAllRunsTheHooksOnce: once per call, over every row (Ruling 6).
func TestInsertAllRunsTheHooksOnce(t *testing.T) {
	trace = nil
	e := &velox.FakeExec{Affected: 2}
	db := velox.OpenFake(velox.Postgres, e)
	bs := []dBook{{ID: 1, AuthorID: 1, Title: " a "}, {ID: 2, AuthorID: 1, Title: " b "}}
	if err := db.InsertAll(bs).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if bs[0].Title != "a" || bs[1].Title != "b" || e.Calls != 1 || strings.Join(trace, "|") != "outer before insert|outer after" {
		t.Fatalf("%+v, %d calls, trace %v", bs, e.Calls, trace)
	}
}

func (*dAuthor) Hooks() []velox.Hook[dAuthor] {
	return []velox.Hook[dAuthor]{func(next velox.Mutator[dAuthor]) velox.Mutator[dAuthor] {
		return velox.MutateFunc[dAuthor](func(ctx context.Context, m *velox.Mutation[dAuthor]) error {
			if v, _ := ctx.Value(blankName{}).(bool); v && m.Op() == velox.OpUpdate {
				m.Set(dAuthors.Name.Set("")) // validated after the hook
			}
			if v, _ := ctx.Value(touchCreated{}).(bool); v && m.Op() == velox.OpUpdate {
				m.Set(dAuthors.CreatedAt.Set(someTime)) // immutable: refused after the hook
			}
			if v, _ := ctx.Value(renameTo{}).(string); v != "" && m.Op() == velox.OpUpdate {
				m.Set(dAuthors.Name.Set(v)) // Save writes it back
			}
			return next.Mutate(ctx, m)
		})
	}}
}

type blankName struct{}
type touchCreated struct{}
type renameTo struct{}

var someTime = time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)

func (*dAuthor) Policy() velox.Policy[dAuthor] {
	return velox.Policy[dAuthor]{Insert: func(ctx context.Context, a *dAuthor) error {
		if a.Name == "mallory" {
			return velox.Deny("not mallory")
		}
		return nil
	}}
}

func TestHookSetIsValidated(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Affected: 1})
	tctx := velox.WithTenant(ctx, int64(7))
	_, err := db.Update[dAuthor]().Set(dAuthors.Name.Set("ok")).Where(dAuthors.ID.EQ(1)).Exec(context.WithValue(tctx, blankName{}, true))
	var ve *velox.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("got %v", err)
	}
	_, err = db.Update[dAuthor]().Set(dAuthors.Name.Set("ok")).Where(dAuthors.ID.EQ(1)).Exec(context.WithValue(tctx, touchCreated{}, true))
	if !errors.Is(err, velox.ErrImmutable) {
		t.Fatalf("got %v", err)
	}
}

func TestPolicyMethod(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	err := db.Insert(&dAuthor{Name: "mallory"}).Exec(velox.WithTenant(ctx, int64(7)))
	if !errors.Is(err, velox.ErrDenied) {
		t.Fatalf("got %v", err)
	}
}

// TestSaveRunsTheHooks: Save is an update to the hooks, and writes back what
// they Set.
func TestSaveRunsTheHooks(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	a := dAuthor{ID: 1, OrgID: 7, Name: "ada"}
	if err := db.Save(context.WithValue(velox.WithTenant(ctx, int64(7)), renameTo{}, "grace"), &a, dAuthors.Name); err != nil {
		t.Fatal(err)
	}
	if a.Name != "grace" || e.Args[0] != "grace" {
		t.Fatalf("Name %q, args %v", a.Name, e.Args)
	}
}

// hFlex's one hook runs flexHook, which each test sets.
type hFlex struct {
	ID    int64
	Title string
}

var flexHook func(ctx context.Context, m *velox.Mutation[hFlex], next velox.Mutator[hFlex]) error

func (*hFlex) Hooks() []velox.Hook[hFlex] {
	return []velox.Hook[hFlex]{func(next velox.Mutator[hFlex]) velox.Mutator[hFlex] {
		return velox.MutateFunc[hFlex](func(ctx context.Context, m *velox.Mutation[hFlex]) error {
			return flexHook(ctx, m, next)
		})
	}}
}

var hFlexMeta velox.MetaOnce[hFlex]

func (*hFlex) VeloxMeta() *velox.Meta[hFlex] { return hFlexMeta.Load(hFlexDesc) }

func hFlexDesc() velox.Desc[hFlex] {
	return velox.Desc[hFlex]{
		Table:      "flex",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Title", Name: "title"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Addr: func(x *hFlex, i int) any {
			if i == 0 {
				return &x.ID
			}
			return &x.Title
		},
	}
}

var hfTitle = velox.StringCol[hFlex, string]{I: 1}

func TestMutationResult(t *testing.T) {
	var got int64 = -1
	flexHook = func(ctx context.Context, m *velox.Mutation[hFlex], next velox.Mutator[hFlex]) error {
		err := next.Mutate(ctx, m)
		got = m.Result().Affected
		return err
	}
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Affected: 3})
	if _, err := db.Update[hFlex]().Set(hfTitle.Set("x")).All().Exec(ctx); err != nil || got != 3 {
		t.Fatalf("%v, Result().Affected %d", err, got)
	}
	if err := db.InsertAll([]hFlex{{ID: 1}, {ID: 2}}).Exec(ctx); err != nil || got != 3 {
		t.Fatalf("%v, Result().Affected %d", err, got)
	}
	if _, _, err := db.Update[hFlex]().Set(hfTitle.Set("x")).All().SQL(ctx); err != nil || got != 0 {
		t.Fatalf("%v, under SQL Result().Affected %d", err, got)
	}
}

func TestMutationMisuseOnAnInsert(t *testing.T) {
	flexHook = func(ctx context.Context, m *velox.Mutation[hFlex], next velox.Mutator[hFlex]) error {
		if m.Rows() == nil {
			return errors.New("an insert has rows")
		}
		m.Set(hfTitle.Set("x"))
		return next.Mutate(ctx, m)
	}
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	err := db.Insert(&hFlex{ID: 1}).Exec(ctx)
	if err == nil || err.Error() != "velox: velox_test.hFlex: hook: Set and Where are for updates and deletes; change Rows" || e.Calls != 0 {
		t.Fatalf("%v, %d calls", err, e.Calls)
	}
}

func TestMutationRowsIsNilForAnUpdate(t *testing.T) {
	flexHook = func(ctx context.Context, m *velox.Mutation[hFlex], next velox.Mutator[hFlex]) error {
		if m.Rows() != nil {
			return errors.New("an update has no rows")
		}
		return next.Mutate(ctx, m)
	}
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Affected: 1})
	if _, err := db.Update[hFlex]().Set(hfTitle.Set("x")).All().Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestHookMayReplaceTheCoreError(t *testing.T) {
	flexHook = func(ctx context.Context, m *velox.Mutation[hFlex], next velox.Mutator[hFlex]) error {
		if err := next.Mutate(ctx, m); err != nil {
			return errors.Join(errRefused, err)
		}
		return nil
	}
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	_, err := db.Delete[hFlex]().Exec(ctx)
	if !errors.Is(err, errRefused) || !errors.Is(err, velox.ErrNoWhere) || !strings.HasPrefix(err.Error(), "velox: velox_test.hFlex: hook: ") {
		t.Fatalf("got %v", err)
	}
}

// hNil's Hooks lists a nil hook.
type hNil struct{ ID int64 }

func (*hNil) Hooks() []velox.Hook[hNil] { return []velox.Hook[hNil]{nil} }

var hNilMeta velox.MetaOnce[hNil]

func (*hNil) VeloxMeta() *velox.Meta[hNil] {
	return hNilMeta.Load(func() velox.Desc[hNil] {
		return velox.Desc[hNil]{Table: "n", Columns: []velox.ColumnDesc{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0}, Tenant: -1,
			Addr: func(x *hNil, i int) any { return &x.ID }}
	})
}

// hPanic's Policy reads its receiver, which is nil.
type hPanic struct{ ID int64 }

func (x *hPanic) Policy() velox.Policy[hPanic] {
	_ = x.ID
	return velox.Policy[hPanic]{}
}

var hPanicMeta velox.MetaOnce[hPanic]

func (*hPanic) VeloxMeta() *velox.Meta[hPanic] {
	return hPanicMeta.Load(func() velox.Desc[hPanic] {
		return velox.Desc[hPanic]{Table: "p", Columns: []velox.ColumnDesc{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0}, Tenant: -1,
			Addr: func(x *hPanic, i int) any { return &x.ID }}
	})
}

func TestHooksAndPolicyRefusals(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	var se *velox.SchemaError
	err := db.Insert(&hNil{ID: 1}).Exec(ctx)
	if !errors.As(err, &se) || err.Error() != "velox: velox_test.hNil: Hooks()[0] is nil" {
		t.Fatalf("got %v", err)
	}
	err = db.Insert(&hPanic{ID: 1}).Exec(ctx)
	if !errors.As(err, &se) || !strings.HasPrefix(err.Error(), "velox: velox_test.hPanic: Policy() panicked: ") {
		t.Fatalf("got %v", err)
	}
}

// dPlain is dBook without relations or methods: the Desc path with no hooks.
type dPlain struct {
	ID       int64
	AuthorID int64
	Title    string
}

var dPlainMeta velox.MetaOnce[dPlain]

func (*dPlain) VeloxMeta() *velox.Meta[dPlain] {
	return dPlainMeta.Load(func() velox.Desc[dPlain] {
		return velox.Desc[dPlain]{
			Table:      "books",
			Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "AuthorID", Name: "author_id"}, {Field: "Title", Name: "title"}},
			PrimaryKey: []int{0},
			Tenant:     -1,
			Addr: func(x *dPlain, i int) any {
				switch i {
				case 0:
					return &x.ID
				case 1:
					return &x.AuthorID
				}
				return &x.Title
			},
		}
	})
}

// oPlain is dPlain on the old path.
type oPlain struct {
	ID       int64
	AuthorID int64
	Title    string
}

func (*oPlain) Schema(s *velox.Schema) { s.Table("books") }

var oPlainMeta velox.MetaOnce[oPlain]

func (*oPlain) VeloxMeta() *velox.Meta[oPlain] {
	return oPlainMeta.Get(func() velox.Shape[oPlain] {
		return velox.Shape[oPlain]{Columns: []string{"ID", "AuthorID", "Title"}, Addr: func(x *oPlain, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.AuthorID
			}
			return &x.Title
		}}
	})
}

// TestDescInsertBudget: an entity on the Desc path without hooks pays
// nothing for them: a cached one-row Insert allocates as the old path's does.
func TestDescInsertBudget(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{{int64(5)}}})
	desc := func() {
		x := dPlain{AuthorID: 1, Title: "dune"}
		if err := db.Insert(&x).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	old := func() {
		x := oPlain{AuthorID: 1, Title: "dune"}
		if err := db.Insert(&x).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	desc() // fill the render cache
	old()
	d, o := testing.AllocsPerRun(200, desc), testing.AllocsPerRun(200, old)
	t.Logf("a one-row Insert: %.0f allocations on the Desc path, %.0f on the old path", d, o)
	if d > o {
		t.Errorf("an entity with no hooks pays %.0f allocations for them on a one-row Insert", d-o)
	}
}
