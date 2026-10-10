package shapes

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/syssam/velox/tests/orm/shapes/db/predicate"

	client "github.com/syssam/velox/tests/orm/shapes/db"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/orm"
	"github.com/syssam/velox/tests/orm/shapes/db/article"
	"github.com/syssam/velox/tests/orm/shapes/db/author"
	"github.com/syssam/velox/tests/orm/shapes/db/badge"
	"github.com/syssam/velox/tests/orm/shapes/db/bare"
	"github.com/syssam/velox/tests/orm/shapes/db/blob"
	"github.com/syssam/velox/tests/orm/shapes/db/course"
	"github.com/syssam/velox/tests/orm/shapes/db/defaultshape"
	"github.com/syssam/velox/tests/orm/shapes/db/edgesenum"
	"github.com/syssam/velox/tests/orm/shapes/db/edgesfield"
	"github.com/syssam/velox/tests/orm/shapes/db/enrollment"
	"github.com/syssam/velox/tests/orm/shapes/db/entity"
	nfield "github.com/syssam/velox/tests/orm/shapes/db/field"
	"github.com/syssam/velox/tests/orm/shapes/db/gadget"
	"github.com/syssam/velox/tests/orm/shapes/db/gotypeshape"
	"github.com/syssam/velox/tests/orm/shapes/db/int64key"
	"github.com/syssam/velox/tests/orm/shapes/db/intkey"
	"github.com/syssam/velox/tests/orm/shapes/db/labelkey"
	"github.com/syssam/velox/tests/orm/shapes/db/manualkey"
	"github.com/syssam/velox/tests/orm/shapes/db/namedbool"
	"github.com/syssam/velox/tests/orm/shapes/db/namedboolnil"
	"github.com/syssam/velox/tests/orm/shapes/db/namedbytes"
	"github.com/syssam/velox/tests/orm/shapes/db/note"
	"github.com/syssam/velox/tests/orm/shapes/db/passport"
	"github.com/syssam/velox/tests/orm/shapes/db/person"
	"github.com/syssam/velox/tests/orm/shapes/db/player"
	"github.com/syssam/velox/tests/orm/shapes/db/renamed"
	"github.com/syssam/velox/tests/orm/shapes/db/scalar"
	"github.com/syssam/velox/tests/orm/shapes/db/stamped"
	"github.com/syssam/velox/tests/orm/shapes/db/stringkey"
	"github.com/syssam/velox/tests/orm/shapes/db/student"
	"github.com/syssam/velox/tests/orm/shapes/db/team"
	"github.com/syssam/velox/tests/orm/shapes/db/topic"
	"github.com/syssam/velox/tests/orm/shapes/db/uuidkey"
	"github.com/syssam/velox/tests/orm/shapes/db/weightkey"
	"github.com/syssam/velox/tests/orm/shapes/types"
)

// The tests of this file assert behavior that was once pinned as a gap in
// known_gaps_runtime_test.go and has been corrected since. Each keeps the
// hard case that showed the gap.

// The field descriptor types of an entity package are exported, so a helper
// can take one.
var (
	_ scalar.NullableStringColumn = scalar.StringNil
	_ scalar.IntColumn            = scalar.ID
)

// A bool with a named Go type, required and nillable: a NULL between a true
// and a false.
func TestNamedBool_RoundTrip(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	a := must(client.NamedBool.Create(db).Set(namedbool.Label.Set("a")).Save(ctx))
	assert.Equal(t, types.Flag(true), a.Flag, "the default")
	b := must(client.NamedBool.Create(db).Set(namedbool.Label.Set("b"), namedbool.Flag.Set(false)).Save(ctx))
	rows := must(client.NamedBool.Query(db).OrderBy(namedbool.ID.Asc()).All(ctx))
	require.Len(t, rows, 2)
	assert.Equal(t, types.Flag(true), rows[0].Flag)
	assert.Equal(t, types.Flag(false), rows[1].Flag)
	assert.Equal(t, []int{b.ID}, must(client.NamedBool.Query(db).Where(namedbool.Flag.EQ(false)).IDs(ctx)))
	got := must(client.NamedBool.Update(db).Set(namedbool.Flag.Set(false)).Where(namedbool.ID.EQ(a.ID)).SaveOne(ctx))
	assert.Equal(t, types.Flag(false), got.Flag)
	assert.Equal(t, 0, rawValue[int](t, db, "SELECT flag FROM named_bools WHERE id = ?", a.ID))
	assert.Equal(t, []int{a.ID, b.ID}, must(client.NamedBool.Query(db).OrderBy(namedbool.Flag.Asc(), namedbool.ID.Asc()).IDs(ctx)))

	must(client.NamedBoolNil.Create(db).Set(namedboolnil.Label.Set("t"), namedboolnil.Flag.Set(true)).Save(ctx))
	n := must(client.NamedBoolNil.Create(db).Set(namedboolnil.Label.Set("null")).Save(ctx))
	assert.Nil(t, n.Flag)
	must(client.NamedBoolNil.Create(db).Set(namedboolnil.Label.Set("f"), namedboolnil.Flag.Set(false)).Save(ctx))
	nrows := must(client.NamedBoolNil.Query(db).OrderBy(namedboolnil.ID.Asc()).All(ctx))
	require.Len(t, nrows, 3)
	require.NotNil(t, nrows[0].Flag)
	assert.Equal(t, types.Flag(true), *nrows[0].Flag)
	assert.Nil(t, nrows[1].Flag)
	require.NotNil(t, nrows[2].Flag, "false is not NULL")
	assert.Equal(t, types.Flag(false), *nrows[2].Flag)
	assert.Equal(t, "null", must(client.NamedBoolNil.Query(db).Where(namedboolnil.Flag.IsNil()).Only(ctx)).Label)
	ngot := must(client.NamedBoolNil.Update(db).Set(namedboolnil.Flag.Clear()).Where(namedboolnil.Label.EQ("t")).SaveOne(ctx))
	assert.Nil(t, ngot.Flag)
}

// The id is not an assignable column of an update, whatever its type.
func TestKeys_IDCannotBeUpdated(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	s := must(client.StringKey.Create(db).Set(stringkey.ID.Set("a"), stringkey.Name.Set("a")).Save(ctx))
	i := must(client.IntKey.Create(db).Set(intkey.Name.Set("a")).Save(ctx))
	u := must(client.UUIDKey.Create(db).Set(uuidkey.Name.Set("a")).Save(ctx))
	i64 := must(client.Int64Key.Create(db).Set(int64key.Name.Set("a")).Save(ctx))

	_, err := client.StringKey.Update(db).Set(stringkey.Name.Set("never"), stringkey.ID.Set("b")).Where(stringkey.ID.EQ(s.ID)).Exec(ctx)
	invalid(t, err, "id")
	_, err = client.StringKey.Update(db).Set(stringkey.ID.Set("a")).Where(stringkey.ID.EQ(s.ID)).SaveOne(ctx)
	invalid(t, err, "id", "even the same value")
	_, err = client.IntKey.Update(db).Set(intkey.ID.Set(i.ID + 100)).Exec(ctx)
	invalid(t, err, "id")
	_, err = client.UUIDKey.Update(db).Set(uuidkey.ID.Set(u2)).Where(uuidkey.ID.EQ(u.ID)).SaveOne(ctx)
	invalid(t, err, "id")
	_, err = client.Int64Key.Update(db).Set(int64key.ID.Set(9)).Where(int64key.ID.EQ(i64.ID)).Exec(ctx)
	invalid(t, err, "id")

	assert.Equal(t, []string{"a"}, must(client.StringKey.Query(db).IDs(ctx)))
	assert.Equal(t, "a", must(client.StringKey.Query(db).Only(ctx)).Name, "the rest of the rejected update was not written")
	assert.Equal(t, []int{i.ID}, must(client.IntKey.Query(db).IDs(ctx)))
	assert.Equal(t, []uuid.UUID{u.ID}, must(client.UUIDKey.Query(db).IDs(ctx)))
}

