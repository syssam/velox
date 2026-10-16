// Package schema declares the otel example's entity, as a billing team would
// own it. go tool velox-gen reads it and writes the gen package beside it.
package schema

import (
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/field"
)

// Invoice is a bill sent to a customer. No two invoices share a number.
type Invoice struct{ schema.Schema }

// Fields returns the columns after the ID. Status is an enum: velox-gen writes
// its Go type, gen.InvoiceStatus, and a constant for each value.
func (Invoice) Fields() []schema.Field {
	return []schema.Field{
		field.String("number").Unique(),
		field.String("customer"),
		field.Enum("status").Values("sent", "paid"),
		field.Int64("total_cents"),
	}
}
