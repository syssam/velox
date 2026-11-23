package apitest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/untyped"
)

// lAnd lists two policies, two hook lists and two interceptor lists, as a
// descriptor whose behaviour comes from mixins and the entity does.
type lAnd struct {
	ID   int64
	Name string
}

var (
	lAndMeta  velox.MetaOnce[lAnd]
	lAndName  = velox.StringCol[lAnd, string]{I: 1}
	lAndTrace []string
)

type lModeKey struct{}

// lMode returns ctx under which lAnd's first policy's Read does m: "err"
// denies, "allow" returns Allow.
func lMode(ctx context.Context, m string) context.Context {
	return context.WithValue(ctx, lModeKey{}, m)
}

func (*lAnd) VeloxMeta() *velox.Meta[lAnd] {
	return lAndMeta.Load(func() velox.Descriptor[lAnd] {
		policy := func(label string, insert string) func() velox.Policy {
			return func() velox.Policy {
				return velox.Policy{
					Read: func(ctx context.Context, _ *velox.DB) (velox.Predicate, error) {
						lAndTrace = append(lAndTrace, "read "+label)
						if label == "a" {
							switch ctx.Value(lModeKey{}) {
							case "err":
								return velox.Predicate{}, velox.Deny("a says no")
							case "allow":
								return velox.Allow(), nil
							}
						}
						return velox.FieldNEQ("name", label), nil
					},
					Insert: func(_ context.Context, _ *velox.DB, r velox.Row) error {
						lAndTrace = append(lAndTrace, "insert "+label)
						if r.Value("name") == insert {
							return velox.Deny(label + " refuses " + insert)
						}
						return nil
					},
				}
			}
		}
		hook := func(label string) velox.Hook {
			return func(next velox.Mutator) velox.Mutator {
				return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
					lAndTrace = append(lAndTrace, label)
					return next.Mutate(ctx, m)
				})
			}
		}
		filter := func(label string) func() []velox.Interceptor {
			return func() []velox.Interceptor {
				return []velox.Interceptor{{Filter: func(context.Context, *velox.DB) (velox.Predicate, error) {
					return velox.FieldNEQ("name", label), nil
				}}}
			}
		}
		return velox.Descriptor[lAnd]{
			Table:      "ands",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"}},
			PrimaryKey: []int{0},
			Schema: fixture.Fns{Policy: []func() velox.Policy{policy("a", "ia"), policy("b", "ib")}, Hooks: []func() []velox.Hook{
				func() []velox.Hook { return []velox.Hook{hook("h1"), hook("h2")} },
				func() []velox.Hook { return []velox.Hook{hook("h3")} },
			}, Interceptors: []func() []velox.Interceptor{filter("f1"), filter("f2")}},

			FieldPtr: func(x *lAnd, i int) any { return [...]any{&x.ID, &x.Name}[i] },
		}
	})
}

// TestPoliciesANDedInOrder: the listed policies, a mixin's first, are ANDed
// in order, after the interceptors in theirs; an Allow does not lift the
// next policy; the first denial ends the evaluation; and every Insert runs,
// in order, until one refuses.
func TestPoliciesANDedInOrder(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(1)}}, Answer: func(sql string, _ []any) ([][]any, bool) {
		return nil, strings.HasPrefix(sql, "SELECT")
	}}
	db := openPolicied(velox.Postgres, e)
	ctx := context.Background()

	lAndTrace = nil
	if _, err := db.Query[lAnd]().Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `SELECT "id", "name" FROM "ands" WHERE "name" <> $1 AND "name" <> $2 AND "name" <> $3 AND "name" <> $4 LIMIT 1`; e.SQL != want {
		t.Errorf("SQL\n%s\nwant\n%s", e.SQL, want)
	}
	if want := "[f1 f2 a b]"; fmt.Sprint(e.Args) != want {
		t.Errorf("args %v, want %v", e.Args, want)
	}

	// An Allow from the first policy is no restriction of its own: the second
	// still applies.
	if _, err := db.Query[lAnd]().Limit(1).All(lMode(ctx, "allow")); err != nil {
		t.Fatal(err)
	}
	if want := "[f1 f2 b]"; fmt.Sprint(e.Args) != want {
		t.Errorf("args under the first policy's Allow %v, want %v", e.Args, want)
	}

	// The first denial ends it: the second policy's Read is not called.
	lAndTrace = nil
	calls := e.Calls
	_, err := db.Query[lAnd]().Limit(1).All(lMode(ctx, "err"))
	if !errors.Is(err, velox.ErrDenied) || !strings.Contains(err.Error(), "a says no") {
		t.Errorf("a denial: %v", err)
	}
	if strings.Join(lAndTrace, "|") != "read a" || e.Calls != calls {
		t.Errorf("after a denial: trace %q, %d statements sent", lAndTrace, e.Calls-calls)
	}

	// Insert: each policy checks the row in order; the first refusal ends it.
	lAndTrace = nil
	if err := db.Insert(&lAnd{Name: "ia"}).Exec(ctx); !errors.Is(err, velox.ErrDenied) || !strings.Contains(err.Error(), "a refuses ia") {
		t.Errorf("insert ia: %v", err)
	}
	if got := strings.Join(lAndTrace, "|"); got != "h1|h2|h3|insert a" {
		t.Errorf("insert ia: trace %q", got)
	}
	lAndTrace = nil
	if err := db.Insert(&lAnd{Name: "ib"}).Exec(ctx); !errors.Is(err, velox.ErrDenied) || !strings.Contains(err.Error(), "b refuses ib") {
		t.Errorf("insert ib: %v", err)
	}
	if got := strings.Join(lAndTrace, "|"); got != "h1|h2|h3|insert a|insert b" {
		t.Errorf("insert ib: trace %q", got)
	}
}

