//go:build ignore

package schema

import (
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/field"
)

// Note belongs to an organisation: org_id is its tenant.
type Note struct{ schema.Schema }

func (Note) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Tenant(),
		field.String("text"),
	}
}
