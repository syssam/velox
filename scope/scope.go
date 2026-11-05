// Package scope confines an entity's rows to a value the context carries: a
// tenant, an owner, a region. A scope is a Key, which a request sets, and a
// mixin of the application's schema package, whose Policy and Hooks read it,
// with nothing in the engine:
//
//	var OrgKey = scope.NewKey[int64]("org")
//
//	type InOrg struct{ mixin.Schema }
//
//	func (InOrg) Fields() []schema.Field {
//		return []schema.Field{field.Int64("org_id").Immutable()}
//	}
//
//	func (InOrg) Policy() velox.Policy {
//		return velox.Policy{
//			Read: func(ctx context.Context) (velox.Predicate, error) {
//				org, err := OrgKey.Require(ctx)
//				if err != nil {
//					return velox.Predicate{}, err
//				}
//				return velox.FieldEQ("org_id", org), nil
//			},
//			Insert: func(ctx context.Context, r velox.Row) error {
//				return OrgKey.Match(ctx, r.Value("org_id"))
//			},
//		}
//	}
//
//	func (InOrg) Hooks() []velox.Hook {
//		return []velox.Hook{velox.Before(func(ctx context.Context, m *velox.Mutation) error {
//			if org, ok := OrgKey.From(ctx); ok {
//				return m.Fill("org_id", org)
//			}
//			return nil
//		}, velox.OpInsert)}
//	}
//
//	func (Project) Mixin() []schema.Mixin { return []schema.Mixin{InOrg{}} }
//
// and a request carries the value:
//
//	ctx = schema.OrgKey.With(ctx, 7)
//
// Reads, updates and deletes then see only rows whose column is 7; an insert
// that leaves the column unset gets 7, and one that names another value is
// refused with [ErrMismatch]; a statement whose context has no value fails
// with [ErrNoScope], not with every row. velox.WithoutPolicy lifts the
// scope, with a reason, as it lifts every other policy. The entity's own
// Policy is ANDed with the mixin's, so it cannot lift the scope.
//
// The column must be Immutable and not nullable, so that an update cannot
// move a row to another scope and every row is in one. The mixin declares
// it so, beside the code that filters on it. Package scopetest checks that
// an entity is confined, and that its column is Immutable and NOT NULL.
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
	// The batcher keeps callers of different values apart by RowScope, which
	// shows the value because it was recorded here.
	return velox.WithScopeValue(context.WithValue(ctx, ctxKey[V]{k}, v), k.name, v)
}

// From returns the key's value in ctx, and false when there is none or it is
// the zero value, which is what parsing a missing claim gives.
func (k *Key[V]) From(ctx context.Context) (V, bool) {
	v, ok := ctx.Value(ctxKey[V]{k}).(V)
	var zero V
	return v, ok && v != zero
}

// Require returns the key's value in ctx, or [ErrNoScope], wrapped with the
// key's name, when From finds none. A policy's Read returns its error, so a
// statement without a value fails and reads nothing.
func (k *Key[V]) Require(ctx context.Context) (V, error) {
	v, ok := k.From(ctx)
	if !ok {
		return v, fmt.Errorf("%w: %s", ErrNoScope, k.name)
	}
	return v, nil
}

// Match returns nil when v, a row's value of the scope's column, is the
// key's value in ctx: [ErrNoScope] when ctx has none, and [ErrMismatch]
// when v is another value, or not a V. A policy's Insert returns its error.
func (k *Key[V]) Match(ctx context.Context, v any) error {
	want, err := k.Require(ctx)
	if err != nil {
		return err
	}
	if got, ok := v.(V); !ok || got != want {
		return fmt.Errorf("%w: %s is %v, the context's is %v", ErrMismatch, k.name, v, want)
	}
	return nil
}
