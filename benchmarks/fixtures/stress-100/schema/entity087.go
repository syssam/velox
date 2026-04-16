package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Entity087 struct{ velox.Schema }

func (Entity087) Mixin() []velox.Mixin {
	return []velox.Mixin{mixin.Time{}}
}

func (Entity087) Fields() []velox.Field {
	return []velox.Field{
		field.String("name"),
		field.String("description").Optional().Nillable(),
		field.Int("count").Default(0),
		field.String("status").Optional().Nillable(),
	}
}

func (Entity087) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("children", Entity088.Type),
	}
}
