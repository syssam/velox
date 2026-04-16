package schema

import (
	"github.com/google/uuid"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/field"
)

// Token is a minimal entity with a UUID primary key, used by the
// integration tests to exercise the bulk-create path on schemas
// whose IDs are user-assigned at create time (rather than DB
// auto-increment). The single-row and bulk paths both have
// ID-dependent branches that are otherwise not reached by the
// auto-increment entities (User, Post, Comment, Tag).
type Token struct {
	velox.Schema
}

// Fields of the Token.
func (Token) Fields() []velox.Field {
	return []velox.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.String("name").
			Unique().
			NotEmpty().
			MaxLen(100),
	}
}