// TestHookOrderMixinsFirst: the listed hook sources run in the order
// velox-gen lists them, a mixin's (h1, h2) before the entity's (h3), the
// first hook of the first source outermost, on every kind of write.
func TestHookOrderMixinsFirst(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(1)}}, Affected: 1}
	db := openFake(velox.Postgres, e)
	ctx := context.Background()
	for name, write := range map[string]func() error{
		"insert": func() error { return db.Insert(&lAnd{Name: "x"}).Exec(ctx) },
		"update": func() error { _, err := db.Update[lAnd]().Set(lAndName.Set("y")).All().Exec(ctx); return err },
		"delete": func() error { _, err := db.Delete[lAnd]().All().Exec(ctx); return err },
	} {
		lAndTrace = nil
		if err := write(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := strings.Join(lAndTrace, "|"); got != "h1|h2|h3" {
			t.Errorf("%s: trace %q, want h1|h2|h3", name, got)
		}
	}
}

// lBad is an entity whose descriptor lBadDesc sets, and whose metadata
// lBadMeta holds: a test sets both.
type lBad struct{ ID, B int64 }

var (
	lBadDesc func(d *velox.Descriptor[lBad])
	lBadMeta *velox.MetaOnce[lBad]
)

func (*lBad) VeloxMeta() *velox.Meta[lBad] {
	return lBadMeta.Load(func() velox.Descriptor[lBad] {
		d := velox.Descriptor[lBad]{Table: "bad", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}},
			PrimaryKey: []int{0}, FieldPtr: func(x *lBad, i int) any { return [...]any{&x.ID, &x.B}[i] }}
		lBadDesc(&d)
		return d
	})
}

// panicMixin is a Descriptor.Schema whose Mixin method panics.
type panicMixin struct{}

func (panicMixin) Mixin() []schema.Mixin { panic("boom") }

// lBadErr is lBad's metadata error with the descriptor set by f.
func lBadErr(f func(d *velox.Descriptor[lBad])) error {
	lBadDesc, lBadMeta = f, new(velox.MetaOnce[lBad])
	return velox.CheckEntity[lBad]()
}

