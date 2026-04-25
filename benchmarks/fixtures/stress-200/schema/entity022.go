package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Entity022 struct{ velox.Schema }

func (Entity022) Mixin() []velox.Mixin {
	return []velox.Mixin{mixin.Time{}}
}

func (Entity022) Fields() []velox.Field {
	return []velox.Field{
		field.String("name"),
		field.String("description").Optional().Nillable(),
		field.Int("count").Default(0),
		field.String("status").Optional().Nillable(),
	}
}

func (Entity022) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("children", Entity023.Type),
	}
}
