package velox

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

type nullTenant struct {
	ID    int64
	OrgID *int64
}

func (x *nullTenant) Schema(s *Schema) { s.Tenant(&x.OrgID) }

type sliceTenant struct {
	ID  int64
	Org []byte
}

func (x *sliceTenant) Schema(s *Schema) { s.Tenant(&x.Org) }

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
	_, err := infoOf[nullTenant]([]string{"ID", "OrgID"}, nil)
	schemaErr(t, err, "velox: velox.nullTenant.OrgID: the tenant column must be NOT NULL and comparable")
	_, err = infoOf[sliceTenant]([]string{"ID", "Org"}, nil)
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

// tagMix is embedded after ID, so that its hook needs the field's offset.
type tagMix struct{ Tag string }

func (*tagMix) Mixin(s *Schema) {
	s.BeforeInsert(func(_ context.Context, x *tagMix) error {
		x.Tag += "mixin;"
		return nil
	})
}

type hooked struct {
	ID int64
	tagMix
	Title string
}

var (
	hookedTag   = Col[hooked, string]{I: 1}
	hookedTitle = Col[hooked, string]{I: 2}
)

func (x *hooked) Schema(s *Schema) {
	s.BeforeInsert(func(_ context.Context, x *hooked) error {
		x.Tag += "entity;"
		return nil
	})
	s.Policy(Policy[hooked]{
		Read: func(context.Context) (Pred[hooked], error) { return hookedTitle.EQ("r"), nil },
		Insert: func(_ context.Context, x *hooked) error {
			if x.Title == "" {
				return Deny("no title")
			}
			return nil
		},
	})
	s.BeforeUpdate(func(_ context.Context, m *Mutation[hooked]) error {
		m.Set(hookedTag.Set("updated"))
		return nil
	})
	s.BeforeDelete(func(_ context.Context, m *Mutation[hooked]) error {
		m.Set(hookedTag.Set("x"))
		return nil
	})
}

func TestSchemaPolicyHooks(t *testing.T) {
	info, err := infoOf[hooked]([]string{"ID", "Tag", "Title"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if len(info.beforeInsert) != 2 {
		t.Fatalf("%d BeforeInsert hooks, want 2", len(info.beforeInsert))
	}
	var x hooked
	if err := info.beforeInsert[0](ctx, unsafe.Pointer(&x)); err != nil || x.Tag != "mixin;" {
		t.Fatalf("the mixin's hook, run first: Tag %q, err %v", x.Tag, err)
	}
	if err := info.beforeInsert[1](ctx, unsafe.Pointer(&x)); err != nil || x.Tag != "mixin;entity;" {
		t.Fatalf("the entity's hook, run second: Tag %q, err %v", x.Tag, err)
	}

	p := info.policy
	if p == nil || p.read == nil || p.write != nil || p.insert == nil {
		t.Fatalf("policy %+v", p)
	}
	if pr, err := p.read(ctx); err != nil || pr.n.op != opEQ || pr.n.col != 2 || pr.n.v != "r" {
		t.Errorf("read: %+v, %v", pr, err)
	}
	if err := p.insert(ctx, unsafe.Pointer(&x)); !errors.Is(err, ErrDenied) {
		t.Errorf("insert of an untitled row: %v", err)
	}
	x.Title = "t"
	if err := p.insert(ctx, unsafe.Pointer(&x)); err != nil {
		t.Errorf("insert of a titled row: %v", err)
	}

	st := Statement{Op: OpUpdate, Entity: info, Set: []Assignment{{Column: 2, Op: AssignSet, Value: "t"}}}
	if len(info.beforeUpdate) != 1 || info.beforeUpdate[0](ctx, &st) != nil {
		t.Fatalf("BeforeUpdate hooks %d", len(info.beforeUpdate))
	}
	if want := []Assignment{{2, AssignSet, "t"}, {1, AssignSet, "updated"}}; !reflect.DeepEqual(st.Set, want) {
		t.Errorf("set %+v, want %+v", st.Set, want)
	}
	// Set on a delete is the hook's error, though the hook returned nil.
	del := Statement{Op: OpDelete, Entity: info}
	if err := info.beforeDelete[0](ctx, &del); err == nil || del.Set != nil {
		t.Errorf("Set on a delete: %v, set %+v", err, del.Set)
	}
}

func TestMutation(t *testing.T) {
	info, err := infoOf[hooked]([]string{"ID", "Tag", "Title"}, nil)
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

// A tenant column that a default or OnUpdate would fill could put a row in a
// tenant the context never named.
type defaultTenant struct {
	ID  int64
	Org string
}

func (x *defaultTenant) Schema(s *Schema) {
	s.Tenant(&x.Org)
	s.Column(&x.Org).Default("acme")
}

type defaultFuncTenant struct {
	ID    int64
	OrgID int64
}

func (x *defaultFuncTenant) Schema(s *Schema) {
	s.Tenant(&x.OrgID)
	s.Column(&x.OrgID).DefaultFunc(func() int64 { return 1 })
}

type onUpdateTenant struct {
	ID    int64
	OrgID int64
}

func (x *onUpdateTenant) Schema(s *Schema) {
	s.Tenant(&x.OrgID)
	s.Column(&x.OrgID).OnUpdate(func() int64 { return 1 })
}

func TestSchemaPolicyTenantColumnIsNotFilled(t *testing.T) {
	_, err := infoOf[defaultTenant]([]string{"ID", "Org"}, nil)
	schemaErr(t, err, "velox: velox.defaultTenant.Org: the tenant column cannot have Default, DefaultFunc or OnUpdate")
	_, err = infoOf[defaultFuncTenant]([]string{"ID", "OrgID"}, nil)
	schemaErr(t, err, "velox: velox.defaultFuncTenant.OrgID: the tenant column cannot have Default, DefaultFunc or OnUpdate")
	_, err = infoOf[onUpdateTenant]([]string{"ID", "OrgID"}, nil)
	schemaErr(t, err, "velox: velox.onUpdateTenant.OrgID: the tenant column cannot have Default, DefaultFunc or OnUpdate")
}
