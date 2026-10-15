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
	boom := errors.New("boom")
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Err: boom})
	// The executor fails inside next, and velox wraps its error once.
	_, err := db.Delete[dBook]().Where(dBooks.ID.EQ(1)).Exec(ctx)
	if !errors.Is(err, boom) || err.Error() != "velox: velox_test.dBook: boom" {
		t.Fatalf("got %v", err)
	}
}

func TestHooksUnderSQL(t *testing.T) {
	trace = nil
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	q, args, err := db.Insert(&dBook{AuthorID: 1, Title: " x "}).SQL(ctx)
	if err != nil || !strings.HasPrefix(q, "INSERT") || strings.Join(trace, "|") != "outer before insert|outer after" {
		t.Fatalf("%q %v %v", q, err, trace)
	}
	if len(args) != 2 || args[1] != "x" {
		t.Fatalf("SQL returns the arguments as the hook left them: %v", args)
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

// hFlex's one hook runs flexHook, which each test sets. Stamp has an
// UpdateDefault.
type hFlex struct {
	ID    int64
	Title string
	Stamp int64
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
		Hooks: func() velox.BoundHooks { return velox.BindHooks((*hFlex)(nil).Hooks()) },
		Table: "flex",
		Columns: []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Title", Name: "title"},
			{Field: "Stamp", Name: "stamp", UpdateDefault: func() any { return int64(9) }}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Addr: func(x *hFlex, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.Title
			}
			return &x.Stamp
		},
	}
}

var (
	hfID    = velox.NumberCol[hFlex, int64]{I: 0}
	hfTitle = velox.StringCol[hFlex, string]{I: 1}
	hfStamp = velox.NumberCol[hFlex, int64]{I: 2}
)

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
	boom := errors.New("boom")
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Err: boom}) // the executor fails inside next
	_, err := db.Delete[hFlex]().Where(hfID.EQ(1)).Exec(ctx)
	if !errors.Is(err, errRefused) || !errors.Is(err, boom) || !strings.HasPrefix(err.Error(), "velox: velox_test.hFlex: hook: ") {
		t.Fatalf("got %v", err)
	}
}

// hNil's Hooks lists a nil hook.
type hNil struct{ ID int64 }

func (*hNil) Hooks() []velox.Hook[hNil] { return []velox.Hook[hNil]{nil} }

var hNilMeta velox.MetaOnce[hNil]