// An Immutable() edge is set on create and rejected on update, for every
// setter of every edge kind; a Required() edge cannot be cleared.
func TestEdges_ImmutableAndRequiredOnUpdate(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	l := library(t, db)

	// To-one, bound to an immutable field.
	s1 := must(client.Student.Create(db).Set(student.Name.Set("s1")).Save(ctx))
	s2 := must(client.Student.Create(db).Set(student.Name.Set("s2")).Save(ctx))
	c := must(client.Course.Create(db).Set(course.Title.Set("c")).Save(ctx))
	e := must(client.Enrollment.Create(db).Set(enrollment.Student.Set(s1.ID), enrollment.Course.Set(c.ID)).Save(ctx))
	_, err := client.Enrollment.Update(db).Set(enrollment.Credits.Set(9), enrollment.Student.Set(s2.ID)).Where(enrollment.ID.EQ(e.ID)).Exec(ctx)
	invalid(t, err, "student_id")
	_, err = client.Enrollment.Update(db).Set(enrollment.Student.Set(s1.ID)).Where(enrollment.ID.EQ(e.ID)).SaveOne(ctx)
	invalid(t, err, "student_id", "even the same neighbor")
	_, err = client.Enrollment.Update(db).Set(enrollment.Course.Clear()).Where(enrollment.ID.EQ(e.ID)).Exec(ctx)
	invalid(t, err, "course_id")
	_, err = client.Enrollment.Update(db).Set(enrollment.StudentID.Set(s2.ID)).Where(enrollment.ID.EQ(e.ID)).Exec(ctx)
	invalid(t, err, "student_id")
	got := must(client.Enrollment.Query(db).Only(ctx))
	assert.Equal(t, s1.ID, got.StudentID)
	assert.Equal(t, 3, got.Credits, "the rest of the rejected update was not written")

	// To-many, O2M and M2M.
	n1 := must(client.Note.Create(db).Set(note.Text.Set("n1")).Save(ctx))
	n2 := must(client.Note.Create(db).Set(note.Text.Set("n2")).Save(ctx))
	r := must(client.Renamed.Create(db).Set(renamed.Frozen.Add(n1.ID), renamed.FrozenTopics.Add(l.tgo.ID, l.sql.ID)).Save(ctx))
	for name, set := range map[string]predicate.RenamedSetter{
		"o2m add": renamed.Frozen.Add(n2.ID), "o2m remove": renamed.Frozen.Remove(n1.ID), "o2m clear": renamed.Frozen.Clear(),
		"m2m add": renamed.FrozenTopics.Add(l.rust.ID), "m2m remove": renamed.FrozenTopics.Remove(l.tgo.ID), "m2m clear": renamed.FrozenTopics.Clear(),
	} {
		_, err := client.Renamed.Update(db).Set(renamed.Title.Set("never"), set).Where(renamed.ID.EQ(r.ID)).Exec(ctx)
		var verr *orm.ValidationError
		require.ErrorAs(t, err, &verr, name)
		assert.Contains(t, []string{"frozen", "frozen_topics"}, verr.Field, name)
	}
	assert.Equal(t, "n1", must(r.QueryFrozen(db).Only(ctx)).Text)
	assert.Equal(t, []string{"go", "sql"}, topicNames(must(r.QueryFrozenTopics(db).OrderBy(topic.Name.Asc()).All(ctx))))
	assert.Equal(t, "untitled", must(client.Renamed.Query(db).Only(ctx)).Title)

	// Required edges cannot be cleared: hidden key and bound key, M2O and O2O.
	_, err = client.Article.Update(db).Set(article.Title.Set("never"), article.Author.Clear()).Where(article.ID.EQ(l.a1.ID)).Exec(ctx)
	invalid(t, err, "author_id")
	_, err = client.Passport.Update(db).Set(passport.Owner.Clear()).Where(passport.ID.EQ(l.pAnn.ID)).SaveOne(ctx)
	invalid(t, err, "owner")
	assert.Equal(t, "a1", must(client.Article.Query(db).Where(article.ID.EQ(l.a1.ID)).Only(ctx)).Title)
	// ...but they can be reassigned, and an optional one cleared.
	must(client.Article.Update(db).Set(article.Author.Set(l.cid.ID), article.Editor.Clear()).Where(article.ID.EQ(l.a1.ID)).Exec(ctx))
	must(client.Passport.Update(db).Set(passport.Owner.Set(l.cid.ID)).Where(passport.ID.EQ(l.pAnn.ID)).Exec(ctx))
	// Clear then Set of a required edge in one update is a reassignment.
	_, err = client.Article.Update(db).Set(article.Author.Clear(), article.Author.Set(l.dee.ID)).Where(article.ID.EQ(l.a1.ID)).Exec(ctx)
	if err != nil {
		invalid(t, err, "author", "clear-and-replace of a required edge is rejected (velox does the same)")
	}
}

// On create the last setter of a column wins, whether it is the field or
// the to-one edge bound to it, and the entity returned agrees with the row.
func TestEdges_LastSetterOfAKeyWins(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	ann := must(client.Author.Create(db).Set(author.Name.Set("ann")).Save(ctx))
	bob := must(client.Author.Create(db).Set(author.Name.Set("bob")).Save(ctx))
	cid := must(client.Author.Create(db).Set(author.Name.Set("cid")).Save(ctx))
	stored := func(a *entity.Article) (int, *int) {
		q := must(client.Article.Query(db).Where(article.ID.EQ(a.ID)).Only(ctx))
		return q.AuthorID, q.EditorID
	}
	cases := []struct {
		name string
		sets []predicate.ArticleSetter
		want int
	}{
		{"field then edge", []predicate.ArticleSetter{article.AuthorID.Set(bob.ID), article.Author.Set(ann.ID)}, ann.ID},
		{"edge then field", []predicate.ArticleSetter{article.Author.Set(ann.ID), article.AuthorID.Set(bob.ID)}, bob.ID},
		{"edge twice", []predicate.ArticleSetter{article.Author.Set(ann.ID), article.Author.Set(bob.ID)}, bob.ID},
		{"field twice", []predicate.ArticleSetter{article.AuthorID.Set(ann.ID), article.AuthorID.Set(bob.ID)}, bob.ID},
		{"three", []predicate.ArticleSetter{article.Author.Set(ann.ID), article.AuthorID.Set(bob.ID), article.Author.Set(cid.ID)}, cid.ID},
	}
	var bulk []*entity.ArticleCreate
	for _, c := range cases {
		a := must(client.Article.Create(db).Set(article.Title.Set(c.name)).Set(c.sets...).Save(ctx))
		assert.Equal(t, c.want, a.AuthorID, "%s: the returned entity", c.name)
		got, _ := stored(a)
		assert.Equal(t, c.want, got, "%s: the row", c.name)
		bulk = append(bulk, client.Article.Create(db).Set(article.Title.Set("bulk "+c.name)).Set(c.sets...))
	}
	for i, a := range must(client.Article.CreateBulk(db, bulk...).Save(ctx)) {
		assert.Equal(t, cases[i].want, a.AuthorID, "bulk %s: the returned entity", cases[i].name)
		got, _ := stored(a)
		assert.Equal(t, cases[i].want, got, "bulk %s: the row", cases[i].name)
	}

	// The optional key: set, then cleared; cleared, then set.
	a := must(client.Article.Create(db).Set(article.Title.Set("set clear"), article.Author.Set(ann.ID), article.Editor.Set(bob.ID), article.EditorID.Clear()).Save(ctx))
	assert.Nil(t, a.EditorID)
	_, ed := stored(a)
	assert.Nil(t, ed)
	a = must(client.Article.Create(db).Set(article.Title.Set("clear set"), article.Author.Set(ann.ID), article.EditorID.Clear(), article.Editor.Set(bob.ID)).Save(ctx))
	require.NotNil(t, a.EditorID)
	assert.Equal(t, bob.ID, *a.EditorID)
	_, ed = stored(a)
	require.NotNil(t, ed)
	assert.Equal(t, bob.ID, *ed)

	// (On update: TestUpdate_SettersApplyInOrder.)
}

