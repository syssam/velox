package apitest

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

func TestHookOrderAndRows(t *testing.T) {
	fixture.Trace = nil
	db := openFake(velox.Postgres, &fakeconn.Exec{Rows: [][]any{{int64(5)}}})
	b := fixture.DBook{AuthorID: 1, Title: "  dune  "}
	if err := db.Insert(&b).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if b.Title != "dune" || b.ID != 5 {
		t.Fatalf("the hook trims before next, and next writes back the key: %+v", b)
	}
	if strings.Join(fixture.Trace, "|") != "outer before insert|outer after" {
		t.Fatalf("trace %v", fixture.Trace)
	}
}

func TestOnSelectsOps(t *testing.T) {
	fixture.Trace = nil
	db := openFake(velox.Postgres, &fakeconn.Exec{Affected: 2})
	n, err := db.Delete[fixture.DBook]().Where(fixture.DBooks.AuthorID.EQ(1)).Exec(ctx)
	if err != nil || n != 2 || strings.Join(fixture.Trace, "|") != "outer before delete|delete only|outer after" {
		t.Fatalf("%d %v %v", n, err, fixture.Trace)
	}
}

func TestHookErrorIsWrapped(t *testing.T) {
	fixture.HookRefuses = true
	defer func() { fixture.HookRefuses = false }()
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	_, err := db.Delete[fixture.DBook]().Where(fixture.DBooks.ID.EQ(1)).Exec(ctx)
	if !errors.Is(err, fixture.ErrRefused) || err.Error() != "velox: fixture.DBook: hook: refused" {
		t.Fatalf("got %v", err)
	}
}

func TestHookPassesVeloxErrorsThrough(t *testing.T) {
	boom := errors.New("boom")
	db := openFake(velox.Postgres, &fakeconn.Exec{Err: boom})
	// The executor fails inside next, and velox wraps its error once.
	_, err := db.Delete[fixture.DBook]().Where(fixture.DBooks.ID.EQ(1)).Exec(ctx)
	if !errors.Is(err, boom) || err.Error() != "velox: fixture.DBook: boom" {
		t.Fatalf("got %v", err)
	}
}

func TestHooksUnderSQL(t *testing.T) {
	fixture.Trace = nil
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	q, args, err := db.Insert(&fixture.DBook{AuthorID: 1, Title: " x "}).SQL(ctx)
	if err != nil || !strings.HasPrefix(q, "INSERT") || strings.Join(fixture.Trace, "|") != "outer before insert|outer after" {
		t.Fatalf("%q %v %v", q, err, fixture.Trace)
	}
	if len(args) != 2 || args[1] != "x" {
		t.Fatalf("SQL returns the arguments as the hook left them: %v", args)
	}
}

// TestInsertAllRunsTheHooksOnce: once per call, over every row.
func TestInsertAllRunsTheHooksOnce(t *testing.T) {
	fixture.Trace = nil
	e := &fakeconn.Exec{Affected: 2}
	db := openFake(velox.Postgres, e)
	bs := []fixture.DBook{{ID: 1, AuthorID: 1, Title: " a "}, {ID: 2, AuthorID: 1, Title: " b "}}
	if err := db.InsertAll(bs).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if bs[0].Title != "a" || bs[1].Title != "b" || e.Calls != 1 || strings.Join(fixture.Trace, "|") != "outer before insert|outer after" {
		t.Fatalf("%+v, %d calls, trace %v", bs, e.Calls, fixture.Trace)
	}
}

func TestHookSetIsValidated(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{Affected: 1})
	tctx := testmodel.Org.With(context.Background(), int64(7))
	_, err := db.Update[fixture.DAuthor]().Set(fixture.DAuthors.Name.Set("ok")).Where(fixture.DAuthors.ID.EQ(1)).Exec(context.WithValue(tctx, fixture.BlankName{}, true))
	if _, ok := errors.AsType[*velox.ValidationError](err); !ok {
		t.Fatalf("got %v", err)
	}
	_, err = db.Update[fixture.DAuthor]().Set(fixture.DAuthors.Name.Set("ok")).Where(fixture.DAuthors.ID.EQ(1)).Exec(context.WithValue(tctx, fixture.TouchCreated{}, true))
	if !errors.Is(err, velox.ErrImmutable) {
		t.Fatalf("got %v", err)
	}
}

func TestPolicyMethod(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	err := db.Insert(&fixture.DAuthor{Name: "mallory"}).Exec(testmodel.Org.With(context.Background(), int64(7)))
	if !errors.Is(err, velox.ErrDenied) {
		t.Fatalf("got %v", err)
	}
}

// TestSaveRunsTheHooks: Save is an update to the hooks, and writes back what
// they Set.
func TestSaveRunsTheHooks(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	a := fixture.DAuthor{ID: 1, OrgID: 7, Name: "ada"}
	if err := db.Save(&a, fixture.DAuthors.Name).Exec(context.WithValue(testmodel.Org.With(context.Background(), int64(7)), fixture.RenameTo{}, "grace")); err != nil {
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

var flexHook func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error

func (*hFlex) Hooks() []velox.Hook {
	return []velox.Hook{func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			return flexHook(ctx, m, next)
		})
	}}
}

