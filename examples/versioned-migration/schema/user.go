// Package schema defines a minimal User entity for the versioned-migration
// example. The schema here is only used to generate the entity/migrate
// scaffolding; the actual migrations live in ../migrations as raw .sql files.
package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/field"
)

type User struct {
	velox.Schema
}

func (User) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").NotEmpty(),
		field.String("email").Unique().NotEmpty(),
	}
}