// Clear() on create writes NULL — a database default does not fill it — and
// the returned entity agrees with the row.
func TestCreate_ClearWritesNull(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	c := must(client.DefaultShape.Create(db).Set(defaultshape.Name.Set("c"), defaultshape.Code.Set("cc"), defaultshape.NilNote.Clear()).Save(ctx))
	assert.Nil(t, c.NilNote)
	assert.Equal(t, 1, rawValue[int](t, db, "SELECT nil_note IS NULL FROM default_shapes WHERE id = ?", c.ID))
	assert.Nil(t, must(client.DefaultShape.Query(db).Where(defaultshape.ID.EQ(c.ID)).Only(ctx)).NilNote)
	// The default still applies to the row next to it, in the same batch.
	rows := must(client.DefaultShape.CreateBulk(db,
		client.DefaultShape.Create(db).Set(defaultshape.Name.Set("d"), defaultshape.Code.Set("cd")),
		client.DefaultShape.Create(db).Set(defaultshape.Name.Set("e"), defaultshape.Code.Set("ce"), defaultshape.NilNote.Clear()),
		client.DefaultShape.Create(db).Set(defaultshape.Name.Set("f"), defaultshape.Code.Set("cf"), defaultshape.NilNote.Set("mine")),
	).Save(ctx))
	for name, rs := range map[string][]*entity.DefaultShape{"created": rows, "queried": must(client.DefaultShape.Query(db).Where(defaultshape.Name.In("d", "e", "f")).OrderBy(defaultshape.Name.Asc()).All(ctx))} {
		require.Len(t, rs, 3, name)
		require.NotNil(t, rs[0].NilNote, name)
		assert.Equal(t, "nil", *rs[0].NilNote, name)
		assert.Nil(t, rs[1].NilNote, name)
		require.NotNil(t, rs[2].NilNote, name)
		assert.Equal(t, "mine", *rs[2].NilNote, name)
	}

	// (JSON, and NULL where it is not allowed: TestNull_OnlyInNullableColumns.)
}

// The default of a slice-typed field is copied per row.
func TestDefaults_SliceDefaultIsNotShared(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	a := must(client.DefaultShape.Create(db).Set(defaultshape.Name.Set("a"), defaultshape.Code.Set("c")).Save(ctx))
	a.Tags[0] = "MUTATED"
	a.Blob[0] = 'X'
	b := must(client.DefaultShape.Create(db).Set(defaultshape.Name.Set("b"), defaultshape.Code.Set("c")).Save(ctx))
	assert.Equal(t, []string{"a", "b"}, b.Tags)
	assert.Equal(t, []byte("dflt"), b.Blob)
	b.Tags = append(b.Tags[:1], "z")
	rows := must(client.DefaultShape.CreateBulk(db,
		client.DefaultShape.Create(db).Set(defaultshape.Name.Set("c"), defaultshape.Code.Set("c")),
		client.DefaultShape.Create(db).Set(defaultshape.Name.Set("d"), defaultshape.Code.Set("c")),
	).Save(ctx))
	rows[0].Tags[1] = "MUTATED"
	assert.Equal(t, []string{"a", "b"}, rows[1].Tags, "the rows of one bulk do not share the default either")
	for _, r := range must(client.DefaultShape.Query(db).All(ctx)) {
		assert.Equal(t, []string{"a", "b"}, r.Tags, r.Name)
		assert.Equal(t, []byte("dflt"), r.Blob, r.Name)
	}
}

// QueryXxx of a to-one edge asks the database, not the entity in hand.
func TestEdges_QueryNeighbourIsCurrent(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	l := library(t, db)
	must(client.Article.Update(db).Set(article.Author.Set(l.bob.ID), article.Editor.Clear()).Where(article.ID.EQ(l.a1.ID)).Exec(ctx))
	// l.a1 still says author ann, editor bob.
	require.Equal(t, l.ann.ID, l.a1.AuthorID)
	assert.Equal(t, "bob", must(l.a1.QueryAuthor(db).Only(ctx)).Name)
	_, err := l.a1.QueryEditor(db).Only(ctx)
	assert.ErrorIs(t, err, orm.ErrNotFound)
	assert.Equal(t, 1, must(l.a1.QueryAuthor(db).Where(author.Age.GT(35)).Count(ctx)), "the target's own predicates on top")
	// O2O with a hidden key and with a bound key.
	must(client.Passport.Update(db).Set(passport.Owner.Set(l.cid.ID)).Where(passport.ID.EQ(l.pAnn.ID)).Exec(ctx))
	assert.Equal(t, "cid", must(l.pAnn.QueryOwner(db).Only(ctx)).Name)
	// A row that is gone has no neighbor; neither has an entity never saved.
	must(client.Article.Delete(db).Where(article.ID.EQ(l.a2.ID)).Exec(ctx))
	_, err = l.a2.QueryAuthor(db).Only(ctx)
	assert.ErrorIs(t, err, orm.ErrNotFound)
	_, err = (&entity.Article{AuthorID: l.ann.ID}).QueryAuthor(db).Only(ctx)
	assert.ErrorIs(t, err, orm.ErrNotFound, "the key in the struct is not consulted")
	// Inside a transaction it sees the transaction's write.
	_ = db.RunTx(ctx, func(tx *orm.Tx) error {
		must(client.Article.Update(tx).Set(article.Author.Set(l.dee.ID)).Where(article.ID.EQ(l.a1.ID)).Exec(ctx))
		assert.Equal(t, "dee", must(l.a1.QueryAuthor(tx).Only(ctx)).Name)
		return errors.New("roll back")
	})
	assert.Equal(t, "bob", must(l.a1.QueryAuthor(db).Only(ctx)).Name)
}

