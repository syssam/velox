package schema

import (
	"github.com/syssam/velox2/internal/testmodel/kind"
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
)

// Sample has a field of every handle kind, and both kinds of relation.
type Sample struct{ schema.Schema }

// Fields returns the columns after the ID.
func (Sample) Fields() []schema.Field {
	return []schema.Field{
		field.String("name"),
		field.String("status").GoType(kind.Status("")),
		field.String("prev").GoType(kind.Status("")).Optional().Nillable(),
		field.Int64("price").GoType(kind.Cents(0)),
		field.Float("ratio").Optional().Nillable(),
		field.Bool("flag"),
		field.Bool("maybe_flag").Optional().Nillable(),
		field.Time("when"),
		field.Time("maybe_when").Optional().Nillable(),
		field.Bytes("blob"),
		field.Int64("parent_id").Optional().Nillable(),
		field.Int64("score").GoType(kind.Score(0)),
	}
}

// Edges returns Lines, to-many, and Parent, a sample's own to-one.
func (Sample) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("lines", Line.Type),
		edge.To("parent", Sample.Type).Unique().Field("parent_id"),
	}
}
