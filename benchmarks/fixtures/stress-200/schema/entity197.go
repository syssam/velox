package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Entity197 struct{ velox.Schema }

func (Entity197) Mixin() []velox.Mixin {
	return []velox.Mixin{mixin.Time{}}
}

func (Entity197) Fields() []velox.Field {
	return []velox.Field{
		field.String("name"),
		field.String("description").Optional().Nillable(),
		field.Int("count").Default(0),
		field.String("status").Optional().Nillable(),
	}
}

func (Entity197) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("children", Entity198.Type),
	}
}
