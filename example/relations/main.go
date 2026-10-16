// Command relations shows one-to-many and belongs-to relations: eager
// loading with With, EXISTS conditions with Has and HasWhere, and keyset
// pagination with After.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/example/relations/gen"
)

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// ddl creates the tables by hand: planning migrations from the schemas is a
// later velox2 sub-project.
const ddl = `
CREATE TABLE authors  (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE posts    (id INTEGER PRIMARY KEY, author_id INTEGER NOT NULL REFERENCES authors (id),
                       title TEXT NOT NULL, votes INTEGER NOT NULL);
CREATE TABLE comments (id INTEGER PRIMARY KEY, post_id INTEGER NOT NULL REFERENCES posts (id),
                       body TEXT NOT NULL);`

func run(ctx context.Context, w io.Writer) error {
	sqlDB, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	sqlDB.SetMaxOpenConns(1) // an in-memory database lives as long as its one connection
	if _, err := sqlDB.ExecContext(ctx, ddl); err != nil {
		return err
	}
	db := velox.Open(velox.SQLite, sqlDB)
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
		posts, loaded := a.Posts.Get()
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
	posts, _ := authors[0].Posts.Get()
	for _, p := range posts {
		comments, _ := p.Comments.Get()
		say("nested With: %s / %s, %d votes, comments=%d", authors[0].Name, p.Title, p.Votes, len(comments))
	}

	// Belongs-to: each post loads its author. Owners with the same key share
	// one loaded Author.
	top, err := db.Query[gen.Post]().OrderBy(P.Votes.Desc()).With(P.Author).Limit(3).All(ctx)
	if err != nil {
		return err
	}
	for _, p := range top {
		a, _ := p.Author.Get()
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
	return nil
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