// M2M eager loading between ids of every Go type: UUID, string, uint16,
// named string, named int — with order, per-parent limit and nesting.
func TestKeys_EdgesM2MEagerLoad(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	f := keysData(t, db)
	ann := must(client.Person.Create(db).Set(person.Name.Set("ann")).Save(ctx))
	must(client.Badge.Update(db).Set(badge.Holders.Add(ann.ID)).Where(badge.ID.EQ(f.b1.ID)).Exec(ctx))

	teams := must(client.Team.Query(db).OrderBy(team.Name.Asc()).WithFans(client.Player.Query(db).OrderBy(player.ID.Desc()).WithFanOf().WithTeam()).WithRivals(client.Team.Query(db).WithBadges(client.Badge.Query(db).OrderBy(badge.Name.Asc()))).WithBadges(client.Badge.Query(db).OrderBy(badge.Name.Desc())).All(ctx))
	require.Len(t, teams, 3)
	assert.Equal(t, []string{"p4", "p3"}, playerIDs(teams[0].Edges.Fans), "UUID owner, string target, in the load's order")
	assert.Equal(t, []string{"p1"}, playerIDs(teams[1].Edges.Fans))
	fans, err := teams[2].Edges.FansOrErr()
	require.NoError(t, err)
	assert.Empty(t, fans)
	assert.Equal(t, []string{"t1"}, teamNames(teams[0].Edges.Fans[0].Edges.FanOf), "nested: string owner, UUID target")
	assert.Equal(t, "t2", teams[0].Edges.Fans[1].Edges.Team.Name)
	_, err = teams[0].Edges.Fans[0].Edges.TeamOrErr()
	assert.ErrorIs(t, err, orm.ErrNotFound, "p4 has no team")
	assert.Equal(t, []string{"t2"}, teamNames(teams[0].Edges.Rivals), "UUID on both sides")
	assert.Equal(t, []string{"t1"}, teamNames(teams[1].Edges.Rivals))
	require.Len(t, teams[1].Edges.Rivals[0].Edges.Badges, 2, "nested: t1's badges, through t2's rival")
	assert.Equal(t, "gold", teams[1].Edges.Rivals[0].Edges.Badges[0].Name)
	require.Len(t, teams[0].Edges.Badges, 2, "UUID owner, uint16 target")
	assert.Equal(t, "silver", teams[0].Edges.Badges[0].Name)
	require.Len(t, teams[1].Edges.Badges, 1)
	assert.Empty(t, teams[2].Edges.Badges)

	players := must(client.Player.Query(db).OrderBy(player.ID.Asc()).WithFanOf(client.Team.Query(db).OrderBy(team.Name.Asc())).All(ctx))
	require.Len(t, players, 4)
	assert.Equal(t, []string{"t2"}, teamNames(players[0].Edges.FanOf))
	assert.Empty(t, players[1].Edges.FanOf)
	assert.Equal(t, []string{"t1"}, teamNames(players[2].Edges.FanOf))
	assert.Equal(t, []string{"t1"}, teamNames(players[3].Edges.FanOf))
	badges := must(client.Badge.Query(db).OrderBy(badge.Name.Asc()).WithTeams(client.Team.Query(db).OrderBy(team.Name.Desc())).WithHolders().All(ctx))
	require.Len(t, badges, 2)
	assert.Equal(t, []string{"t1"}, teamNames(badges[0].Edges.Teams), "uint16 owner, UUID target")
	assert.Equal(t, []string{"t2", "t1"}, teamNames(badges[1].Edges.Teams))
	assert.Equal(t, []string{"ann"}, personNames(badges[0].Edges.Holders), "uint16 owner, int target")
	assert.Empty(t, badges[1].Edges.Holders)

	// Per-parent limit, with order and with a predicate.
	must(client.Team.Update(db).Set(team.Fans.Add("p1", "p2")).Where(team.ID.EQ(f.t1.ID)).Exec(ctx)) // t1: p1..p4
	must(client.Team.Update(db).Set(team.Fans.Add("p2", "p4")).Where(team.ID.EQ(f.t2.ID)).Exec(ctx)) // t2: p1, p2, p4
	teams = must(client.Team.Query(db).OrderBy(team.Name.Asc()).WithFans(client.Player.Query(db).OrderBy(player.ID.Desc()).Limit(2)).All(ctx))
	assert.Equal(t, []string{"p4", "p3"}, playerIDs(teams[0].Edges.Fans))
	assert.Equal(t, []string{"p4", "p2"}, playerIDs(teams[1].Edges.Fans))
	assert.Empty(t, teams[2].Edges.Fans)
	teams = must(client.Team.Query(db).OrderBy(team.Name.Asc()).WithFans(client.Player.Query(db).Where(player.ID.NEQ("p4")).OrderBy(player.ID.Asc()).Limit(1)).All(ctx))
	assert.Equal(t, []string{"p1"}, playerIDs(teams[0].Edges.Fans))
	assert.Equal(t, []string{"p1"}, playerIDs(teams[1].Edges.Fans))
	players = must(client.Player.Query(db).OrderBy(player.ID.Asc()).WithFanOf(client.Team.Query(db).OrderBy(team.Name.Desc()).Limit(1)).All(ctx))
	assert.Equal(t, []string{"t2"}, teamNames(players[0].Edges.FanOf))
	assert.Equal(t, []string{"t2"}, teamNames(players[1].Edges.FanOf))
	assert.Equal(t, []string{"t1"}, teamNames(players[2].Edges.FanOf))
	assert.Equal(t, []string{"t2"}, teamNames(players[3].Edges.FanOf))

	// Ids with a named Go type.
	la, lb, w1, w2 := namedKeys(t, db)
	_, _ = la, lb
	w3 := must(client.WeightKey.Create(db).Set(weightkey.ID.Set(60), weightkey.Name.Set("w3"), weightkey.Labels.Add("lb"), weightkey.Peers.Add(w1.ID, w2.ID)).Save(ctx))
	labels := must(client.LabelKey.Query(db).OrderBy(labelkey.ID.Asc()).WithWeights(client.WeightKey.Query(db).OrderBy(weightkey.ID.Desc())).All(ctx))
	require.Len(t, labels, 2)
	weightIDs := func(ws []*entity.WeightKey) []types.Weight {
		out := make([]types.Weight, len(ws))
		for i, w := range ws {
			out[i] = w.ID
		}
		return out
	}
	assert.Equal(t, []types.Weight{50}, weightIDs(labels[0].Edges.Weights), "named string owner, named int target")
	assert.Equal(t, []types.Weight{60, 50}, weightIDs(labels[1].Edges.Weights))
	weights := must(client.WeightKey.Query(db).OrderBy(weightkey.ID.Asc()).
		WithLabels(client.LabelKey.Query(db).OrderBy(labelkey.ID.Desc()).Limit(1)).WithPeers(client.WeightKey.Query(db).OrderBy(weightkey.ID.Asc())).All(ctx))
	require.Len(t, weights, 3)
	assert.Empty(t, weights[0].Edges.Labels)
	require.Len(t, weights[1].Edges.Labels, 1, "named int owner, named string target, limited per parent")
	assert.Equal(t, types.Label("lb"), weights[1].Edges.Labels[0].ID)
	require.Len(t, weights[2].Edges.Labels, 1)
	assert.Equal(t, []types.Weight{50, 60}, weightIDs(weights[0].Edges.Peers), "named int on both sides")
	assert.Equal(t, []types.Weight{w1.ID, 60}, weightIDs(weights[1].Edges.Peers))
	assert.Equal(t, []types.Weight{w1.ID, 50}, weightIDs(weights[2].Edges.Peers))
	_ = w3
}

// An M2M Add through an edge schema inserts the join entity with the
// defaults of its own fields.
func TestThrough_AddAppliesJoinEntityDefaults(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	s := must(client.Student.Create(db).Set(student.Name.Set("s")).Save(ctx))
	c1 := must(client.Course.Create(db).Set(course.Title.Set("c1")).Save(ctx))
	c2 := must(client.Course.Create(db).Set(course.Title.Set("c2")).Save(ctx))

	must(client.Student.Update(db).Set(student.Courses.Add(c1.ID, c2.ID)).Where(student.ID.EQ(s.ID)).Exec(ctx))
	es := must(client.Enrollment.Query(db).OrderBy(enrollment.CourseID.Asc()).All(ctx))
	require.Len(t, es, 2)
	for _, e := range es {
		assert.Equal(t, s.ID, e.StudentID)
		assert.Equal(t, enrollment.StatusActive, e.Status)
		assert.Equal(t, 3, e.Credits)
		assert.False(t, e.EnrolledAt.IsZero())
	}
	assert.Equal(t, []int{c1.ID, c2.ID}, []int{es[0].CourseID, es[1].CourseID})
	// On create, from the inverse side, and in a bulk.
	s2 := must(client.Student.Create(db).Set(student.Name.Set("s2"), student.Courses.Add(c2.ID)).Save(ctx))
	c3 := must(client.Course.Create(db).Set(course.Title.Set("c3"), course.Students.Add(s.ID, s2.ID)).Save(ctx))
	rows := must(client.Student.CreateBulk(db,
		client.Student.Create(db).Set(student.Name.Set("s3"), student.Courses.Add(c1.ID)),
		client.Student.Create(db).Set(student.Name.Set("s4")),
		client.Student.Create(db).Set(student.Name.Set("s5"), student.Courses.Add(c1.ID, c3.ID)),
	).Save(ctx))
	require.Len(t, rows, 3)
	assert.Equal(t, 8, must(client.Enrollment.Query(db).Where(enrollment.Credits.EQ(3), enrollment.Status.EQ(enrollment.StatusActive)).Count(ctx)))
	assert.Equal(t, 3, must(c3.QueryStudents(db).Count(ctx)))
	assert.Zero(t, must(rows[1].QueryCourses(db).Count(ctx)))
	assert.Equal(t, 2, must(rows[2].QueryEnrollments(db).Count(ctx)))
	// Remove and Clear delete join entities.
	must(client.Student.Update(db).Set(student.Courses.Remove(c1.ID)).Where(student.ID.EQ(s.ID)).Exec(ctx))
	assert.Equal(t, 2, must(s.QueryEnrollments(db).Count(ctx)))
	must(client.Course.Update(db).Set(course.Students.Clear()).Where(course.ID.EQ(c3.ID)).Exec(ctx))
	assert.Equal(t, 4, must(client.Enrollment.Query(db).Count(ctx)))
	// Inside a transaction that rolls back.
	_ = db.RunTx(ctx, func(tx *orm.Tx) error {
		must(client.Student.Update(tx).Set(student.Courses.Add(c3.ID)).Where(student.ID.EQ(s.ID)).Exec(ctx))
		assert.Equal(t, 5, must(client.Enrollment.Query(tx).Count(ctx)))
		return errors.New("roll back")
	})
	assert.Equal(t, 4, must(client.Enrollment.Query(db).Count(ctx)))
}

