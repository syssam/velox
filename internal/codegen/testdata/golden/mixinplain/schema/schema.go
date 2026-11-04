//go:build ignore

// The schema does not import gen, and its behaviour comes from mixins
// alone: gen binds it, and there is no package runtime.
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

func (InOrg) Policy[E any]() velox.Policy[E] {
	org := velox.Field[E, int64]("org_id")
	return velox.Policy[E]{Read: func(context.Context) (velox.Pred[E], error) { return org.EQ(1), nil }}
}

// Memo is in an organization.
type Memo struct{ schema.Schema }

func (Memo) Mixin() []schema.Mixin { return []schema.Mixin{InOrg{}} }

func (Memo) Fields() []schema.Field { return []schema.Field{field.String("text")} }

// Note is soft-deleted.
type Note struct{ schema.Schema }

func (Note) Mixin() []schema.Mixin { return []schema.Mixin{softdel.Mixin{}} }

func (Note) Fields() []schema.Field { return []schema.Field{field.String("text")} }
