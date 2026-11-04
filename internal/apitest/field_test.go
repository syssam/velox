package apitest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
)

// orgMixin is behaviour written once for any entity with an org_id column
// and a deleted_at column, as a mixin writes it: it cannot name the
// generated handles, so it finds the columns by name.
type orgMixin struct{}

func (orgMixin) Policy[E any]() velox.Policy[E] {
	org := velox.Field[E, int64]("org_id")
	return velox.Policy[E]{Read: func(context.Context) (velox.Pred[E], error) { return org.EQ(7), nil }}
}

func (orgMixin) Hooks[E any]() []velox.Hook[E] {
	org := velox.Field[E, int64]("org_id")
	return []velox.Hook[E]{velox.On(func(next velox.Mutator[E]) velox.Mutator[E] {
		return velox.MutateFunc[E](func(ctx context.Context, m *velox.Mutation[E]) error {
			for _, x := range m.Rows() {
				if p := org.Ptr(x); *p == 0 {
					*p = 7
				}
			}
			return next.Mutate(ctx, m)
		})
	}, velox.OpInsert)}
}

func (orgMixin) Interceptors[E any]() []velox.Interceptor[E] {
	del := velox.Field[E, *time.Time]("deleted_at")
	return []velox.Interceptor[E]{{Filter: func(context.Context) (velox.Pred[E], error) { return del.IsNull(), nil }}}
}

// fNote is an entity whose behaviour is orgMixin's.
type fNote struct {
	ID        int64
	OrgID     int64
	Title     string
	DeletedAt *time.Time
}

var fNoteMeta velox.MetaOnce[fNote]

func (*fNote) VeloxMeta() *velox.Meta[fNote] {
	return fNoteMeta.Load(func() velox.Descriptor[fNote] {
		return velox.Descriptor[fNote]{
			Table: "notes",
			Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id", Immutable: true},
				{Field: "Title", Name: "title"}, {Field: "DeletedAt", Name: "deleted_at"}},
			PrimaryKey:   []int{0},
			Policy:       []func() velox.BoundPolicy{func() velox.BoundPolicy { return velox.BindPolicy(orgMixin{}.Policy[fNote]()) }},
			Hooks:        []func() velox.BoundHooks{func() velox.BoundHooks { return velox.BindHooks(orgMixin{}.Hooks[fNote]()) }},
			Interceptors: []func() velox.BoundInterceptors{func() velox.BoundInterceptors { return velox.BindInterceptors(orgMixin{}.Interceptors[fNote]()) }},
			FieldPtr:     func(x *fNote, i int) any { return [...]any{&x.ID, &x.OrgID, &x.Title, &x.DeletedAt}[i] },
		}
	})
}

func fakeNotes() (*velox.DB, *fakeconn.Exec) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(1)}}, Affected: 1, Answer: func(sql string, _ []any) ([][]any, bool) {
		return nil, strings.HasPrefix(sql, "SELECT")
	}}
	return openFake(velox.Postgres, e), e
}

// TestFieldInsideLoad: a mixin's Policy, Hooks and Interceptors find their
// columns by name while velox builds the entity's metadata, and their
// conditions and hook reach the statements.
func TestFieldInsideLoad(t *testing.T) {
	if err := velox.CheckEntity[fNote](); err != nil {
		t.Fatal(err)
	}
	db, e := fakeNotes()
	ctx := context.Background()
	if _, err := db.Query[fNote]().Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `SELECT "id", "org_id", "title", "deleted_at" FROM "notes" WHERE "deleted_at" IS NULL AND "org_id" = $1 LIMIT $2`; e.SQL != want {
		t.Errorf("SQL\n%s\nwant\n%s", e.SQL, want)
	}
	if want := "[7 1]"; fmt.Sprint(e.Args) != want {
		t.Errorf("args %v, want %v", e.Args, want)
	}
	n := &fNote{ID: 1, Title: "a"}
	if err := db.Insert(n).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if n.OrgID != 7 || !strings.Contains(fmt.Sprint(e.Args), "7") {
		t.Errorf("the hook did not fill org_id: %+v, args %v", n, e.Args)
	}
}