// A bulk create of rows that have nothing to write inserts every row.
func TestBare_BulkCreateInsertsEveryRow(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	rows := must(client.Bare.CreateBulk(db, client.Bare.Create(db), client.Bare.Create(db), client.Bare.Create(db)).Save(ctx))
	require.Len(t, rows, 3)
	assert.Equal(t, 3, must(client.Bare.Query(db).Count(ctx)))
	ids := must(client.Bare.Query(db).OrderBy(bare.ID.Asc()).IDs(ctx))
	assert.Equal(t, ids, []int{rows[0].ID, rows[1].ID, rows[2].ID}, "each entity carries the id of a row, in order")
	require.NoError(t, client.Bare.CreateBulk(db, client.Bare.Create(db), client.Bare.Create(db)).Exec(ctx))
	assert.Equal(t, 5, must(client.Bare.Query(db).Count(ctx)))
	// All or nothing inside a transaction.
	_ = db.RunTx(ctx, func(tx *orm.Tx) error {
		must(client.Bare.CreateBulk(tx, client.Bare.Create(tx), client.Bare.Create(tx)).Save(ctx))
		return errors.New("roll back")
	})
	assert.Equal(t, 5, must(client.Bare.Query(db).Count(ctx)))
}

// An optional to-one edge bound to a key field that is Optional but not
// Nillable: the Go field is an int, the column a nullable foreign key. An
// unset owner is NULL in the database — not a zero written into the key —
// and 0 in Go, between two rows that have one.
func TestEdges_OptionalNonNillableKey(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	ann := must(client.Author.Create(db).Set(author.Name.Set("ann")).Save(ctx))
	bob := must(client.Author.Create(db).Set(author.Name.Set("bob")).Save(ctx))

	g1 := must(client.Gadget.Create(db).Set(gadget.Name.Set("g1"), gadget.Owner.Set(ann.ID)).Save(ctx))
	g2 := must(client.Gadget.Create(db).Set(gadget.Name.Set("g2")).Save(ctx))
	g3 := must(client.Gadget.Create(db).Set(gadget.Name.Set("g3"), gadget.OwnerID.Set(bob.ID)).Save(ctx))
	assert.Equal(t, []int{ann.ID, 0, bob.ID}, []int{g1.OwnerID, g2.OwnerID, g3.OwnerID})
	assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM gadgets WHERE owner_id IS NULL"))
	rows := must(client.Gadget.CreateBulk(db,
		client.Gadget.Create(db).Set(gadget.Name.Set("b1"), gadget.OwnerID.Set(ann.ID)),
		client.Gadget.Create(db).Set(gadget.Name.Set("b2")),
		client.Gadget.Create(db).Set(gadget.Name.Set("b3"), gadget.Owner.Set(bob.ID), gadget.OwnerID.Clear()),
		client.Gadget.Create(db).Set(gadget.Name.Set("b4"), gadget.Owner.Set(bob.ID)),
	).Save(ctx))
	assert.Equal(t, []int{ann.ID, 0, 0, bob.ID}, []int{rows[0].OwnerID, rows[1].OwnerID, rows[2].OwnerID, rows[3].OwnerID})
	assert.Equal(t, 3, rawValue[int](t, db, "SELECT COUNT(*) FROM gadgets WHERE owner_id IS NULL"))

	names := func(ps ...predicate.Gadget) []string {
		var out []string
		for _, g := range must(client.Gadget.Query(db).Where(ps...).OrderBy(gadget.Name.Asc()).All(ctx)) {
			out = append(out, g.Name)
		}
		return out
	}
	assert.Equal(t, []string{"b2", "b3", "g2"}, names(gadget.OwnerID.IsNil()))
	assert.Equal(t, []string{"b1", "b4", "g1", "g3"}, names(gadget.OwnerID.NotNil()))
	assert.Equal(t, []string{"b2", "b3", "g2"}, names(gadget.Not(gadget.Owner.Has())))
	assert.Equal(t, []string{"b1", "g1"}, names(gadget.Owner.HasWith(author.Name.EQ("ann"))))
	assert.Equal(t, []string{"b1", "g1"}, names(gadget.OwnerID.EQ(ann.ID)))

	loaded := must(client.Gadget.Query(db).Where(gadget.Name.HasPrefix("g")).OrderBy(gadget.Name.Asc()).WithOwner().All(ctx))
	require.Len(t, loaded, 3)
	assert.Equal(t, "ann", loaded[0].Edges.Owner.Name)
	_, err := loaded[1].Edges.OwnerOrErr()
	assert.ErrorIs(t, err, orm.ErrNotFound)
	assert.Zero(t, loaded[1].OwnerID)
	assert.Equal(t, "bob", loaded[2].Edges.Owner.Name)
	assert.Equal(t, "ann", must(g1.QueryOwner(db).Only(ctx)).Name)
	_, err = g2.QueryOwner(db).Only(ctx)
	assert.ErrorIs(t, err, orm.ErrNotFound)
	authors := must(client.Author.Query(db).OrderBy(author.Name.Asc()).WithGadgets(client.Gadget.Query(db).OrderBy(gadget.Name.Asc())).All(ctx))
	assert.Len(t, authors[0].Edges.Gadgets, 2)
	assert.Len(t, authors[1].Edges.Gadgets, 2)

	// Clear, through the edge and through the field; set again both ways.
	got := must(client.Gadget.Update(db).Set(gadget.Owner.Clear()).Where(gadget.ID.EQ(g1.ID)).SaveOne(ctx))
	assert.Zero(t, got.OwnerID)
	got = must(client.Gadget.Update(db).Set(gadget.OwnerID.Clear()).Where(gadget.ID.EQ(g3.ID)).SaveOne(ctx))
	assert.Zero(t, got.OwnerID)
	assert.Equal(t, 5, rawValue[int](t, db, "SELECT COUNT(*) FROM gadgets WHERE owner_id IS NULL"))
	got = must(client.Gadget.Update(db).Set(gadget.Owner.Set(bob.ID)).Where(gadget.ID.EQ(g1.ID)).SaveOne(ctx))
	assert.Equal(t, bob.ID, got.OwnerID)
	got = must(client.Gadget.Update(db).Set(gadget.OwnerID.Set(ann.ID)).Where(gadget.ID.EQ(g2.ID)).SaveOne(ctx))
	assert.Equal(t, ann.ID, got.OwnerID)
	// From the other side.
	must(client.Author.Update(db).Set(author.Gadgets.Add(g3.ID), author.Gadgets.Remove(g2.ID)).Where(author.ID.EQ(ann.ID)).Exec(ctx))
	assert.Equal(t, []string{"b1", "g3"}, names(gadget.Owner.HasWith(author.ID.EQ(ann.ID))))
	// An update that does not name the key leaves a NULL key NULL.
	must(client.Gadget.Update(db).Set(gadget.Name.Set("G2")).Where(gadget.ID.EQ(g2.ID)).Exec(ctx))
	assert.Equal(t, 1, rawValue[int](t, db, "SELECT owner_id IS NULL FROM gadgets WHERE id = ?", g2.ID))
	// A key that references nothing is still a constraint error.
	_, err = client.Gadget.Create(db).Set(gadget.Name.Set("x"), gadget.OwnerID.Set(0)).Save(ctx)
	require.True(t, orm.IsConstraintError(err), "got %v", err)
}

