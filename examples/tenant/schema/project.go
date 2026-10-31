// Package schema declares the tenant example's entity. go tool velox-gen reads
// it and writes the gen package beside it.
package schema

import (
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/field"
	"github.com/syssam/velox2/schema/index"
)

// Project belongs to one organization, the tenant. Its name is unique within
// the organization, not across organizations.
type Project struct{ schema.Schema }

// Fields returns the columns after the ID. Tenant makes OrgID the tenant
// column: every statement on Project is scoped to the context's tenant, and
// fails without one.
func (Project) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Tenant(),
		field.String("name"),
		field.String("status"),
	}
}

// Indexes makes OrgID and Name unique together.
func (Project) Indexes() []schema.Index {
	return []schema.Index{index.Fields("org_id", "name").Unique()}
}
