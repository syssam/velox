// Command relations shows one-to-many, belongs-to and many-to-many relations:
// eager loading with With, EXISTS conditions with Has and HasWith, linking
// and unlinking through a join entity and through an edge schema, and keyset
// pagination with After.
package main

import (
	"context"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/relations/veloxgen"
	"github.com/syssam/velox/migrate"
)

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// The tables come from migrations/, made, in the example module, by
//
//	go tool velox-gen migrate diff -dialect sqlite -name init -dir ./relations/migrations ./relations/veloxschema
//
// and, once Tag was added, again with -name tags, and once Club and
// Membership were, with -name memberships.
// and applied with migrate.Up. The temporary SQLite database is this demo's;
// a real application opens its own.
//
//go:embed migrations
var migrations embed.FS

func run(ctx context.Context, w io.Writer) error {
	dir, err := os.MkdirTemp("", "velox-relations-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	db, err := velox.Connect("sqlite", filepath.Join(dir, "relations.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	if _, err := migrate.Up(ctx, velox.Pool(db), sub); err != nil {
		return err
	}
	A, P := veloxgen.Authors, veloxgen.Posts
	say := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format+"\n", args...) }

	if err := seed(ctx, db); err != nil {
		return err
	}

	// With loads a relation with one more query (WHERE author_id IN (...)).
	// Nothing is ever loaded lazily: Get reports whether it was loaded.
	authors, err := db.Query[veloxgen.Author]().
		OrderBy(A.Name.Asc()).
		With(A.Posts).
		Limit(10).
		All(ctx)
	if err != nil {
		return err
	}
	for _, a := range authors {
		posts, loaded := a.Edges.Posts.Get()
		say("With: %s wrote %s (loaded=%t)", a.Name, titles(posts), loaded)
	}

	// A relation takes its own Where and OrderBy, and With for a nested
	// relation: posts with 10 votes or more, best first, with their comments.
	authors, err = db.Query[veloxgen.Author]().
		Where(A.Name.EQ("Grace")).
		With(A.Posts.Where(P.Votes.GTE(10)).OrderBy(P.Votes.Desc()).With(P.Comments)).
		Limit(1).
		All(ctx)
	if err != nil {
		return err
	}
	posts, _ := authors[0].Edges.Posts.Get()
	for _, p := range posts {
		comments, _ := p.Edges.Comments.Get()
		say("nested With: %s / %s, %d votes, comments=%d", authors[0].Name, p.Title, p.Votes, len(comments))
	}

	// Belongs-to: each post loads its author. Owners with the same key share
	// one loaded Author.
	top, err := db.Query[veloxgen.Post]().OrderBy(P.Votes.Desc()).With(P.Author).Limit(3).All(ctx)
	if err != nil {
		return err
	}
	for _, p := range top {
		a, _ := p.Edges.Author.Get()
		say("belongs-to: %s by %s", p.Title, a.Name)
	}

	// Has and HasWith are EXISTS subqueries; Not(Has()) finds the authors
	// with no posts.
	for _, c := range []struct {
		name string
		pred velox.Pred[veloxgen.Author]
	}{
		{"Has", A.Posts.Has()},
		{"HasWith votes > 20", A.Posts.HasWith(P.Votes.GT(20))},
		{"Not Has", velox.Not(A.Posts.Has())},
	} {
		got, err := db.Query[veloxgen.Author]().Where(c.pred).OrderBy(A.Name.Asc()).Limit(10).All(ctx)
		if err != nil {
			return err
		}
		say("%s: %s", c.name, names(got))
	}
	text, _, err := db.Query[veloxgen.Author]().Where(A.Posts.HasWith(P.Votes.GT(20))).Limit(10).SQL(ctx)
	if err != nil {
		return err
	}
	say("SQL: %s", text)

	// After continues after the previous page's last row: keyset pagination.
	// The OrderBy must include the primary key or a unique column.
	page := db.Query[veloxgen.Post]().OrderBy(P.Votes.Desc(), P.ID.Asc()).Limit(2)
	for i := 1; ; i++ {
		got, err := page.All(ctx)
		if err != nil {
			return err
		}
		if len(got) == 0 {
			break
		}
		say("page %d: %s", i, titles(got))
		page = page.After(got[len(got)-1])
	}
	if err := manyToMany(ctx, db, say); err != nil {
		return err
	}
	return edgeSchema(ctx, db, say)
}

