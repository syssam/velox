package shapes

import (
	"context"
	"testing"

	"github.com/syssam/velox/vx/tests/shapes/db/predicate"

	client "github.com/syssam/velox/vx/tests/shapes/db"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/vx/orm"
	"github.com/syssam/velox/vx/tests/shapes/db/course"
	"github.com/syssam/velox/vx/tests/shapes/db/enrollment"
	"github.com/syssam/velox/vx/tests/shapes/db/entity"
	"github.com/syssam/velox/vx/tests/shapes/db/node"
	"github.com/syssam/velox/vx/tests/shapes/db/person"
	"github.com/syssam/velox/vx/tests/shapes/db/student"
)

func nodeNames(ns []*entity.Node) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.Name
	}
	return out
}

func personNames(ps []*entity.Person) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return out
}

// A self-referencing O2M (a tree): root -> c1, c2; c1 -> g1.
func TestSelfRef_Tree(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	mk := func(name string, sets ...predicate.NodeSetter) *entity.Node {
		return must(client.Node.Create(db).Set(node.Name.Set(name)).Set(sets...).Save(ctx))
	}
	root := mk("root")
	c1 := mk("c1", node.Parent.Set(root.ID))
	c2 := mk("c2", node.Parent.Set(root.ID))
	g1 := mk("g1", node.Parent.Set(c1.ID))
	names := func(ps ...predicate.Node) []string {
		return nodeNames(must(client.Node.Query(db).Where(ps...).OrderBy(node.Name.Asc()).All(ctx)))
	}
	none := []string{}

	// A condition on an edge nested in a condition on the same edge: the
	// nodes that have a grandchild, and the other spellings of the question.
	// (The SQL layer used to give the inner subquery the outer one's alias,
	// and the first of these matched nothing.)
	assert.Equal(t, []string{"root"}, names(node.Children.HasWith(node.Children.Has())))
	assert.Equal(t, []string{"root"}, names(node.Children.HasWith(node.Children.HasWith(node.Name.EQ("g1")))))
	assert.Equal(t, []string{"g1"}, names(node.Parent.HasWith(node.Parent.Has())))
	assert.Equal(t, []string{"c1", "root"}, names(node.Children.HasWith(node.Parent.Has())))

	// Traversal, from created and from queried entities.
	assert.Equal(t, []string{"c1", "c2"}, nodeNames(must(root.QueryChildren(db).OrderBy(node.Name.Asc()).All(ctx))))
	assert.Equal(t, "root", must(c1.QueryParent(db).Only(ctx)).Name, "the key of the entity Create returned")
	_, err := root.QueryParent(db).Only(ctx)
	assert.ErrorIs(t, err, orm.ErrNotFound)
	assert.Empty(t, must(g1.QueryChildren(db).All(ctx)))
	qg1 := must(client.Node.Query(db).Where(node.Name.EQ("g1")).Only(ctx))
	assert.Equal(t, "c1", must(qg1.QueryParent(db).Only(ctx)).Name)
	assert.Equal(t, "root", must(must(qg1.QueryParent(db).Only(ctx)).QueryParent(db).Only(ctx)).Name)

	// Eager loading, both directions, nested on itself.
	rows := must(client.Node.Query(db).OrderBy(node.Name.Asc()).
		WithParent(client.Node.Query(db).WithParent()).WithChildren(client.Node.Query(db).OrderBy(node.Name.Desc()).WithChildren()).All(ctx))
	require.Len(t, rows, 4)
	qc1, qc2, qg, qroot := rows[0], rows[1], rows[2], rows[3]
	assert.Equal(t, []string{"c2", "c1"}, nodeNames(qroot.Edges.Children))
	assert.Equal(t, []string{"g1"}, nodeNames(qroot.Edges.Children[1].Edges.Children))
	assert.Empty(t, qroot.Edges.Children[0].Edges.Children)
	_, err = qroot.Edges.ParentOrErr()
	assert.ErrorIs(t, err, orm.ErrNotFound)
	assert.Equal(t, "root", qc1.Edges.Parent.Name)
	assert.Equal(t, "root", qc2.Edges.Parent.Name)
	_, err = qc1.Edges.Parent.Edges.ParentOrErr()
	assert.ErrorIs(t, err, orm.ErrNotFound, "root's parent: loaded, and absent")
	assert.Equal(t, "c1", qg.Edges.Parent.Name)
	assert.Equal(t, "root", qg.Edges.Parent.Edges.Parent.Name)
	assert.Equal(t, []string{"g1"}, nodeNames(qc1.Edges.Children))
	kids, err := qg.Edges.ChildrenOrErr()
	require.NoError(t, err)
	assert.Empty(t, kids)
	// Per-parent limit on the tree.
	rows = must(client.Node.Query(db).Where(node.Children.Has()).OrderBy(node.Name.Asc()).
		WithChildren(client.Node.Query(db).OrderBy(node.Name.Desc()).Limit(1)).All(ctx))
	require.Len(t, rows, 2)
	assert.Equal(t, []string{"g1"}, nodeNames(rows[0].Edges.Children))
	assert.Equal(t, []string{"c2"}, nodeNames(rows[1].Edges.Children))

	// Has / HasWith: the subquery is on the table of the outer query.
	assert.Equal(t, []string{"c1", "root"}, names(node.Children.Has()))
	assert.Equal(t, []string{"c2", "g1"}, names(node.Not(node.Children.Has())))
	assert.Equal(t, []string{"c1", "c2", "g1"}, names(node.Parent.Has()))
	assert.Equal(t, []string{"root"}, names(node.Not(node.Parent.Has())))
	assert.Equal(t, []string{"c1"}, names(node.Children.HasWith(node.Name.EQ("g1"))))
	assert.Equal(t, []string{"c1", "c2"}, names(node.Parent.HasWith(node.Name.EQ("root"))))
	assert.Equal(t, none, names(node.Parent.HasWith(node.Name.EQ("g1"))))
	assert.Equal(t, []string{"c1"}, names(node.Parent.Has(), node.Children.Has()), "an inner node")
	// Nested on itself, with a field predicate at the bottom.
	assert.Equal(t, []string{"root"}, names(node.Children.HasWith(node.Children.HasWith(node.Name.EQ("g1")))))
	assert.Equal(t, []string{"g1"}, names(node.Parent.HasWith(node.Parent.HasWith(node.Name.EQ("root")))))
	assert.Equal(t, []string{"c2"}, names(node.Parent.HasWith(node.Children.HasWith(node.Name.EQ("c1"))), node.Name.NEQ("c1")), "siblings of c1")

	// Edge changes on update.
	c3 := mk("c3")
	must(client.Node.Update(db).Set(node.Children.Add(c3.ID)).Where(node.ID.EQ(root.ID)).Exec(ctx))
	assert.Equal(t, []string{"c1", "c2", "c3"}, nodeNames(must(root.QueryChildren(db).OrderBy(node.Name.Asc()).All(ctx))))
	must(client.Node.Update(db).Set(node.Children.Remove(c2.ID, c3.ID)).Where(node.ID.EQ(root.ID)).Exec(ctx))
	assert.Equal(t, []string{"c1"}, nodeNames(must(root.QueryChildren(db).All(ctx))))
	got := must(client.Node.Update(db).Set(node.Parent.Set(c1.ID)).Where(node.ID.EQ(c2.ID)).SaveOne(ctx))
	assert.Equal(t, "c1", must(got.QueryParent(db).Only(ctx)).Name)
	assert.Equal(t, []string{"c2", "g1"}, nodeNames(must(c1.QueryChildren(db).OrderBy(node.Name.Asc()).All(ctx))))
	got = must(client.Node.Update(db).Set(node.Parent.Clear()).Where(node.ID.EQ(g1.ID)).SaveOne(ctx))
	_, err = got.QueryParent(db).Only(ctx)
	assert.ErrorIs(t, err, orm.ErrNotFound)
	must(client.Node.Update(db).Set(node.Children.Clear()).Where(node.ID.EQ(c1.ID)).Exec(ctx))
	assert.Equal(t, []string{"c1"}, names(node.Parent.Has()), "only c1 still has a parent")
}