// NULL can only be written where the column allows it. The generated API has
// no Clear for such a column (TestCompileTimeChecks); a setter built by hand
// is a validation error, on create and update.
func TestNull_OnlyInNullableColumns(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	s := must(client.Scalar.Create(db).Set(scalarRequired(1)...).Set(scalarFull(1)...).Save(ctx))
	for _, col := range []string{"string_opt", "int_opt", "bytes_opt", "time_opt", "seq", "string_ptr"} {
		null := predicate.ScalarSetter{Kind: orm.SetNull, Col: col}
		_, err := client.Scalar.Create(db).Set(scalarRequired(2)...).Set(null).Save(ctx)
		invalid(t, err, col, "create")
		_, err = client.Scalar.Update(db).Set(scalar.IntNil.Set(77), null).Where(scalar.ID.EQ(s.ID)).Exec(ctx)
		invalid(t, err, col, "update")
	}
	assert.Equal(t, 1, must(client.Scalar.Query(db).Count(ctx)))
	assertScalarFull(t, 1, must(client.Scalar.Query(db).Only(ctx)))

	// JSON: never set, cleared on create and cleared on update are all SQL
	// NULL, and IsNil finds the three.
	base := func(seq int) *entity.BlobCreate {
		return client.Blob.Create(db).Set(blob.Seq.Set(seq), blob.Data.Set([]byte("d")), blob.Req.Set(types.Address{}))
	}
	must(base(1).Save(ctx))
	b2 := must(base(2).Set(blob.Tags.Set([]string{"x"}), blob.Tags.Clear(), blob.Nilb.Set([]byte("x")), blob.Nilb.Clear()).Save(ctx))
	assert.Nil(t, b2.Tags)
	assert.Nil(t, b2.Nilb)
	must(base(3).Set(blob.Tags.Set([]string{"x"})).Save(ctx))
	must(client.Blob.Update(db).Set(blob.Tags.Clear()).Where(blob.Seq.EQ(3)).Exec(ctx))
	must(base(4).Set(blob.Tags.Set([]string{})).Save(ctx))
	must(base(5).Set(blob.Tags.Clear(), blob.Tags.Set([]string{"y"})).Save(ctx))
	assert.Equal(t, 3, rawValue[int](t, db, "SELECT COUNT(*) FROM blobs WHERE tags IS NULL"))
	var seqs []int
	for _, r := range must(client.Blob.Query(db).Where(blob.Tags.IsNil()).OrderBy(blob.Seq.Asc()).All(ctx)) {
		seqs = append(seqs, r.Seq)
		assert.Nil(t, r.Tags)
	}
	assert.Equal(t, []int{1, 2, 3}, seqs)
	assert.Equal(t, 2, must(client.Blob.Query(db).Where(blob.Tags.NotNil()).Count(ctx)), "an empty list and a list")
}

// On update, as on create, setters of one column apply in order.
func TestUpdate_SettersApplyInOrder(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	ann := must(client.Author.Create(db).Set(author.Name.Set("ann"), author.Age.Set(10)).Save(ctx))
	bob := must(client.Author.Create(db).Set(author.Name.Set("bob")).Save(ctx))
	cid := must(client.Author.Create(db).Set(author.Name.Set("cid")).Save(ctx))
	a := must(client.Article.Create(db).Set(article.Title.Set("a"), article.Author.Set(cid.ID)).Save(ctx))
	stored := func() (int, *int) {
		q := must(client.Article.Query(db).Where(article.ID.EQ(a.ID)).Only(ctx))
		return q.AuthorID, q.EditorID
	}
	for _, c := range []struct {
		name string
		sets []predicate.ArticleSetter
		want int
	}{
		{"field then edge", []predicate.ArticleSetter{article.AuthorID.Set(bob.ID), article.Author.Set(ann.ID)}, ann.ID},
		{"edge then field", []predicate.ArticleSetter{article.Author.Set(ann.ID), article.AuthorID.Set(bob.ID)}, bob.ID},
		{"edge twice", []predicate.ArticleSetter{article.Author.Set(ann.ID), article.Author.Set(cid.ID)}, cid.ID},
		{"three", []predicate.ArticleSetter{article.Author.Set(ann.ID), article.AuthorID.Set(bob.ID), article.Author.Set(ann.ID)}, ann.ID},
	} {
		got := must(client.Article.Update(db).Set(c.sets...).Where(article.ID.EQ(a.ID)).SaveOne(ctx))
		assert.Equal(t, c.want, got.AuthorID, c.name)
		au, _ := stored()
		assert.Equal(t, c.want, au, c.name)
		// Through Exec over several rows too.
		must(client.Article.Update(db).Set(c.sets...).Exec(ctx))
		au, _ = stored()
		assert.Equal(t, c.want, au, c.name+" (exec)")
	}
	// The optional key: set then clear, clear then set, through both spellings.
	must(client.Article.Update(db).Set(article.Editor.Set(bob.ID), article.EditorID.Clear()).Where(article.ID.EQ(a.ID)).Exec(ctx))
	_, ed := stored()
	assert.Nil(t, ed)
	must(client.Article.Update(db).Set(article.Editor.Clear(), article.EditorID.Set(bob.ID)).Where(article.ID.EQ(a.ID)).Exec(ctx))
	_, ed = stored()
	require.NotNil(t, ed)
	must(client.Article.Update(db).Set(article.EditorID.Set(ann.ID), article.Editor.Clear()).Where(article.ID.EQ(a.ID)).Exec(ctx))
	_, ed = stored()
	assert.Nil(t, ed)

	// To and Add of one column compose in order.
	age := func(sets ...predicate.AuthorSetter) int {
		return must(client.Author.Update(db).Set(sets...).Where(author.ID.EQ(ann.ID)).SaveOne(ctx)).Age
	}
	assert.Equal(t, 6, age(author.Age.Set(5), author.Age.Add(1)))
	assert.Equal(t, 5, age(author.Age.Add(1), author.Age.Set(5)))
	assert.Equal(t, 8, age(author.Age.Add(1), author.Age.Add(2)))
	assert.Equal(t, 3, age(author.Age.Add(100), author.Age.Set(1), author.Age.Add(2)))
	assert.Equal(t, 3, rawValue[int](t, db, "SELECT age FROM authors WHERE id = ?", ann.ID))
	// An edge whose key is a schema field is written as that field: the
	// field's validators see it (author_id has none; the immutable case is
	// TestEdges_ImmutableAndRequiredOnUpdate).
	_, err := client.Article.Update(db).Set(article.Author.Set(9999)).Where(article.ID.EQ(a.ID)).Exec(ctx)
	require.True(t, orm.IsConstraintError(err), "got %v", err)
}

// Count counts the matching rows, whatever the query's limit, offset, order
// or eager loads, and each row once whatever its edges.
func TestQuery_Count(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	library(t, db)
	q := func() *entity.ArticleQuery { return client.Article.Query(db) }
	assert.Equal(t, 4, must(q().Count(ctx)))
	assert.Equal(t, 4, must(q().Limit(1).Count(ctx)))
	assert.Equal(t, 4, must(q().Offset(1).Count(ctx)))
	assert.Equal(t, 4, must(q().Offset(10).Count(ctx)))
	assert.Equal(t, 4, must(q().Limit(2).Offset(1).OrderBy(article.Title.Desc()).Count(ctx)))
	assert.Equal(t, 4, must(q().Distinct().Count(ctx)))
	assert.Equal(t, 4, must(q().WithTopics().WithAuthor().Count(ctx)))
	// a1 has two topics and two notes: one row.
	assert.Equal(t, 3, must(q().Where(article.Topics.Has()).Count(ctx)))
	assert.Equal(t, 3, must(q().Where(article.Topics.Has()).Distinct().Limit(1).Count(ctx)))
	assert.Equal(t, 1, must(q().Where(article.Topics.HasWith(topic.Name.In("go", "sql")), article.Notes.Has()).Offset(1).Count(ctx)), "a1 only")
	assert.Equal(t, 3, must(q().Where(article.Author.HasWith(author.Articles.HasWith(article.Words.GT(150)))).Count(ctx)), "ann's two and bob's one")
	assert.Equal(t, 3, must(client.Author.Query(db).Where(author.Articles.Has()).Count(ctx)), "ann has two articles and counts once")
	assert.Equal(t, 2, must(client.Topic.Query(db).Where(topic.Articles.Has()).Limit(1).Count(ctx)))
	// The same query still pages.
	paged := q().OrderBy(article.Title.Asc()).Limit(2).Offset(1)
	assert.Equal(t, 4, must(paged.Count(ctx)))
	assert.Equal(t, []string{"a2", "b1"}, titles(must(paged.All(ctx))))
	// Exist and IDs do honor the window.
	assert.False(t, must(q().Offset(4).Exists(ctx)))
	assert.Len(t, must(q().Limit(3).IDs(ctx)), 3)
}

