// Package edge provides fluent builders for defining entity relationships in Velox ORM.
//
// Edges define relationships between entities. They determine how entities
// are connected in the database through foreign keys and join tables.
//
// # Edge Types
//
// There are two primary edge types:
//
//   - edge.To: Defines the association (forward direction)
//   - edge.From: Defines the back-reference (inverse direction)
//
// # Relationship Cardinality
//
// Relationships are determined by the Unique() modifier:
//
//	// One-to-Many (default): User has many Posts
//	edge.To("posts", Post.Type)
//
//	// One-to-One: User has one Profile
//	edge.To("profile", Profile.Type).Unique()
//
//	// Many-to-One: Post belongs to User
//	edge.From("author", User.Type).Ref("posts").Unique()
//
//	// Many-to-Many: User has many Groups
//	edge.To("groups", Group.Type)
//
// # Bidirectional Edges
//
// Most edges are bidirectional, requiring edges on both entities:
//
//	// User schema
//	func (User) Edges() []velox.Edge {
//	    return []velox.Edge{
//	        edge.To("posts", Post.Type),  // User -> Posts (O2M)
//	    }
//	}
//
//	// Post schema
//	func (Post) Edges() []velox.Edge {
//	    return []velox.Edge{
//	        edge.From("author", User.Type).  // Post -> User (M2O)
//	            Ref("posts").
//	            Unique(),
//	    }
//	}
//
// # Edge Options
//
// Edges support various configuration options:
//
//	edge.To("comments", Comment.Type).
//	    Required().          // Edge is required
//	    Immutable().         // Cannot be changed after creation
//	    Comment("Post comments")
//
// # Edge Fields (Foreign Keys)
//
// You can expose the foreign key as a field:
//
//	// In User schema fields
//	field.Int64("department_id").Optional()
//
//	// In User schema edges
//	edge.From("department", Department.Type).
//	    Ref("employees").
//	    Field("department_id").
//	    Unique()
//
// # Foreign Key Actions
//
// Control what happens when referenced entities are deleted:
//
//	import "github.com/syssam/velox/dialect/sqlschema"
//
//	edge.To("posts", Post.Type).
//	    Annotations(sqlschema.OnDelete(sqlschema.Cascade))
//
// Available actions:
//   - sqlschema.Cascade: Delete related entities
//   - sqlschema.SetNull: Set foreign key to NULL
//   - sqlschema.Restrict: Prevent deletion
//   - sqlschema.NoAction: Database default
//   - sqlschema.SetDefault: Set to default value
//
// # Through Edges (Join Tables)
//
// For M2M relationships with additional fields:
//
//	// Define edge through a join table entity
//	edge.To("groups", Group.Type).
//	    Through("memberships", Membership.Type)
//
// # Self-Referential Edges
//
// Entities can reference themselves:
//
//	// User has followers (other Users)
//	edge.To("followers", User.Type)
//	edge.To("following", User.Type)
//
// # Storage Key Customization
//
// Customize the foreign key column name:
//
//	edge.From("owner", User.Type).
//	    Ref("pets").
//	    Unique().
//	    StorageKey(edge.Column("user_id"))
//
// For M2M edges, customize the join table:
//
//	edge.To("groups", Group.Type).
//	    StorageKey(
//	        edge.Table("user_groups"),
//	        edge.Columns("user_id", "group_id"),
//	    )
package edge
