package codegen

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/codegen/loadjson"
	"github.com/syssam/velox/migrate"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/load"
)

// Learners take courses. Each link is an Enrollment, a schema of its own
// with an ID, a role and the time it was made: the edge schema of the
// many-to-many relation courses.
type Learner struct{ schema.Schema }

func (Learner) Fields() []schema.Field { return []schema.Field{field.String("name")} }
func (Learner) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("courses", Course.Type).Through("enrollments", Enrollment.Type), // esCourses
	}
}

type Course struct{ schema.Schema }

func (Course) Edges() []schema.Edge {
	return []schema.Edge{
		edge.From("learners", Learner.Type).Ref("courses").Through("enrollments", Enrollment.Type),
	}
}

type Enrollment struct{ schema.Schema }

func (Enrollment) Fields() []schema.Field {
	return []schema.Field{
		field.Enum("role").Values("owner", "member").Default("member"),
		field.Time("joined_at").Default(time.Now).Immutable(),
		field.Int64("learner_id"),
		field.Int64("course_id"),
	}
}
func (Enrollment) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("learner", Learner.Type).Unique().Required().Field("learner_id"),
		edge.To("course", Course.Type).Unique().Required().Field("course_id"),
	}
}

// Fans follow fans, through Follow. The relation is to its own schema, so
// both of Follow's edges point to Fan, and only a field's name tells which
// column each binds. Follow declares them in the other order from the
// columns'.
type Fan struct{ schema.Schema }

func (Fan) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("following", Fan.Type).Through("follows", Follow.Type).From("followers").Through("followed_by", Follow.Type),
	}
}

type Follow struct{ schema.Schema }

func (Follow) Fields() []schema.Field {
	return []schema.Field{field.Int64("follower_id"), field.Int64("fan_id")}
}
func (Follow) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("follower", Fan.Type).Unique().Required().Field("follower_id"),
		edge.To("fan", Fan.Type).Unique().Required().Field("fan_id"),
	}
}

// Pal's friends is bidirectional: one edge to its own schema, with no inverse.
type Pal struct{ schema.Schema }

func (Pal) Edges() []schema.Edge {
	return []schema.Edge{edge.To("friends", Pal.Type).Through("friendships", Palship.Type)}
}

type Palship struct{ schema.Schema }

func (Palship) Fields() []schema.Field {
	return []schema.Field{field.Int64("pal_id"), field.Int64("friend_id")}
}
func (Palship) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("pal", Pal.Type).Unique().Required().Field("pal_id"),
		edge.To("friend", Pal.Type).Unique().Required().Field("friend_id"),
	}
}

// loadModel marshals ss into the load model, which a test may change before
// it builds the model.
func loadModel(t *testing.T, ss ...schema.Interface) *loadjson.Model {
	t.Helper()
	b, err := load.Marshal("github.com/syssam/velox/internal/codegen", ss...)
	if err != nil {
		t.Fatal(err)
	}
	var m loadjson.Model
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Errors) > 0 {
		t.Fatalf("builder errors %+v", m.Errors)
	}
	return &m
}

