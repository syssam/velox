// Package graphql holds the annotations a velox schema uses to shape its
// GraphQL: which entities are connections, which fields filter and order,
// which mutations exist, directives, federation keys, and resolver mappings.
//
//	func (Product) Annotations() []schema.Annotation {
//		return []schema.Annotation{
//			graphql.RelayConnection(),
//			graphql.WhereInputFields("sku", "name"),
//			graphql.Mutations(graphql.MutationCreate()),
//		}
//	}
//
// The generator that reads them is graphqlgen, run from generate.go. They are
// separate because velox's generated runtime imports the schema package, so
// whatever this package imports is linked into every server; it imports only
// velox/schema (TestAnnotationsImportNoGenerator).
package graphql
