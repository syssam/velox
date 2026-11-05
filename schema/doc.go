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
//	go tool velox-gen ./schema
//
// which writes the package ./gen. Nothing in a schema runs at program start.
// A schema also declares its entity's behaviour, as ent's do: the methods
// Policy, Hooks and Interceptors. The schema package does not import gen,
// which imports it, so they name columns by name, as the fields do:
//
//	func (User) Policy() velox.Policy {
//		return velox.Policy{
//			Read: func(ctx context.Context) (velox.Cond, error) {
//				return velox.C("deleted_at").IsNull(), nil
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
// and the typed shape of an older velox (velox.Policy[gen.User]) among
// them. A many-to-many join entity has no schema: declare an empty struct
// of its name, type UserFriend struct{}, and the methods on it.
//
// gen imports the schema package, as sch, and each entity's descriptor
// lists the method values, sch.User{}.Policy. velox-gen refuses a schema
// package that imports gen, directly or through another package. A
// relation that a behaviour method names with velox.Edge, by a constant,
// is linked in the descriptor, so that its Has and HasWhere apply the
// target's Read policy.
//
// A mixin declares behaviour too, for every entity that lists it, with the
// same methods, which are not generic:
//
//	func (InOrg) Policy() velox.Policy {
//		return velox.Policy{Read: func(ctx context.Context) (velox.Cond, error) {
//			return velox.C("org_id").EQ(orgOf(ctx)), nil
//		}}
//	}
//
// velox runs an entity's mixins' behaviour first, in Mixin order, then its
// schema's: the policies are ANDed, the hooks run in that order, the first
// outermost, and the interceptors' conditions are ANDed. velox-gen refuses
// a method of another shape at its line. A column name an entity lacks
// fails the statement that resolves it. A mixin that is not its type's
// zero value is read where the schema returns it, with MixinAt.
package schema
