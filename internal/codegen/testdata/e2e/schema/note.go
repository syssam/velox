//go:build ignore

package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/field"
)

// Note belongs to an organisation: org_id is its scope. It is written with the
// root package's schema types, as the old velox's schemas were, to keep them
// covered through discovery and generation.
type Note struct{ velox.Schema }

func (Note) Fields() []velox.Field {
	return []velox.Field{
		field.Int64("org_id").Immutable(),
		field.String("text"),
	}
}
