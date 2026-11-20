package schema

import (
	"github.com/syssam/velox/internal/testmodel/kind"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

// Ledger holds decimals of a Go type of their own, kind.Dec: their columns
// are numeric, so their handles are NumberCol and NullNumberCol.
type Ledger struct{ schema.Schema }

// Fields returns the columns after the ID.
func (Ledger) Fields() []schema.Field {
	dec := map[string]string{field.Postgres: "numeric(19,4)", field.MySQL: "decimal(19,4)", field.SQLite: "numeric"}
	return []schema.Field{
		field.Other("amount", kind.Dec("")).SchemaType(dec),
		field.Other("fee", kind.Dec("")).SchemaType(dec).Optional().Nillable(),
	}
}
