// Package schema is the schema DSL: the declarations velox-gen reads to
// generate entities. A schema is a struct that embeds Schema and overrides the
// methods it needs. The field, edge, index and mixin packages build the
// declarations.
//
//	package schema
//
//	import (
//		"github.com/syssam/velox/schema"
//		"github.com/syssam/velox/schema/edge"
//		"github.com/syssam/velox/schema/field"
//		"github.com/syssam/velox/schema/index"
//		"github.com/syssam/velox/schema/mixin"
//	)
//
//	type User struct{ schema.Schema }
//
//	func (User) Fields() []schema.Field {
//		return []schema.Field{
//			field.Int64("org_id").Immutable(),
//			field.String("name").NotEmpty().MaxLen(100),
//			field.String("email").Unique(),
//			field.Enum("role").Values("viewer", "admin").Default("viewer"),
//			field.Time("deleted_at").Optional().Nillable(),
//		}
//	}
//
//	func (User) Edges() []schema.Edge {
//		return []schema.Edge{
//			edge.To("posts", Post.Type),
//			edge.From("org", Org.Type).Ref("users").Field("org_id").Unique().Required(),
//		}
//	}
//
//	func (User) Indexes() []schema.Index {
//		return []schema.Index{index.Fields("org_id", "name").Unique()}
//	}
//
//	func (User) Mixin() []schema.Mixin { return []schema.Mixin{mixin.Time{}} }
//
//	func (User) Annotations() []schema.Annotation {
//		return []schema.Annotation{schema.Table("users")}
//	}
//
// Generate the entities from the package with
//
//	go tool velox-gen ./veloxschema
//
// which writes the package ./veloxgen. Nothing in a schema runs at program start.
// A schema also declares its entity's behaviour, as ent's do: the methods
// Policy, Hooks and Interceptors. The schema package does not import gen,
// which imports it, so they name columns by name, as the fields do:
//
//	func (User) Policy() velox.Policy {
//		return velox.Policy{
//			Read: func(ctx context.Context) (velox.Predicate, error) {
//				return velox.FieldIsNull("deleted_at"), nil
//			},
//		}
//	}
//
//	func (User) Hooks() []velox.Hook { ... }
//
//	func (User) Interceptors() []velox.Interceptor { ... }
//
// The method takes nothing and returns velox.Policy, []velox.Hook or
// []velox.Interceptor; velox-gen refuses another shape, a generic method
// and the typed shape of an older velox (velox.Policy[veloxgen.User]) among
// them. A many-to-many join entity has no schema: declare an empty struct
// of its name, type UserFriend struct{}, and the methods on it.
//
// gen imports the schema package by its own name, and the descriptor of
// each entity with behaviour names the schema's value, Schema:
// schema.User{}; velox reads the methods of each value Mixin returns, in
// order, then the schema's own. velox-gen refuses a schema package that
// imports gen, directly or through another package. A relation that a
// behaviour method names with velox.HasEdge or velox.HasEdgeWith, by a
// constant, is linked in the descriptor, so that the condition applies the
// target's Read policy.
//
// A mixin declares behaviour too, for every entity that lists it, with the
// same methods, which are not generic:
//
//	func (InOrg) Policy() velox.Policy {
//		return velox.Policy{Read: func(ctx context.Context) (velox.Predicate, error) {
//			return velox.FieldEQ("org_id", orgOf(ctx)), nil
//		}}
//	}
//
// velox runs an entity's mixins' behaviour first, in Mixin order, then its
// schema's: the policies are ANDed, the hooks run in that order, the first
// outermost, and the interceptors' conditions are ANDed. velox-gen refuses
// a method of another shape at its line. A column name an entity lacks
// fails the statement that resolves it. velox reads the values the
// schema's Mixin method returns, so a mixin's configuration is its own.
package schema