// edgeSchema links authors and clubs through the edge schema Membership. A
// link is a Membership row, with an ID and a role. Clubs.Add inserts the
// row with its defaults and keeps a link that exists; Remove deletes it.
// A row inserted directly sets the other columns. Memberships, the edge
// velox-gen adds, reads the rows.
func edgeSchema(ctx context.Context, db *velox.DB, say func(string, ...any)) error {
	A, C, M := veloxgen.Authors, veloxgen.Clubs, veloxgen.Memberships
	clubs := map[string]int64{}
	for _, name := range []string{"Chess", "Go"} {
		c := veloxgen.Club{Name: name}
		if err := db.Insert(&c).Exec(ctx); err != nil {
			return err
		}
		clubs[name] = c.ID
	}
	authors := map[string]int64{}
	all, err := db.Query[veloxgen.Author]().Limit(10).All(ctx)
	if err != nil {
		return err
	}
	for _, a := range all {
		authors[a.Name] = a.ID
	}

	// Add inserts a Membership per club, role "member" by its default.
	// Adding Chess again keeps the row.
	grace := A.ID.EQ(authors["Grace"])
	if _, err := db.Update[veloxgen.Author]().Set(A.Clubs.Add(clubs["Chess"], clubs["Go"])).Where(grace).Exec(ctx); err != nil {
		return err
	}
	if _, err := db.Update[veloxgen.Author]().Set(A.Clubs.Add(clubs["Chess"])).Where(grace).Exec(ctx); err != nil {
		return err
	}
	// A Membership inserted directly sets its role; an update changes one.
	owner := veloxgen.Membership{AuthorID: authors["Ada"], ClubID: clubs["Chess"], Role: veloxgen.MembershipRoleOwner}
	if err := db.Insert(&owner).Exec(ctx); err != nil {
		return err
	}
	if _, err := db.Update[veloxgen.Membership]().Set(M.Role.Set(veloxgen.MembershipRoleOwner)).
		Where(M.AuthorID.EQ(authors["Grace"]), M.ClubID.EQ(clubs["Go"])).Exec(ctx); err != nil {
		return err
	}

	// The many-to-many edge reads the clubs; the through edge reads the
	// links, with their columns and their own edges.
	chess, err := db.Query[veloxgen.Club]().Where(C.Name.EQ("Chess")).With(C.Members.OrderBy(A.Name.Asc())).One(ctx)
	if err != nil {
		return err
	}
	members, _ := chess.Edges.Members.Get()
	say("edge schema: %s has %s", chess.Name, names(members))
	g, err := db.Query[veloxgen.Author]().Where(grace).With(A.Memberships.OrderBy(M.ID.Asc()).With(M.Club)).One(ctx)
	if err != nil {
		return err
	}
	links, _ := g.Edges.Memberships.Get()
	for _, l := range links {
		c, _ := l.Edges.Club.Get()
		say("membership: %s in %s as %s", g.Name, c.Name, l.Role)
	}
	owners, err := db.Query[veloxgen.Author]().Where(A.Memberships.HasWith(M.Role.EQ(veloxgen.MembershipRoleOwner))).
		OrderBy(A.Name.Asc()).Limit(10).All(ctx)
	if err != nil {
		return err
	}
	say("owners: %s", names(owners))

	// Remove deletes Grace's Chess row; deleting the club Go deletes its
	// rows, by the keys' ON DELETE CASCADE.
	if _, err := db.Update[veloxgen.Author]().Set(A.Clubs.Remove(clubs["Chess"])).Where(grace).Exec(ctx); err != nil {
		return err
	}
	if _, err := db.Delete[veloxgen.Club]().Where(C.ID.EQ(clubs["Go"])).Exec(ctx); err != nil {
		return err
	}
	n, err := db.Query[veloxgen.Membership]().Count(ctx)
	if err != nil {
		return err
	}
	say("memberships left: %d", n)
	return nil
}

