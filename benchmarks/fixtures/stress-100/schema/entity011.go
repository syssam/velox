package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Entity011 struct{ velox.Schema }

func (Entity011) Mixin() []velox.Mixin {
	return []velox.Mixin{mixin.Time{}}
}

func (Entity011) Fields() []velox.Field {
	return []velox.Field{
		field.String("name"),
		field.String("description").Optional().Nillable(),
		field.Int("count").Default(0),
		field.String("status").Optional().Nillable(),
	}
}

func (Entity011) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("children", Entity012.Type),
	}
}
