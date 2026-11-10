// Package schema is the DSL schema of the test model. velox-gen writes the
// parent package, testmodel, from it:
//
//	go run ./cmd/velox-gen -out internal/testmodel -package testmodel ./internal/testmodel/schema
package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/mixin"
)

// Invoice is the entity velox's tests query. Its OrgID and Number are unique
// together, and OrgID is its tenant. Its CreatedAt is set when an Invoice is
// inserted, and its UpdatedAt on every insert and update.
type Invoice struct{ schema.Schema }

// Mixin adds created_at and updated_at, and org_id, the tenant, with its
// scope.
func (Invoice) Mixin() []schema.Mixin { return []schema.Mixin{mixin.Time{}, InOrg{}} }

// Fields returns the columns after the ID and the mixin's.
func (Invoice) Fields() []schema.Field {
	return []schema.Field{
		field.String("number"),
		field.Float("total"),
		field.Bool("paid"),
		field.Time("paid_at").Optional().Nillable(),
		// Optional, and NOT NULL: "" means unset, which NotEmpty lets pass.
		field.String("note").Optional().Default("").NotEmpty(),
		field.Bytes("data"),
	}
}

// Edges returns Items, the lines of an Invoice.
func (Invoice) Edges() []schema.Edge { return []schema.Edge{edge.To("items", Item.Type)} }

// Indexes makes OrgID and Number unique together.
func (Invoice) Indexes() []schema.Index {
	return []schema.Index{index.Fields("org_id", "number").Unique()}
}
