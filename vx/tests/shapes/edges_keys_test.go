package shapes

import (
	"context"
	"testing"

	"github.com/syssam/velox/vx/tests/shapes/db/predicate"

	client "github.com/syssam/velox/vx/tests/shapes/db"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/vx/orm"
	"github.com/syssam/velox/vx/tests/shapes/db/article"
	"github.com/syssam/velox/vx/tests/shapes/db/author"
	"github.com/syssam/velox/vx/tests/shapes/db/badge"
	"github.com/syssam/velox/vx/tests/shapes/db/entity"
	"github.com/syssam/velox/vx/tests/shapes/db/player"
	"github.com/syssam/velox/vx/tests/shapes/db/team"
)

// createTeam saves b; name is for the reader.
func createTeam(t *testing.T, db orm.Executor, b *entity.TeamCreate, name string) *entity.Team {
	t.Helper()
	tm := must(b.Save(context.Background()))
	require.Equal(t, name, tm.Name)
	return tm
}

func playerIDs(ps []*entity.Player) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	return out
}

func teamNames(ts []*entity.Team) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}

// keysFixture: teams t1, t2 (UUID ids); players p1, p2 in t1, p3 in t2, p4 in
// none (string ids); badges 1 and 2 (uint16 ids).
//
//	fans    t1 <- p3, p4;  t2 <- p1
//	rivals  t1 <-> t2
//	badges  t1: 1, 2;  t2: 2
type keysFixture struct {
	t1, t2, t3     *entity.Team
	p1, p2, p3, p4 *entity.Player
	b1, b2         *entity.Badge
}

func keysData(t *testing.T, db orm.Executor) *keysFixture {
	t.Helper()
	ctx := context.Background()
	f := &keysFixture{}
	f.t1 = createTeam(t, db, client.Team.Create(db).Set(team.ID.Set(u1), team.Name.Set("t1")), "t1")
	f.t2 = createTeam(t, db, client.Team.Create(db).Set(team.Name.Set("t2")), "t2")
	f.t3 = createTeam(t, db, client.Team.Create(db).Set(team.Name.Set("t3")), "t3")
	f.p1 = must(client.Player.Create(db).Set(player.ID.Set("p1"), player.Name.Set("one"), player.Team.Set(f.t1.ID)).Save(ctx))
	f.p2 = must(client.Player.Create(db).Set(player.ID.Set("p2"), player.Name.Set("two"), player.Team.Set(f.t1.ID)).Save(ctx))
	f.p3 = must(client.Player.Create(db).Set(player.ID.Set("p3"), player.Name.Set("three"), player.Team.Set(f.t2.ID)).Save(ctx))
	f.p4 = must(client.Player.Create(db).Set(player.ID.Set("p4"), player.Name.Set("four")).Save(ctx))
	f.b1 = must(client.Badge.Create(db).Set(badge.Name.Set("gold")).Save(ctx))
	f.b2 = must(client.Badge.Create(db).Set(badge.Name.Set("silver")).Save(ctx))
	must(client.Team.Update(db).Set(team.Fans.Add("p3", "p4"), team.Rivals.Add(f.t2.ID), team.Badges.Add(f.b1.ID, f.b2.ID)).Where(team.ID.EQ(f.t1.ID)).Exec(ctx))
	must(client.Team.Update(db).Set(team.Badges.Add(f.b2.ID)).Where(team.ID.EQ(f.t2.ID)).Exec(ctx))
	must(client.Player.Update(db).Set(player.FanOf.Add(f.t2.ID)).Where(player.ID.EQ("p1")).Exec(ctx)) // from the inverse side
	return f
}

