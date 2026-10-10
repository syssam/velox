package schema

import (
	"time"

	"github.com/google/uuid"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"

	"github.com/syssam/velox/tests/orm/shapes/types"
)

// Author owns articles (O2M), edits articles (a second O2M to the same
// entity, through an optional key) and holds one passport (O2O).
type Author struct{ velox.Schema }

// Fields of the Author.
func (Author) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").NotEmpty(),
		field.Int("age").Default(30),
	}
}

// Edges of the Author.
func (Author) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("articles", Article.Type),
		edge.To("edited", Article.Type),
		edge.To("passport", Passport.Type).Unique(),
		edge.To("locker", Locker.Type).Unique(),
		edge.To("gadgets", Gadget.Type),
	}
}

// Article is the M2O side, both keys bound with Field(): one required, one
// optional. It also has two M2M edges to the same entity.
type Article struct{ velox.Schema }

// Fields of the Article.
func (Article) Fields() []velox.Field {
	return []velox.Field{
		field.String("title").NotEmpty(),
		field.Int("words").Default(0),
		field.String("section").Default("news"),
		field.Int("author_id"),
		field.Int("editor_id").Optional().Nillable(),
	}
}

// Edges of the Article.
func (Article) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("author", Author.Type).Ref("articles").Unique().Field("author_id"),
		edge.From("editor", Author.Type).Ref("edited").Unique().Field("editor_id"),
		edge.To("topics", Topic.Type),
		edge.To("banned_topics", Topic.Type).
			StorageKey(edge.Table("article_bans"), edge.Columns("art", "top")),
		edge.To("notes", Note.Type).StorageKey(edge.Column("article_ref")),
	}
}

// Passport is the O2O side holding a required key the schema does not name.
type Passport struct{ velox.Schema }

// Fields of the Passport.
func (Passport) Fields() []velox.Field {
	return []velox.Field{field.String("number")}
}

// Edges of the Passport.
func (Passport) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("owner", Author.Type).Ref("passport").Unique().Required(),
	}
}

// Locker is the O2O side holding an optional key bound to a field.
type Locker struct{ velox.Schema }

// Fields of the Locker.
func (Locker) Fields() []velox.Field {
	return []velox.Field{
		field.String("code"),
		field.Int("holder_id").Optional().Nillable().Unique(),
	}
}

// Edges of the Locker.
func (Locker) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("holder", Author.Type).Ref("locker").Unique().Field("holder_id"),
	}
}

// Topic is the inverse side of both Article M2M edges.
type Topic struct{ velox.Schema }

// Fields of the Topic.
func (Topic) Fields() []velox.Field {
	return []velox.Field{field.String("name").Unique()}
}

// Edges of the Topic.
func (Topic) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("articles", Article.Type).Ref("topics"),
		edge.From("banned_in", Article.Type).Ref("banned_topics"),
	}
}

// Note is the many side of an O2M whose key column is overridden; it has no
// edge back.
type Note struct{ velox.Schema }

// Fields of the Note.
func (Note) Fields() []velox.Field {
	return []velox.Field{field.String("text")}
}

// Node is a self-referencing tree (O2M) and a linked list (O2O).
type Node struct{ velox.Schema }

// Fields of the Node.
func (Node) Fields() []velox.Field {
	return []velox.Field{field.String("name")}
}

// Edges of the Node.
func (Node) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("children", Node.Type).From("parent").Unique(),
		edge.To("next", Node.Type).Unique().From("prev").Unique(),
	}
}

// Person holds the self-referencing edges that have one name for both
// directions (bidirectional O2O and M2M) and an M2M with two names.
type Person struct{ velox.Schema }

// Fields of the Person.
func (Person) Fields() []velox.Field {
	return []velox.Field{field.String("name")}
}

// Edges of the Person.
func (Person) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("spouse", Person.Type).Unique(),
		edge.To("friends", Person.Type),
		edge.To("following", Person.Type).From("followers"),
	}
}

// Team and Player have ids that are not ints: a UUID parent with string-keyed
// children (O2M) and an M2M between the two.
type Team struct{ velox.Schema }