// TestFieldWrongNameIsSchemaError: a name that is no column fails the
// entity's metadata build, so every statement on it, and a direct call
// panics with the same *SchemaError.
func TestFieldWrongNameIsSchemaError(t *testing.T) {
	err := velox.CheckEntity[fBadName]()
	var se *velox.SchemaError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), `Field: no column is named "nope"`) {
		t.Fatalf("CheckEntity: %v", err)
	}
	db, _ := fakeNotes()
	if _, err := db.Query[fBadName]().Limit(1).All(context.Background()); !errors.As(err, &se) {
		t.Errorf("a statement: %v", err)
	}
	p := panicOf(func() { velox.Field[fNote, int64]("nope") })
	if !errors.As(asError(p), &se) || se.Entity != "apitest.fNote" || !strings.Contains(se.Reason, `no column is named "nope"`) {
		t.Errorf("direct call: %v", p)
	}
}

// TestFieldWrongTypeIsSchemaError: the Go type must be the field's exactly,
// so a value type for a pointer field fails, and a pointer type for a value
// field fails, at the build and in a direct call.
func TestFieldWrongTypeIsSchemaError(t *testing.T) {
	err := velox.CheckEntity[fBadType]()
	var se *velox.SchemaError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), `column "deleted_at" is of type *time.Time, not time.Time`) {
		t.Fatalf("CheckEntity: %v", err)
	}
	for _, c := range []struct {
		call func()
		want string
	}{
		{func() { velox.Field[fNote, time.Time]("deleted_at") }, `column "deleted_at" is of type *time.Time, not time.Time`},
		{func() { velox.Field[fNote, *int64]("org_id") }, `column "org_id" is of type int64, not *int64`},
		{func() { velox.Field[fNote, int32]("org_id") }, `column "org_id" is of type int64, not int32`},
	} {
		p := panicOf(c.call)
		if !errors.As(asError(p), &se) || se.Field == "" || !strings.Contains(se.Reason, c.want) {
			t.Errorf("panic %v, want a *SchemaError with %q", p, c.want)
		}
	}
}

// TestFieldNotAnEntity: a type that is not an entity panics with the error
// every generic misuse gets.
func TestFieldNotAnEntity(t *testing.T) {
	type plain struct{ ID int64 }
	p := panicOf(func() { velox.Field[plain, int64]("id") })
	if !strings.Contains(fmt.Sprint(p), "is not an entity") {
		t.Errorf("panic %v", p)
	}
}