// Edges between entities whose ids are not ints: O2M / M2O with a UUID key.
func TestKeys_EdgesO2M(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	f := keysData(t, db)

	teams := must(client.Team.Query(db).OrderBy(team.Name.Asc()).WithPlayers(client.Player.Query(db).OrderBy(player.ID.Desc())).All(ctx))
	require.Len(t, teams, 3)
	assert.Equal(t, []string{"p2", "p1"}, playerIDs(teams[0].Edges.Players))
	assert.Equal(t, []string{"p3"}, playerIDs(teams[1].Edges.Players))
	assert.Empty(t, teams[2].Edges.Players)
	players := must(client.Player.Query(db).OrderBy(player.ID.Asc()).WithTeam().All(ctx))
	require.Len(t, players, 4)
	assert.Equal(t, "t1", players[0].Edges.Team.Name)
	assert.Equal(t, "t1", players[1].Edges.Team.Name)
	assert.Equal(t, "t2", players[2].Edges.Team.Name)
	_, err := players[3].Edges.TeamOrErr()
	assert.ErrorIs(t, err, orm.ErrNotFound)

	assert.Equal(t, []string{"p1", "p2"}, playerIDs(must(f.t1.QueryPlayers(db).OrderBy(player.ID.Asc()).All(ctx))))
	assert.Equal(t, "t1", must(f.p1.QueryTeam(db).Only(ctx)).Name, "the key of the entity Create returned")
	assert.Equal(t, "t2", must(players[2].QueryTeam(db).Only(ctx)).Name)
	_, err = f.p4.QueryTeam(db).Only(ctx)
	assert.ErrorIs(t, err, orm.ErrNotFound)

	assert.Equal(t, []string{"t1", "t2"}, teamNames(must(client.Team.Query(db).Where(team.Players.Has()).OrderBy(team.Name.Asc()).All(ctx))))
	assert.Equal(t, []string{"t2"}, teamNames(must(client.Team.Query(db).Where(team.Players.HasWith(player.Name.EQ("three"))).All(ctx))))
	assert.Equal(t, []string{"p1", "p2"}, playerIDs(must(client.Player.Query(db).Where(player.Team.HasWith(team.ID.EQ(u1))).OrderBy(player.ID.Asc()).All(ctx))))
	assert.Equal(t, []string{"p4"}, playerIDs(must(client.Player.Query(db).Where(player.Not(player.Team.Has())).All(ctx))))

	// Reassign, remove, clear.
	must(client.Player.Update(db).Set(player.Team.Set(f.t3.ID)).Where(player.ID.EQ("p4")).Exec(ctx))
	assert.Equal(t, []string{"p4"}, playerIDs(must(f.t3.QueryPlayers(db).All(ctx))))
	must(client.Team.Update(db).Set(team.Players.Remove("p1")).Where(team.ID.EQ(f.t1.ID)).Exec(ctx))
	assert.Equal(t, []string{"p2"}, playerIDs(must(f.t1.QueryPlayers(db).All(ctx))))
	must(client.Team.Update(db).Set(team.Players.Add("p1")).Where(team.ID.EQ(f.t3.ID)).Exec(ctx))
	assert.Equal(t, []string{"p1", "p4"}, playerIDs(must(f.t3.QueryPlayers(db).OrderBy(player.ID.Asc()).All(ctx))))
	must(client.Team.Update(db).Set(team.Players.Clear()).Where(team.ID.EQ(f.t3.ID)).Exec(ctx))
	assert.Empty(t, must(f.t3.QueryPlayers(db).All(ctx)))
	got := must(client.Player.Update(db).Set(player.Team.Clear()).Where(player.ID.EQ("p3")).SaveOne(ctx))
	_, err = got.QueryTeam(db).Only(ctx)
	assert.ErrorIs(t, err, orm.ErrNotFound)
}

// M2M traversal, predicates and changes between non-int ids.
func TestKeys_EdgesM2MQueryAndPredicates(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	f := keysData(t, db)

	assert.Equal(t, []string{"p3", "p4"}, playerIDs(must(f.t1.QueryFans(db).OrderBy(player.ID.Asc()).All(ctx))))
	assert.Equal(t, []string{"p1"}, playerIDs(must(f.t2.QueryFans(db).All(ctx))))
	assert.Equal(t, []string{"t2"}, teamNames(must(f.p1.QueryFanOf(db).All(ctx))))
	assert.Equal(t, []string{"t1"}, teamNames(must(f.p4.QueryFanOf(db).All(ctx))))
	assert.Empty(t, must(f.p2.QueryFanOf(db).All(ctx)))
	assert.Equal(t, []string{"t2"}, teamNames(must(f.t1.QueryRivals(db).All(ctx))))
	assert.Equal(t, []string{"t1"}, teamNames(must(f.t2.QueryRivals(db).All(ctx))), "a bidirectional edge")
	assert.Equal(t, 2, must(f.t1.QueryBadges(db).Count(ctx)))
	assert.Equal(t, []string{"t1", "t2"}, teamNames(must(f.b2.QueryTeams(db).OrderBy(team.Name.Asc()).All(ctx))))
	assert.Equal(t, []string{"t1"}, teamNames(must(f.b1.QueryTeams(db).All(ctx))))

	names := func(ps ...predicate.Team) []string {
		return teamNames(must(client.Team.Query(db).Where(ps...).OrderBy(team.Name.Asc()).All(ctx)))
	}
	assert.Equal(t, []string{"t1", "t2"}, names(team.Fans.Has()))
	assert.Equal(t, []string{"t1"}, names(team.Fans.HasWith(player.ID.EQ("p4"))))
	assert.Equal(t, []string{"t1", "t2"}, names(team.Rivals.Has()))
	assert.Equal(t, []string{"t2"}, names(team.Rivals.HasWith(team.ID.EQ(u1))))
	assert.Equal(t, []string{"t1"}, names(team.Badges.HasWith(badge.Name.EQ("gold"))))
	assert.Equal(t, []string{"t3"}, names(team.Not(team.Badges.Has())))
	assert.Equal(t, []string{"p1", "p3", "p4"}, playerIDs(must(client.Player.Query(db).Where(player.FanOf.Has()).OrderBy(player.ID.Asc()).All(ctx))))
	assert.Equal(t, []string{"p1"}, playerIDs(must(client.Player.Query(db).Where(player.FanOf.HasWith(team.Name.EQ("t2"))).All(ctx))))
	assert.Equal(t, 1, must(client.Badge.Query(db).Where(badge.Teams.HasWith(team.Name.EQ("t2"))).Count(ctx)))

	must(client.Team.Update(db).Set(team.Fans.Remove("p3"), team.Badges.Remove(f.b1.ID), team.Rivals.Clear()).Where(team.ID.EQ(f.t1.ID)).Exec(ctx))
	assert.Equal(t, []string{"p4"}, playerIDs(must(f.t1.QueryFans(db).All(ctx))))
	assert.Equal(t, 1, must(f.t1.QueryBadges(db).Count(ctx)))
	assert.Empty(t, must(f.t2.QueryRivals(db).All(ctx)))
	must(client.Badge.Update(db).Set(badge.Teams.Clear()).Where(badge.ID.EQ(f.b2.ID)).Exec(ctx))
	assert.Zero(t, must(client.Team.Query(db).Where(team.Badges.Has()).Count(ctx)))
}

