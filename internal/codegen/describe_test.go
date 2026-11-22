package codegen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
)

func TestDescribeText(t *testing.T) {
	var b bytes.Buffer
	if err := Describe(&b, mustBuild(t, User{}, Post{}, Group{}), false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"User (users)", "org_id", "immutable", "O2M → Post", "M2M → Group", "user_groups(user_id, group_id)", "pointer"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q in\n%s", want, b.String())
		}
	}
}

type partial struct{ schema.Schema }

func (partial) Fields() []schema.Field { return []schema.Field{field.Int64("a"), field.Int64("b")} }
func (partial) Indexes() []schema.Index {
	return []schema.Index{index.Fields("a").Where("a < b AND b > 0 & 1")}
}

func TestDescribeJSONDoesNotEscapeHTML(t *testing.T) {
	var b bytes.Buffer
	if err := Describe(&b, mustBuild(t, partial{}), true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `"where": "a < b AND b > 0 & 1"`) || !strings.HasSuffix(b.String(), "}\n") {
		t.Fatalf("got\n%s", b.String())
	}
}

func TestDescribeDeterministic(t *testing.T) {
	var a, b bytes.Buffer
	_ = Describe(&a, mustBuild(t, User{}, Post{}, Group{}, Node{}), true)
	_ = Describe(&b, mustBuild(t, Node{}, Group{}, Post{}, User{}), true)
	if a.String() != b.String() || !strings.HasPrefix(a.String(), "{") {
		t.Fatal("describe -json depends on the order of the schemas")
	}
}

// A relation through an edge schema names it, and the through edges are
// listed with the declared ones.
func TestDescribeEdgeSchema(t *testing.T) {
	var b bytes.Buffer
	if err := Describe(&b, mustBuild(t, Learner{}, Course{}, Enrollment{}), false); err != nil {
		t.Fatal(err)
	}
	want := `Course (courses)
  id           int64                         id  auto
  learners     M2M via Enrollment → Learner  enrollments(course_id, learner_id)
  enrollments  O2M → Enrollment              enrollments.course_id

Enrollment (enrollments)
  id          int64          id          auto
  role        enum           role
  joined_at   time           joined_at   immutable
  learner_id  int64          learner_id
  course_id   int64          course_id
  learner     M2O → Learner  enrollments.learner_id
  course      M2O → Course   enrollments.course_id

Learner (learners)
  id           int64                        id    auto
  name         string                       name
  courses      M2M via Enrollment → Course  enrollments(learner_id, course_id)
  enrollments  O2M → Enrollment             enrollments.learner_id
`
	if b.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", b.String(), want)
	}
}
