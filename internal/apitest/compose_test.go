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
	"github.com/syssam/velox/scope"
)

// cRow is the row of the compose entities: c followed by three bits, whether
// it has a scope, a policy and hooks. Their descriptors are what velox-gen
// writes for each combination: one line for each part, and velox composes them.
type cRow struct {
	ID, OrgID int64
	Name      string
}

type (
	c000 cRow
	c001 cRow
	c010 cRow
	c011 cRow
	c100 cRow
	c101 cRow
	c110 cRow
	c111 cRow
)

var (
	c000Meta velox.MetaOnce[c000]
	c001Meta velox.MetaOnce[c001]
	c010Meta velox.MetaOnce[c010]
	c011Meta velox.MetaOnce[c011]
	c100Meta velox.MetaOnce[c100]
	c101Meta velox.MetaOnce[c101]
	c110Meta velox.MetaOnce[c110]
	c111Meta velox.MetaOnce[c111]
)

func (*c000) VeloxMeta() *velox.Meta[c000] {
	return c000Meta.Load(func() velox.Descriptor[c000] { return composeDesc("c000", rowOf[c000]) })
}

func (*c001) VeloxMeta() *velox.Meta[c001] {
	return c001Meta.Load(func() velox.Descriptor[c001] { return composeDesc("c001", rowOf[c001]) })
}

func (*c010) VeloxMeta() *velox.Meta[c010] {
	return c010Meta.Load(func() velox.Descriptor[c010] { return composeDesc("c010", rowOf[c010]) })
}

func (*c011) VeloxMeta() *velox.Meta[c011] {
	return c011Meta.Load(func() velox.Descriptor[c011] { return composeDesc("c011", rowOf[c011]) })
}

func (*c100) VeloxMeta() *velox.Meta[c100] {
	return c100Meta.Load(func() velox.Descriptor[c100] { return composeDesc("c100", rowOf[c100]) })
}

func (*c101) VeloxMeta() *velox.Meta[c101] {
	return c101Meta.Load(func() velox.Descriptor[c101] { return composeDesc("c101", rowOf[c101]) })
}

func (*c110) VeloxMeta() *velox.Meta[c110] {
	return c110Meta.Load(func() velox.Descriptor[c110] { return composeDesc("c110", rowOf[c110]) })
}

func (*c111) VeloxMeta() *velox.Meta[c111] {
	return c111Meta.Load(func() velox.Descriptor[c111] { return composeDesc("c111", rowOf[c111]) })
}

// rowOf is x as its cRow.
func rowOf[E any](x *E) *cRow { return any(x).(interface{ row() *cRow }).row() }

func (x *c000) row() *cRow { return (*cRow)(x) }
func (x *c001) row() *cRow { return (*cRow)(x) }
func (x *c010) row() *cRow { return (*cRow)(x) }
func (x *c011) row() *cRow { return (*cRow)(x) }
func (x *c100) row() *cRow { return (*cRow)(x) }
func (x *c101) row() *cRow { return (*cRow)(x) }
func (x *c110) row() *cRow { return (*cRow)(x) }
func (x *c111) row() *cRow { return (*cRow)(x) }

var composeOrg = scope.NewKey[int64]("org")

// composeTrace is what the compose entities' hooks saw, in order.
var composeTrace []string

// composeDesc is the descriptor of the compose entity named table, whose bits
// say which of a scope, a policy and hooks it has. The policy hides the name
// "hidden" and refuses to insert "banned"; the hooks, two, record the OrgID
// they see.
func composeDesc[E any](table string, row func(*E) *cRow) velox.Descriptor[E] {
	d := velox.Descriptor[E]{
		Table: table,
		Columns: []velox.ColumnDescriptor{
			{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id", Immutable: true}, {Field: "Name", Name: "name"},
		},
		PrimaryKey: []int{0},
		FieldPtr: func(x *E, i int) any {
			r := row(x)
			return [...]any{&r.ID, &r.OrgID, &r.Name}[i]
		},
	}
	var policies []func() velox.Policy
	var hooks []func() []velox.Hook
	if table[1] == '1' { // a tenancy mixin's, first
		policies = append(policies, func() velox.Policy { return fixture.KeyPolicy(composeOrg, "org_id") })
		hooks = append(hooks, func() []velox.Hook { return fixture.KeyHooks(composeOrg, "org_id") })
	}
	if table[2] == '1' {
		policies = append(policies, func() velox.Policy {
			return velox.Policy{
				Read: func(context.Context) (velox.Predicate, error) { return velox.FieldNEQ("name", "hidden"), nil },
				Insert: func(_ context.Context, r velox.Row) error {
					if r.Value("name") == "banned" {
						return errors.New("banned")
					}
					return nil
				},
			}
		})
	}
	if table[3] == '1' {
		hook := func(label string) velox.Hook {
			return func(next velox.Mutator) velox.Mutator {
				return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
					for r := range m.Rows() {
						composeTrace = append(composeTrace, fmt.Sprintf("%s org=%d", label, row(r.Entity().(*E)).OrgID))
					}
					return next.Mutate(ctx, m)
				})
			}
		}
		hooks = append(hooks, func() []velox.Hook { return []velox.Hook{hook("h1"), hook("h2")} })
	}
	if len(policies)+len(hooks) > 0 {
		d.Schema = fixture.Fns{Policy: policies, Hooks: hooks}
	}
	return d
}

