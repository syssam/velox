//go:build ignore

package schema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
	"github.com/syssam/velox/scope"
)

var (
	OrgKey    = scope.NewKey[int64]("org")
	RegionKey = scope.NewKey[string]("region")
)

// InOrg scopes an entity by org: a tenancy mixin over package scope.
type InOrg struct{ mixin.Schema }

func (InOrg) Fields() []schema.Field { return []schema.Field{field.Int64("org_id").Immutable()} }

func (InOrg) Policy() velox.Policy { return keyPolicy(OrgKey, "org_id") }

func (InOrg) Hooks() []velox.Hook {
	return []velox.Hook{keyFill(OrgKey, "org_id")}
}

// InRegion scopes an entity by a string, under another key.
type InRegion struct{ mixin.Schema }

func (InRegion) Fields() []schema.Field { return []schema.Field{field.String("region").Immutable()} }

func (InRegion) Policy() velox.Policy { return keyPolicy(RegionKey, "region") }

func (InRegion) Hooks() []velox.Hook {
	return []velox.Hook{keyFill(RegionKey, "region")}
}

// Note is scoped by org, and declares neither Policy nor Hooks.
type Note struct{ schema.Schema }

func (Note) Mixin() []schema.Mixin  { return []schema.Mixin{InOrg{}} }
func (Note) Fields() []schema.Field { return []schema.Field{field.String("title")} }

// Task is scoped by org, and declares both (the golden Config binds them).
type Task struct{ schema.Schema }

func (Task) Mixin() []schema.Mixin  { return []schema.Mixin{InOrg{}} }
func (Task) Fields() []schema.Field { return []schema.Field{field.String("title")} }

// Memo is scoped by org and declares only a Policy.
type Memo struct{ schema.Schema }

func (Memo) Mixin() []schema.Mixin { return []schema.Mixin{InOrg{}} }

// Tag is scoped by org and declares only Hooks.
type Tag struct{ schema.Schema }

func (Tag) Mixin() []schema.Mixin { return []schema.Mixin{InOrg{}} }

// Home is scoped by a string, under another key.
type Home struct{ schema.Schema }

func (Home) Mixin() []schema.Mixin { return []schema.Mixin{InRegion{}} }

// Owner has no scope.
type Owner struct{ schema.Schema }

func (Owner) Fields() []schema.Field { return []schema.Field{field.String("name")} }

// keyPolicy confines rows to key's value in the context, by column.
func keyPolicy[V comparable](key *scope.Key[V], column string) velox.Policy {
	return velox.Policy{
		Read: func(ctx context.Context) (velox.Predicate, error) {
			v, err := key.Require(ctx)
			if err != nil {
				return velox.Predicate{}, err
			}
			return velox.FieldEQ(column, v), nil
		},
		Insert: func(ctx context.Context, r velox.Row) error { return key.Match(ctx, r.Value(column)) },
	}
}

// keyFill fills column with key's value in the context on insert.
func keyFill[V comparable](key *scope.Key[V], column string) velox.Hook {
	return velox.Before(func(ctx context.Context, m *velox.Mutation) error {
		if v, ok := key.From(ctx); ok {
			return m.Fill(column, v)
		}
		return nil
	}, velox.OpInsert)
}
