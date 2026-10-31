// Package schema declares the routing example's entity. go tool velox-gen
// reads it and writes the gen package beside it.
package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

// Note belongs to one organization, the tenant, and so to one shard: the
// organization's rows are all in the same database.
type Note struct{ schema.Schema }

// Fields returns the columns after the ID. Tenant makes OrgID the tenant
// column; WithShardFor maps the context's tenant to a shard.
func (Note) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Tenant(),
		field.String("body"),
	}
}
