package valid

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
)

// User holds the schema definition for the User entity.
type User struct {
	velox.Schema
}

// Fields of the User.
func (User) Fields() []velox.Field {
	return []velox.Field{
		field.String("name"),
		field.String("email").Unique(),
		field.Int("age").Optional(),
	}
}

// Edges of the User.
func (User) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("groups", Group.Type),
		edge.To("tags", Tag.Type),
	}
}

// Indexes of the User.
func (User) Indexes() []velox.Index {
	return []velox.Index{
		index.Fields("name", "email").Unique(),
	}
}

// Group holds the schema definition for the Group entity.
type Group struct {
	velox.Schema
}

// Fields of the Group.
func (Group) Fields() []velox.Field {
	return []velox.Field{
		field.String("name"),
		field.String("description").Optional(),
	}
}

// Edges of the Group.
func (Group) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("users", User.Type).Ref("groups"),
	}
}

// Tag holds the schema definition for the Tag entity.
type Tag struct {
	velox.Schema
}

// Fields of the Tag.
func (Tag) Fields() []velox.Field {
	return []velox.Field{
		field.String("value"),
	}
}

// Edges of the Tag.
func (Tag) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("users", User.Type).Ref("tags"),
	}
}
