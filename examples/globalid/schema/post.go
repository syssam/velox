package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/field"
)

type Post struct {
	velox.Schema
}

func (Post) Fields() []velox.Field {
	return []velox.Field{
		field.String("title").NotEmpty(),
	}
}
