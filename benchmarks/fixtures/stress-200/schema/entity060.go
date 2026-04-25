package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Entity060 struct{ velox.Schema }

func (Entity060) Mixin() []velox.Mixin {
	return []velox.Mixin{mixin.Time{}}
}

func (Entity060) Fields() []velox.Field {
	return []velox.Field{
		field.String("name"),
		field.String("description").Optional().Nillable(),
		field.Int("count").Default(0),
		field.String("status").Optional().Nillable(),
	}
}

func (Entity060) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("children", Entity061.Type),
	}
}