// TestNilInterceptorRefused: an Interceptor with a nil Filter, by its index
// across the lists, and a nil function in a Descriptor's list are refused
// when the metadata is built, with a *SchemaError.
func TestNilInterceptorRefused(t *testing.T) {
	allow := velox.Interceptor{Filter: func(context.Context, *velox.DB) (velox.Predicate, error) { return velox.Allow(), nil }}
	bind := func(is ...velox.Interceptor) func() []velox.Interceptor {
		return func() []velox.Interceptor { return is }
	}
	nilHook := func() []velox.Hook { return []velox.Hook{nil} }
	someHook := func() []velox.Hook {
		return []velox.Hook{func(next velox.Mutator) velox.Mutator { return next }}
	}
	for _, c := range []struct {
		name string
		set  func(d *velox.Descriptor[lBad])
		want string
	}{
		{"a nil Filter", func(d *velox.Descriptor[lBad]) {
			d.Schema = fixture.Fns{Interceptors: []func() []velox.Interceptor{bind(allow), bind(allow, velox.Interceptor{})}}
		}, "velox: apitest.lBad: Interceptors()[2] has a nil Filter"},
		{"a nil hook", func(d *velox.Descriptor[lBad]) {
			d.Schema = fixture.Fns{Hooks: []func() []velox.Hook{someHook, nilHook}}
		}, "velox: apitest.lBad: Hooks()[1] is nil"},
		{"a panic in Mixin", func(d *velox.Descriptor[lBad]) {
			d.Schema = panicMixin{}
		}, "velox: apitest.lBad: Mixin() panicked: boom"},
		{"a panic", func(d *velox.Descriptor[lBad]) {
			d.Schema = fixture.Fns{Interceptors: []func() []velox.Interceptor{func() []velox.Interceptor { panic("boom") }}}
		}, "velox: apitest.lBad: Interceptors() panicked: boom"},
		{"on a Bidi entity", func(d *velox.Descriptor[lBad]) {
			d.Bidi = true
			d.Columns = []velox.ColumnDescriptor{{Field: "ID", Name: "a"}, {Field: "B", Name: "b"}}
			d.PrimaryKey = []int{0, 1}
			d.Schema = fixture.Fns{Interceptors: []func() []velox.Interceptor{bind(allow)}}
		}, "cannot have interceptors"},
	} {
		err := lBadErr(c.set)
		if _, ok := errors.AsType[*velox.SchemaError](err); !ok || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %s", c.name, err, c.want)
		}
	}
}

// lJoinOwner has a many-to-many relation through lJoin, which has an
// interceptor.
type (
	lJoinOwner struct {
		ID    int64
		Peers velox.Many[lJoinOwner]
	}
	lJoin struct{ A, B int64 }
)

var (
	lJoinOwnerMeta velox.MetaOnce[lJoinOwner]
	lJoinMeta      velox.MetaOnce[lJoin]
	lJoinPeers     = velox.Rel[lJoinOwner, lJoinOwner]{I: 0}
)

func (*lJoinOwner) VeloxMeta() *velox.Meta[lJoinOwner] {
	return lJoinOwnerMeta.Load(func() velox.Descriptor[lJoinOwner] {
		return velox.Descriptor[lJoinOwner]{
			Table: "join_owners", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0},
			Edges:        []velox.EdgeDescriptor{velox.EdgeThrough[lJoin, lJoinOwner]("peers", 0, 0, 1, 0)},
			FieldPtr:     func(x *lJoinOwner, i int) any { return &x.ID },
			EdgeFieldPtr: func(x *lJoinOwner, i int) any { return &x.Peers },
		}
	})
}

func (*lJoin) VeloxMeta() *velox.Meta[lJoin] {
	return lJoinMeta.Load(func() velox.Descriptor[lJoin] {
		return velox.Descriptor[lJoin]{
			Table: "joins", Columns: []velox.ColumnDescriptor{{Field: "A", Name: "a"}, {Field: "B", Name: "b"}}, PrimaryKey: []int{0, 1},
			Schema: fixture.Fns{Interceptors: []func() []velox.Interceptor{func() []velox.Interceptor {
				return []velox.Interceptor{{Filter: func(context.Context, *velox.DB) (velox.Predicate, error) {
					return velox.Allow(), nil
				}}}
			}}},
			FieldPtr: func(x *lJoin, i int) any { return [...]any{&x.A, &x.B}[i] },
		}
	})
}

// TestInterceptorOnJoinEntityRefused: a join entity is read through its two
// sides, so the relation through one with an interceptor is refused when
// it resolves.
func TestInterceptorOnJoinEntityRefused(t *testing.T) {
	err := velox.CheckRelation(lJoinPeers)
	if err == nil || !strings.Contains(err.Error(), "its join entity apitest.lJoin has interceptors") {
		t.Errorf("CheckRelation: %v", err)
	}
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	_, err = db.Query[lJoinOwner]().Where(lJoinPeers.Has()).Limit(1).All(context.Background())
	if err == nil || !strings.Contains(err.Error(), "has interceptors") {
		t.Errorf("Has: %v", err)
	}
}

// lUnbound has an Interceptors method that its descriptor does not bind.
type lUnbound struct{ ID int64 }

var lUnboundMeta velox.MetaOnce[lUnbound]

func (*lUnbound) VeloxMeta() *velox.Meta[lUnbound] {
	return lUnboundMeta.Load(func() velox.Descriptor[lUnbound] {
		return velox.Descriptor[lUnbound]{Table: "unbound", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}},
			PrimaryKey: []int{0}, FieldPtr: func(x *lUnbound, i int) any { return &x.ID }}
	})
}

// Interceptors is never bound.
func (*lUnbound) Interceptors() []velox.Interceptor { return nil }

