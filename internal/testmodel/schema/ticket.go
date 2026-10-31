package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/mixin"
)

// Stamp records who created the row. Ticket's first hook, stamp, fills it.
type Stamp struct{ mixin.Schema }

// Fields returns created_by.
func (Stamp) Fields() []schema.Field { return []schema.Field{field.String("created_by")} }

// Ticket is tenant-scoped, unique by OrgID and Title, and has a policy and
// hooks for every kind of write (Policy and Hooks are in testmodel/ticket.go).
type Ticket struct{ schema.Schema }

// Mixin adds created_by.
func (Ticket) Mixin() []schema.Mixin { return []schema.Mixin{Stamp{}} }

// Fields returns the columns after the ID and the mixin's.
func (Ticket) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Tenant(),
		field.String("owner"),
		field.String("title"),
		field.Bool("done"),
		field.Int64("invoice_id"),
		field.Int64("parent_id").Optional().Nillable(),
	}
}

// Edges returns Invoice, to-one with its key in the table, and Children and
// Parent, a tree whose key is parent_id.
func (Ticket) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("invoice", Invoice.Type).Unique().Required().Field("invoice_id"),
		edge.To("children", Ticket.Type).From("parent").Unique().Field("parent_id"),
	}
}

// Indexes makes OrgID and Title unique together.
func (Ticket) Indexes() []schema.Index {
	return []schema.Index{index.Fields("org_id", "title").Unique()}
}