// TestFieldAfterLoad: a Filter may find a column at statement time, once
// the metadata is built.
func TestFieldAfterLoad(t *testing.T) {
	db, e := fakeNotes()
	if _, err := db.Query[fLate]().Limit(1).All(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := `SELECT "id", "title" FROM "lates" WHERE "title" <> $1 LIMIT $2`; e.SQL != want {
		t.Errorf("SQL\n%s\nwant\n%s", e.SQL, want)
	}
}

// TestFieldBeforeFirstUse: Field on an entity no statement has used builds
// its metadata, once.
func TestFieldBeforeFirstUse(t *testing.T) {
	if fLazyBuilds != 0 {
		t.Fatalf("built %d times before its first use", fLazyBuilds)
	}
	title := velox.Field[fLazy, string]("title")
	if fLazyBuilds != 1 {
		t.Fatalf("built %d times by Field", fLazyBuilds)
	}
	db, e := fakeNotes()
	if _, err := db.Query[fLazy]().Where(title.EQ("a")).Limit(1).All(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fLazyBuilds != 1 || !strings.Contains(e.SQL, `WHERE "title" = $1`) {
		t.Errorf("built %d times; SQL %s", fLazyBuilds, e.SQL)
	}
}

// TestFieldConcurrentFirstUse: two entities whose policies find each other's
// columns, built at once from two goroutines, neither deadlock nor race.
func TestFieldConcurrentFirstUse(t *testing.T) {
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Go(func() { <-start; errs <- velox.CheckEntity[fPing]() })
	wg.Go(func() { <-start; errs <- velox.CheckEntity[fPong]() })
	close(start)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the two builds deadlocked")
	}
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
}

// TestNamedPtrSetNull: Ptr is the row's field; Set of a nil pointer sets
// NULL and of another the value it points to; EQ and NEQ of nil are IS NULL
// and IS NOT NULL; and a Named is a column for Columns, Save and OnConflict.
func TestNamedPtrSetNull(t *testing.T) {
	ctx := context.Background()
	db, e := fakeNotes()
	id := velox.Field[fNote, int64]("id")
	title := velox.Field[fNote, string]("title")
	del := velox.Field[fNote, *time.Time]("deleted_at")
	org := velox.Field[fNote, int64]("org_id")
	if !org.Immutable() || org.Nullable() || !del.Nullable() || del.Immutable() {
		t.Errorf("flags: org immutable %v nullable %v, deleted_at immutable %v nullable %v", org.Immutable(), org.Nullable(), del.Immutable(), del.Nullable())
	}

	n := fNote{Title: "a"}
	*title.Ptr(&n) = "b"
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	*del.Ptr(&n) = &ts
	if n.Title != "b" || n.DeletedAt != &ts {
		t.Errorf("Ptr: %+v", n)
	}

	q, args, err := db.Update[fNote]().Set(del.Set(nil)).Where(id.EQ(1)).SQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := `UPDATE "notes" SET "deleted_at" = $1 WHERE "id" = $2 AND "deleted_at" IS NULL AND "org_id" = $3`; q != want || args[0] != nil {
		t.Errorf("Set(nil)\n%s %v\nwant\n%s, NULL first", q, args, want)
	}
	q, args, err = db.Update[fNote]().Set(del.Set(&ts)).Where(del.NEQ(nil), del.EQ(&ts)).SQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := `UPDATE "notes" SET "deleted_at" = $1 WHERE "deleted_at" IS NOT NULL AND "deleted_at" = $2 AND "deleted_at" IS NULL AND "org_id" = $3`; q != want {
		t.Errorf("Set(&ts)\n%s\nwant\n%s", q, want)
	}
	if _, ok := args[0].(time.Time); !ok || len(args) != 3 {
		t.Errorf("args %#v: the time is not passed by value", args)
	}
	q, _, err = db.Query[fNote]().Where(del.EQ(nil), title.In("a", "b")).Columns(id, title).Limit(1).SQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := `SELECT "id", "title" FROM "notes" WHERE "deleted_at" IS NULL AND "title" IN ($1, $2) AND "deleted_at" IS NULL AND "org_id" = $3 LIMIT $4`; q != want {
		t.Errorf("Columns\n%s\nwant\n%s", q, want)
	}
	if err := db.Save(&fNote{ID: 1, Title: "c"}, title).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(e.SQL, `UPDATE "notes" SET "title" = $1 WHERE "id" = $2`) {
		t.Errorf("Save: %s", e.SQL)
	}
	q, _, err = db.Insert(&fNote{ID: 1, Title: "c"}).OnConflict(id).Update(title).SQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q, `ON CONFLICT ("id") DO UPDATE SET "title" = EXCLUDED."title"`) {
		t.Errorf("OnConflict: %s", q)
	}
}