// Fields of the Team.
func (Team) Fields() []velox.Field {
	return []velox.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.String("name"),
	}
}

// Edges of the Team.
func (Team) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("players", Player.Type),
		edge.To("fans", Player.Type),
		edge.To("rivals", Team.Type),
		edge.To("badges", Badge.Type),
	}
}

// Player has a string id.
type Player struct{ velox.Schema }

// Fields of the Player.
func (Player) Fields() []velox.Field {
	return []velox.Field{
		field.String("id").MaxLen(16),
		field.String("name"),
	}
}

// Edges of the Player.
func (Player) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("team", Team.Type).Ref("players").Unique(),
		edge.From("fan_of", Team.Type).Ref("fans"),
	}
}

// Badge has a small unsigned id and is reached through an M2M.
type Badge struct{ velox.Schema }

// Fields of the Badge.
func (Badge) Fields() []velox.Field {
	return []velox.Field{
		field.Uint16("id"),
		field.String("name"),
	}
}

// Edges of the Badge.
func (Badge) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("teams", Team.Type).Ref("badges"),
		edge.To("holders", Person.Type),
	}
}

// Student is one end of an M2M edge declared Through() an edge schema,
// Enrollment, that has its own id and columns with defaults; Course is the
// other end.
type Student struct{ velox.Schema }

// Fields of the Student.
func (Student) Fields() []velox.Field {
	return []velox.Field{field.String("name")}
}

// Edges of the Student.
func (Student) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("courses", Course.Type).Through("enrollments", Enrollment.Type),
	}
}

// Course is the other end of Student.courses.
type Course struct{ velox.Schema }

// Fields of the Course.
func (Course) Fields() []velox.Field {
	return []velox.Field{field.String("title")}
}

// Edges of the Course.
func (Course) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("students", Student.Type).Ref("courses").Through("enrollments", Enrollment.Type),
	}
}

// Enrollment is the join entity. Its edges are required and immutable.
type Enrollment struct{ velox.Schema }

// Fields of the Enrollment.
func (Enrollment) Fields() []velox.Field {
	return []velox.Field{
		field.Time("enrolled_at").Default(time.Now).Immutable(),
		field.Enum("status").Values("active", "dropped").Default("active"),
		field.Int("credits").Default(3),
		field.Int("student_id").Immutable(),
		field.Int("course_id").Immutable(),
	}
}

// Edges of the Enrollment.
func (Enrollment) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("student", Student.Type).Unique().Required().Immutable().Field("student_id"),
		edge.To("course", Course.Type).Unique().Required().Immutable().Field("course_id"),
	}
}

// LabelKey and WeightKey have ids with a named Go type (a string kind and an
// int kind) and every edge kind between them.
type LabelKey struct{ velox.Schema }

// Fields of the LabelKey.
func (LabelKey) Fields() []velox.Field {
	return []velox.Field{
		field.String("id").GoType(types.Label("")),
		field.String("name"),
	}
}

// Edges of the LabelKey.
func (LabelKey) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("items", WeightKey.Type),
		edge.To("weights", WeightKey.Type).StorageKey(edge.Table("label_weights")),
	}
}

// WeightKey has an int id with a named Go type.
type WeightKey struct{ velox.Schema }

// Fields of the WeightKey.
func (WeightKey) Fields() []velox.Field {
	return []velox.Field{
		field.Int("id").GoType(types.Weight(0)),
		field.String("name"),
	}
}

// Edges of the WeightKey.
func (WeightKey) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("owner", LabelKey.Type).Ref("items").Unique(),
		edge.From("labels", LabelKey.Type).Ref("weights"),
		edge.To("peers", WeightKey.Type),
	}
}

// Gadget has an optional edge bound to a key field that is Optional but not
// Nillable: the Go field is an int, the column is a nullable foreign key.
type Gadget struct{ velox.Schema }

// Fields of the Gadget.
func (Gadget) Fields() []velox.Field {
	return []velox.Field{
		field.String("name"),
		field.Int("owner_id").Optional(),
	}
}

// Edges of the Gadget.
func (Gadget) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("owner", Author.Type).Ref("gadgets").Unique().Field("owner_id"),
	}
}
