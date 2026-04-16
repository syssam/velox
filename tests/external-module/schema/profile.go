package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type Profile struct{ velox.Schema }

func (Profile) Fields() []velox.Field {
	return []velox.Field{
		field.String("bio").Optional().Nillable().MaxLen(500),
		field.String("avatar_url").Optional().Nillable(),
	}
}

func (Profile) Edges() []velox.Edge {
	return []velox.Edge{
		// O2O inverse: Profile → User
		edge.From("user", User.Type).Ref("profile").Unique().Required(),
	}
}
