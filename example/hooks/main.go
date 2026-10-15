// Command hooks shows entity hooks: BeforeInsert for validation and derived
// fields (a mixin's included), BeforeUpdate through a Mutation, BeforeDelete
// narrowing a delete, Save storing what hooks set, and IsDryRun.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/example/hooks/model"
)

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// ddl creates the table by hand: planning migrations from the structs is a
// later velox2 sub-project.
const ddl = `
CREATE TABLE tasks (
	id         INTEGER PRIMARY KEY,
	created_by TEXT NOT NULL,
	title      TEXT NOT NULL,
	slug       TEXT NOT NULL,
	done       BOOLEAN NOT NULL
)`

func run(ctx context.Context, w io.Writer) error {
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	sqlDB.SetMaxOpenConns(1) // an in-memory database lives as long as its one connection
	if _, err := sqlDB.ExecContext(ctx, ddl); err != nil {
		return err
	}
	db := velox.Open(velox.SQLite, sqlDB)
	T := model.Tasks
	say := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format+"\n", args...) }

	var log []string
	ada := model.WithLog(model.AsUser(ctx, "ada"), &log)

	// BeforeInsert: the mixin's hook fills CreatedBy, Task's checks the title
	// and derives the slug, in the caller's struct.
	err = db.Insert(&model.Task{}).Exec(ada)
	say("Insert with no title: %v", err)
	readme := model.Task{Title: "Write the README"}
	if err := db.Insert(&readme).Exec(ada); err != nil {
		return err
	}
	say("Insert: id %d, slug %q, created by %s", readme.ID, readme.Slug, readme.CreatedBy)
	more := []model.Task{{Title: "Fix CI", Done: true}, {Title: "Tag v1", Done: false}}
	if err := db.InsertAll(more).Exec(ada); err != nil {
		return err
	}
	say("InsertAll: slugs %q and %q", more[0].Slug, more[1].Slug)

	// BeforeUpdate adds the slug to an update that sets the title. SQL(ctx)
	// runs the hook too, so the statement it prints has the slug; the hook's
	// IsDryRun check keeps it out of the audit log.
	rename := db.Update[model.Task]().Set(T.Title.Set("Tag v1.0")).Where(T.Slug.EQ("tag-v1"))
	text, args, err := rename.SQL(ada)
	if err != nil {
		return err
	}
	say("SQL: %s %v", text, args)
	say("audit log after SQL(ctx): %d entries", len(log))
	if _, err := rename.Exec(ada); err != nil {
		return err
	}
	say("audit log after Exec: %v", log)

	_, err = db.Update[model.Task]().Set(T.Title.Set("")).Where(T.ID.EQ(readme.ID)).Exec(ada)
	say("Update to no title: %v", err)

	// Save runs BeforeUpdate as well, then stores every value it sent into
	// the struct: the slug the hook set included.
	readme.Title = "Write the docs"
	if err := db.Save(ada, &readme, T.Title); err != nil {
		return err
	}
	say("Save: title %q, slug %q", readme.Title, readme.Slug)

	// BeforeDelete narrows even a delete of every row to the finished tasks.
	wipe := db.Delete[model.Task]().All()
	text, _, err = wipe.SQL(ada)
	if err != nil {
		return err
	}
	say("SQL: %s", text)
	n, err := wipe.Exec(ada)
	if err != nil {
		return err
	}
	left, err := db.Query[model.Task]().Count(ada)
	if err != nil {
		return err
	}
	say("Delete All: %d removed, %d left", n, left)
	return nil
}
