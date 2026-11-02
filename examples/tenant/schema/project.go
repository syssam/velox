// Package schema declares the tenant example's entity. go tool velox-gen reads
// it and writes the gen package beside it.
package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
)

// Project belongs to one organization, the tenant. Its name is unique within
// the organization, not across organizations.
type Project struct{ schema.Schema }

// Fields returns the columns after the ID. OrgID is Immutable, so that an
// update cannot move a row to another organization. gen/scope.go confines every
// statement on Project to the organization the context names.
func (Project) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Immutable(),
		field.String("name"),
		field.String("status"),
	}
}

// Indexes makes OrgID and Name unique together.
func (Project) Indexes() []schema.Index {
	return []schema.Index{index.Fields("org_id", "name").Unique()}
}