var hFlexMeta velox.MetaOnce[hFlex]

func (*hFlex) VeloxMeta() *velox.Meta[hFlex] { return hFlexMeta.Load(hFlexDescriptor) }

func hFlexDescriptor() velox.Descriptor[hFlex] {
	return velox.Descriptor[hFlex]{
		Hooks: []func() []velox.Hook{(*hFlex)(nil).Hooks},
		Table: "flex",
		Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Title", Name: "title"},
			{Field: "Stamp", Name: "stamp", UpdateDefault: func() any { return int64(9) }}},
		PrimaryKey: []int{0},
		FieldPtr: func(x *hFlex, i int) any {
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
)

// TestMutationMisuseOnAnInsert: Where on an insert is a misuse, and nothing
// is sent.
func TestMutationMisuseOnAnInsert(t *testing.T) {
	flexHook = func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error {
		n := 0
		for range m.Rows() {
			n++
		}
		if n != 1 {
			return errors.New("an insert has rows")
		}
		m.Where(velox.C("title").EQ("x"))
		return next.Mutate(ctx, m)
	}
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	err := db.Insert(&hFlex{ID: 1}).Exec(ctx)
	if err == nil || err.Error() != "velox: apitest.hFlex: hook: Where is for updates and deletes; an insert's rows are set with Set and Fill" || e.Calls != 0 {
		t.Fatalf("%v, %d calls", err, e.Calls)
	}
}

func TestMutationRowsIsEmptyForAnUpdate(t *testing.T) {
	flexHook = func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error {
		for range m.Rows() {
			return errors.New("an update has no rows")
		}
		return next.Mutate(ctx, m)
	}
	db := openFake(velox.Postgres, &fakeconn.Exec{Affected: 1})
	if _, err := db.Update[hFlex]().Set(hfTitle.Set("x")).All().Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestHookMayReplaceTheCoreError(t *testing.T) {
	flexHook = func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error {
		if err := next.Mutate(ctx, m); err != nil {
			return errors.Join(fixture.ErrRefused, err)
		}
		return nil
	}
	boom := errors.New("boom")
	db := openFake(velox.Postgres, &fakeconn.Exec{Err: boom}) // the executor fails inside next
	_, err := db.Delete[hFlex]().Where(hfID.EQ(1)).Exec(ctx)
	if !errors.Is(err, fixture.ErrRefused) || !errors.Is(err, boom) || !strings.HasPrefix(err.Error(), "velox: apitest.hFlex: hook: ") {
		t.Fatalf("got %v", err)
	}
}

// hNil's Hooks lists a nil hook.
type hNil struct{ ID int64 }

func (*hNil) Hooks() []velox.Hook { return []velox.Hook{nil} }

var hNilMeta velox.MetaOnce[hNil]

func (*hNil) VeloxMeta() *velox.Meta[hNil] {
	return hNilMeta.Load(func() velox.Descriptor[hNil] {
		return velox.Descriptor[hNil]{Hooks: []func() []velox.Hook{(*hNil)(nil).Hooks}, Table: "n", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0},
			FieldPtr: func(x *hNil, i int) any { return &x.ID }}
	})
}

// hNilAfter's Hooks lists a nil hook, and a mixin's binds a hook before it.
type hNilAfter struct{ ID int64 }

func (*hNilAfter) Hooks() []velox.Hook { return []velox.Hook{nil} }

var hNilAfterMeta velox.MetaOnce[hNilAfter]

func (*hNilAfter) VeloxMeta() *velox.Meta[hNilAfter] {
	return hNilAfterMeta.Load(func() velox.Descriptor[hNilAfter] {
		return velox.Descriptor[hNilAfter]{Hooks: []func() []velox.Hook{nilAfterMixin, (*hNilAfter)(nil).Hooks}, Table: "n", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0},
			FieldPtr: func(x *hNilAfter, i int) any { return &x.ID }}
	})
}

// nilAfterMixin is a mixin's Hooks for hNilAfter: one hook that does nothing.
func nilAfterMixin() []velox.Hook {
	return []velox.Hook{func(next velox.Mutator) velox.Mutator { return next }}
}

// hPanic's Policy reads its receiver, which is nil.
type hPanic struct{ ID int64 }

func (x *hPanic) Policy() velox.Policy {
	_ = x.ID
	return velox.Policy{}
}

var hPanicMeta velox.MetaOnce[hPanic]

func (*hPanic) VeloxMeta() *velox.Meta[hPanic] {
	return hPanicMeta.Load(func() velox.Descriptor[hPanic] {
		return velox.Descriptor[hPanic]{Policy: []func() velox.Policy{(*hPanic)(nil).Policy}, Table: "p", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0},
			FieldPtr: func(x *hPanic, i int) any { return &x.ID }}
	})
}

