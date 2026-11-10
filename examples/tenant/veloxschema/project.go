// Package schema declares the tenant example's entity. go tool velox-gen reads
// it and writes the gen package beside it.
package veloxschema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
)

// Project belongs to one organization, the tenant. Its name is unique within
// the organization, not across organizations.
type Project struct{ schema.Schema }

// Mixin adds org_id: InOrg (inorg.go) confines every statement on Project to
// the organization the context names.
func (Project) Mixin() []schema.Mixin { return []schema.Mixin{InOrg{}} }

// Fields returns the columns after the ID and the mixin's.
func (Project) Fields() []schema.Field {
	return []schema.Field{
		field.String("name"),
		field.String("status"),
	}
}

// Indexes makes OrgID and Name unique together.
func (Project) Indexes() []schema.Index {
	return []schema.Index{index.Fields("org_id", "name").Unique()}
}
