package schema

import (
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// Student, Course and Enrollment are shape 5: an M2M edge declared Through()
// an edge schema whose own columns all have defaults.
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

// Enrollment is the join entity.
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
