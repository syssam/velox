package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Entity012 struct{ velox.Schema }

func (Entity012) Mixin() []velox.Mixin {
	return []velox.Mixin{mixin.Time{}}
}

func (Entity012) Fields() []velox.Field {
	return []velox.Field{
		field.String("name"),
		field.String("description").Optional().Nillable(),
		field.Int("count").Default(0),
		field.String("status").Optional().Nillable(),
	}
}

func (Entity012) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("children", Entity013.Type),
	}
}