// A self-referencing O2O with two names (a linked list): n1 -> n2 -> n3.
func TestSelfRef_List(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	mk := func(name string, sets ...predicate.NodeSetter) *entity.Node {
		return must(client.Node.Create(db).Set(node.Name.Set(name)).Set(sets...).Save(ctx))
	}
	n1 := mk("n1")
	n2 := mk("n2", node.Prev.Set(n1.ID)) // from the side holding the key
	n3 := mk("n3")
	must(client.Node.Update(db).Set(node.Next.Set(n3.ID)).Where(node.ID.EQ(n2.ID)).Exec(ctx)) // from the other side
	names := func(ps ...predicate.Node) []string {
		return nodeNames(must(client.Node.Query(db).Where(ps...).OrderBy(node.Name.Asc()).All(ctx)))
	}

	assert.Equal(t, "n2", must(n1.QueryNext(db).Only(ctx)).Name)
	assert.Equal(t, "n1", must(n2.QueryPrev(db).Only(ctx)).Name, "the key of the entity Create returned")
	assert.Equal(t, "n3", must(n2.QueryNext(db).Only(ctx)).Name)
	_, err := n3.QueryNext(db).Only(ctx)
	assert.ErrorIs(t, err, orm.ErrNotFound)
	_, err = n1.QueryPrev(db).Only(ctx)
	assert.ErrorIs(t, err, orm.ErrNotFound)
	q3 := must(client.Node.Query(db).Where(node.Name.EQ("n3")).Only(ctx))
	assert.Equal(t, "n2", must(q3.QueryPrev(db).Only(ctx)).Name)

	rows := must(client.Node.Query(db).OrderBy(node.Name.Asc()).WithNext(client.Node.Query(db).WithNext()).WithPrev(client.Node.Query(db).WithPrev()).All(ctx))
	require.Len(t, rows, 3)
	assert.Equal(t, "n2", rows[0].Edges.Next.Name)
	assert.Equal(t, "n3", rows[0].Edges.Next.Edges.Next.Name)
	_, err = rows[0].Edges.PrevOrErr()
	assert.ErrorIs(t, err, orm.ErrNotFound)
	assert.Equal(t, "n3", rows[1].Edges.Next.Name)
	assert.Equal(t, "n1", rows[1].Edges.Prev.Name)
	_, err = rows[2].Edges.NextOrErr()
	assert.ErrorIs(t, err, orm.ErrNotFound)
	assert.Equal(t, "n2", rows[2].Edges.Prev.Name)
	assert.Equal(t, "n1", rows[2].Edges.Prev.Edges.Prev.Name)

	assert.Equal(t, []string{"n1", "n2"}, names(node.Next.Has()))
	assert.Equal(t, []string{"n2", "n3"}, names(node.Prev.Has()))
	assert.Equal(t, []string{"n2"}, names(node.Next.HasWith(node.Name.EQ("n3"))))
	assert.Equal(t, []string{"n2"}, names(node.Prev.HasWith(node.Name.EQ("n1"))))
	assert.Equal(t, []string{"n1"}, names(node.Next.HasWith(node.Next.HasWith(node.Name.EQ("n3")))))
	assert.Equal(t, []string{"n2"}, names(node.Next.Has(), node.Prev.Has()))

	// The list is a tree nowhere: the two self-referencing edge pairs of Node
	// do not leak into each other.
	assert.Empty(t, names(node.Children.Has()))
	assert.Empty(t, names(node.Parent.Has()))

	// Unlink and relink.
	must(client.Node.Update(db).Set(node.Next.Clear()).Where(node.ID.EQ(n1.ID)).Exec(ctx))
	// n2 is the entity from before the update: the traversal asks the database.
	_, err = n2.QueryPrev(db).Only(ctx)
	assert.ErrorIs(t, err, orm.ErrNotFound)
	must(client.Node.Update(db).Set(node.Prev.Set(n3.ID)).Where(node.ID.EQ(n1.ID)).Exec(ctx))
	assert.Equal(t, "n1", must(n3.QueryNext(db).Only(ctx)).Name)
	must(client.Node.Update(db).Set(node.Prev.Clear()).Where(node.ID.EQ(n1.ID)).Exec(ctx))
	assert.Equal(t, []string{"n2"}, names(node.Next.Has()))
}