func (*hNil) VeloxMeta() *velox.Meta[hNil] {
	return hNilMeta.Load(func() velox.Desc[hNil] {
		return velox.Desc[hNil]{Hooks: func() velox.BoundHooks { return velox.BindHooks((*hNil)(nil).Hooks()) }, Table: "n", Columns: []velox.ColumnDesc{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0}, Tenant: -1,
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
		return velox.Desc[hPanic]{Policy: func() velox.BoundPolicy { return velox.BindPolicy((*hPanic)(nil).Policy()) }, Table: "p", Columns: []velox.ColumnDesc{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0}, Tenant: -1,
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

// TestNextTwiceSendsOnce: a second call of next sends nothing and fails the
// write, whether the hook returns that error or drops it.
func TestNextTwiceSendsOnce(t *testing.T) {
	for _, keep := range []bool{true, false} {
		flexHook = func(ctx context.Context, m *velox.Mutation[hFlex], next velox.Mutator[hFlex]) error {
			if err := next.Mutate(ctx, m); err != nil {
				return err
			}
			err := next.Mutate(ctx, m)
			if keep {
				return err
			}
			return nil
		}
		e := &velox.FakeExec{Affected: 1}
		db := velox.OpenFake(velox.Postgres, e)
		err := db.Insert(&hFlex{ID: 1}).Exec(ctx)
		if err == nil || err.Error() != "velox: velox_test.hFlex: hook: a hook called next twice" || e.Calls != 1 {
			t.Fatalf("keep %v: %v, %d calls", keep, err, e.Calls)
		}
	}
}

// TestValidatorsRunBeforeTheInsertPolicy: a row that fails both is a
// *ValidationError (Ruling 5), and nothing is sent.
func TestValidatorsRunBeforeTheInsertPolicy(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	tctx := velox.WithTenant(ctx, int64(7))
	var ve *velox.ValidationError
	bad := "no at sign"
	err := db.Insert(&dAuthor{Name: "mallory", Email: &bad}).Exec(tctx)
	if !errors.As(err, &ve) || errors.Is(err, velox.ErrDenied) || e.Calls != 0 {
		t.Fatalf("Insert: %v, %d calls", err, e.Calls)
	}
	err = db.InsertAll([]dAuthor{{Name: "ada"}, {Name: "mallory", Email: &bad}}).Exec(tctx)
	if !errors.As(err, &ve) || errors.Is(err, velox.ErrDenied) || e.Calls != 0 {
		t.Fatalf("InsertAll: %v, %d calls", err, e.Calls)
	}
}

// TestMisuseWithoutNextIsReported: a hook that records a misuse and returns
// nil without calling next fails the write, and nothing is sent.
func TestMisuseWithoutNextIsReported(t *testing.T) {
	flexHook = func(ctx context.Context, m *velox.Mutation[hFlex], next velox.Mutator[hFlex]) error {
		m.Where(velox.Pred[hFlex]{})
		return nil
	}
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	_, err := db.Delete[hFlex]().Where(hfID.EQ(1)).Exec(ctx)
	if err == nil || !strings.HasPrefix(err.Error(), "velox: velox_test.hFlex: hook: ") || !strings.Contains(err.Error(), "zero Predicate") || e.Calls != 0 {
		t.Fatalf("%v, %d calls", err, e.Calls)
	}
}

// TestHooksDoNotSeeUpdateDefaults: next adds them (Ruling 5).
func TestHooksDoNotSeeUpdateDefaults(t *testing.T) {
	var before, after bool
	flexHook = func(ctx context.Context, m *velox.Mutation[hFlex], next velox.Mutator[hFlex]) error {
		_, before = m.Value(hfStamp)
		err := next.Mutate(ctx, m)
		_, after = m.Value(hfStamp)
		return err
	}
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	if _, err := db.Update[hFlex]().Set(hfTitle.Set("x")).All().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if before || !after || len(e.Args) != 2 || e.Args[1] != int64(9) {
		t.Fatalf("before next %v, after %v, args %v", before, after, e.Args)
	}
}

// hNilMut's hook returns a nil Mutator.
type hNilMut struct{ ID int64 }

func (*hNilMut) Hooks() []velox.Hook[hNilMut] {
	return []velox.Hook[hNilMut]{func(velox.Mutator[hNilMut]) velox.Mutator[hNilMut] { return nil }}
}

var hNilMutMeta velox.MetaOnce[hNilMut]

func (*hNilMut) VeloxMeta() *velox.Meta[hNilMut] {
	return hNilMutMeta.Load(func() velox.Desc[hNilMut] {
		return velox.Desc[hNilMut]{Hooks: func() velox.BoundHooks { return velox.BindHooks((*hNilMut)(nil).Hooks()) }, Table: "m", Columns: []velox.ColumnDesc{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0}, Tenant: -1,
			Addr: func(x *hNilMut, i int) any { return &x.ID }}
	})
}

// hPanicHooks's Hooks reads its receiver, which is nil.
type hPanicHooks struct{ ID int64 }

func (x *hPanicHooks) Hooks() []velox.Hook[hPanicHooks] {
	_ = x.ID
	return nil
}

var hPanicHooksMeta velox.MetaOnce[hPanicHooks]

func (*hPanicHooks) VeloxMeta() *velox.Meta[hPanicHooks] {
	return hPanicHooksMeta.Load(func() velox.Desc[hPanicHooks] {
		return velox.Desc[hPanicHooks]{Hooks: func() velox.BoundHooks { return velox.BindHooks((*hPanicHooks)(nil).Hooks()) }, Table: "ph", Columns: []velox.ColumnDesc{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0}, Tenant: -1,
			Addr: func(x *hPanicHooks, i int) any { return &x.ID }}
	})
}

func TestNilMutatorAndPanickingHooks(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	err := db.Insert(&hNilMut{ID: 1}).Exec(ctx)
	if err == nil || err.Error() != "velox: velox_test.hNilMut: hook: a hook returned a nil Mutator" || e.Calls != 0 {
		t.Fatalf("%v, %d calls", err, e.Calls)
	}
	var se *velox.SchemaError
	err = db.Insert(&hPanicHooks{ID: 1}).Exec(ctx)
	if !errors.As(err, &se) || !strings.HasPrefix(err.Error(), "velox: velox_test.hPanicHooks: Hooks() panicked: ") {
		t.Fatalf("got %v", err)
	}
}

// oHooked and oPoliced are old-path entities with the Desc path's methods,
// which would be ignored.
type oHooked struct{ ID int64 }

func (*oHooked) Schema(*velox.Schema)         {}
func (*oHooked) Hooks() []velox.Hook[oHooked] { return nil }

var oHookedMeta velox.MetaOnce[oHooked]

func (*oHooked) VeloxMeta() *velox.Meta[oHooked] {
	return oHookedMeta.Get(func() velox.Shape[oHooked] {
		return velox.Shape[oHooked]{Columns: []string{"ID"}, Addr: func(x *oHooked, i int) any { return &x.ID }}
	})
}

type oPoliced struct{ ID int64 }

func (*oPoliced) Schema(*velox.Schema)           {}
func (*oPoliced) Policy() velox.Policy[oPoliced] { return velox.Policy[oPoliced]{} }

var oPolicedMeta velox.MetaOnce[oPoliced]

func (*oPoliced) VeloxMeta() *velox.Meta[oPoliced] {
	return oPolicedMeta.Get(func() velox.Shape[oPoliced] {
		return velox.Shape[oPoliced]{Columns: []string{"ID"}, Addr: func(x *oPoliced, i int) any { return &x.ID }}
	})
}

func TestOldPathRefusesDescMethods(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	var se *velox.SchemaError
	err := db.Insert(&oHooked{ID: 1}).Exec(ctx)
	if !errors.As(err, &se) || !strings.HasPrefix(err.Error(), "velox: velox_test.oHooked: has a Hooks method, which only generated (Desc) entities use") {
		t.Fatalf("got %v", err)
	}
	err = db.Insert(&oPoliced{ID: 1}).Exec(ctx)
	if !errors.As(err, &se) || !strings.HasPrefix(err.Error(), "velox: velox_test.oPoliced: has a Policy method, which only generated (Desc) entities use") {
		t.Fatalf("got %v", err)
	}
}

// uPolicy and uHooks declare a Policy and a Hooks method that their Desc
// does not bind: stale generated code, which must not leave them unrun.
type uPolicy struct{ ID int64 }

func (*uPolicy) Policy() velox.Policy[uPolicy] {
	return velox.Policy[uPolicy]{Insert: func(context.Context, *uPolicy) error { return velox.Deny("never") }}
}

var uPolicyMeta velox.MetaOnce[uPolicy]

func (*uPolicy) VeloxMeta() *velox.Meta[uPolicy] {
	return uPolicyMeta.Load(func() velox.Desc[uPolicy] {
		return velox.Desc[uPolicy]{Table: "up", Columns: []velox.ColumnDesc{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0}, Tenant: -1,
			Addr: func(x *uPolicy, i int) any { return &x.ID }}
	})
}

type uHooks struct{ ID int64 }

func (*uHooks) Hooks() []velox.Hook[uHooks] { return nil }

var uHooksMeta velox.MetaOnce[uHooks]

func (*uHooks) VeloxMeta() *velox.Meta[uHooks] {
	return uHooksMeta.Load(func() velox.Desc[uHooks] {
		return velox.Desc[uHooks]{Table: "uh", Columns: []velox.ColumnDesc{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0}, Tenant: -1,
			Addr: func(x *uHooks, i int) any { return &x.ID }}
	})
}

// uPlain has neither method, and its Desc binds neither.
type uPlain struct{ ID int64 }

var uPlainMeta velox.MetaOnce[uPlain]

func (*uPlain) VeloxMeta() *velox.Meta[uPlain] {
	return uPlainMeta.Load(func() velox.Desc[uPlain] {
		return velox.Desc[uPlain]{Table: "pl", Columns: []velox.ColumnDesc{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0}, Tenant: -1,
			Addr: func(x *uPlain, i int) any { return &x.ID }}
	})
}

// A Policy or Hooks method with the wrong signature needs no test here: the
// BindPolicy or BindHooks call velox-gen writes into Desc.Policy or
// Desc.Hooks does not compile.
func TestUnboundMethodsAreRefused(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	var se *velox.SchemaError
	err := db.Insert(&uPolicy{ID: 1}).Exec(ctx)
	if !errors.As(err, &se) || err.Error() != "velox: velox_test.uPolicy: has a Policy method, but the generated metadata does not bind it; run go tool velox-gen" {
		t.Fatalf("got %v", err)
	}
	err = db.Insert(&uHooks{ID: 1}).Exec(ctx)
	if !errors.As(err, &se) || err.Error() != "velox: velox_test.uHooks: has a Hooks method, but the generated metadata does not bind it; run go tool velox-gen" {
		t.Fatalf("got %v", err)
	}
	if e.Calls != 0 {
		t.Fatalf("%d calls", e.Calls)
	}
	if err := velox.CheckEntity[uPlain](); err != nil {
		t.Fatalf("nil Policy and Hooks with no methods: %v", err)
	}
	if err := db.Insert(&uPlain{ID: 1}).Exec(ctx); err != nil || e.Calls != 1 {
		t.Fatalf("%v, %d calls", err, e.Calls)
	}
}

// hUp has a Read policy, so that an upsert's update branch is conditioned on
// it, and a hook that reads the Result of the write once next returns.
type hUp struct{ ID, N int64 }

var hUps = struct {
	ID, N velox.NumberCol[hUp, int64]
}{velox.NumberCol[hUp, int64]{I: 0}, velox.NumberCol[hUp, int64]{I: 1}}

var hUpAffected int64 = -1

func (*hUp) Policy() velox.Policy[hUp] {
	return velox.Policy[hUp]{Read: func(context.Context) (velox.Pred[hUp], error) { return hUps.N.GT(0), nil }}
}

func (*hUp) Hooks() []velox.Hook[hUp] {
	return []velox.Hook[hUp]{func(next velox.Mutator[hUp]) velox.Mutator[hUp] {
		return velox.MutateFunc[hUp](func(ctx context.Context, m *velox.Mutation[hUp]) error {
			err := next.Mutate(ctx, m)
			hUpAffected = m.Result().Affected
			return err
		})
	}}
}

var hUpMeta velox.MetaOnce[hUp]

func (*hUp) VeloxMeta() *velox.Meta[hUp] {
	return hUpMeta.Load(func() velox.Desc[hUp] {
		return velox.Desc[hUp]{
			Policy: func() velox.BoundPolicy { return velox.BindPolicy((*hUp)(nil).Policy()) }, Hooks: func() velox.BoundHooks { return velox.BindHooks((*hUp)(nil).Hooks()) },
			Table: "ups", Columns: []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "N", Name: "n"}}, PrimaryKey: []int{0}, Tenant: -1,
			Addr: func(x *hUp, i int) any { return [...]any{&x.ID, &x.N}[i] },
		}
	})
}

// A write's Result.Affected is 0 after an error: an upsert whose existing row
// the policy hides fails after the statement ran, and reports none of the
// rows it affected.
func TestMutationResultAfterAnError(t *testing.T) {
	for _, affected := range []int64{0, 1} {
		hUpAffected = -1
		e := &velox.FakeExec{Affected: affected}
		db := velox.OpenFake(velox.Postgres, e)
		err := db.InsertAll([]hUp{{ID: 1, N: 1}, {ID: 2, N: 1}}).OnConflict(hUps.ID).Update(hUps.N).Exec(ctx)
		if !errors.Is(err, velox.ErrDenied) || hUpAffected != 0 {
			t.Errorf("affected %d: %v, Result().Affected %d", affected, err, hUpAffected)
		}
	}
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	if err := db.Insert(&hUp{ID: 1, N: 1}).OnConflict(hUps.ID).Update(hUps.N).Exec(ctx); err != nil || hUpAffected != 1 {
		t.Errorf("%v, Result().Affected %d", err, hUpAffected)
	}
}
