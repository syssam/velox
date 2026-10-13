// Package schema holds the shapes of a GraphQL schema that need Go of their
// own in the generated tree: an interface the schema defines, a field of
// several edges, an edge under another name, a union, a nillable bytes field
// and a JSON field of a Go type.
package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"

	"github.com/syssam/velox/tests/orm/gqlshapes/types"
)

// Org owns assets. With Person it is the interface Owner, which the schema
// defines because both give their assets under one name, "holdings".
type Org struct{ velox.Schema }

// Fields of the Org.
func (Org) Fields() []velox.Field {
	return []velox.Field{field.String("name")}
}

// Edges of the Org.
func (Org) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("assets", Asset.Type).Annotations(graphql.InterfaceField("holdings")),
	}
}

// Annotations of the Org.
func (Org) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.RelayConnection(),
		graphql.Implements("Owner"),
		graphql.UnionMember("SearchResult"),
	}
}

// Person owns assets, and makes them.
type Person struct{ velox.Schema }

// Fields of the Person.
func (Person) Fields() []velox.Field {
	return []velox.Field{field.String("name")}
}

// Edges of the Person.
func (Person) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("assets", Asset.Type).Annotations(graphql.InterfaceField("holdings")),
		edge.To("made", Asset.Type),
	}
}

// Annotations of the Person.
func (Person) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.RelayConnection(),
		graphql.Implements("Owner"),
		graphql.UnionMember("SearchResult"),
	}
}

// Asset is owned by an org or by a person: the schema has one field for the
// two edges, "owner", typed as the interface.
type Asset struct{ velox.Schema }

// Fields of the Asset.
func (Asset) Fields() []velox.Field {
	return []velox.Field{
		field.String("label"),
		// A *[]byte, which the schema reads through ThumbnailOrNil.
		field.Bytes("thumbnail").Optional().Nillable(),
		// The scalar Specs of the schema.
		field.JSON("specs", types.Specs{}).Optional(),
	}
}

// Edges of the Asset.
func (Asset) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("org", Org.Type).Ref("assets").Unique().
			Annotations(graphql.InterfaceField("owner")),
		edge.From("person", Person.Type).Ref("assets").Unique().
			Annotations(graphql.InterfaceField("owner")),
		// One edge under another name: "creator" is the maker.
		edge.From("maker", Person.Type).Ref("made").Unique().
			Annotations(graphql.InterfaceField("creator")),
	}
}

// Annotations of the Asset.
func (Asset) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.RelayConnection(),
	}
}