// Self-referencing edges with one name for both directions (O2O spouse, M2M
// friends) and an M2M with two names (following / followers).
func TestSelfRef_Person(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	mk := func(name string, sets ...predicate.PersonSetter) *entity.Person {
		return must(client.Person.Create(db).Set(person.Name.Set(name)).Set(sets...).Save(ctx))
	}
	ann, bob, cid, dee := mk("ann"), mk("bob"), mk("cid"), mk("dee")
	names := func(ps ...predicate.Person) []string {
		return personNames(must(client.Person.Query(db).Where(ps...).OrderBy(person.Name.Asc()).All(ctx)))
	}
	friends := func(p *entity.Person) []string {
		return personNames(must(p.QueryFriends(db).OrderBy(person.Name.Asc()).All(ctx)))
	}

	// Bidirectional M2M: one Add is seen from both ends.
	must(client.Person.Update(db).Set(person.Friends.Add(bob.ID, cid.ID)).Where(person.ID.EQ(ann.ID)).Exec(ctx))
	assert.Equal(t, []string{"bob", "cid"}, friends(ann))
	assert.Equal(t, []string{"ann"}, friends(bob))
	assert.Equal(t, []string{"ann"}, friends(cid))
	assert.Empty(t, friends(dee))
	rows := must(client.Person.Query(db).OrderBy(person.Name.Asc()).WithFriends(client.Person.Query(db).OrderBy(person.Name.Desc())).All(ctx))
	assert.Equal(t, []string{"cid", "bob"}, personNames(rows[0].Edges.Friends))
	assert.Equal(t, []string{"ann"}, personNames(rows[1].Edges.Friends))
	assert.Equal(t, []string{"ann"}, personNames(rows[2].Edges.Friends))
	fs, err := rows[3].Edges.FriendsOrErr()
	require.NoError(t, err)
	assert.Empty(t, fs)
	rows = must(client.Person.Query(db).OrderBy(person.Name.Asc()).WithFriends(client.Person.Query(db).OrderBy(person.Name.Asc()).Limit(1)).All(ctx))
	assert.Equal(t, []string{"bob"}, personNames(rows[0].Edges.Friends))
	assert.Equal(t, []string{"ann"}, personNames(rows[1].Edges.Friends))
	assert.Equal(t, []string{"ann", "bob", "cid"}, names(person.Friends.Has()))
	assert.Equal(t, []string{"dee"}, names(person.Not(person.Friends.Has())))
	assert.Equal(t, []string{"ann"}, names(person.Friends.HasWith(person.Name.EQ("cid"))))
	assert.Equal(t, []string{"bob", "cid"}, names(person.Friends.HasWith(person.Name.EQ("ann"))))
	assert.Equal(t, []string{"bob", "cid"}, names(person.Friends.HasWith(person.Friends.HasWith(person.Name.EQ("bob"))), person.Name.NEQ("ann")), "friends of bob's friends")
	// Removing from one end removes it from both.
	must(client.Person.Update(db).Set(person.Friends.Remove(ann.ID)).Where(person.ID.EQ(bob.ID)).Exec(ctx))
	assert.Equal(t, []string{"cid"}, friends(ann))
	assert.Empty(t, friends(bob))
	must(client.Person.Update(db).Set(person.Friends.Clear()).Where(person.ID.EQ(cid.ID)).Exec(ctx))
	assert.Empty(t, friends(ann))
	assert.Zero(t, rawValue[int](t, db, "SELECT COUNT(*) FROM person_friends"))
	// On create.
	eve := mk("eve", person.Friends.Add(dee.ID))
	assert.Equal(t, []string{"eve"}, friends(dee))
	assert.Equal(t, []string{"dee"}, friends(eve))

	// M2M with two names: ann follows bob and cid, bob follows cid.
	must(client.Person.Update(db).Set(person.Following.Add(bob.ID, cid.ID)).Where(person.ID.EQ(ann.ID)).Exec(ctx))
	must(client.Person.Update(db).Set(person.Followers.Add(bob.ID)).Where(person.ID.EQ(cid.ID)).Exec(ctx)) // from the inverse side
	following := func(p *entity.Person) []string {
		return personNames(must(p.QueryFollowing(db).OrderBy(person.Name.Asc()).All(ctx)))
	}
	followers := func(p *entity.Person) []string {
		return personNames(must(p.QueryFollowers(db).OrderBy(person.Name.Asc()).All(ctx)))
	}
	assert.Equal(t, []string{"bob", "cid"}, following(ann))
	assert.Equal(t, []string{"cid"}, following(bob))
	assert.Empty(t, following(cid))
	assert.Empty(t, followers(ann))
	assert.Equal(t, []string{"ann"}, followers(bob))
	assert.Equal(t, []string{"ann", "bob"}, followers(cid))
	rows = must(client.Person.Query(db).Where(person.Name.In("ann", "bob", "cid")).OrderBy(person.Name.Asc()).
		WithFollowing(client.Person.Query(db).OrderBy(person.Name.Asc())).WithFollowers(client.Person.Query(db).OrderBy(person.Name.Asc()).WithFollowing()).All(ctx))
	assert.Equal(t, []string{"bob", "cid"}, personNames(rows[0].Edges.Following))
	assert.Empty(t, rows[0].Edges.Followers)
	assert.Equal(t, []string{"cid"}, personNames(rows[1].Edges.Following))
	assert.Equal(t, []string{"ann"}, personNames(rows[1].Edges.Followers))
	assert.Empty(t, rows[2].Edges.Following)
	assert.Equal(t, []string{"ann", "bob"}, personNames(rows[2].Edges.Followers))
	assert.Len(t, rows[2].Edges.Followers[0].Edges.Following, 2, "nested: whom cid's follower ann follows")
	assert.Equal(t, []string{"ann", "bob"}, names(person.Following.Has()))
	assert.Equal(t, []string{"bob", "cid"}, names(person.Followers.Has()))
	assert.Equal(t, []string{"cid"}, names(person.Followers.HasWith(person.Name.EQ("bob"))))
	assert.Equal(t, []string{"ann"}, names(person.Following.HasWith(person.Name.EQ("bob"))))
	assert.Equal(t, []string{"ann"}, names(person.Following.HasWith(person.Following.HasWith(person.Name.EQ("cid")))))
	assert.Equal(t, []string{"cid"}, names(person.Followers.HasWith(person.Followers.HasWith(person.Name.EQ("ann")))))
	must(client.Person.Update(db).Set(person.Following.Remove(cid.ID)).Where(person.ID.EQ(ann.ID)).Exec(ctx))
	assert.Equal(t, []string{"bob"}, followers(cid))
	must(client.Person.Update(db).Set(person.Followers.Clear()).Where(person.ID.EQ(cid.ID)).Exec(ctx))
	assert.Empty(t, following(bob))
	assert.Equal(t, []string{"bob"}, following(ann), "Clear on cid's followers left ann -> bob")

	// Bidirectional O2O: one Set marries both.
	must(client.Person.Update(db).Set(person.Spouse.Set(bob.ID)).Where(person.ID.EQ(ann.ID)).Exec(ctx))
	qann := must(client.Person.Query(db).Where(person.ID.EQ(ann.ID)).Only(ctx))
	qbob := must(client.Person.Query(db).Where(person.ID.EQ(bob.ID)).Only(ctx))
	assert.Equal(t, "bob", must(qann.QuerySpouse(db).Only(ctx)).Name)
	assert.Equal(t, "ann", must(qbob.QuerySpouse(db).Only(ctx)).Name)
	_, err = cid.QuerySpouse(db).Only(ctx)
	assert.ErrorIs(t, err, orm.ErrNotFound)
	rows = must(client.Person.Query(db).Where(person.Name.In("ann", "bob", "cid")).OrderBy(person.Name.Asc()).WithSpouse().All(ctx))
	assert.Equal(t, "bob", rows[0].Edges.Spouse.Name)
	assert.Equal(t, "ann", rows[1].Edges.Spouse.Name)
	_, err = rows[2].Edges.SpouseOrErr()
	assert.ErrorIs(t, err, orm.ErrNotFound)
	assert.Equal(t, []string{"ann", "bob"}, names(person.Spouse.Has()))
	assert.Equal(t, []string{"bob"}, names(person.Spouse.HasWith(person.Name.EQ("ann"))))
	// A third person cannot marry one of them.
	_, err = client.Person.Update(db).Set(person.Spouse.Set(ann.ID)).Where(person.ID.EQ(cid.ID)).Exec(ctx)
	require.True(t, orm.IsConstraintError(err), "got %v", err)
	// Divorce from one end frees both.
	must(client.Person.Update(db).Set(person.Spouse.Clear()).Where(person.ID.EQ(bob.ID)).Exec(ctx))
	assert.Empty(t, names(person.Spouse.Has()))
	// On create.
	fay := mk("fay", person.Spouse.Set(cid.ID))
	assert.Equal(t, "cid", must(fay.QuerySpouse(db).Only(ctx)).Name, "the key of the entity Create returned")
	assert.Equal(t, "fay", must(must(client.Person.Query(db).Where(person.ID.EQ(cid.ID)).Only(ctx)).QuerySpouse(db).Only(ctx)).Name)
}

