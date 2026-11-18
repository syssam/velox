// Package veloxschema declares the tour's entities, as a team that owns invoices
// would write them: the structure of Invoice and Line, the InOrg mixin that
// confines both to an organization, and their policies and hook, typed with
// the generated handles. go tool velox-gen reads it and writes the veloxgen package
// beside it.
package veloxschema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/mixin"
	"github.com/syssam/velox/schema/mixin/softdelete"
)

// Invoice is a bill sent to a customer. Every invoice belongs to one
// organization, and its number is unique within it.
type Invoice struct{ velox.Schema }

// Mixin adds created_at and updated_at: the first is set on insert, the
// second on insert and on every update. InOrg adds org_id and confines every
// statement to the context's organization. softdelete adds deleted_at: a
// delete sets it, and every read and write skips the rows it is set on.
func (Invoice) Mixin() []velox.Mixin {
	return []velox.Mixin{mixin.Time{}, InOrg{}, softdelete.Mixin{}}
}

// Fields returns the columns after the ID and the mixins'. Status is an enum:
// velox-gen writes veloxgen.InvoiceStatus and a constant for each value.
func (Invoice) Fields() []velox.Field {
	return []velox.Field{
		field.String("number"),
		field.String("customer"),
		field.Enum("status").Values("draft", "sent", "paid"),
		field.Int64("total_cents"),
	}
}

// Edges returns Lines, the items of an Invoice. Line.InvoiceID is its key.
func (Invoice) Edges() []velox.Edge { return []velox.Edge{edge.To("lines", Line.Type)} }

// Indexes makes OrgID and Number unique together among the invoices that
// are not deleted, so that a deleted invoice's number can be used again.
// The tour runs on SQLite, which, as PostgreSQL, has partial indexes.
func (Invoice) Indexes() []velox.Index {
	return []velox.Index{index.Fields("org_id", "number").Unique().Where("deleted_at IS NULL")}
}