// TestComposeScopePolicyHooks: for each combination of a scope, bound as a
// tenancy mixin's policy and hook are, first, a policy and hooks, the policies
// are ANDed, the scope's first, and the hooks are the scope's Fill, then the
// entity's, in order.
func TestComposeScopePolicyHooks(t *testing.T) {
	type ops struct {
		query   func(db *velox.DB, ctx context.Context) error
		insert  func(db *velox.DB, ctx context.Context, name string) (int64, error)
		checked func() error
	}
	cases := map[string]ops{
		"c000": composeOps[c000](), "c001": composeOps[c001](), "c010": composeOps[c010](), "c011": composeOps[c011](),
		"c100": composeOps[c100](), "c101": composeOps[c101](), "c110": composeOps[c110](), "c111": composeOps[c111](),
	}
	inOrg := composeOrg.With(context.Background(), 7)
	for name, o := range cases {
		scoped, policy, hooks := name[1] == '1', name[2] == '1', name[3] == '1'
		t.Run(name, func(t *testing.T) {
			if err := o.checked(); err != nil {
				t.Fatalf("CheckEntity: %v", err)
			}
			// A query finds no row; an insert returns the key 5.
			e := &fakeconn.Exec{Rows: [][]any{{int64(5)}}, Answer: func(sql string, _ []any) ([][]any, bool) {
				return nil, strings.HasPrefix(sql, "SELECT")
			}}
			db := openPolicied(velox.Postgres, e)

			// The Read condition: the scope's, then the entity's.
			if err := o.query(db, inOrg); err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(e.SQL, `"org_id" = $`); got != scoped {
				t.Errorf("the scope's condition is in %q: %v, want %v", e.SQL, got, scoped)
			}
			if got := strings.Contains(e.SQL, `"name" <> $`); got != policy {
				t.Errorf("the policy's condition is in %q: %v, want %v", e.SQL, got, policy)
			}
			if scoped && policy && strings.Index(e.SQL, `"org_id" = $`) > strings.Index(e.SQL, `"name" <> $`) {
				t.Errorf("the scope's condition is not first: %q", e.SQL)
			}
			err := o.query(db, context.Background())
			if got := errors.Is(err, scope.ErrNoScope); got != scoped {
				t.Errorf("a query without a scope value: %v", err)
			}

			// The hooks: the scope's Fill runs first, so the entity's see the
			// filled OrgID, in their order.
			composeTrace = nil
			org, err := o.insert(db, inOrg, "x")
			if err != nil {
				t.Fatal(err)
			}
			wantOrg := int64(0)
			if scoped {
				wantOrg = 7
			}
			if org != wantOrg {
				t.Errorf("OrgID %d after the insert, want %d", org, wantOrg)
			}
			var want []string
			if hooks {
				want = []string{fmt.Sprintf("h1 org=%d", wantOrg), fmt.Sprintf("h2 org=%d", wantOrg)}
			}
			if strings.Join(composeTrace, "|") != strings.Join(want, "|") {
				t.Errorf("trace %q, want %q", composeTrace, want)
			}

			// The Insert check: the scope's, then the entity's.
			if _, err := o.insert(db, inOrg, "banned"); (err != nil) != policy {
				t.Errorf("inserting banned: %v", err)
			}
			_, err = o.insert(db, context.Background(), "x")
			if got := errors.Is(err, scope.ErrNoScope); got != scoped {
				t.Errorf("an insert without a scope value: %v", err)
			}
		})
	}
}

// composeOps runs compose entity E's statements, as TestComposeScopePolicyHooks
// needs them.
func composeOps[E any, PE velox.Entity[E]]() (o struct {
	query   func(db *velox.DB, ctx context.Context) error
	insert  func(db *velox.DB, ctx context.Context, name string) (int64, error)
	checked func() error
}) {
	o.query = func(db *velox.DB, ctx context.Context) error {
		_, err := db.Query[E, PE]().Limit(1).All(ctx)
		return err
	}
	o.insert = func(db *velox.DB, ctx context.Context, name string) (int64, error) {
		x := PE(new(E))
		rowOf(x).Name = name
		err := db.Insert(x).Exec(ctx)
		return rowOf(x).OrgID, err
	}
	o.checked = velox.CheckEntity[E, PE]
	return o
}
