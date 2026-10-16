package velox

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

type policyTwice struct{ ID int64 }

func (*policyTwice) Schema(s *Schema) {
	s.Policy(Policy[policyTwice]{})
	s.Policy(Policy[policyTwice]{})
}

// policyMix declares what a mixin may not: a policy and update and delete hooks.
type policyMix struct{ Note string }

func (*policyMix) Mixin(s *Schema) {
	s.Policy(Policy[policyMix]{})
	s.BeforeUpdate(func(context.Context, *Mutation[policyMix]) error { return nil })
	s.BeforeDelete(func(context.Context, *Mutation[policyMix]) error { return nil })
}

type mixesPolicy struct {
	ID int64
	policyMix
}

func (*mixesPolicy) Schema(*Schema) {}

// strayMix's hook is typed on a struct other than itself.
type strayMix struct{ Note string }

func (*strayMix) Mixin(s *Schema) {
	s.BeforeInsert(func(context.Context, *other) error { return nil })
}

type strayHooks struct {
	ID int64
	strayMix
}

func (*strayHooks) Schema(s *Schema) {
	s.BeforeInsert(func(context.Context, *other) error { return nil })
	s.BeforeUpdate(func(context.Context, *Mutation[other]) error { return nil })
	s.Policy(Policy[other]{})
}

func schemaErr(t *testing.T, err error, parts ...string) {
	t.Helper()
	var se *SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("got %v, want a *SchemaError", err)
	}
	for _, p := range parts {
		if !strings.Contains(err.Error(), p) {
			t.Errorf("%q does not mention %q", err, p)
		}
	}
}

func TestSchemaPolicyTenantColumn(t *testing.T) {
	_, err := loadDesc(nullTenantDesc())
	schemaErr(t, err, "velox: velox.nullTenant.OrgID: the tenant column must be NOT NULL and comparable")
	_, err = loadDesc(sliceTenantDesc())
	schemaErr(t, err, "velox: velox.sliceTenant.Org: the tenant column must be NOT NULL and comparable")
}

func TestSchemaPolicyDeclaredOnce(t *testing.T) {
	_, err := infoOf[policyTwice]([]string{"ID"}, nil)
	schemaErr(t, err, "Policy is declared twice")
}

func TestSchemaPolicyNotInAMixin(t *testing.T) {
	_, err := infoOf[mixesPolicy]([]string{"ID", "Note"}, nil)
	schemaErr(t, err,
		"Policy is declared in mixin velox.policyMix",
		"BeforeUpdate is declared in mixin velox.policyMix",
		"BeforeDelete is declared in mixin velox.policyMix",
	)
}

func TestSchemaPolicyHookOnAnotherStruct(t *testing.T) {
	_, err := infoOf[strayHooks]([]string{"ID", "Note"}, nil)
	schemaErr(t, err,
		"BeforeInsert in mixin velox.strayMix is typed on velox.other",
		"BeforeInsert is typed on velox.other, not on velox.strayHooks",
		"BeforeUpdate is typed on velox.other, not on velox.strayHooks",
		"Policy is typed on velox.other, not on velox.strayHooks",
	)
}