// What the schema says about an edge is enforced when the edge is written.
func TestEdges_RequiredAndSetterKinds(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	l := library(t, db)

	// Setters that only make sense on update are rejected on create.
	_, err := client.Article.Create(db).Set(article.Title.Set("x"), article.Author.Set(l.ann.ID), article.Topics.Clear()).Save(ctx)
	require.Error(t, err)
	_, err = client.Article.Create(db).Set(article.Title.Set("x"), article.Author.Set(l.ann.ID), article.Topics.Remove(l.tgo.ID)).Save(ctx)
	require.Error(t, err)
	_, err = client.Article.Create(db).Set(article.Title.Set("x"), article.Author.Set(l.ann.ID), article.Words.Add(1)).Save(ctx)
	require.Error(t, err)
	assert.Zero(t, must(client.Article.Query(db).Where(article.Title.EQ("x")).Count(ctx)))

	// An Add of nothing is nothing; Exec reports what it updated, which is 0
	// when there is nothing to set (Save still finds the row).
	n := must(client.Article.Update(db).Set(article.Topics.Add()).Where(article.ID.EQ(l.a1.ID)).Exec(ctx))
	assert.Zero(t, n)
	assert.Zero(t, must(client.Article.Update(db).Where(article.ID.EQ(l.a1.ID)).Exec(ctx)))
	assert.Equal(t, l.a1.ID, must(client.Article.Update(db).Where(article.ID.EQ(l.a1.ID)).SaveOne(ctx)).ID)
	assert.Equal(t, 2, must(l.a1.QueryTopics(db).Count(ctx)))

	// Clear and Set of a to-one edge in one update: the new neighbor wins.
	got := must(client.Article.Update(db).Set(article.Editor.Clear(), article.Editor.Set(l.dee.ID)).Where(article.ID.EQ(l.a1.ID)).SaveOne(ctx))
	require.NotNil(t, got.EditorID)
	assert.Equal(t, l.dee.ID, *got.EditorID)

	// An update that matches nothing changes no edge.
	n = must(client.Article.Update(db).Set(article.Topics.Clear()).Where(article.Title.EQ("nope")).Exec(ctx))
	assert.Zero(t, n)
	assert.Equal(t, 4, rawValue[int](t, db, "SELECT COUNT(*) FROM article_topics"))

	// A required edge cannot be cleared (the bound field has no Clear at all).
	_, err = client.Article.Update(db).Set(article.Author.Clear()).Where(article.ID.EQ(l.a1.ID)).Exec(ctx)
	invalid(t, err, "author_id")
	assert.Equal(t, "ann", must(must(client.Article.Query(db).Where(article.ID.EQ(l.a1.ID)).Only(ctx)).QueryAuthor(db).Only(ctx)).Name)
	assert.Equal(t, []string{"ann"}, authorNames(must(client.Author.Query(db).Where(author.Articles.HasWith(article.Title.EQ("a1"))).All(ctx))))
}