// TestUnboundInterceptorsMethodRefused: an Interceptors method the metadata
// does not bind is refused at the entity's first use, rather than silently
// never called.
func TestUnboundInterceptorsMethodRefused(t *testing.T) {
	err := velox.CheckEntity[lUnbound]()
	if _, ok := errors.AsType[*velox.SchemaError](err); !ok || !strings.Contains(err.Error(), "has an Interceptors method, but the metadata does not bind it") {
		t.Errorf("CheckEntity: %v", err)
	}
}

// TestMetaHasRowFilter: an interceptor alone makes HasRowFilter true, as a
// Read policy does.
func TestMetaHasRowFilter(t *testing.T) {
	if !(*lAnd)(nil).VeloxMeta().HasRowFilter() {
		t.Error("lAnd: no row filter")
	}
	if (*lUnbound)(nil).VeloxMeta().HasRowFilter() {
		t.Error("lUnbound, whose metadata failed: a row filter")
	}
	if !untyped.TableOf[lJoin]().HasRowFilter() {
		t.Error("lJoin, with an interceptor only: no row filter")
	}
}

// lCycA's interceptor holds a Has on lCycB, whose interceptor holds a Has on
// lCycA.
type (
	lCycA struct {
		ID, BID int64
		B       velox.One[lCycB]
	}
	lCycB struct {
		ID, AID int64
		A       velox.One[lCycA]
	}
)

var (
	lCycAMeta velox.MetaOnce[lCycA]
	lCycBMeta velox.MetaOnce[lCycB]
)

func (*lCycA) VeloxMeta() *velox.Meta[lCycA] {
	return lCycAMeta.Load(func() velox.Descriptor[lCycA] {
		return velox.Descriptor[lCycA]{
			Table: "cyc_as", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "BID", Name: "b_id"}}, PrimaryKey: []int{0},
			Entities: fixture.Entities(velox.LinkEntity[lCycB]("lCycB", "")),
			Edges:    []velox.EdgeDescriptor{velox.EdgeTo[lCycB]("b", 1, 0)},
			Schema: fixture.Fns{Interceptors: []func() []velox.Interceptor{func() []velox.Interceptor {
				return []velox.Interceptor{{Filter: func(context.Context, *velox.DB) (velox.Predicate, error) { return velox.HasEdge("b"), nil }}}
			}}},
			FieldPtr:     func(x *lCycA, i int) any { return [...]any{&x.ID, &x.BID}[i] },
			EdgeFieldPtr: func(x *lCycA, i int) any { return &x.B },
		}
	})
}

func (*lCycB) VeloxMeta() *velox.Meta[lCycB] {
	return lCycBMeta.Load(func() velox.Descriptor[lCycB] {
		return velox.Descriptor[lCycB]{
			Table: "cyc_bs", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "AID", Name: "a_id"}}, PrimaryKey: []int{0},
			Entities: fixture.Entities(velox.LinkEntity[lCycA]("lCycA", "")),
			Edges:    []velox.EdgeDescriptor{velox.EdgeTo[lCycA]("a", 1, 0)},
			Schema: fixture.Fns{Interceptors: []func() []velox.Interceptor{func() []velox.Interceptor {
				return []velox.Interceptor{{Filter: func(context.Context, *velox.DB) (velox.Predicate, error) { return velox.HasEdge("a"), nil }}}
			}}},
			FieldPtr:     func(x *lCycB, i int) any { return [...]any{&x.ID, &x.AID}[i] },
			EdgeFieldPtr: func(x *lCycB, i int) any { return &x.A },
		}
	})
}

// TestInterceptorCycleIsCut: an interceptor whose Has reaches its own
// entity's row condition again is cut there, under WithoutPolicy too
// (ADR 0091): the inner EXISTS on the entity gets none of its conditions,
// interceptors included.
func TestInterceptorCycleIsCut(t *testing.T) {
	e := &fakeconn.Exec{}
	for _, db := range []*velox.DB{openPolicied(velox.Postgres, e), openFake(velox.Postgres, e)} {
		got, _, err := db.Query[lCycA]().Limit(1).SQL(context.Background())
		const want = `SELECT "id", "b_id" FROM "cyc_as" WHERE EXISTS (SELECT 1 FROM "cyc_bs" AS "vx1" WHERE "vx1"."id" = "cyc_as"."b_id" AND EXISTS (SELECT 1 FROM "cyc_as" AS "vx2" WHERE "vx2"."id" = "vx1"."a_id")) LIMIT 1`
		if err != nil || got != want {
			t.Errorf("got  %s, %v\nwant %s", got, err, want)
		}
	}
}
