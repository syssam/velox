// Command relations shows one-to-many, belongs-to and many-to-many relations:
// eager loading with With, EXISTS conditions with Has and HasWhere, linking
// and unlinking through a join entity, and keyset pagination with After.
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
	"github.com/syssam/velox/examples/relations/gen"
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
//	go tool velox-gen migrate diff -dialect sqlite -name init -dir ./relations/migrations ./relations/schema
//
// and, once Tag was added, again with -name tags.
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
	A, P := gen.Authors, gen.Posts
	say := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format+"\n", args...) }

	if err := seed(ctx, db); err != nil {
		return err
	}

	// With loads a relation with one more query (WHERE author_id IN (...)).
	// Nothing is ever loaded lazily: Get reports whether it was loaded.
	authors, err := db.Query[gen.Author]().
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
	authors, err = db.Query[gen.Author]().
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
	top, err := db.Query[gen.Post]().OrderBy(P.Votes.Desc()).With(P.Author).Limit(3).All(ctx)
	if err != nil {
		return err
	}
	for _, p := range top {
		a, _ := p.Edges.Author.Get()
		say("belongs-to: %s by %s", p.Title, a.Name)
	}

	// Has and HasWhere are EXISTS subqueries; Not(Has()) finds the authors
	// with no posts.
	for _, c := range []struct {
		name string
		pred velox.Pred[gen.Author]
	}{
		{"Has", A.Posts.Has()},
		{"HasWhere votes > 20", A.Posts.HasWhere(P.Votes.GT(20))},
		{"Not Has", velox.Not(A.Posts.Has())},
	} {
		got, err := db.Query[gen.Author]().Where(c.pred).OrderBy(A.Name.Asc()).Limit(10).All(ctx)
		if err != nil {
			return err
		}
		say("%s: %s", c.name, names(got))
	}
	text, _, err := db.Query[gen.Author]().Where(A.Posts.HasWhere(P.Votes.GT(20))).Limit(10).SQL(ctx)
	if err != nil {
		return err
	}
	say("SQL: %s", text)

	// After continues after the previous page's last row: keyset pagination.
	// The OrderBy must include the primary key or a unique column.
	page := db.Query[gen.Post]().OrderBy(P.Votes.Desc(), P.ID.Asc()).Limit(2)
	for i := 1; ; i++ {
		got, err := page.All(ctx)
		if err != nil {
			return err
		}
		if len(got) == 0 {
			break
		}
		say("page %d: %s", i, titles(got))
		page = page.After(&got[len(got)-1])
	}
	return manyToMany(ctx, db, say)
}

// manyToMany links posts and tags. A many-to-many edge has no column on either
// side: a link is a row of the join entity, gen.PostTag, which you insert and
// delete like any other. Its primary key is (post_id, tag_id), so linking twice
// fails, unless the insert says OnConflict(...).DoNothing().
func manyToMany(ctx context.Context, db *velox.DB, say func(string, ...any)) error {
	P, T, PT := gen.Posts, gen.Tags, gen.PostTags
	tags := map[string]int64{}
	for _, name := range []string{"history", "languages"} {
		tg := gen.Tag{Name: name}
		if err := db.Insert(&tg).Exec(ctx); err != nil {
			return err
		}
		tags[name] = tg.ID
	}
	posts := map[string]int64{}
	all, err := db.Query[gen.Post]().Limit(10).All(ctx)
	if err != nil {
		return err
	}
	for _, p := range all {
		posts[p.Title] = p.ID
	}

	// Link: one join row per (post, tag).
	if err := db.InsertAll([]gen.PostTag{
		{PostID: posts["COBOL"], TagID: tags["history"]},
		{PostID: posts["COBOL"], TagID: tags["languages"]},
		{PostID: posts["Compilers"], TagID: tags["languages"]},
		{PostID: posts["Engines"], TagID: tags["history"]},
	}).Exec(ctx); err != nil {
		return err
	}
	// Linking again keeps the existing row.
	again := gen.PostTag{PostID: posts["COBOL"], TagID: tags["history"]}
	if err := db.Insert(&again).OnConflict(PT.PostID, PT.TagID).DoNothing().Exec(ctx); err != nil {
		return err
	}

	// With and Has work through the join table as on any relation.
	tagged, err := db.Query[gen.Post]().
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
	langs, err := db.Query[gen.Tag]().Where(T.Name.EQ("languages")).With(T.Posts.OrderBy(P.Title.Asc())).One(ctx)
	if err != nil {
		return err
	}
	ps, _ := langs.Edges.Posts.Get()
	say("many-to-many, the other way: %s tags %s", langs.Name, titles(ps))

	// Unlink: delete the join row.
	n, err := db.Delete[gen.PostTag]().
		Where(PT.PostID.EQ(posts["COBOL"]), PT.TagID.EQ(tags["history"])).
		Exec(ctx)
	if err != nil {
		return err
	}
	history, err := db.Query[gen.Post]().Where(P.Tags.HasWhere(T.Name.EQ("history"))).OrderBy(P.Title.Asc()).Limit(10).All(ctx)
	if err != nil {
		return err
	}
	say("unlinked %d: history tags %s", n, titles(history))
	return nil
}

func tagNames(ts []gen.Tag) string {
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
		a := gen.Author{Name: name}
		if err := db.Insert(&a).Exec(ctx); err != nil {
			return err
		}
		ids[name] = a.ID
	}
	posts := []gen.Post{
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
	return db.InsertAll([]gen.Comment{
		{PostID: posts[2].ID, Body: "Classic."},
		{PostID: posts[2].ID, Body: "Still true."},
		{PostID: posts[4].ID, Body: "It runs the banks."},
	}).Exec(ctx)
}

func titles(ps []gen.Post) string {
	var s []string
	for _, p := range ps {
		s = append(s, p.Title)
	}
	return "[" + strings.Join(s, ", ") + "]"
}

func names(as []gen.Author) string {
	var s []string
	for _, a := range as {
		s = append(s, a.Name)
	}
	return "[" + strings.Join(s, ", ") + "]"
}
