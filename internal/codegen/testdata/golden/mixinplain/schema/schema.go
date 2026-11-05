//go:build ignore

// The schema's behaviour comes from mixins alone, one of them in a package
// of its own: gen binds it.
package schema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"

	"example.com/tmp/mixins/softdel"
)

// InOrg confines an entity's rows to organization 1.
type InOrg struct{ mixin.Schema }

func (InOrg) Fields() []schema.Field {
	return []schema.Field{field.Int64("org_id").Immutable()}
}

func (InOrg) Policy() velox.Policy {
	return velox.Policy{Read: func(context.Context) (velox.Cond, error) { return velox.C("org_id").EQ(1), nil }}
}

// Memo is in an organization.
type Memo struct{ schema.Schema }

func (Memo) Mixin() []schema.Mixin { return []schema.Mixin{InOrg{}} }

func (Memo) Fields() []schema.Field { return []schema.Field{field.String("text")} }

// Note is soft-deleted.
type Note struct{ schema.Schema }

func (Note) Mixin() []schema.Mixin { return []schema.Mixin{softdel.Mixin{}} }

func (Note) Fields() []schema.Field { return []schema.Field{field.String("text")} }
