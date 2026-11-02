// Package schema declares the routing example's entity. go tool velox-gen
// reads it and writes the gen package beside it.
package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

// Note belongs to one organization, and so to one shard: the
// organization's rows are all in the same database.
type Note struct{ schema.Schema }

// Fields returns the columns after the ID. OrgID is Immutable and Scoped: the
// generated gen.OrgKey and Note's policy confine every statement on Note to the
// context's organization; WithShardFor maps that organization to a shard.
func (Note) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Immutable().Annotations(schema.Scoped("org")),
		field.String("body"),
	}
}