func TestHooksAndPolicyRefusals(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	var se *velox.SchemaError
	err := db.Insert(&hNil{ID: 1}).Exec(ctx)
	if !errors.As(err, &se) || err.Error() != "velox: apitest.hNil: Hooks()[0] is nil" {
		t.Fatalf("got %v", err)
	}
	// The nil hook's index counts the hooks bound before it, a mixin's.
	err = db.Insert(&hNilAfter{ID: 1}).Exec(ctx)
	if !errors.As(err, &se) || err.Error() != "velox: apitest.hNilAfter: Hooks()[1] is nil" {
		t.Fatalf("got %v", err)
	}
	err = db.Insert(&hPanic{ID: 1}).Exec(ctx)
	if !errors.As(err, &se) || !strings.HasPrefix(err.Error(), "velox: apitest.hPanic: Policy() panicked: ") {
		t.Fatalf("got %v", err)
	}
}

// dPlain is dBook without relations or methods: the Descriptor path with no hooks.
type dPlain struct {
	ID       int64
	AuthorID int64
	Title    string
}

var dPlainMeta velox.MetaOnce[dPlain]

func (*dPlain) VeloxMeta() *velox.Meta[dPlain] {
	return dPlainMeta.Load(func() velox.Descriptor[dPlain] {
		return velox.Descriptor[dPlain]{
			Table:      "books",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "AuthorID", Name: "author_id"}, {Field: "Title", Name: "title"}},
			PrimaryKey: []int{0},
			FieldPtr: func(x *dPlain, i int) any {
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

// TestDescriptorInsertBudget: an entity without hooks pays nothing for them: a
// cached one-row Insert allocates 13 times (measured; the test fails above).
func TestDescriptorInsertBudget(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{Rows: [][]any{{int64(5)}}})
	insert := func() {
		x := dPlain{AuthorID: 1, Title: "dune"}
		if err := db.Insert(&x).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	insert() // fill the render cache
	n := testing.AllocsPerRun(200, insert)
	t.Logf("a one-row Insert makes %.0f allocations", n)
	if n > 13 {
		t.Errorf("a one-row Insert of an entity with no hooks makes %.0f allocations, over 13", n)
	}
}

// dPlainDef is dPlain with a scalar Default on Title.
type dPlainDef struct {
	ID       int64
	AuthorID int64
	Title    string
}

var dPlainDefMeta velox.MetaOnce[dPlainDef]

func (*dPlainDef) VeloxMeta() *velox.Meta[dPlainDef] {
	return dPlainDefMeta.Load(func() velox.Descriptor[dPlainDef] {
		return velox.Descriptor[dPlainDef]{
			Table:      "books",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "AuthorID", Name: "author_id"}, {Field: "Title", Name: "title", Default: "dune"}},
			PrimaryKey: []int{0},
			FieldPtr: func(x *dPlainDef, i int) any {
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

// TestDescriptorInsertDefaultBudget: filling a scalar static Default allocates
// nothing: a one-row Insert that takes it costs what dPlain's costs.
func TestDescriptorInsertDefaultBudget(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{Rows: [][]any{{int64(5)}}})
	plain := func() {
		x := dPlain{AuthorID: 1, Title: "dune"}
		if err := db.Insert(&x).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	def := func() {
		x := dPlainDef{AuthorID: 1}
		if err := db.Insert(&x).Exec(ctx); err != nil || x.Title != "dune" {
			t.Fatal(err, x.Title)
		}
	}
	plain()
	def() // fill the render caches
	np, nd := testing.AllocsPerRun(200, plain), testing.AllocsPerRun(200, def)
	if nd > np {
		t.Errorf("an Insert that fills a scalar Default makes %.0f allocations, over the %.0f of one without", nd, np)
	}
}

// TestNextTwiceSendsOnce: a second call of next sends nothing and fails the
// write, whether the hook returns that error or drops it.
func TestNextTwiceSendsOnce(t *testing.T) {
	for _, keep := range []bool{true, false} {
		flexHook = func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error {
			if err := next.Mutate(ctx, m); err != nil {
				return err
			}
			err := next.Mutate(ctx, m)
			if keep {
				return err
			}
			return nil
		}
		e := &fakeconn.Exec{Affected: 1}
		db := openFake(velox.Postgres, e)
		err := db.Insert(&hFlex{ID: 1}).Exec(ctx)
		if err == nil || err.Error() != "velox: apitest.hFlex: hook: a hook called next twice" || e.Calls != 1 {
			t.Fatalf("keep %v: %v, %d calls", keep, err, e.Calls)
		}
	}
}

// TestValidatorsRunBeforeTheInsertPolicy: a row that fails both is a
// *ValidationError, and nothing is sent.
func TestValidatorsRunBeforeTheInsertPolicy(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	tctx := testmodel.Org.With(context.Background(), int64(7))
	var ve *velox.ValidationError
	bad := "no at sign"
	err := db.Insert(&fixture.DAuthor{Name: "mallory", Email: &bad}).Exec(tctx)
	if !errors.As(err, &ve) || errors.Is(err, velox.ErrDenied) || e.Calls != 0 {
		t.Fatalf("Insert: %v, %d calls", err, e.Calls)
	}
	err = db.InsertAll([]fixture.DAuthor{{Name: "ada"}, {Name: "mallory", Email: &bad}}).Exec(tctx)
	if !errors.As(err, &ve) || errors.Is(err, velox.ErrDenied) || e.Calls != 0 {
		t.Fatalf("InsertAll: %v, %d calls", err, e.Calls)
	}
}

// TestMisuseWithoutNextIsReported: a hook that records a misuse and returns
// nil without calling next fails the write, and nothing is sent.
func TestMisuseWithoutNextIsReported(t *testing.T) {
	flexHook = func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error {
		m.Where(velox.Cond{})
		return nil
	}
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	_, err := db.Delete[hFlex]().Where(hfID.EQ(1)).Exec(ctx)
	if err == nil || !strings.HasPrefix(err.Error(), "velox: apitest.hFlex: hook: ") || !strings.Contains(err.Error(), "zero Predicate") || e.Calls != 0 {
		t.Fatalf("%v, %d calls", err, e.Calls)
	}
}

// TestHooksDoNotSeeUpdateDefaults: next adds them.
func TestHooksDoNotSeeUpdateDefaults(t *testing.T) {
	var before, after bool
	flexHook = func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error {
		_, before = m.Value("stamp")
		err := next.Mutate(ctx, m)
		_, after = m.Value("stamp")
		return err
	}
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	if _, err := db.Update[hFlex]().Set(hfTitle.Set("x")).All().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if before || !after || len(e.Args) != 2 || e.Args[1] != int64(9) {
		t.Fatalf("before next %v, after %v, args %v", before, after, e.Args)
	}
}

// hNilMut's hook returns a nil Mutator.
type hNilMut struct{ ID int64 }

func (*hNilMut) Hooks() []velox.Hook {
	return []velox.Hook{func(velox.Mutator) velox.Mutator { return nil }}
}

var hNilMutMeta velox.MetaOnce[hNilMut]

func (*hNilMut) VeloxMeta() *velox.Meta[hNilMut] {
	return hNilMutMeta.Load(func() velox.Descriptor[hNilMut] {
		return velox.Descriptor[hNilMut]{Hooks: []func() []velox.Hook{(*hNilMut)(nil).Hooks}, Table: "m", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0},
			FieldPtr: func(x *hNilMut, i int) any { return &x.ID }}
	})
}

// hPanicHooks's Hooks reads its receiver, which is nil.
type hPanicHooks struct{ ID int64 }

func (x *hPanicHooks) Hooks() []velox.Hook {
	_ = x.ID
	return nil
}

var hPanicHooksMeta velox.MetaOnce[hPanicHooks]

func (*hPanicHooks) VeloxMeta() *velox.Meta[hPanicHooks] {
	return hPanicHooksMeta.Load(func() velox.Descriptor[hPanicHooks] {
		return velox.Descriptor[hPanicHooks]{Hooks: []func() []velox.Hook{(*hPanicHooks)(nil).Hooks}, Table: "ph", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0},
			FieldPtr: func(x *hPanicHooks, i int) any { return &x.ID }}
	})
}

func TestNilMutatorAndPanickingHooks(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	err := db.Insert(&hNilMut{ID: 1}).Exec(ctx)
	if err == nil || err.Error() != "velox: apitest.hNilMut: hook: a hook returned a nil Mutator" || e.Calls != 0 {
		t.Fatalf("%v, %d calls", err, e.Calls)
	}
	var se *velox.SchemaError
	err = db.Insert(&hPanicHooks{ID: 1}).Exec(ctx)
	if !errors.As(err, &se) || !strings.HasPrefix(err.Error(), "velox: apitest.hPanicHooks: Hooks() panicked: ") {
		t.Fatalf("got %v", err)
	}
}

// uPolicy and uHooks declare a Policy and a Hooks method that their Descriptor
// does not bind: stale generated code, which must not leave them unrun.
type uPolicy struct{ ID int64 }

func (*uPolicy) Policy() velox.Policy {
	return velox.Policy{Insert: func(context.Context, velox.Row) error { return velox.Deny("never") }}
}

var uPolicyMeta velox.MetaOnce[uPolicy]

func (*uPolicy) VeloxMeta() *velox.Meta[uPolicy] {
	return uPolicyMeta.Load(func() velox.Descriptor[uPolicy] {
		return velox.Descriptor[uPolicy]{Table: "up", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0},
			FieldPtr: func(x *uPolicy, i int) any { return &x.ID }}
	})
}

type uHooks struct{ ID int64 }

func (*uHooks) Hooks() []velox.Hook { return nil }

var uHooksMeta velox.MetaOnce[uHooks]

func (*uHooks) VeloxMeta() *velox.Meta[uHooks] {
	return uHooksMeta.Load(func() velox.Descriptor[uHooks] {
		return velox.Descriptor[uHooks]{Table: "uh", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0},
			FieldPtr: func(x *uHooks, i int) any { return &x.ID }}
	})
}

// uPlain has neither method, and its Descriptor binds neither.
type uPlain struct{ ID int64 }

var uPlainMeta velox.MetaOnce[uPlain]

func (*uPlain) VeloxMeta() *velox.Meta[uPlain] {
	return uPlainMeta.Load(func() velox.Descriptor[uPlain] {
		return velox.Descriptor[uPlain]{Table: "pl", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0},
			FieldPtr: func(x *uPlain, i int) any { return &x.ID }}
	})
}

// A Policy or Hooks method with the wrong signature needs no test here: the
// method value velox-gen lists in Descriptor.Policy or
// Descriptor.Hooks does not compile.
func TestUnboundMethodsAreRefused(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	var se *velox.SchemaError
	err := db.Insert(&uPolicy{ID: 1}).Exec(ctx)
	if !errors.As(err, &se) || err.Error() != "velox: apitest.uPolicy: has a Policy method, but the generated metadata does not bind it: velox-gen binds the one the schema package declares" {
		t.Fatalf("got %v", err)
	}
	err = db.Insert(&uHooks{ID: 1}).Exec(ctx)
	if !errors.As(err, &se) || err.Error() != "velox: apitest.uHooks: has a Hooks method, but the generated metadata does not bind it: velox-gen binds the one the schema package declares" {
		t.Fatalf("got %v", err)
	}
	if e.Calls != 0 {
		t.Fatalf("%d calls", e.Calls)
	}
	// CheckEntity (velox-gen verify) reports the same.
	for _, c := range []struct {
		err  error
		want string
	}{
		{velox.CheckEntity[uPolicy](), "velox: apitest.uPolicy: has a Policy method, but the generated metadata does not bind it: velox-gen binds the one the schema package declares"},
		{velox.CheckEntity[uHooks](), "velox: apitest.uHooks: has a Hooks method, but the generated metadata does not bind it: velox-gen binds the one the schema package declares"},
	} {
		if !errors.As(c.err, &se) || c.err.Error() != c.want {
			t.Errorf("CheckEntity: got %v, want %s", c.err, c.want)
		}
	}
	if err := velox.CheckEntity[uPlain](); err != nil {
		t.Fatalf("nil Policy and Hooks with no methods: %v", err)
	}
	if err := db.Insert(&uPlain{ID: 1}).Exec(ctx); err != nil || e.Calls != 1 {
		t.Fatalf("%v, %d calls", err, e.Calls)
	}
}

// hUp has a Read policy, so that an upsert's update branch is conditioned on
// it, and a hook that reads the rows the write affected once next returns.
type hUp struct{ ID, N int64 }

var hUps = struct {
	ID, N velox.NumberCol[hUp, int64]
}{velox.NumberCol[hUp, int64]{I: 0}, velox.NumberCol[hUp, int64]{I: 1}}

var hUpAffected int64 = -1

func (*hUp) Policy() velox.Policy {
	return velox.Policy{Read: func(context.Context) (velox.Cond, error) { return velox.C("n").GT(0), nil }}
}

func (*hUp) Hooks() []velox.Hook {
	return []velox.Hook{func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			err := next.Mutate(ctx, m)
			hUpAffected = m.Affected()
			return err
		})
	}}
}

