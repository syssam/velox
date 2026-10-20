// Package graphql holds the annotations that shape the GraphQL schema velox
// generates, and nothing else. A schema package imports it, so it imports only
// the standard library and velox2/schema: the schema package, velox-gen's
// loader and the application's schema code compile no GraphQL library.
//
// Every constructor returns the same type, Annotation, named "graphql". A
// schema, field or edge lists as many as it likes in Annotations(), and the
// loader merges them into one (see Annotation.Merge). A short schema:
//
//	type User struct{ schema.Schema }
//
//	func (User) Fields() []schema.Field {
//		return []schema.Field{
//			field.String("name").Annotations(
//				graphql.OrderField("NAME"),
//				graphql.WhereOps(graphql.OpsEquality, graphql.OpsString),
//			),
//			field.Time("created_at").Default(time.Now).Annotations(graphql.OrderField("CREATED_AT")),
//			field.String("password_hash").Sensitive(), // in no type, where input or order
//		}
//	}
//
//	func (User) Edges() []schema.Edge {
//		return []schema.Edge{
//			edge.To("posts", Post.Type).Annotations(graphql.WhereEdge()),
//		}
//	}
//
//	func (User) Annotations() []schema.Annotation {
//		return []schema.Annotation{
//			graphql.QueryField("users"),
//			graphql.Mutations(graphql.Create(), graphql.Update(), graphql.Delete()),
//			graphql.MultiOrder(),
//		}
//	}
//
// The annotations of an entity are Skip, Type, QueryField, Mutations,
// MultiOrder and Directives; of a field, FieldName, Skip, OrderField,
// WhereOps, CreateInput, UpdateInput, EnumValues and Directives; of an edge,
// Skip, FieldName, WhereEdge, Connection and Directives. One on an element it
// does not apply to makes the generator fail, naming the schema and the
// element.
//
// # Generating the SDL
//
// The generator is a velox-gen plugin, the tool cmd/velox-graphql of this
// module. Install both as tools of the application's module and run velox-gen
// with the plugin:
//
//	go get -tool github.com/syssam/velox2/cmd/velox-gen
//	go get -tool github.com/syssam/velox2/contrib/graphql/cmd/velox-graphql
//	go tool velox-gen -out gen -plugin velox-graphql ./schema
//
// That writes the entities, as velox-gen always does, and gen/velox.graphqls,
// the GraphQL schema: a type for each entity, the Node interface, connections,
// ordering, where inputs and mutation inputs. velox-gen owns the file like
// its own: -check fails when the schema changed, and dropping -plugin
// velox-graphql removes the file on the next run.
//
// The SDL binds to Go by gqlgen's @goField directive (a node's id to IDGQL, an
// edge to <Edge>GQL) and by enum names (velox-gen's Go enum types). It is
// valid GraphQL today, but the Go it binds to is not written yet: the Go
// bindings (IDs, cursors, connections, the edge methods) and the runtime
// package arrive with the next plans, and until then gqlgen cannot load the
// application from this SDL alone.
//
// The annotation reaches the plugin as JSON under the key "graphql" of the
// element's annotations; the plugin decodes an Annotation from it. The JSON
// field names are therefore a contract.
package graphql
