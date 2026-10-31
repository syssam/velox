//go:build ignore

package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// Card is bound to its owner by the field owner_id.
type Card struct{ schema.Schema }

func (Card) Fields() []schema.Field {
	return []schema.Field{
		field.String("number").Sensitive(),
		field.Int64("owner_id"),
	}
}

func (Card) Edges() []schema.Edge {
	return []schema.Edge{edge.From("owner", User.Type).Ref("card").Unique().Required().Field("owner_id")}
}