var hUpMeta velox.MetaOnce[hUp]

func (*hUp) VeloxMeta() *velox.Meta[hUp] {
	return hUpMeta.Load(func() velox.Descriptor[hUp] {
		return velox.Descriptor[hUp]{
			Policy: []func() velox.Policy{(*hUp)(nil).Policy}, Hooks: []func() []velox.Hook{(*hUp)(nil).Hooks},
			Table: "ups", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "N", Name: "n"}}, PrimaryKey: []int{0},
			FieldPtr: func(x *hUp, i int) any { return [...]any{&x.ID, &x.N}[i] },
		}
	})
}

// TestMutationAffected: Affected is the rows the write inserted or matched
// once next has returned, and 0 under SQL(ctx). It is 0 after an error: an
// upsert whose existing row the policy hides fails after the statement ran,
// and reports none of the rows it affected.
func TestMutationAffected(t *testing.T) {
	var got int64 = -1
	flexHook = func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error {
		if m.Affected() != 0 {
			t.Errorf("before next Affected is %d", m.Affected())
		}
		err := next.Mutate(ctx, m)
		got = m.Affected()
		return err
	}
	db := openFake(velox.Postgres, &fakeconn.Exec{Affected: 3})
	if _, err := db.Update[hFlex]().Set(hfTitle.Set("x")).All().Exec(ctx); err != nil || got != 3 {
		t.Fatalf("%v, Affected %d", err, got)
	}
	if err := db.InsertAll([]hFlex{{ID: 1}, {ID: 2}}).Exec(ctx); err != nil || got != 3 {
		t.Fatalf("%v, Affected %d", err, got)
	}
	if _, _, err := db.Update[hFlex]().Set(hfTitle.Set("x")).All().SQL(ctx); err != nil || got != 0 {
		t.Fatalf("%v, under SQL Affected %d", err, got)
	}

	for _, affected := range []int64{0, 1} {
		hUpAffected = -1
		e := &fakeconn.Exec{Affected: affected}
		db := openFake(velox.Postgres, e)
		err := db.InsertAll([]hUp{{ID: 1, N: 1}, {ID: 2, N: 1}}).OnConflict(hUps.ID).Update(hUps.N).Exec(context.Background())
		if !errors.Is(err, velox.ErrDenied) || hUpAffected != 0 {
			t.Errorf("affected %d: %v, Affected %d", affected, err, hUpAffected)
		}
	}
	e := &fakeconn.Exec{Affected: 1}
	db = openFake(velox.Postgres, e)
	if err := db.Insert(&hUp{ID: 1, N: 1}).OnConflict(hUps.ID).Update(hUps.N).Exec(context.Background()); err != nil || hUpAffected != 1 {
		t.Errorf("%v, Affected %d", err, hUpAffected)
	}
}