// A field stored in the column "edges" reaches JSON next to the entity's
// loaded edges.
func TestNames_FieldNamedEdges(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	f := must(client.Field.Create(db).Set(nfield.Label.Set("f1")).Save(ctx))
	e := must(client.EdgesField.Create(db).Set(edgesfield.EdgesField.Set("mine"), edgesfield.Fields.Add(f.ID)).Save(ctx))
	assert.Equal(t, "mine", e.EdgesField)
	got := must(client.EdgesField.Query(db).Where(edgesfield.EdgesField.EQ("mine")).WithFields().Only(ctx))
	require.Len(t, got.Edges.Fields, 1)
	out, err := json.Marshal(got)
	require.NoError(t, err)
	var decoded struct {
		EdgesField string `json:"edges_field"`
		Edges      struct {
			Fields []struct {
				Label string `json:"label"`
			} `json:"fields"`
		} `json:"edges"`
	}
	require.NoError(t, json.Unmarshal(out, &decoded), string(out))
	assert.Equal(t, "mine", decoded.EdgesField)
	require.Len(t, decoded.Edges.Fields, 1, string(out))
	assert.Equal(t, "f1", decoded.Edges.Fields[0].Label)
	assert.Equal(t, "mine", rawValue[string](t, db, "SELECT edges FROM edges_fields"))

	en := must(client.EdgesEnum.Create(db).Save(ctx))
	assert.Equal(t, edgesenum.EdgesA, en.EdgesField)
	en = must(client.EdgesEnum.Update(db).Set(edgesenum.EdgesField.Set(edgesenum.EdgesB)).Where(edgesenum.ID.EQ(en.ID)).SaveOne(ctx))
	out, err = json.Marshal(en)
	require.NoError(t, err)
	assert.Contains(t, string(out), `"edges_field":"b"`)
}

// Zero-fill of unset Optional fields with a named Go type, on velox's tables.
func TestGoType_UnsetOptionalIsZeroFilled(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	g := must(client.GoTypeShape.Create(db).Set(gotypeshape.Price.Set(types.Decimal{Cents: 1}), gotypeshape.Pair.Set(types.Pair{})).Save(ctx))
	assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM go_type_shapes WHERE nick = '' AND extra = 0 AND cost = '0.00' AND alias IS NULL AND fee IS NULL AND pair_nil IS NULL"))
	got := must(client.GoTypeShape.Query(db).Where(gotypeshape.ID.EQ(g.ID), gotypeshape.Nick.EQ(""), gotypeshape.Extra.EQ(0), gotypeshape.Cost.EQ(types.Decimal{})).Only(ctx))
	assert.Equal(t, types.Decimal{}, got.Cost)
	ip := net.ParseIP("10.0.0.1").To4()
	n := must(client.NamedBytes.Create(db).Set(namedbytes.IP.Set(ip)).Save(ctx))
	assert.Empty(t, n.IPOpt)
	assert.Nil(t, n.IPNil)
	qn := must(client.NamedBytes.Query(db).Only(ctx))
	assert.Empty(t, qn.IPOpt)
	assert.Nil(t, qn.IPNil)
	assert.Equal(t, ip, qn.IP)
}

// A nil byte slice is the empty value in a column that does not accept NULL
// (it used to be sent as NULL and fail the constraint), and NULL in one that
// does, where the entity Create returns agrees with the row.
func TestBytes_NilSlice(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	base := func(seq int) *entity.BlobCreate {
		return client.Blob.Create(db).Set(blob.Seq.Set(seq), blob.Req.Set(types.Address{}))
	}
	var none []byte

	a := must(base(1).Set(blob.Data.Set(none), blob.Opt.Set(none), blob.Nilb.Set(none)).Save(ctx))
	assert.Nil(t, a.Nilb, "the returned entity of a NULL bytes column")
	assert.Equal(t, 0, rawValue[int](t, db, "SELECT length(data) FROM blobs WHERE seq = 1"))
	assert.Equal(t, 0, rawValue[int](t, db, "SELECT length(opt) FROM blobs WHERE seq = 1"))
	assert.Equal(t, 1, rawValue[int](t, db, "SELECT count(*) FROM blobs WHERE seq = 1 AND nilb IS NULL"))
	got := must(client.Blob.Query(db).Where(blob.ID.EQ(a.ID)).Only(ctx))
	assert.Nil(t, got.Nilb)
	assert.Empty(t, got.Data)

	b := must(base(2).Set(blob.Data.Set([]byte("d")), blob.Opt.Set([]byte("o"))).Save(ctx))
	_, err := client.Blob.Update(db).Set(blob.Data.Set(none), blob.Opt.Set(none)).Where(blob.ID.EQ(b.ID)).Exec(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, rawValue[int](t, db, "SELECT length(data) + length(opt) FROM blobs WHERE seq = 2"))
}

// A create that leaves out a required edge, or an id the caller must supply,
// is a validation error naming it, before any SQL: not whatever the database
// says about the NOT NULL column, and not nothing at all where the column
// happens to accept NULL.
func TestCreate_MissingRequiredEdgeOrID(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	var verr *orm.ValidationError

	_, err := client.Passport.Create(db).Set(passport.Number.Set("nobody")).Save(ctx)
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "owner", verr.Field)
	assert.ErrorContains(t, err, "missing required edge")
	assert.Zero(t, must(client.Passport.Query(db).Count(ctx)))
	_, err = client.Passport.CreateBulk(db, client.Passport.Create(nil).Set(passport.Number.Set("nobody"))).Save(ctx)
	require.ErrorAs(t, err, &verr)

	_, err = client.ManualKey.Create(db).Set(manualkey.Name.Set("no id")).Save(ctx)
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "id", verr.Field)
	assert.Zero(t, must(client.ManualKey.Query(db).Count(ctx)))

	// An id the database assigns, and one a schema default provides, are not
	// the caller's to give.
	must(client.IntKey.Create(db).Set(intkey.Name.Set("auto")).Save(ctx))
	u := must(client.UUIDKey.Create(db).Set(uuidkey.Name.Set("default")).Save(ctx))
	assert.NotEqual(t, uuid.Nil, u.ID)
}

// A mutation either happens or does not, inside a transaction too. A create
// or an update of several statements (the row, then its join rows) that
// fails at a later one is undone as a whole, by a savepoint, and the
// transaction goes on: a caller may handle the error and commit the rest.
func TestTx_FailedMutationLeavesNothingBehind(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	l := library(t, db)
	require.NoError(t, db.RunTx(ctx, func(tx *orm.Tx) error {
		_, err := client.Article.Create(tx).Set(article.Title.Set("ghost"), article.Author.Set(l.ann.ID), article.Topics.Add(9999)).Save(ctx)
		require.True(t, orm.IsConstraintError(err), "got %v", err)
		_, err = client.Article.Update(tx).Set(article.Title.Set("half"), article.Topics.Add(9999)).Where(article.ID.EQ(l.a2.ID)).Exec(ctx)
		require.True(t, orm.IsConstraintError(err), "got %v", err)
		_, err = client.Article.UpdateByID(tx, l.a2.ID).Set(article.Title.Set("half one"), article.Topics.Add(9999)).SaveOne(ctx)
		require.True(t, orm.IsConstraintError(err), "got %v", err)
		_, err = client.Article.CreateBulk(tx,
			client.Article.Create(nil).Set(article.Title.Set("ghost 1"), article.Author.Set(l.ann.ID)),
			client.Article.Create(nil).Set(article.Title.Set("ghost 2"), article.Author.Set(l.ann.ID), article.Topics.Add(9999)),
		).Save(ctx)
		require.True(t, orm.IsConstraintError(err), "got %v", err)
		// The transaction is intact: what it does next is committed.
		_, err = client.Article.Create(tx).Set(article.Title.Set("kept"), article.Author.Set(l.ann.ID), article.Topics.Add(l.tgo.ID)).Save(ctx)
		return err
	}))
	assert.Zero(t, must(client.Article.Query(db).Where(article.Title.In("ghost", "half", "half one", "ghost 1", "ghost 2")).Count(ctx)),
		"part of a failed mutation was committed")
	kept := must(client.Article.Query(db).Where(article.Title.EQ("kept")).WithTopics().Only(ctx))
	assert.Len(t, kept.Edges.Topics, 1)
}