func schemaIn(t *testing.T, m *loadjson.Model, name string) *loadjson.Schema {
	t.Helper()
	for _, s := range m.Schemas {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no schema %s", name)
	return nil
}

func edgeIn(t *testing.T, m *loadjson.Model, s, name string) *loadjson.Edge {
	t.Helper()
	for _, le := range schemaIn(t, m, s).Edges {
		if le.Name == name {
			return le
		}
	}
	t.Fatalf("schema %s has no edge %s", s, name)
	return nil
}

func fieldIn(t *testing.T, m *loadjson.Model, s, name string) *loadjson.Field {
	t.Helper()
	for _, lf := range schemaIn(t, m, s).Fields {
		if lf.Name == name {
			return lf
		}
	}
	t.Fatalf("schema %s has no field %s", s, name)
	return nil
}

func enrollments(t *testing.T) *loadjson.Model {
	t.Helper()
	return loadModel(t, Learner{}, Course{}, Enrollment{})
}

func mustBuildModel(t *testing.T, m *loadjson.Model) *Graph {
	t.Helper()
	g, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// The edge schema's table holds the relation, by the columns its two
// Required edges bind; each edge that declares Through gains a to-many edge
// to the edge schema, paired with the edge that binds its owner's column;
// and the pair of columns is unique.
func TestEdgeSchemaGraph(t *testing.T) {
	g := mustBuildModel(t, enrollments(t))
	l, c, e := g.Entity("Learner"), g.Entity("Course"), g.Entity("Enrollment")
	if !e.EdgeSchema || l.EdgeSchema || c.EdgeSchema {
		t.Fatalf("EdgeSchema: Enrollment %v, Learner %v, Course %v", e.EdgeSchema, l.EdgeSchema, c.EdgeSchema)
	}
	courses, learners := l.Edge("courses"), c.Edge("learners")
	if courses.Rel.Kind != "M2M" || courses.Rel.Table != "enrollments" || !slices.Equal(courses.Rel.Columns, []string{"learner_id", "course_id"}) || courses.Through != "enrollments" {
		t.Fatalf("courses %+v", courses)
	}
	if learners.Rel.Table != "enrollments" || !slices.Equal(learners.Rel.Columns, []string{"course_id", "learner_id"}) || learners.Through != "enrollments" {
		t.Fatalf("learners %+v", learners)
	}
	if got := len(l.Edges); got != 2 || l.Edges[1].Name != "enrollments" {
		t.Fatalf("Learner's edges %d: the through edge follows the declared ones", got)
	}
	want := &Edge{
		Name: "enrollments", GoName: "Enrollments", Target: "Enrollment", Inverse: true, Ref: "learner", Field: "learner_id",
		Rel: Rel{Kind: "O2M", Table: "enrollments", Columns: []string{"learner_id"}}, Pos: courses.Pos,
	}
	if got := l.Edge("enrollments"); !equalJSON(t, got, want) || got.Pos != want.Pos {
		t.Fatalf("Learner.enrollments\n got %s\nwant %s", jsonOf(t, got), jsonOf(t, want))
	}
	if got := c.Edge("enrollments"); got == nil || got.Ref != "course" || got.Field != "course_id" || !slices.Equal(got.Rel.Columns, []string{"course_id"}) || got.Pos != learners.Pos {
		t.Fatalf("Course.enrollments %+v", got)
	}
	// The edges that bind the keys are Enrollment's own, unchanged.
	if ed := e.Edge("learner"); ed.Ref != "" || ed.Rel.Kind != "M2O" || ed.Field != "learner_id" || ed.Rel.OnDelete != "" {
		t.Fatalf("Enrollment.learner %+v", ed)
	}
	if len(e.Indexes) != 1 {
		t.Fatalf("indexes %+v", e.Indexes)
	}
	if ix := e.Indexes[0]; ix.Name != "enrollments_enrollment_learner_id_course_id" || !ix.Derived || !ix.Unique || ix.Where != "" || !slices.Equal(ix.Columns, []string{"learner_id", "course_id"}) {
		t.Fatalf("the pair's index %+v", ix)
	}
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func equalJSON(t *testing.T, a, b any) bool { return jsonOf(t, a) == jsonOf(t, b) }

// The through edge takes the action of the edge it pairs with.
func TestEdgeSchemaOnDelete(t *testing.T) {
	m := enrollments(t)
	edgeIn(t, m, "Enrollment", "learner").OnDelete = "CASCADE"
	g := mustBuildModel(t, m)
	if got := g.Entity("Learner").Edge("enrollments").Rel.OnDelete; got != "CASCADE" {
		t.Fatalf("Learner.enrollments: on delete %q", got)
	}
	if got := g.Entity("Course").Edge("enrollments").Rel.OnDelete; got != "" {
		t.Fatalf("Course.enrollments: on delete %q", got)
	}
}

// A column no field of the edge schema is named after takes the field of
// the one edge to its side that binds one, as ent renames it.
func TestEdgeSchemaMatchByType(t *testing.T) {
	m := enrollments(t)
	fieldIn(t, m, "Enrollment", "learner_id").Name = "student_id"
	edgeIn(t, m, "Enrollment", "learner").Field = "student_id"
	g := mustBuildModel(t, m)
	if got := g.Entity("Learner").Edge("courses").Rel.Columns; !slices.Equal(got, []string{"student_id", "course_id"}) {
		t.Fatalf("courses columns %v", got)
	}
	if got := g.Entity("Course").Edge("learners").Rel.Columns; !slices.Equal(got, []string{"course_id", "student_id"}) {
		t.Fatalf("learners columns %v", got)
	}
	if ed := g.Entity("Learner").Edge("enrollments"); ed.Field != "student_id" || ed.Rel.Columns[0] != "student_id" {
		t.Fatalf("Learner.enrollments %+v", ed)
	}
	if ix := g.Entity("Enrollment").Indexes[0]; !slices.Equal(ix.Columns, []string{"student_id", "course_id"}) {
		t.Fatalf("the pair's index %+v", ix)
	}
}

// Only an edge that declares Through gains a through edge; an edge of the
// relation that does not still uses the edge schema's table.
func TestEdgeSchemaOneSide(t *testing.T) {
	for _, c := range []struct{ without, with string }{{"Course", "Learner"}, {"Learner", "Course"}} {
		m := enrollments(t)
		name := map[string]string{"Learner": "courses", "Course": "learners"}
		edgeIn(t, m, c.without, name[c.without]).Through = nil
		g := mustBuildModel(t, m)
		if ed := g.Entity(c.without).Edge("enrollments"); ed != nil {
			t.Errorf("%s has a through edge %+v", c.without, ed)
		}
		if ed := g.Entity(c.without).Edge(name[c.without]); ed.Rel.Table != "enrollments" || ed.Through != "" {
			t.Errorf("%s.%s %+v", c.without, name[c.without], ed)
		}
		if ed := g.Entity(c.with).Edge("enrollments"); ed == nil {
			t.Errorf("%s has no through edge", c.with)
		}
		if !g.Entity("Enrollment").EdgeSchema || len(g.Entity("Enrollment").Indexes) != 1 {
			t.Errorf("Enrollment %+v", g.Entity("Enrollment"))
		}
	}
}

// On a relation to its own schema, each column is matched by its field's
// name alone, and each edge's columns are its own owner's first.
func TestEdgeSchemaSelf(t *testing.T) {
	g := mustBuild(t, Fan{}, Follow{})
	f := g.Entity("Fan")
	following, followers := f.Edge("following"), f.Edge("followers")
	if following.Rel.Table != "follows" || !slices.Equal(following.Rel.Columns, []string{"fan_id", "follower_id"}) {
		t.Fatalf("following %+v", following)
	}
	if followers.Rel.Table != "follows" || !slices.Equal(followers.Rel.Columns, []string{"follower_id", "fan_id"}) {
		t.Fatalf("followers %+v", followers)
	}
	if ed := f.Edge("follows"); ed == nil || ed.Ref != "fan" || ed.Field != "fan_id" || ed.Target != "Follow" {
		t.Fatalf("follows %+v", ed)
	}
	if ed := f.Edge("followed_by"); ed == nil || ed.Ref != "follower" || ed.Field != "follower_id" {
		t.Fatalf("followed_by %+v", ed)
	}
	if ix := g.Entity("Follow").Indexes; len(ix) != 1 || !slices.Equal(ix[0].Columns, []string{"fan_id", "follower_id"}) {
		t.Fatalf("Follow's indexes %+v", ix)
	}
}

// A unique index the edge schema declares over the two columns, in either
// order and over every row, stands for the pair's; a partial one, or one
// that is not unique, does not.
func TestEdgeSchemaDeclaredIndex(t *testing.T) {
	for _, c := range []struct {
		name    string
		ix      loadjson.Index
		derived bool
	}{
		{"reversed", loadjson.Index{Fields: []string{"course_id", "learner_id"}, Unique: true}, false},
		{"in order", loadjson.Index{Fields: []string{"learner_id", "course_id"}, Unique: true}, false},
		{"by edges", loadjson.Index{Edges: []string{"course", "learner"}, Unique: true}, false},
		{"partial", loadjson.Index{Fields: []string{"learner_id", "course_id"}, Unique: true, Where: "role = 'owner'"}, true},
		{"not unique", loadjson.Index{Fields: []string{"course_id", "learner_id"}}, true},
		{"three columns", loadjson.Index{Fields: []string{"course_id", "learner_id", "role"}, Unique: true}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := enrollments(t)
			ix := c.ix
			schemaIn(t, m, "Enrollment").Indexes = []*loadjson.Index{&ix}
			e := mustBuildModel(t, m).Entity("Enrollment")
			want := 1
			if c.derived {
				want = 2
			}
			if len(e.Indexes) != want {
				t.Fatalf("%d indexes, want %d: %s", len(e.Indexes), want, jsonOf(t, e.Indexes))
			}
			if c.derived {
				if ix := e.Indexes[1]; !ix.Unique || ix.Where != "" || !slices.Equal(ix.Columns, []string{"learner_id", "course_id"}) {
					t.Fatalf("the pair's index %+v", ix)
				}
			}
		})
	}
}

// Each rule of an edge schema is one error, at the edge that declares
// Through, which says what to write.
func TestEdgeSchemaRules(t *testing.T) {
	other := "github.com/syssam/velox/internal/codegen/internal/other"
	for _, c := range []struct {
		name   string
		ss     []schema.Interface // Learner, Course and Enrollment when nil
		change func(t *testing.T, m *loadjson.Model)
		at     [2]string // the schema and edge the error is at
		want   string
	}{
		{"63 not many-to-many", nil, func(t *testing.T, m *loadjson.Model) {
			inv := edgeIn(t, m, "Course", "learners")
			inv.Unique, inv.Through = true, nil
		}, [2]string{"Learner", "courses"}, `Through is allowed only on a many-to-many edge; this edge is O2M`},
		{"64 unknown schema", nil, func(t *testing.T, m *loadjson.Model) {
			edgeIn(t, m, "Learner", "courses").Through.Type = "Nope"
			edgeIn(t, m, "Course", "learners").Through = nil
		}, [2]string{"Learner", "courses"}, `Through names Nope, which is not a schema of this package`},
		{"64 other package", nil, func(t *testing.T, m *loadjson.Model) {
			th := edgeIn(t, m, "Learner", "courses").Through
			th.Type, th.TypePkg = "Thing", other
			edgeIn(t, m, "Course", "learners").Through = nil
		}, [2]string{"Learner", "courses"}, `Through names other.Thing, which is not a schema of this package`},
		{"64 own schema", nil, func(t *testing.T, m *loadjson.Model) {
			edgeIn(t, m, "Learner", "courses").Through.Type = "Learner"
			edgeIn(t, m, "Course", "learners").Through = nil
		}, [2]string{"Learner", "courses"}, `an edge cannot go through its own schema`},
		{"64 target's schema", nil, func(t *testing.T, m *loadjson.Model) {
			edgeIn(t, m, "Learner", "courses").Through.Type = "Course"
			edgeIn(t, m, "Course", "learners").Through = nil
		}, [2]string{"Learner", "courses"}, `an edge cannot go through its target's schema`},
		{"65 an edge's name", nil, func(t *testing.T, m *loadjson.Model) {
			edgeIn(t, m, "Learner", "courses").Through.Name = "courses"
		}, [2]string{"Learner", "courses"}, `Through's name "courses" is taken by edge "courses" of Learner`},
		{"65 a field's name", nil, func(t *testing.T, m *loadjson.Model) {
			edgeIn(t, m, "Learner", "courses").Through.Name = "name"
		}, [2]string{"Learner", "courses"}, `Through's name "name" is taken by field "name" of Learner`},
		{"65 a Go name", nil, func(t *testing.T, m *loadjson.Model) {
			edgeIn(t, m, "Learner", "courses").Through.Name = "Name"
		}, [2]string{"Learner", "courses"}, `Through's name "Name" has the Go name Name of field "name" of Learner`},
		{"65 an edge's Go name", nil, func(t *testing.T, m *loadjson.Model) {
			edgeIn(t, m, "Learner", "courses").Through.Name = "Courses"
		}, [2]string{"Learner", "courses"}, `Through's name "Courses" has the Go name Courses of edge "courses" of Learner`},
		{"65 no name", nil, func(t *testing.T, m *loadjson.Model) {
			edgeIn(t, m, "Learner", "courses").Through.Name = ""
		}, [2]string{"Learner", "courses"}, `Through needs a name`},
		{"65 the through edge declared", nil, func(t *testing.T, m *loadjson.Model) {
			s := schemaIn(t, m, "Learner")
			courses := edgeIn(t, m, "Learner", "courses")
			s.Edges = append(s.Edges, &loadjson.Edge{Name: "enrollments", Type: "Enrollment", TypePkg: courses.TypePkg, Inverse: true, Ref: "learner", Pos: courses.Pos})
		}, [2]string{"Learner", "courses"}, `Through's name "enrollments" is taken by edge "enrollments" of Learner`},
		{"66 a second relation", nil, func(t *testing.T, m *loadjson.Model) {
			s := schemaIn(t, m, "Learner")
			courses := edgeIn(t, m, "Learner", "courses")
			second := *courses
			second.Name, second.Through = "electives", &loadjson.Through{Name: "elective_enrollments", Type: "Enrollment", TypePkg: courses.TypePkg}
			s.Edges = append(s.Edges, &second)
			c := schemaIn(t, m, "Course")
			inv := *edgeIn(t, m, "Course", "learners")
			inv.Name, inv.Ref, inv.Through = "elective_learners", "electives", nil
			c.Edges = append(c.Edges, &inv)
		}, [2]string{"Learner", "electives"}, `Enrollment is already the edge schema of Learner.courses`},
		{"66 the inverse names another", nil, func(t *testing.T, m *loadjson.Model) {
			edgeIn(t, m, "Course", "learners").Through.Type = "Learner"
		}, [2]string{"Course", "learners"}, `Enrollment is already the edge schema of Learner.courses`},
		{"67 no edge", nil, func(t *testing.T, m *loadjson.Model) {
			s := schemaIn(t, m, "Enrollment")
			s.Edges = s.Edges[:1]
		}, [2]string{"Learner", "courses"}, `edge schema Enrollment has no edge to Course bound to field "course_id"; declare edge.To("course", Course.Type).Unique().Required().Field("course_id")`},
		{"67 not required", nil, func(t *testing.T, m *loadjson.Model) {
			edgeIn(t, m, "Enrollment", "course").Required = false
			f := fieldIn(t, m, "Enrollment", "course_id")
			f.Optional, f.Nillable = true, true
		}, [2]string{"Learner", "courses"}, `edge "course" of Enrollment (@Enrollment.course) must be Required: a link has both ends`},
		{"67 two edges to the side", nil, func(t *testing.T, m *loadjson.Model) {
			fieldIn(t, m, "Enrollment", "learner_id").Name = "student_id"
			edgeIn(t, m, "Enrollment", "learner").Field = "student_id"
			s := schemaIn(t, m, "Enrollment")
			s.Fields = append(s.Fields, &loadjson.Field{Name: "tutor_id", Type: "int64", Position: loadjson.Position{Index: 4}})
			tutor := *edgeIn(t, m, "Enrollment", "learner")
			tutor.Name, tutor.Field = "tutor", "tutor_id"
			s.Edges = append(s.Edges, &tutor)
		}, [2]string{"Learner", "courses"}, `edge schema Enrollment has no edge to Learner bound to field "learner_id"; bind field "learner_id" on edge "learner"`},
		{"67 own schema by name alone", []schema.Interface{Fan{}, Follow{}}, func(t *testing.T, m *loadjson.Model) {
			// Follow's other edge to Fan is the only one left, and still
			// not taken: on a relation to its own schema, the type
			// decides nothing.
			fieldIn(t, m, "Follow", "follower_id").Name = "followee_id"
			edgeIn(t, m, "Follow", "follower").Field = "followee_id"
		}, [2]string{"Fan", "following"}, `edge schema Follow has no edge to Fan bound to field "follower_id"; bind field "follower_id" on edge "follower"`},
		{"67 one-to-one key", nil, func(t *testing.T, m *loadjson.Model) {
			s := schemaIn(t, m, "Learner")
			courses := edgeIn(t, m, "Learner", "courses")
			s.Edges = append(s.Edges, &loadjson.Edge{Name: "enrollment", Type: "Enrollment", TypePkg: courses.TypePkg, Unique: true, Pos: courses.Pos})
			key := edgeIn(t, m, "Enrollment", "learner")
			key.Inverse, key.Ref = true, "enrollment"
		}, [2]string{"Learner", "courses"}, `edge "learner" of Enrollment (@Enrollment.learner) must be many-to-one: a Learner has many links; remove Unique from Learner.enrollment`},
		{"35 under Through", []schema.Interface{Fan{}, Follow{}}, func(t *testing.T, m *loadjson.Model) {
			edgeIn(t, m, "Fan", "followers").Name = "fans"
		}, [2]string{"Fan", "fans"}, `the relation's two columns are both named fan_id, after this edge's name; rename the edge, as Through takes its columns from the edge names`},
		{"68 table", nil, func(t *testing.T, m *loadjson.Model) {
			edgeIn(t, m, "Learner", "courses").Table = "learner_courses"
		}, [2]string{"Learner", "courses"}, `Through takes its table and columns from Enrollment; remove StorageKey (edge.Table, edge.Columns, edge.Symbols)`},
		{"68 columns on the inverse", nil, func(t *testing.T, m *loadjson.Model) {
			edgeIn(t, m, "Course", "learners").Columns = []string{"a_id", "b_id"}
		}, [2]string{"Course", "learners"}, `Through takes its table and columns from Enrollment; remove StorageKey (edge.Table, edge.Columns, edge.Symbols)`},
		{"68 symbols", nil, func(t *testing.T, m *loadjson.Model) {
			edgeIn(t, m, "Learner", "courses").Symbols = []string{"a", "b"}
		}, [2]string{"Learner", "courses"}, `Through takes its table and columns from Enrollment; remove StorageKey (edge.Table, edge.Columns, edge.Symbols)`},
		{"69 bidirectional", []schema.Interface{Pal{}, Palship{}}, nil,
			[2]string{"Pal", "friends"}, `Through on a bidirectional edge (an M2M to its own schema with no inverse) is not supported; give it an inverse edge, or drop Through`},
	} {
		t.Run(c.name, func(t *testing.T) {
			ss := c.ss
			if ss == nil {
				ss = []schema.Interface{Learner{}, Course{}, Enrollment{}}
			}
			m := loadModel(t, ss...)
			if c.change != nil {
				c.change(t, m)
			}
			pos := edgeIn(t, m, c.at[0], c.at[1]).Pos
			_, err := Build(m)
			want := pos + ": schema " + c.at[0] + `: edge "` + c.at[1] + `": ` + atPos.ReplaceAllStringFunc(c.want, func(ref string) string {
				s, e, _ := strings.Cut(ref[1:], ".")
				return edgeIn(t, m, s, e).Pos
			})
			if err == nil || err.Error() != want {
				t.Fatalf("got\n%v\nwant\n%s", err, want)
			}
		})
	}
}

// atPos is a reference, in a rule's text, to the position of an edge:
// @Schema.edge.
var atPos = regexp.MustCompile(`@[A-Za-z]+\.[a-z_]+`)

// The error is at the line that declares Through.
func TestEdgeSchemaRulePos(t *testing.T) {
	m := enrollments(t)
	edgeIn(t, m, "Learner", "courses").Table = "x"
	_, err := Build(m)
	ge, ok := errors.AsType[*Error](err)
	if !ok {
		t.Fatalf("got %v", err)
	}
	src, rerr := os.ReadFile("edgeschema_test.go")
	if rerr != nil {
		t.Fatal(rerr)
	}
	line := slices.IndexFunc(strings.Split(string(src), "\n"), func(l string) bool { return strings.HasSuffix(l, "// esCourses") }) + 1
	if want := "edgeschema_test.go:" + strconv.Itoa(line); !strings.HasSuffix(ge.Pos, want) {
		t.Fatalf("pos %q, want …%s", ge.Pos, want)
	}
}

// The edge schema's table is an entity's: its own ID, the keys of its two
// edges with no action on delete unless they say one, and the unique pair.
// No join table is written.
func TestTablesEdgeSchema(t *testing.T) {
	for _, d := range allDialects {
		s, err := Tables(mustBuildModel(t, enrollments(t)), d)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, tb := range s.Tables {
			names = append(names, tb.Name)
		}
		if !slices.Equal(names, []string{"courses", "enrollments", "learners"}) {
			t.Fatalf("%v: tables %v", d, names)
		}
		tb := table(t, s, "enrollments")
		if !slices.Equal(tb.PrimaryKey, []string{"id"}) {
			t.Errorf("%v: primary key %v", d, tb.PrimaryKey)
		}
		for _, c := range []string{"id", "role", "joined_at", "learner_id", "course_id"} {
			column(t, tb, c)
		}
		if len(tb.ForeignKeys) != 2 {
			t.Fatalf("%v: foreign keys %+v", d, tb.ForeignKeys)
		}
		for _, fk := range tb.ForeignKeys {
			if fk.OnDelete != "" {
				t.Errorf("%v: %s on delete %q, want none", d, fk.Name, fk.OnDelete)
			}
		}
		var unique []string
		for _, ix := range tb.Indexes {
			if ix.Unique {
				unique = append(unique, strings.Join(ix.Columns, ","))
			}
		}
		if !slices.Equal(unique, []string{"learner_id,course_id"}) {
			t.Errorf("%v: unique indexes %v", d, unique)
		}
	}
}

// The migration that creates the edge schema's tables, on each dialect.
func TestEdgeSchemaMigrationGolden(t *testing.T) {
	for _, d := range allDialects {
		s, err := Tables(mustBuildModel(t, enrollments(t)), d)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := migrate.NewPlan(&migrate.Schema{Dialect: d}, s, migrate.PlanOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var got bytes.Buffer
		for _, sc := range plan.Scripts {
			for _, st := range sc.Statements {
				got.WriteString(st.SQL + ";\n")
			}
		}
		path := filepath.Join("testdata", "edgeschema", d.String()+".sql")
		if *update {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, got.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got.Bytes(), want) {
			t.Errorf("%v: the migration differs from %s; run with -update and read the diff:\n%s", d, path, got.Bytes())
		}
	}
}

// A plain many-to-many relation whose join table becomes an edge schema of
// the same name changes the table's primary key, which Diff refuses: the
// migration is written by hand.
func TestEdgeSchemaConversionRefused(t *testing.T) {
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
		plain := enrollments(t)
		edgeIn(t, plain, "Learner", "courses").Through = nil
		edgeIn(t, plain, "Course", "learners").Through = nil
		plain.Schemas = slices.DeleteFunc(plain.Schemas, func(s *loadjson.Schema) bool { return s.Name == "Enrollment" })
		from, err := Tables(mustBuildModel(t, plain), d)
		if err != nil {
			t.Fatal(err)
		}
		if table(t, from, "learner_courses") == nil {
			t.Fatal("no join table")
		}
		m := enrollments(t)
		schemaIn(t, m, "Enrollment").Annotations = map[string]json.RawMessage{"Table": json.RawMessage(`{"table":"learner_courses"}`)}
		to, err := Tables(mustBuildModel(t, m), d)
		if err != nil {
			t.Fatal(err)
		}
		_, err = migrate.Diff(from, to, migrate.DiffOptions{})
		if me, ok := errors.AsType[*migrate.Error](err); !ok || me.Code != "primary-key" {
			t.Errorf("%v: got %v, want a primary-key refusal", d, err)
		}
	}
}

// A declared index over the pair in its order that is not unique stands
// where the pair's unique index goes: it is refused at its own line. The
// derived index's line is the edge's that names Through.
func TestEdgeSchemaNonUniqueIndex(t *testing.T) {
	m := enrollments(t)
	ix := loadjson.Index{Fields: []string{"learner_id", "course_id"}, Pos: "schema.go:7"}
	schemaIn(t, m, "Enrollment").Indexes = []*loadjson.Index{&ix}
	_, err := Build(m)
	want := `schema.go:7: schema Enrollment: index 0: over (learner_id, course_id) must be Unique: Through's links are unique pairs`
	if err == nil || err.Error() != want {
		t.Fatalf("got\n%v\nwant\n%s", err, want)
	}
	// A unique index over the pair, in either order, stands for the pair's
	// wherever it is declared: the plain index beside it is taken.
	m = enrollments(t)
	plain := loadjson.Index{Fields: []string{"learner_id", "course_id"}}
	unique := loadjson.Index{Fields: []string{"course_id", "learner_id"}, Unique: true}
	schemaIn(t, m, "Enrollment").Indexes = []*loadjson.Index{&plain, &unique}
	if ixs := mustBuildModel(t, m).Entity("Enrollment").Indexes; len(ixs) != 2 {
		t.Fatalf("indexes %s", jsonOf(t, ixs))
	}
	e := mustBuildModel(t, enrollments(t)).Entity("Enrollment")
	if got, want := e.Indexes[0].Pos, edgeIn(t, enrollments(t), "Learner", "courses").Pos; got != want {
		t.Fatalf("the pair's index is at %q, want %q", got, want)
	}
}

// A key edge may be the inverse of an edge its side declares: the through
// edge pairs with it, and its foreign key is listed once.
func TestEdgeSchemaInverseKeyEdge(t *testing.T) {
	m := enrollments(t)
	courses := edgeIn(t, m, "Learner", "courses")
	key := edgeIn(t, m, "Enrollment", "learner")
	key.Inverse, key.Ref = true, "links"
	s := schemaIn(t, m, "Learner")
	s.Edges = append(s.Edges, &loadjson.Edge{Name: "links", Type: "Enrollment", TypePkg: courses.TypePkg, Pos: courses.Pos})
	g := mustBuildModel(t, m)
	l := g.Entity("Learner")
	if ed := l.Edge("enrollments"); ed == nil || ed.Ref != "learner" || ed.Field != "learner_id" {
		t.Fatalf("Learner.enrollments %+v", ed)
	}
	if ed := l.Edge("links"); ed.Rel.Kind != "O2M" || ed.Ref != "learner" || ed.Field != "learner_id" {
		t.Fatalf("Learner.links %+v", ed)
	}
	if ed := g.Entity("Enrollment").Edge("learner"); ed.Ref != "links" || ed.Rel.Kind != "M2O" {
		t.Fatalf("Enrollment.learner %+v", ed)
	}
	for _, d := range allDialects {
		s, err := Tables(g, d)
		if err != nil {
			t.Fatal(err)
		}
		if fks := table(t, s, "enrollments").ForeignKeys; len(fks) != 2 {
			t.Errorf("%v: foreign keys %+v", d, fks)
		}
	}
}