// hCond's first hook adds stamp = 2 to an update or a delete; its second
// runs condsHook.
type hCond struct{ ID, Stamp int64 }

var (
	hcID    = velox.NumberCol[hCond, int64]{I: 0}
	hcStamp = velox.NumberCol[hCond, int64]{I: 1}
)

var condsHook func(m *velox.Mutation)

func (*hCond) Hooks() []velox.Hook {
	return []velox.Hook{
		func(next velox.Mutator) velox.Mutator {
			return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
				if m.Op() != velox.OpInsert {
					m.Where(velox.C("stamp").EQ(2))
				}
				return next.Mutate(ctx, m)
			})
		},
		func(next velox.Mutator) velox.Mutator {
			return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
				condsHook(m)
				return next.Mutate(ctx, m)
			})
		},
	}
}

var hCondMeta velox.MetaOnce[hCond]

func (*hCond) VeloxMeta() *velox.Meta[hCond] {
	return hCondMeta.Load(func() velox.Descriptor[hCond] {
		return velox.Descriptor[hCond]{
			Hooks: []func() []velox.Hook{(*hCond)(nil).Hooks},
			Table: "conds", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Stamp", Name: "stamp"}},
			PrimaryKey: []int{0},
			FieldPtr:   func(x *hCond, i int) any { return [...]any{&x.ID, &x.Stamp}[i] },
		}
	})
}

