package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Entity166 struct{ velox.Schema }

func (Entity166) Mixin() []velox.Mixin {
	return []velox.Mixin{mixin.Time{}}
}

func (Entity166) Fields() []velox.Field {
	return []velox.Field{
		field.String("name"),
		field.String("description").Optional().Nillable(),
		field.Int("count").Default(0),
		field.String("status").Optional().Nillable(),
	}
}

func (Entity166) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("children", Entity167.Type),
	}
}