// manyToMany links posts and tags. A many-to-many edge has no column on either
// side: a link is a row of the join entity, veloxgen.PostTag, which you insert and
// delete like any other. Its primary key is (post_id, tag_id), so linking twice
// fails, unless the insert says OnConflict(...).DoNothing().
func manyToMany(ctx context.Context, db *velox.DB, say func(string, ...any)) error {
	P, T, PT := veloxgen.Posts, veloxgen.Tags, veloxgen.PostTags
	tags := map[string]int64{}
	for _, name := range []string{"history", "languages"} {
		tg := veloxgen.Tag{Name: name}
		if err := db.Insert(&tg).Exec(ctx); err != nil {
			return err
		}
		tags[name] = tg.ID
	}
	posts := map[string]int64{}
	all, err := db.Query[veloxgen.Post]().Limit(10).All(ctx)
	if err != nil {
		return err
	}
	for _, p := range all {
		posts[p.Title] = p.ID
	}

	// Link: one join row per (post, tag).
	if err := db.InsertAll([]veloxgen.PostTag{
		{PostID: posts["COBOL"], TagID: tags["history"]},
		{PostID: posts["COBOL"], TagID: tags["languages"]},
		{PostID: posts["Compilers"], TagID: tags["languages"]},
		{PostID: posts["Engines"], TagID: tags["history"]},
	}).Exec(ctx); err != nil {
		return err
	}
	// Linking again keeps the existing row.
	again := veloxgen.PostTag{PostID: posts["COBOL"], TagID: tags["history"]}
	if err := db.Insert(&again).OnConflict(PT.PostID, PT.TagID).DoNothing().Exec(ctx); err != nil {
		return err
	}

	// With and Has work through the join table as on any relation.
	tagged, err := db.Query[veloxgen.Post]().
		Where(P.Tags.Has()).
		OrderBy(P.Title.Asc()).
		With(P.Tags.OrderBy(T.Name.Asc())).
		Limit(10).
		All(ctx)
	if err != nil {
		return err
	}
	for _, p := range tagged {
		ts, _ := p.Edges.Tags.Get()
		say("many-to-many: %s tagged %s", p.Title, tagNames(ts))
	}
	langs, err := db.Query[veloxgen.Tag]().Where(T.Name.EQ("languages")).With(T.Posts.OrderBy(P.Title.Asc())).One(ctx)
	if err != nil {
		return err
	}
	ps, _ := langs.Edges.Posts.Get()
	say("many-to-many, the other way: %s tags %s", langs.Name, titles(ps))

	// Unlink: delete the join row.
	n, err := db.Delete[veloxgen.PostTag]().
		Where(PT.PostID.EQ(posts["COBOL"]), PT.TagID.EQ(tags["history"])).
		Exec(ctx)
	if err != nil {
		return err
	}
	history, err := db.Query[veloxgen.Post]().Where(P.Tags.HasWith(T.Name.EQ("history"))).OrderBy(P.Title.Asc()).Limit(10).All(ctx)
	if err != nil {
		return err
	}
	say("unlinked %d: history tags %s", n, titles(history))
	return nil
}

func tagNames(ts []*veloxgen.Tag) string {
	var s []string
	for _, t := range ts {
		s = append(s, t.Name)
	}
	return "[" + strings.Join(s, ", ") + "]"
}

// seed inserts three authors, five posts and three comments. Insert reads
// each generated key back, which the rows that refer to it need.
func seed(ctx context.Context, db *velox.DB) error {
	ids := map[string]int64{}
	for _, name := range []string{"Ada", "Grace", "Linus"} {
		a := veloxgen.Author{Name: name}
		if err := db.Insert(&a).Exec(ctx); err != nil {
			return err
		}
		ids[name] = a.ID
	}
	posts := []veloxgen.Post{
		{AuthorID: ids["Ada"], Title: "Engines", Votes: 12},
		{AuthorID: ids["Ada"], Title: "Notes", Votes: 3},
		{AuthorID: ids["Grace"], Title: "Compilers", Votes: 30},
		{AuthorID: ids["Grace"], Title: "Bugs", Votes: 8},
		{AuthorID: ids["Grace"], Title: "COBOL", Votes: 15},
	}
	for i := range posts {
		if err := db.Insert(&posts[i]).Exec(ctx); err != nil {
			return err
		}
	}
	// InsertAll reads no keys back; these rows need none.
	return db.InsertAll([]veloxgen.Comment{
		{PostID: posts[2].ID, Body: "Classic."},
		{PostID: posts[2].ID, Body: "Still true."},
		{PostID: posts[4].ID, Body: "It runs the banks."},
	}).Exec(ctx)
}

func titles(ps []*veloxgen.Post) string {
	var s []string
	for _, p := range ps {
		s = append(s, p.Title)
	}
	return "[" + strings.Join(s, ", ") + "]"
}

func names(as []*veloxgen.Author) string {
	var s []string
	for _, a := range as {
		s = append(s, a.Name)
	}
	return "[" + strings.Join(s, ", ") + "]"
}
