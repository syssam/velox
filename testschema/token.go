package schema

import (
	"github.com/google/uuid"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/testschema/types"
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

		// Custom GoType fields. Each one reaches a generator path that a
		// plain field does not: a validator on a GoType is declared at the
		// basic type and called as Validator(string(v)); a GoType enum has
		// no generated constants, so its validator switches on the declared
		// values; String() must convert a string-kind GoType. Every one of
		// these once generated code that did not compile, and nothing in
		// the repo noticed because no schema used them.
		field.Enum("tier").
			GoType(types.Tier("")).
			Default(string(types.TierFree)),
		field.Enum("grade").
			GoType(types.Tier("")).
			Optional().
			Nillable(),
		field.String("label").
			GoType(types.Label("")).
			NotEmpty().
			MaxLen(20).
			Default("token"),
		field.String("alias").
			GoType(types.Label("")).
			Optional().
			Nillable().
			NotEmpty(),
		field.Int("weight").
			GoType(types.Weight(0)).
			Positive().
			Default(1),
	}
}
