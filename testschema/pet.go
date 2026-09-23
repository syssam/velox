package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// Pet holds the schema definition for the Pet entity. It is the testschema's
// edge whose foreign key is a user-declared field bound with .Field(): the
// key is an ordinary, projectable column, not an auto-created one riding on
// withFKs, so eager loads through it must add it to a projection themselves.
type Pet struct {
	velox.Schema
}

// Fields of the Pet.
func (Pet) Fields() []velox.Field {
	return []velox.Field{
		field.String("name"),
		field.Int("owner_id").Optional().Nillable(),
	}
}

// Edges of the Pet.
func (Pet) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("owner", User.Type).Ref("pets").Unique().Field("owner_id"),
	}
}
