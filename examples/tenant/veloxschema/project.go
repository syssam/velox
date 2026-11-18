// Package veloxschema declares the tenant example's entity. go tool velox-gen reads
// it and writes the veloxgen package beside it.
package veloxschema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
)

// Project belongs to one organization, the tenant. Its name is unique within
// the organization, not across organizations.
type Project struct{ velox.Schema }

// Mixin adds org_id: InOrg (inorg.go) confines every statement on Project to
// the organization the context names.
func (Project) Mixin() []velox.Mixin { return []velox.Mixin{InOrg{}} }

// Fields returns the columns after the ID and the mixin's.
func (Project) Fields() []velox.Field {
	return []velox.Field{
		field.String("name"),
		field.String("status"),
	}
}

// Indexes makes OrgID and Name unique together.
func (Project) Indexes() []velox.Index {
	return []velox.Index{index.Fields("org_id", "name").Unique()}
}
