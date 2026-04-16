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
	}
}
