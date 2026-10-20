// Package schema is the DSL schema of the test model. velox-gen writes the
// parent package, testmodel, from it:
//
//	go run ./cmd/velox-gen -out internal/testmodel -package testmodel ./internal/testmodel/schema
package schema

import (
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
	"github.com/syssam/velox2/schema/index"
	"github.com/syssam/velox2/schema/mixin"
)

// Invoice is the entity velox's tests query. Its OrgID and Number are unique
// together, and OrgID is its tenant. Its CreatedAt is set when an Invoice is
// inserted, and its UpdatedAt on every insert and update.
type Invoice struct{ schema.Schema }

// Mixin adds created_at and updated_at.
func (Invoice) Mixin() []schema.Mixin { return []schema.Mixin{mixin.Time{}} }

// Fields returns the columns after the ID and the mixin's.
func (Invoice) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Tenant(),
		field.String("number"),
		field.Float("total"),
		field.Bool("paid"),
		field.Time("paid_at").Optional().Nillable(),
		field.String("note").Optional().Nillable(),
		field.Bytes("data"),
	}
}

// Edges returns Items, the lines of an Invoice.
func (Invoice) Edges() []schema.Edge { return []schema.Edge{edge.To("items", Item.Type)} }

// Indexes makes OrgID and Number unique together.
func (Invoice) Indexes() []schema.Index {
	return []schema.Index{index.Fields("org_id", "number").Unique()}
}