// TestConds: Conds is the caller's Where and the earlier hooks' conditions,
// in order, as a copy; nil for an insert.
func TestConds(t *testing.T) {
	want := []velox.Cond{velox.Cond(hcID.EQ(1)), velox.Cond(hcStamp.GT(0)), velox.C("stamp").EQ(2)}
	var got, again []velox.Cond
	condsHook = func(m *velox.Mutation) {
		got = m.Conds()
		if len(got) > 0 {
			cs := m.Conds()
			cs[0] = velox.C("id").EQ(9)
			_ = append(cs[:1], velox.C("id").EQ(8))
			_ = append(cs, velox.C("id").EQ(7))
			again = m.Conds()
		}
	}
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	if _, err := db.Update[hCond]().Set(hcStamp.Set(5)).Where(hcID.EQ(1), hcStamp.GT(0)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(again, want) {
		t.Errorf("Conds %v, after changing a copy %v; want %v", got, again, want)
	}
	if e.SQL != `UPDATE "conds" SET "stamp" = $1 WHERE "id" = $2 AND "stamp" > $3 AND "stamp" = $4` {
		t.Errorf("SQL %s", e.SQL)
	}
	if _, err := db.Delete[hCond]().Where(hcID.EQ(1), hcStamp.GT(0)).Exec(ctx); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("on a delete: %v, Conds %v", err, got)
	}
	// All: only the hook's condition.
	if _, err := db.Delete[hCond]().All().Exec(ctx); err != nil || !reflect.DeepEqual(got, want[2:]) {
		t.Errorf("on a delete of All: %v, Conds %v", err, got)
	}
	got = []velox.Cond{}
	if err := db.Insert(&hCond{ID: 1}).Exec(ctx); err != nil || got != nil {
		t.Errorf("on an insert: %v, Conds %v", err, got)
	}
}

// hSoft is confined to the context's org, with a Read policy. Its delete hook updates Deleted
// instead of deleting: it does not call next, and reports the update's count
// with SetAffected unless the context holds noCount. Its update hook records
// Conds once next has returned in softConds.
type hSoft struct {
	ID, OrgID int64
	Deleted   bool
}

var hSofts = struct {
	ID, OrgID velox.NumberCol[hSoft, int64]
	Deleted   velox.Col[hSoft, bool]
}{velox.NumberCol[hSoft, int64]{I: 0}, velox.NumberCol[hSoft, int64]{I: 1}, velox.Col[hSoft, bool]{I: 2}}

func (*hSoft) Policy() velox.Policy {
	return velox.Policy{Read: func(context.Context) (velox.Cond, error) { return velox.C("id").GT(0), nil }}
}

type noCount struct{}

var softConds []velox.Cond

func (*hSoft) Hooks() []velox.Hook {
	return []velox.Hook{
		velox.On(func(next velox.Mutator) velox.Mutator {
			return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
				t := m.Table()
				col, _ := t.Info().Column("deleted")
				set, err := t.Assign(col, true)
				if err != nil {
					return err
				}
				q := m.DB().UpdateTable(t).Assign(set)
				if cs := m.Conds(); len(cs) > 0 {
					q = q.Where(velox.Predicates(cs)...)
				} else {
					q = q.All()
				}
				n, err := q.Exec(ctx)
				if ctx.Value(noCount{}) == nil {
					m.SetAffected(n)
				}
				return err
			})
		}, velox.OpDelete),
		velox.On(func(next velox.Mutator) velox.Mutator {
			return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
				err := next.Mutate(ctx, m)
				softConds = m.Conds()
				return err
			})
		}, velox.OpUpdate),
	}
}

var hSoftMeta velox.MetaOnce[hSoft]