// TestNamedInvalidConditions: a nil pointer in In, IsNull on a column that
// cannot hold NULL, and a condition of the zero Named each fail the
// statement; the zero Named's other uses panic.
func TestNamedInvalidConditions(t *testing.T) {
	ctx := context.Background()
	db, _ := fakeNotes()
	ts := time.Now()
	del := velox.Field[fNote, *time.Time]("deleted_at")
	org := velox.Field[fNote, int64]("org_id")
	var zero velox.Named[fNote, string]
	for name, p := range map[string]velox.Pred[fNote]{
		"In(nil)":        del.In(&ts, nil),
		"IsNull":         org.IsNull(),
		"NotNull":        org.NotNull(),
		"the zero Named": zero.EQ("a"),
	} {
		if _, _, err := db.Query[fNote]().Where(p).Limit(1).SQL(ctx); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	for name, f := range map[string]func(){
		"Set":     func() { zero.Set("a") },
		"Ptr":     func() { zero.Ptr(&fNote{}) },
		"Columns": func() { db.Query[fNote]().Columns(zero) },
	} {
		if p := panicOf(f); !strings.Contains(fmt.Sprint(p), "the zero Named names no column") {
			t.Errorf("%s of the zero Named: panic %v", name, p)
		}
	}
	if q, args, err := db.Query[fNote]().Where(del.In(&ts)).Limit(1).SQL(ctx); err != nil || !strings.Contains(q, `"deleted_at" IN ($1)`) || args[0] != any(ts) {
		t.Errorf("In(&ts): %s %v %v", q, args, err)
	}
}

// TestFieldNotLinkedRetry: a binding that finds the column of an entity
// whose package has not run its init fails as that entity's build does, and
// is not kept: once the entity is linked, the next use builds again and
// succeeds. The layout of the entity whose binding failed was published, so
// its columns can be found meanwhile.
func TestFieldNotLinkedRetry(t *testing.T) {
	err := velox.CheckEntity[fRetry]()
	if err == nil || !strings.Contains(err.Error(), "fUnlinked: not linked") {
		t.Fatalf("before the link: %v", err)
	}
	if err := velox.CheckEntity[fRetry](); err == nil {
		t.Fatal("before the link, again: no error")
	}
	if c := velox.Field[fRetry, int64]("id"); c.Nullable() {
		t.Error("the published layout: id is nullable")
	}
	fUnlinkedLinked = true
	if err := velox.CheckEntity[fRetry](); err != nil {
		t.Fatalf("after the link: %v", err)
	}
	if fRetryBuilds != 3 {
		t.Errorf("fRetry built %d times, want 3", fRetryBuilds)
	}
}

// panicOf returns what f panics with, nil when it does not.
func panicOf(f func()) (p any) {
	defer func() { p = recover() }()
	f()
	return nil
}

// asError is p as an error, nil when it is not one.
func asError(p any) error {
	err, _ := p.(error)
	return err
}

// One-column entities for the failure cases.
type (
	fBadName struct{ ID int64 }
	fBadType struct {
		ID        int64
		DeletedAt *time.Time
	}
	fLate struct {
		ID    int64
		Title string
	}
	fLazy struct {
		ID    int64
		Title string
	}
	fPing     struct{ ID, PongID int64 }
	fPong     struct{ ID, PingID int64 }
	fRetry    struct{ ID int64 }
	fUnlinked struct{ ID int64 }
)

var (
	fBadNameMeta    velox.MetaOnce[fBadName]
	fBadTypeMeta    velox.MetaOnce[fBadType]
	fLateMeta       velox.MetaOnce[fLate]
	fLazyMeta       velox.MetaOnce[fLazy]
	fLazyBuilds     int
	fPingMeta       velox.MetaOnce[fPing]
	fPongMeta       velox.MetaOnce[fPong]
	fRetryMeta      velox.MetaOnce[fRetry]
	fRetryBuilds    int
	fUnlinkedMeta   velox.MetaOnce[fUnlinked]
	fUnlinkedLinked bool
)

func (*fBadName) VeloxMeta() *velox.Meta[fBadName] {
	return fBadNameMeta.Load(func() velox.Descriptor[fBadName] {
		return velox.Descriptor[fBadName]{Table: "bad_names", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0},
			Policy: []func() velox.BoundPolicy{func() velox.BoundPolicy {
				c := velox.Field[fBadName, int64]("nope")
				return velox.BindPolicy(velox.Policy[fBadName]{Read: func(context.Context) (velox.Pred[fBadName], error) { return c.EQ(1), nil }})
			}},
			FieldPtr: func(x *fBadName, i int) any { return &x.ID }}
	})
}