// TestSchemaPolicyHooks: an entity's hooks and policy, bound from its Hooks and
// Policy methods, act on its writes.
func TestSchemaPolicyHooks(t *testing.T) {
	info, err := loadDesc(hookedDesc())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if len(info.hooks) != 3 {
		t.Fatalf("%d hooks, want 3", len(info.hooks))
	}
	e := &FakeExec{Rows: [][]any{{int64(1)}}, Affected: 1}
	db := OpenFake(Postgres, e)
	var x hooked
	if err := db.Insert(&x).Exec(ctx); !errors.Is(err, ErrDenied) {
		t.Fatalf("insert of an untitled row: %v", err)
	}
	x = hooked{Title: "t"} // the hook ran on the refused row too
	if err := db.Insert(&x).Exec(ctx); err != nil || x.Tag != "entity;" {
		t.Fatalf("the insert hook, run before the write: Tag %q, err %v", x.Tag, err)
	}

	p := info.policy
	if p == nil || p.read == nil || p.write != nil || p.insert == nil {
		t.Fatalf("policy %+v", p)
	}
	if pr, err := p.read(ctx); err != nil || pr.n.op != opEQ || pr.n.col != 2 || pr.n.v != "r" {
		t.Errorf("read: %+v, %v", pr, err)
	}
	x.Title = ""
	if err := p.insert(ctx, unsafe.Pointer(&x)); !errors.Is(err, ErrDenied) {
		t.Errorf("insert of an untitled row: %v", err)
	}
	x.Title = "t"
	if err := p.insert(ctx, unsafe.Pointer(&x)); err != nil {
		t.Errorf("insert of a titled row: %v", err)
	}

	// The update hook's Set follows the query's own.
	if _, err := db.Update[hooked]().Set(hookedTitle.Set("t")).Where(hookedTitle.EQ("u")).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(e.SQL, `UPDATE "hooked" SET "title" = $1, "tag" = $2 WHERE `) || e.Args[0] != "t" || e.Args[1] != "updated" {
		t.Errorf("update: %s %v", e.SQL, e.Args)
	}
	// Set on a delete is the hook's error, though the hook returned nil.
	calls := e.Calls
	if _, err := db.Delete[hooked]().Where(hookedTitle.EQ("u")).Exec(ctx); err == nil || e.Calls != calls {
		t.Errorf("Set on a delete: %v, %d statements sent", err, e.Calls-calls)
	}
}

func TestMutation(t *testing.T) {
	info, err := loadDesc(hookedDesc())
	if err != nil {
		t.Fatal(err)
	}
	set := []Assignment{{Column: 2, Op: AssignSet, Value: "t"}, {Column: 1, Op: AssignAdd, Value: "z"}}
	st := Statement{Op: OpUpdate, Entity: info, Set: set[:2:2]}
	m := &Mutation[hooked]{st: &st}
	if m.Op() != OpUpdate {
		t.Errorf("Op %v", m.Op())
	}
	if v, ok := m.Value(hookedTitle); !ok || v != "t" {
		t.Errorf("Value(Title) = %v, %v", v, ok)
	}
	if v, ok := m.Value(hookedTag); ok || v != nil {
		t.Errorf("Value of a column assigned by Add = %v, %v; want nothing", v, ok)
	}
	m.Set(hookedTitle.Set("u"), hookedTag.Set("a"))
	if want := []Assignment{{2, AssignSet, "u"}, {1, AssignSet, "a"}}; !reflect.DeepEqual(st.Set, want) {
		t.Errorf("set %+v, want %+v", st.Set, want)
	}
	if set[0].Value != "t" {
		t.Error("Set changed the query's own assignments")
	}
	m.Where(hookedTitle.EQ("u"))
	if m.err != nil || len(st.Where) != 1 {
		t.Fatalf("Where: err %v, %d conditions", m.err, len(st.Where))
	}
	m.Where(Pred[hooked]{})
	if m.err == nil || !strings.Contains(m.err.Error(), "zero Predicate") || len(st.Where) != 1 {
		t.Errorf("a zero condition: err %v, %d conditions", m.err, len(st.Where))
	}
	bad := errors.New("unresolved")
	m = &Mutation[hooked]{st: &st}
	m.Where(Pred[hooked]{node{op: opInvalid, v: bad}})
	if !errors.Is(m.err, bad) || len(st.Where) != 1 {
		t.Errorf("an invalid condition: err %v, %d conditions", m.err, len(st.Where))
	}
	del := Statement{Op: OpDelete, Entity: info}
	m = &Mutation[hooked]{st: &del}
	m.Set(hookedTag.Set("x"))
	if m.err == nil || del.Set != nil {
		t.Errorf("Set on a delete: err %v, set %+v", m.err, del.Set)
	}
}

func TestSchemaPolicyTenantColumnIsNotFilled(t *testing.T) {
	_, err := loadDesc(defaultTenantDesc())
	schemaErr(t, err, "velox: velox.defaultTenant.Org: the tenant column cannot have Default, DefaultFunc or UpdateDefault")
	_, err = loadDesc(defaultFuncTenantDesc())
	schemaErr(t, err, "velox: velox.defaultFuncTenant.OrgID: the tenant column cannot have Default, DefaultFunc or UpdateDefault")
	_, err = loadDesc(onUpdateTenantDesc())
	schemaErr(t, err, "velox: velox.onUpdateTenant.OrgID: the tenant column cannot have Default, DefaultFunc or UpdateDefault")
}
