//go:build ignore

package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

// Note belongs to an organisation: org_id is its scope.
type Note struct{ schema.Schema }

func (Note) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Immutable(),
		field.String("text"),
	}
}