// An M2M declared Through() an edge schema with its own id and columns.
func TestThrough_EdgeSchema(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	s1 := must(client.Student.Create(db).Set(student.Name.Set("s1")).Save(ctx))
	s2 := must(client.Student.Create(db).Set(student.Name.Set("s2")).Save(ctx))
	s3 := must(client.Student.Create(db).Set(student.Name.Set("s3")).Save(ctx))
	c1 := must(client.Course.Create(db).Set(course.Title.Set("c1")).Save(ctx))
	c2 := must(client.Course.Create(db).Set(course.Title.Set("c2")).Save(ctx))
	enroll := func(s *entity.Student, c *entity.Course, sets ...predicate.EnrollmentSetter) *entity.Enrollment {
		return must(client.Enrollment.Create(db).Set(enrollment.StudentID.Set(s.ID), enrollment.CourseID.Set(c.ID)).Set(sets...).Save(ctx))
	}
	e11 := enroll(s1, c1)
	enroll(s1, c2, enrollment.Credits.Set(5), enrollment.Status.Set(enrollment.StatusDropped))
	// Through the edges instead of the fields.
	e22 := must(client.Enrollment.Create(db).Set(enrollment.Student.Set(s2.ID), enrollment.Course.Set(c2.ID)).Save(ctx))
	assert.Equal(t, s2.ID, e22.StudentID)
	assert.Equal(t, c2.ID, e22.CourseID)
	// The join entity's own defaults.
	assert.Equal(t, enrollment.StatusActive, e11.Status)
	assert.Equal(t, 3, e11.Credits)
	assert.False(t, e11.EnrolledAt.IsZero())

	// Both required edges are required.
	_, err := client.Enrollment.Create(db).Set(enrollment.StudentID.Set(s1.ID)).Save(ctx)
	invalid(t, err, "course_id")
	_, err = client.Enrollment.Create(db).Set(enrollment.Course.Set(c1.ID)).Save(ctx)
	invalid(t, err, "student_id")

	courseTitles := func(cs []*entity.Course) []string {
		out := make([]string, len(cs))
		for i, c := range cs {
			out[i] = c.Title
		}
		return out
	}
	studentNames := func(ss []*entity.Student) []string {
		out := make([]string, len(ss))
		for i, s := range ss {
			out[i] = s.Name
		}
		return out
	}

	// The M2M through the join entity, and the O2M to the join entity.
	students := must(client.Student.Query(db).OrderBy(student.Name.Asc()).
		WithCourses(client.Course.Query(db).OrderBy(course.Title.Asc())).WithEnrollments(client.Enrollment.Query(db).OrderBy(enrollment.Credits.Asc()).WithCourse()).All(ctx))
	require.Len(t, students, 3)
	assert.Equal(t, []string{"c1", "c2"}, courseTitles(students[0].Edges.Courses))
	assert.Equal(t, []string{"c2"}, courseTitles(students[1].Edges.Courses))
	assert.Empty(t, students[2].Edges.Courses)
	require.Len(t, students[0].Edges.Enrollments, 2)
	assert.Equal(t, "c1", students[0].Edges.Enrollments[0].Edges.Course.Title)
	assert.Equal(t, 5, students[0].Edges.Enrollments[1].Credits)
	assert.Empty(t, students[2].Edges.Enrollments)
	courses := must(client.Course.Query(db).OrderBy(course.Title.Asc()).
		WithStudents(client.Student.Query(db).OrderBy(student.Name.Asc())).WithEnrollments().All(ctx))
	assert.Equal(t, []string{"s1"}, studentNames(courses[0].Edges.Students))
	assert.Equal(t, []string{"s1", "s2"}, studentNames(courses[1].Edges.Students))
	assert.Len(t, courses[1].Edges.Enrollments, 2)

	assert.Equal(t, []string{"c1", "c2"}, courseTitles(must(s1.QueryCourses(db).OrderBy(course.Title.Asc()).All(ctx))))
	assert.Equal(t, []string{"s1", "s2"}, studentNames(must(c2.QueryStudents(db).OrderBy(student.Name.Asc()).All(ctx))))
	assert.Equal(t, 2, must(s1.QueryEnrollments(db).Count(ctx)))
	assert.Equal(t, 1, must(c1.QueryEnrollments(db).Count(ctx)))
	assert.Equal(t, "s1", must(e11.QueryStudent(db).Only(ctx)).Name)
	assert.Equal(t, "c1", must(e11.QueryCourse(db).Only(ctx)).Title)

	names := func(ps ...predicate.Student) []string {
		return studentNames(must(client.Student.Query(db).Where(ps...).OrderBy(student.Name.Asc()).All(ctx)))
	}
	assert.Equal(t, []string{"s1", "s2"}, names(student.Courses.Has()))
	assert.Equal(t, []string{"s3"}, names(student.Not(student.Courses.Has())))
	assert.Equal(t, []string{"s1"}, names(student.Courses.HasWith(course.Title.EQ("c1"))))
	assert.Equal(t, []string{"s1", "s2"}, names(student.Enrollments.Has()))
	assert.Equal(t, []string{"s1"}, names(student.Enrollments.HasWith(enrollment.Status.EQ(enrollment.StatusDropped))))
	assert.Equal(t, []string{"s2"}, names(student.Enrollments.HasWith(enrollment.Course.HasWith(course.Title.EQ("c2")), enrollment.Status.EQ(enrollment.StatusActive))))
	assert.Equal(t, 2, must(client.Enrollment.Query(db).Where(enrollment.Student.HasWith(student.Name.EQ("s1"))).Count(ctx)))

	// The join entity's own columns can change; its immutable keys cannot.
	got := must(client.Enrollment.Update(db).Set(enrollment.Credits.Add(1), enrollment.Status.Set(enrollment.StatusDropped)).Where(enrollment.ID.EQ(e11.ID)).SaveOne(ctx))
	assert.Equal(t, 4, got.Credits)
	_, err = client.Enrollment.Update(db).Set(enrollment.StudentID.Set(s3.ID)).Where(enrollment.ID.EQ(e11.ID)).Exec(ctx)
	invalid(t, err, "student_id")
	_, err = client.Enrollment.Update(db).Set(enrollment.Student.Set(s3.ID)).Where(enrollment.ID.EQ(e11.ID)).Exec(ctx)
	invalid(t, err, "student_id")
	// Ordering by a foreign-key column.
	byCourse := must(client.Enrollment.Query(db).OrderBy(enrollment.CourseID.Desc(), enrollment.StudentID.Asc()).All(ctx))
	require.Len(t, byCourse, 3)
	assert.Equal(t, []int{c2.ID, c2.ID, c1.ID}, []int{byCourse[0].CourseID, byCourse[1].CourseID, byCourse[2].CourseID})
	assert.Equal(t, s1.ID, byCourse[0].StudentID)
	_, err = client.Enrollment.Update(db).Set(enrollment.EnrolledAt.Set(t0)).Where(enrollment.ID.EQ(e11.ID)).Exec(ctx)
	invalid(t, err, "enrolled_at")
}
