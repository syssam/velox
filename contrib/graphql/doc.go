// Package graphql holds the annotations that shape the GraphQL schema velox
// generates, and nothing else. A schema package imports it, so it imports only
// the standard library and velox2/schema: the schema package, velox-gen's
// loader and the application's schema code compile no GraphQL library.
//
// Every constructor returns the same type, Annotation, named "graphql". A
// schema, field or edge lists as many as it likes in Annotations(), and the
// loader merges them into one (see Annotation.Merge):
//
//	func (User) Annotations() []schema.Annotation {
//		return []schema.Annotation{
//			graphql.QueryField("users"),
//			graphql.Mutations(),
//			graphql.MultiOrder(),
//		}
//	}
//
//	func (User) Fields() []schema.Field {
//		return []schema.Field{
//			field.Time("created_at").Annotations(graphql.OrderField("CREATED_AT")),
//			field.String("name").Annotations(graphql.WhereOps(graphql.OpsEquality, graphql.OpsString)),
//		}
//	}
//
// The annotation reaches the plugin as JSON under the key "graphql" of the
// element's annotations; the plugin (cmd/velox-graphql) decodes an Annotation
// from it. The JSON field names are therefore a contract.
package graphql