func (*hSoft) VeloxMeta() *velox.Meta[hSoft] {
	return hSoftMeta.Load(func() velox.Descriptor[hSoft] {
		return velox.Descriptor[hSoft]{
			Policy: []func() velox.Policy{fixture.OrgPolicy, (*hSoft)(nil).Policy},
			Hooks:  []func() []velox.Hook{(*hSoft)(nil).Hooks},
			Table:  "softs",
			Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id", Immutable: true},
				{Field: "Deleted", Name: "deleted"}},
			PrimaryKey: []int{0},
			FieldPtr:   func(x *hSoft, i int) any { return [...]any{&x.ID, &x.OrgID, &x.Deleted}[i] },
		}
	})
}

// TestSoftDeleteThroughConds: a delete hook rewrites the delete into an
// update through Conds. The update adds the org and the policy once, as
// they are not in Conds, and the delete sends nothing itself. It reports the
// update's count, which the hook gave SetAffected, and 0 without it. A
// delete of All gives no Conds, and the update says All.
func TestSoftDeleteThroughConds(t *testing.T) {
	e := &fakeconn.Exec{Affected: 4}
	db := openFake(velox.Postgres, e)
	tctx := testmodel.Org.With(context.Background(), int64(7))
	n, err := db.Delete[hSoft]().Where(hSofts.ID.EQ(1)).Exec(tctx)
	if err != nil || n != 4 || e.Calls != 1 {
		t.Fatalf("%v, %d rows, %d statements", err, n, e.Calls)
	}
	const one = `UPDATE "softs" SET "deleted" = $1 WHERE "id" = $2 AND "org_id" = $3 AND "id" > $4`
	if e.SQL != one || !reflect.DeepEqual(e.Args, []any{true, int64(1), int64(7), int64(0)}) {
		t.Errorf("SQL %s %v, want %s", e.SQL, e.Args, one)
	}
	// The update's hook, after next, saw the delete's conditions only.
	if want := []velox.Cond{velox.Cond(hSofts.ID.EQ(1))}; !reflect.DeepEqual(softConds, want) {
		t.Errorf("Conds after next %v, want %v", softConds, want)
	}
	n, err = db.Delete[hSoft]().All().Exec(tctx)
	if err != nil || n != 4 || e.Calls != 2 {
		t.Fatalf("All: %v, %d rows, %d statements", err, n, e.Calls)
	}
	const all = `UPDATE "softs" SET "deleted" = $1 WHERE "org_id" = $2 AND "id" > $3`
	if e.SQL != all || !reflect.DeepEqual(e.Args, []any{true, int64(7), int64(0)}) {
		t.Errorf("All: SQL %s %v, want %s", e.SQL, e.Args, all)
	}
	if len(softConds) != 0 {
		t.Errorf("All: Conds after next %v", softConds)
	}
	n, err = db.Delete[hSoft]().Where(hSofts.ID.EQ(1)).Exec(context.WithValue(tctx, noCount{}, true))
	if err != nil || n != 0 || e.Calls != 3 {
		t.Errorf("without SetAffected: %v, %d rows, %d statements", err, n, e.Calls)
	}
	// Under SQL(ctx) the hook's update is a write, refused.
	if _, _, err := db.Delete[hSoft]().Where(hSofts.ID.EQ(1)).SQL(tctx); !errors.Is(err, velox.ErrDryRun) || e.Calls != 3 {
		t.Errorf("SQL: %v, %d statements", err, e.Calls)
	}
}

// TestCondsAfterNext: once next has returned, Conds is still the conditions
// as given, without the org's, the policy's or a Rewrite hook's, which
// the statement sent.
func TestCondsAfterNext(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	rw := velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		st.Where = append(st.Where, velox.ColumnEQ(2, false))
		return nil
	})
	db := openFake(velox.Postgres, e, rw)
	softConds = nil
	if _, err := db.Update[hSoft]().Set(hSofts.Deleted.Set(true)).Where(hSofts.ID.EQ(1), hSofts.ID.LT(9)).Exec(testmodel.Org.With(context.Background(), int64(7))); err != nil {
		t.Fatal(err)
	}
	if want := []velox.Cond{velox.Cond(hSofts.ID.EQ(1)), velox.Cond(hSofts.ID.LT(9))}; !reflect.DeepEqual(softConds, want) {
		t.Errorf("Conds after next %v, want %v", softConds, want)
	}
	if !strings.Contains(e.SQL, `"org_id" = `) || !strings.Contains(e.SQL, `"id" > `) || strings.Count(e.SQL, `"deleted" = `) != 2 {
		t.Errorf("SQL %s", e.SQL)
	}
}

