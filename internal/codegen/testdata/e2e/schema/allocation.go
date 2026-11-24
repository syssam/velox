//go:build ignore

package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// Allocation is reversed by another allocation: a one-to-one edge to its own
// schema whose assoc edge binds the key, so reversed_by_id in a row holds the
// ID of the allocation that reverses it.
type Allocation struct{ schema.Schema }

func (Allocation) Fields() []schema.Field {
	return []schema.Field{
		field.String("label"),
		field.Int64("reversed_by_id").Optional().Nillable(),
	}
}

func (Allocation) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("reversed_by", Allocation.Type).Field("reversed_by_id").Unique(),
		edge.From("reversal_of", Allocation.Type).Ref("reversed_by").Unique(),
	}
}
