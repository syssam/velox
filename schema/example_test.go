package schema_test

import (
	"fmt"

	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
)

// A schema is a struct that embeds schema.Schema and overrides what it needs:
// Fields, Edges, Indexes, Mixin and Annotations.
type Account struct{ schema.Schema }

func (Account) Fields() []schema.Field {
	return []schema.Field{
		field.String("email").Unique(),
		field.Int("age").Optional().Nillable(),
		field.Int64("org_id"),
	}
}

func (Account) Edges() []schema.Edge {
	return []schema.Edge{edge.To("org", Org{}).Field("org_id").Unique().Required()}
}

func (Account) Indexes() []schema.Index {
	return []schema.Index{index.Fields("org_id", "email")}
}

func (Account) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("accounts"), schema.Comment("Who can sign in.")}
}

// Org is the other end of Account's edge.
type Org struct{ schema.Schema }

func ExampleInterface() {
	var s schema.Interface = Account{}
	fmt.Println(len(s.Fields()), "fields,", len(s.Edges()), "edge,", len(s.Indexes()), "index")
	// Output: 3 fields, 1 edge, 1 index
}