// TestSetAffected: a hook that makes the write itself reports its count
// with SetAffected, and Exec returns it, -1, the count its own Exec did not
// know, included. With next, in either order, or a negative count other than
// -1, it is a misuse.
func TestSetAffected(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	flexHook = func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error {
		m.SetAffected(5)
		return nil
	}
	if n, err := db.Update[hFlex]().Set(hfTitle.Set("x")).All().Exec(ctx); err != nil || n != 5 || e.Calls != 0 {
		t.Errorf("own write: %v, %d rows, %d statements", err, n, e.Calls)
	}
	const both = "velox: apitest.hFlex: hook: SetAffected is for a hook that makes the write itself; it called next too"
	flexHook = func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error {
		m.SetAffected(5)
		return next.Mutate(ctx, m)
	}
	if n, err := db.Delete[hFlex]().All().Exec(ctx); err == nil || err.Error() != both || n != 0 || e.Calls != 0 {
		t.Errorf("SetAffected, then next: %v, %d rows, %d statements", err, n, e.Calls)
	}
	flexHook = func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error {
		err := next.Mutate(ctx, m)
		m.SetAffected(5)
		return err
	}
	if n, err := db.Delete[hFlex]().All().Exec(ctx); err == nil || err.Error() != both || n != 0 {
		t.Errorf("next, then SetAffected: %v, %d rows", err, n)
	}
	flexHook = func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error {
		m.SetAffected(-1)
		return nil
	}
	if n, err := db.Delete[hFlex]().All().Exec(ctx); err != nil || n != -1 {
		t.Errorf("unknown: %v, %d rows; want -1 and no error", err, n)
	}
	flexHook = func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error {
		m.SetAffected(-2)
		return nil
	}
	if n, err := db.Delete[hFlex]().All().Exec(ctx); err == nil || !strings.HasSuffix(err.Error(), "hook: SetAffected of a negative count other than -1") || n != 0 {
		t.Errorf("negative: %v, %d rows", err, n)
	}
}

// SetAffected on an insert is a misuse, as Set and Where are: an insert's
// count is its rows, and its Exec returns none.
func TestSetAffectedOnAnInsert(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	flexHook = func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error {
		m.SetAffected(1)
		return nil
	}
	err := db.Insert(&hFlex{ID: 1, Title: "a"}).Exec(ctx)
	if want := "velox: apitest.hFlex: hook: SetAffected is for updates and deletes; an insert's count is its rows"; err == nil || err.Error() != want {
		t.Errorf("SetAffected on an insert: %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}

// A Save whose hook makes the write itself, with SetAffected, leaves *x as
// it was, the hook's assignments not stored: velox wrote nothing.
func TestSaveWithAHooksOwnWrite(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	for _, n := range []int64{0, 1} {
		flexHook = func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error {
			_ = m.Set("title", "hooked")
			m.SetAffected(n)
			return nil
		}
		x := hFlex{ID: 1, Title: "new"}
		if err := db.Save(&x, hfTitle).Exec(ctx); err != nil {
			t.Fatalf("SetAffected(%d): %v", n, err)
		}
		if x != (hFlex{ID: 1, Title: "new"}) || e.Calls != 0 {
			t.Errorf("SetAffected(%d): *x %+v, %d statements", n, x, e.Calls)
		}
	}
}

// TestMutationSetOfAnotherKind: a value of another kind than the column's
// fails the write, naming the column and both kinds, and nothing is sent;
// Set returns the misuse too.
func TestMutationSetOfAnotherKind(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	var serr error
	flexHook = func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error {
		serr = m.Set("title", 5)
		return next.Mutate(ctx, m)
	}
	_, err := db.Update[hFlex]().Set(hfTitle.Set("x")).All().Exec(ctx)
	const text = `velox: apitest.hFlex: hook: Set("title", 5): title is a string, not an int`
	if err == nil || err.Error() != text || serr == nil || e.Calls != 0 {
		t.Errorf("got %v (Set: %v), %d sent; want %q", err, serr, e.Calls, text)
	}
}

// TestMutationValueAfterRewrite: once next has returned, Value reports the
// assignment the statement sent, a Rewrite hook's included.
func TestMutationValueAfterRewrite(t *testing.T) {
	rw := velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		for i := range st.Set {
			if st.Set[i].Column == 1 {
				st.Set[i].Value = "rewritten"
			}
		}
		return nil
	})
	db := openFake(velox.Postgres, &fakeconn.Exec{Affected: 1}, rw)
	var before, after any
	flexHook = func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error {
		before, _ = m.Value("title")
		err := next.Mutate(ctx, m)
		after, _ = m.Value("title")
		return err
	}
	if _, err := db.Update[hFlex]().Set(hfTitle.Set("x")).All().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if before != "x" || after != "rewritten" {
		t.Errorf("Value before next %v, after %v", before, after)
	}
}

// TestRewriteLeavesExistsTables: a Rewrite hook that points Entity at
// another table moves the statement's own table only. The tables in the
// EXISTS that Has adds keep their names.
func TestRewriteLeavesExistsTables(t *testing.T) {
	moved := func(_ context.Context, st *velox.Statement) error {
		info := *st.Entity
		info.Table = "t_" + st.Entity.Table
		st.Entity = &info
		return nil
	}
	db := openFake(velox.Postgres, &fakeconn.Exec{}, velox.WithRewrite(moved))
	sql, _, err := db.Query[testmodel.Invoice]().Where(I.Items.Has()).Limit(1).SQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := `FROM "t_invoices" WHERE EXISTS (SELECT 1 FROM "items" AS "vx1"`; !strings.Contains(sql, want) {
		t.Errorf("SQL %s\nwant it to contain %s", sql, want)
	}
}