func (*fBadType) VeloxMeta() *velox.Meta[fBadType] {
	return fBadTypeMeta.Load(func() velox.Descriptor[fBadType] {
		return velox.Descriptor[fBadType]{Table: "bad_types", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "DeletedAt", Name: "deleted_at"}},
			PrimaryKey: []int{0},
			Interceptors: []func() velox.BoundInterceptors{func() velox.BoundInterceptors {
				c := velox.Field[fBadType, time.Time]("deleted_at")
				return velox.BindInterceptors([]velox.Interceptor[fBadType]{{Filter: func(context.Context) (velox.Pred[fBadType], error) {
					return c.EQ(time.Time{}), nil
				}}})
			}},
			FieldPtr: func(x *fBadType, i int) any { return [...]any{&x.ID, &x.DeletedAt}[i] }}
	})
}

func (*fLate) VeloxMeta() *velox.Meta[fLate] {
	return fLateMeta.Load(func() velox.Descriptor[fLate] {
		return velox.Descriptor[fLate]{Table: "lates", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Title", Name: "title"}},
			PrimaryKey: []int{0},
			Interceptors: []func() velox.BoundInterceptors{func() velox.BoundInterceptors {
				return velox.BindInterceptors([]velox.Interceptor[fLate]{{Filter: func(context.Context) (velox.Pred[fLate], error) {
					return velox.Field[fLate, string]("title").NEQ("x"), nil
				}}})
			}},
			FieldPtr: func(x *fLate, i int) any { return [...]any{&x.ID, &x.Title}[i] }}
	})
}

func (*fLazy) VeloxMeta() *velox.Meta[fLazy] {
	return fLazyMeta.Load(func() velox.Descriptor[fLazy] {
		fLazyBuilds++
		return velox.Descriptor[fLazy]{Table: "lazies", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Title", Name: "title"}},
			PrimaryKey: []int{0}, FieldPtr: func(x *fLazy, i int) any { return [...]any{&x.ID, &x.Title}[i] }}
	})
}

func (*fPing) VeloxMeta() *velox.Meta[fPing] {
	return fPingMeta.Load(func() velox.Descriptor[fPing] {
		return velox.Descriptor[fPing]{Table: "pings", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "PongID", Name: "pong_id"}},
			PrimaryKey: []int{0},
			Policy: []func() velox.BoundPolicy{func() velox.BoundPolicy {
				velox.Field[fPong, int64]("ping_id")
				return velox.BindPolicy(velox.Policy[fPing]{})
			}},
			FieldPtr: func(x *fPing, i int) any { return [...]any{&x.ID, &x.PongID}[i] }}
	})
}

func (*fPong) VeloxMeta() *velox.Meta[fPong] {
	return fPongMeta.Load(func() velox.Descriptor[fPong] {
		return velox.Descriptor[fPong]{Table: "pongs", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "PingID", Name: "ping_id"}},
			PrimaryKey: []int{0},
			Policy: []func() velox.BoundPolicy{func() velox.BoundPolicy {
				velox.Field[fPing, int64]("pong_id")
				return velox.BindPolicy(velox.Policy[fPong]{})
			}},
			FieldPtr: func(x *fPong, i int) any { return [...]any{&x.ID, &x.PingID}[i] }}
	})
}

func (*fRetry) VeloxMeta() *velox.Meta[fRetry] {
	return fRetryMeta.Load(func() velox.Descriptor[fRetry] {
		fRetryBuilds++
		return velox.Descriptor[fRetry]{Table: "retries", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0},
			Policy: []func() velox.BoundPolicy{func() velox.BoundPolicy {
				velox.Field[fRetry, int64]("id")
				velox.Field[fUnlinked, int64]("id")
				return velox.BindPolicy(velox.Policy[fRetry]{})
			}},
			FieldPtr: func(x *fRetry, i int) any { return &x.ID }}
	})
}

func (*fUnlinked) VeloxMeta() *velox.Meta[fUnlinked] {
	return fUnlinkedMeta.Load(func() velox.Descriptor[fUnlinked] {
		if !fUnlinkedLinked {
			panic(velox.NotLinkedError("fUnlinked: not linked: import the generated package"))
		}
		return velox.Descriptor[fUnlinked]{Table: "unlinked", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0},
			FieldPtr: func(x *fUnlinked, i int) any { return &x.ID }}
	})
}
