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
// Policy, Hooks and Interceptors, typed with the generated entity and
// handles of package gen:
//
//	func (User) Policy() velox.Policy[gen.User] {
//		return velox.Policy[gen.User]{
//			Read: func(ctx context.Context) (velox.Pred[gen.User], error) {
//				return gen.Users.DeletedAt.IsNull(), nil
//			},
//		}
//	}
//
//	func (User) Hooks() []velox.Hook[gen.User] { ... }
//
//	func (User) Interceptors() []velox.Interceptor[gen.User] { ... }
//
// The method takes nothing and returns velox.Policy[gen.E],
// []velox.Hook[gen.E] or []velox.Interceptor[gen.E], E being the schema;
// velox-gen refuses another shape. A many-to-many join entity has no schema: declare an empty struct
// of its name, type UserFriend struct{}, and the methods on it.
//
// The schema package then imports gen, so gen does not import it.
// velox-gen writes gen/runtime, which imports both and binds the methods,
// and the defaults and validators gen cannot call by name, when the program
// starts. A program imports it once, for its effect, in its main package
// and in each test package that uses the entities, as
// import _ "<module>/gen/runtime". A program that does not fails at its
// first statement on an entity, rather than run without the policies. The
// schema package may not import gen/runtime, directly or through another
// package, as it imports the schema. velox-gen reads the schema without
// these methods and what only they use, so it generates even when gen is
// stale or missing.
//
// A mixin declares behaviour too, for every entity that lists it, with the
// same methods made generic over the entity. It names its columns by name,
// with velox.Field:
//
//	func (InOrg) Policy[E any]() velox.Policy[E] {
//		org := velox.Field[E, int64]("org_id")
//		return velox.Policy[E]{Read: func(ctx context.Context) (velox.Pred[E], error) {
//			return org.EQ(orgOf(ctx)), nil
//		}}
//	}
//
// velox runs an entity's mixins' behaviour first, in Mixin order, then its
// schema's: the policies are ANDed, the hooks run in that order, the first
// outermost, and the interceptors' conditions are ANDed. velox-gen refuses
// a method of another shape, and a literal column name that an entity
// listing the mixin lacks, or of another type, each at its line. A mixin
// that is not its type's zero value is read where the schema returns it,
// with MixinAt. A schema that does not import gen and whose behaviour comes
// from mixins alone needs no gen/runtime: gen binds it.
package schema
