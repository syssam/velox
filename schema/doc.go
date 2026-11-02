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
// which writes the package ./gen. A schema holds only declarative structure.
// Nothing in it runs at program start. Policies and hooks are not part of it:
// they are methods, Policy and Hooks, that you write on the generated entity
// in a file of your own in the generated package, typed with the generated
// handles:
//
//	func (*User) Policy() velox.Policy[User] {
//		return velox.Policy[User]{
//			Read: func(ctx context.Context) (velox.Pred[User], error) {
//				return Users.DeletedAt.IsNull(), nil
//			},
//		}
//	}
//
// velox-gen binds them in the generated metadata when the entity declares
// them, so a wrong signature does not compile.
package schema