// A column that may be NULL, held in a field that is not a pointer, reads
// NULL as zero. A page's cursor has to say NULL all the same: with zero the
// walk continued after "0" instead of after the NULLs, and rows were never
// served (ascending) or a page came back for ever (descending). Found by an
// independent review of the runtime.
func TestPaginate_NullInAFieldThatIsNotAPointer(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	owner := must(client.Author.Create(db).Set(author.Name.Set("ann")).Save(ctx))
	var want []int
	for _, name := range []string{"a", "b", "c", "d", "e"} { // no owner: NULL
		want = append(want, must(client.Gadget.Create(db).Set(gadget.Name.Set(name)).Save(ctx)).ID)
	}
	for _, name := range []string{"f", "g"} {
		want = append(want, must(client.Gadget.Create(db).Set(gadget.Name.Set(name), gadget.OwnerID.Set(owner.ID)).Save(ctx)).ID)
	}
	require.Equal(t, 5, rawValue[int](t, db, "SELECT COUNT(*) FROM gadgets WHERE owner_id IS NULL"))
	two := 2
	for _, term := range []predicate.GadgetOrderTerm{gadget.OwnerID.Asc(), gadget.OwnerID.Desc()} {
		var got []int
		var after *orm.Cursor
		for page := 0; page < 10; page++ {
			conn := must(client.Gadget.Query(db).OrderBy(term).Paginate(ctx, after, &two, nil, nil))
			for _, e := range conn.Edges {
				got = append(got, e.Node.ID)
			}
			if !conn.PageInfo.HasNextPage {
				break
			}
			after = conn.PageInfo.EndCursor
		}
		assert.ElementsMatch(t, want, got, "forward, descending=%v: the walk must serve every row once", term.Desc)
		got = nil
		var before *orm.Cursor
		for page := 0; page < 10; page++ {
			conn := must(client.Gadget.Query(db).OrderBy(term).Paginate(ctx, nil, nil, before, &two))
			for _, e := range conn.Edges {
				got = append(got, e.Node.ID)
			}
			if !conn.PageInfo.HasPreviousPage {
				break
			}
			before = conn.PageInfo.StartCursor
		}
		assert.ElementsMatch(t, want, got, "backward, descending=%v", term.Desc)
	}
}

// A cursor of a row whose key is NULL in a column this schema would have
// filled with a zero value: a table made by something else. The first fix
// for NULL keys refused such a cursor as "not made here" for every column
// the schema calls NOT NULL, which made the second page of this walk an
// error; before that fix the walk dropped rows.
func TestPaginate_NullWhereTheSchemaHasNone(t *testing.T) {
	ctx := context.Background()
	db := openDDL(t, ddlNullableOptional)
	var want []int
	for n := 1; n <= 4; n++ {
		want = append(want, must(client.Scalar.Create(db).Set(scalarRequired(n)...).Set(scalarFull(n)...).Save(ctx)).ID)
	}
	nullOptionals(t, db, 1)
	nullOptionals(t, db, 2)
	one := 1
	for _, term := range []predicate.ScalarOrderTerm{scalar.IntOpt.Asc(), scalar.IntOpt.Desc(), scalar.StringOpt.Asc(), scalar.TimeOpt.Desc()} {
		var got []int
		var after *orm.Cursor
		for page := 0; page < 10; page++ {
			conn, err := client.Scalar.Query(db).OrderBy(term).Paginate(ctx, after, &one, nil, nil)
			require.NoError(t, err, "page %d of %+v", page, term)
			for _, e := range conn.Edges {
				got = append(got, e.Node.ID)
			}
			if !conn.PageInfo.HasNextPage {
				break
			}
			after = conn.PageInfo.EndCursor
		}
		assert.ElementsMatch(t, want, got, "%+v: the walk must serve every row once", term)
	}
}

// A time field of a Go type of its own (a struct with Value and Scan) is
// written as a time is stored, and compared that way: the driver would have
// written what Value returned, monotonic reading and nanoseconds included,
// and the row would not have been found by the value it was created with,
// nor by the one read back.
func TestTimeOfItsOwnType(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	for i, at := range []time.Time{time.Now(), time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC)} {
		made := must(client.Stamped.Create(db).Set(stamped.At.Set(types.Stamp{T: at}), stamped.Plain.Set(at)).Save(ctx))
		got := must(client.Stamped.Get(ctx, db, made.ID))
		assert.True(t, got.At.T.Equal(at.Truncate(time.Microsecond)), "time %d: stored %v for %v", i, got.At.T, at)
		for name, by := range map[string]types.Stamp{"created with": {T: at}, "returned": made.At, "read back": got.At} {
			n := must(client.Stamped.Query(db).Where(stamped.ID.EQ(made.ID), stamped.At.EQ(by)).Count(ctx))
			assert.Equal(t, 1, n, "time %d, by the value %s", i, name)
		}
		n := must(client.Stamped.Query(db).Where(stamped.ID.EQ(made.ID), stamped.Plain.EQ(at)).Count(ctx))
		assert.Equal(t, 1, n, "time %d, the plain column", i)
	}
}

// Every column can be asked whether it is NULL. One that cannot be never
// is: IsNil matches no row and NotNil every row, on tables this schema made.
// On a table made by something else, where an Optional column holds NULL,
// they tell the rows apart, which nothing else in the API could.
func TestIsNilOnEveryColumn(t *testing.T) {
	ctx := context.Background()
	db := openDDL(t, ddlNullableOptional)
	for n := 1; n <= 3; n++ {
		must(client.Scalar.Create(db).Set(scalarRequired(n)...).Set(scalarFull(n)...).Save(ctx))
	}
	assert.Equal(t, 0, must(client.Scalar.Query(db).Where(scalar.Seq.IsNil()).Count(ctx)), "a required column")
	assert.Equal(t, 3, must(client.Scalar.Query(db).Where(scalar.Seq.NotNil()).Count(ctx)))
	assert.Equal(t, 0, must(client.Scalar.Query(db).Where(scalar.IntOpt.IsNil()).Count(ctx)), "an optional column this schema fills")
	nullOptionals(t, db, 2)
	assert.Equal(t, 1, must(client.Scalar.Query(db).Where(scalar.IntOpt.IsNil()).Count(ctx)), "after NULL was stored by something else")
	assert.Equal(t, 2, must(client.Scalar.Query(db).Where(scalar.IntOpt.NotNil(), scalar.StringOpt.NotNil()).Count(ctx)))
	// And as code written once for any column sees it.
	isNull := func(c orm.NullableColumn[predicate.Scalar]) predicate.Scalar { return c.IsNil() }
	assert.Equal(t, 1, must(client.Scalar.Query(db).Where(isNull(&scalar.IntOpt)).Count(ctx)))
	assert.Equal(t, 0, must(client.Scalar.Query(db).Where(isNull(&scalar.ID)).Count(ctx)))
	var ordered orm.OrderedColumn[int, predicate.Scalar] = &scalar.IntOpt
	assert.Equal(t, 1, must(client.Scalar.Query(db).Where(ordered.IsNil()).Count(ctx)))
}
