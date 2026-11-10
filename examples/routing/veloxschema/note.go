// Package schema declares the routing example's entity. go tool velox-gen
// reads it and writes the gen package beside it.
package veloxschema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

// Note belongs to one organization, and so to one shard: the
// organization's rows are all in the same database.
type Note struct{ schema.Schema }

// Mixin adds org_id: InOrg (inorg.go) confines every statement on Note to
// the context's organization, OrgKey; WithShardFor maps that organization to
// a shard.
func (Note) Mixin() []schema.Mixin { return []schema.Mixin{InOrg{}} }

// Fields returns the columns after the ID and the mixin's.
func (Note) Fields() []schema.Field {
	return []schema.Field{
		field.String("body"),
	}
}
