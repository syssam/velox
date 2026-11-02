// Package scope confines an entity's rows to a value the context carries: a
// tenant, an owner, a region. It is built from Policy and Hooks, with nothing
// in the engine, so a rule of another shape (a soft delete, published rows
// only) is written the same way.
//
// An entity declares the scope in its schema methods:
//
//	var Org = scope.NewKey[int64]("org")
//
//	func (Issue) Policy() velox.Policy[gen.Issue] { return issueScope.Policy() }
//	func (Issue) Hooks() []velox.Hook[gen.Issue]  { return []velox.Hook[gen.Issue]{issueScope.Fill()} }
//
//	var issueScope = scope.By(Org, gen.Issues.OrgID.EQ, func(x *gen.Issue) *int64 { return &x.OrgID })
//
// and a request carries the value:
//
//	ctx = Org.With(ctx, 7)
//
// Reads, updates and deletes then see only rows whose column is 7; an insert
// that leaves the column unset gets 7, and one that names another value is
// refused; a statement whose context has no value fails with [ErrNoScope],
// not with every row. velox.WithoutPolicy lifts the scope, with a reason, as
// it lifts every other policy.
//
// Make the column Immutable in the schema, so that an update cannot move a row
// to another scope.
//
// The scope does not reach rows it does not filter: an entity without it reads
// every row, a foreign key may name a row of another scope, and the rows of a
// bare many-to-many join table have no scope. A composite foreign key,
// (org_id, project_id) to (org_id, id), makes the database refuse the cross
// reference, and an edge schema gives a join table its own Policy.
package scope

import (
	"context"
	"errors"
	"fmt"

	"github.com/syssam/velox"
)

// ErrNoScope is the error of a statement whose context carries no value, or
// the zero value, for the scope: such a statement fails and reads nothing.
var ErrNoScope = errors.New("velox/scope: the context has no scope value")

// ErrMismatch is the error of an insert that names a scope value other than the
// context's.
var ErrMismatch = errors.New("velox/scope: the row is not in the context's scope")

type ctxKey[V comparable] struct{ k *Key[V] }

// Key names a scope and carries its value in a context. Make one with
// [NewKey] for each scope, in a package-level variable.
type Key[V comparable] struct{ name string }

// NewKey returns a key; name appears in errors.
func NewKey[V comparable](name string) *Key[V] { return &Key[V]{name: name} }

// With returns ctx carrying v for the key. A later With replaces it. The zero
// value is no value: statements under it fail with [ErrNoScope].
func (k *Key[V]) With(ctx context.Context, v V) context.Context {
	return context.WithValue(ctx, ctxKey[V]{k}, v)
}

// From returns the key's value in ctx, and false when there is none or it is
// the zero value, which is what parsing a missing claim gives.
func (k *Key[V]) From(ctx context.Context) (V, bool) {
	v, ok := ctx.Value(ctxKey[V]{k}).(V)
	var zero V
	return v, ok && v != zero
}

// Scope is the scope of entity E by one column of type V.
type Scope[E any, V comparable] struct {
	key   *Key[V]
	eq    func(V) velox.Pred[E]
	field func(*E) *V
}

// By returns the scope of E by the column eq compares and field reaches:
//
//	scope.By(Org, gen.Issues.OrgID.EQ, func(x *gen.Issue) *int64 { return &x.OrgID })
func By[E any, V comparable](key *Key[V], eq func(V) velox.Pred[E], field func(*E) *V) Scope[E, V] {
	return Scope[E, V]{key: key, eq: eq, field: field}
}

// Policy returns the policy that confines E's reads, updates and deletes to
// the context's value, and checks that an insert is in it. An entity with no
// policy of its own returns it from its Policy method; one with rules of its
// own uses [Scope.Wrap].
func (s Scope[E, V]) Policy() velox.Policy[E] { return s.Wrap(velox.Policy[E]{}) }

// Wrap returns p with the scope added: a statement's Read condition is the
// scope's and p's both, and an insert must be in the scope and pass p's Insert.
// The engine ANDs Read into every update and delete, so the scope reaches those
// too. p is not changed.
func (s Scope[E, V]) Wrap(p velox.Policy[E]) velox.Policy[E] {
	inner := p.Read
	p.Read = func(ctx context.Context) (velox.Pred[E], error) {
		v, ok := s.key.From(ctx)
		if !ok {
			return velox.Pred[E]{}, fmt.Errorf("%w: %s", ErrNoScope, s.key.name)
		}
		if inner == nil {
			return s.eq(v), nil
		}
		c, err := inner(ctx)
		if err != nil {
			return velox.Pred[E]{}, err
		}
		return velox.And(s.eq(v), c), nil
	}
	insert := p.Insert
	p.Insert = func(ctx context.Context, x *E) error {
		v, ok := s.key.From(ctx)
		if !ok {
			return fmt.Errorf("%w: %s", ErrNoScope, s.key.name)
		}
		if got := *s.field(x); got != v {
			return fmt.Errorf("%w: %s is %v, the context's is %v", ErrMismatch, s.key.name, got, v)
		}
		if insert != nil {
			return insert(ctx, x)
		}
		return nil
	}
	return p
}

// Fill returns the hook that sets E's scope column to the context's value on
// every inserted row that leaves it at the zero value, in the caller's structs
// too. It does nothing when the context has no value: the policy refuses the
// insert then. List it in the entity's Hooks.
func (s Scope[E, V]) Fill() velox.Hook[E] {
	return velox.On(func(next velox.Mutator[E]) velox.Mutator[E] {
		return velox.MutateFunc[E](func(ctx context.Context, m *velox.Mutation[E]) error {
			if v, ok := s.key.From(ctx); ok {
				var zero V
				for _, x := range m.Rows() {
					if p := s.field(x); *p == zero {
						*p = v
					}
				}
			}
			return next.Mutate(ctx, m)
		})
	}, velox.OpInsert)
}
