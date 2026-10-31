// Package schema declares the tour's entities, as a team that owns invoices
// would write them: the structure of Invoice and Line. go tool velox-gen reads
// it and writes the gen package beside it. The policies and the hook are
// methods in gen, typed with the generated handles.
package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/mixin"
)

// Invoice is a bill sent to a customer. Every invoice belongs to one
// organization, the tenant, and its number is unique within it.
type Invoice struct{ schema.Schema }

// Mixin adds created_at and updated_at: the first is set on insert, the
// second on insert and on every update.
func (Invoice) Mixin() []schema.Mixin { return []schema.Mixin{mixin.Time{}} }

// Fields returns the columns after the ID and the mixin's. Tenant makes OrgID
// the tenant column. Status is an enum: velox-gen writes gen.InvoiceStatus and
// a constant for each value.
func (Invoice) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Tenant(),
		field.String("number"),
		field.String("customer"),
		field.Enum("status").Values("draft", "sent", "paid"),
		field.Int64("total_cents"),
	}
}

// Edges returns Lines, the items of an Invoice. Line.InvoiceID is its key.
func (Invoice) Edges() []schema.Edge { return []schema.Edge{edge.To("lines", Line.Type)} }

// Indexes makes OrgID and Number unique together.
func (Invoice) Indexes() []schema.Index {
	return []schema.Index{index.Fields("org_id", "number").Unique()}
}
