package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Product struct{ velox.Schema }

func (Product) Mixin() []velox.Mixin {
	return []velox.Mixin{mixin.Time{}}
}

func (Product) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").
			NotEmpty().
			MaxLen(200),
		field.Float("price").
			Positive(),
		field.Int("stock").
			NonNegative().
			Default(0),
		field.Bytes("thumbnail").
			Optional().
			Nillable(),
		field.Bool("published").
			Default(false),
	}
}

func (Product) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("tags", Tag.Type),
	}
}

func (Product) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.RelayConnection(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
