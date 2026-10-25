// Command hooks shows entity hooks, the Hooks method of gen.Task: an insert
// hook that validates and derives a field, a generic hook that any entity
// lists, an update hook that works through a Mutation and writes an audit row
// on the write's DB (inside db.Tx, it commits or rolls back with the write), a
// delete hook that narrows, On, Save storing what hooks set, and a dry run
// that refuses the audit write.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/example/hooks/gen"
)

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// ddl creates the table by hand: planning migrations from the schemas is a
// later velox2 sub-project.
const ddl = `
CREATE TABLE tasks (
	id         INTEGER PRIMARY KEY,
	created_by TEXT NOT NULL,
	title      TEXT NOT NULL,
	slug       TEXT NOT NULL,
	done       BOOLEAN NOT NULL
);
CREATE TABLE audits (
	id      INTEGER PRIMARY KEY,
	actor   TEXT NOT NULL,
	message TEXT NOT NULL
);`

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
	T := gen.Tasks
	say := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format+"\n", args...) }
	// trail lists the audit rows, written by the update hook.
	trail := func() ([]string, error) {
		var rows []string
		for a, err := range db.Query[gen.Audit]().OrderBy(gen.Audits.ID.Asc()).Unbounded().Iter(ctx) {
			if err != nil {
				return nil, err
			}
			rows = append(rows, a.Actor+": "+a.Message)
		}
		return rows, nil
	}

	ada := gen.WithUser(ctx, "ada")

	// The insert hooks: createdBy, a generic hook, fills CreatedBy from the
	// context's user; deriveSlug checks the title and derives the slug, in
	// the caller's struct.
	err = db.Insert(&gen.Task{}).Exec(ada)
	say("Insert with no title: %v", err)
	readme := gen.Task{Title: "Write the README"}
	if err := db.Insert(&readme).Exec(ada); err != nil {
		return err
	}
	say("Insert: id %d, slug %q, created by %s", readme.ID, readme.Slug, readme.CreatedBy)
	more := []gen.Task{{Title: "Fix CI", Done: true}, {Title: "Tag v1", Done: false}}
	if err := db.InsertAll(more).Exec(ada); err != nil {
		return err
	}
	say("InsertAll: slugs %q and %q", more[0].Slug, more[1].Slug)

	// The update hook adds the slug to an update that sets the title. SQL(ctx)
	// runs the hook too, so the statement it prints has the slug; the hook's
	// audit write is refused (velox.ErrDryRun), so it records nothing.
	rename := db.Update[gen.Task]().Set(T.Title.Set("Tag v1.0")).Where(T.Slug.EQ("tag-v1"))
	text, args, err := rename.SQL(ada)
	if err != nil {
		return err
	}
	say("SQL: %s %v", text, args)
	rows, err := trail()
	if err != nil {
		return err
	}
	say("audit rows after SQL(ctx): %d", len(rows))
	if _, err := rename.Exec(ada); err != nil {
		return err
	}
	if rows, err = trail(); err != nil {
		return err
	}
	say("audit rows after Exec: %q", rows)

	_, err = db.Update[gen.Task]().Set(T.Title.Set("")).Where(T.ID.EQ(readme.ID)).Exec(ada)
	say("Update to no title: %v", err)

	// Save runs the update hook as well, then stores every value it sent
	// into the struct: the slug the hook set included.
	readme.Title = "Write the docs"
	if err := db.Save(&readme, T.Title).Exec(ada); err != nil {
		return err
	}
	say("Save: title %q, slug %q", readme.Title, readme.Slug)

	// The audit row goes through m.DB(), which inside db.Tx is the
	// transaction: when fn fails, the rename and its audit row are both rolled
	// back.
	errAbort := errors.New("changed my mind")
	txErr := db.Tx(ada, func(ctx context.Context, tx *velox.DB) error {
		_, err := tx.Update[gen.Task]().Set(T.Title.Set("Ship it")).Where(T.ID.EQ(readme.ID)).Exec(ctx)
		if err != nil {
			return err
		}
		inside, err := tx.Query[gen.Audit]().Count(ctx)
		if err != nil {
			return err
		}
		say("in the transaction: %d audit rows", inside)
		return errAbort
	})
	again, err := db.Query[gen.Task]().Where(T.ID.EQ(readme.ID)).One(ada)
	if err != nil {
		return err
	}
	if rows, err = trail(); err != nil {
		return err
	}
	say("rolled back (%v): title %q, %d audit rows", txErr, again.Title, len(rows))

	// The delete hook narrows even a delete of every row to the finished
	// tasks.
	wipe := db.Delete[gen.Task]().All()
	text, _, err = wipe.SQL(ada)
	if err != nil {
		return err
	}
	say("SQL: %s", text)
	n, err := wipe.Exec(ada)
	if err != nil {
		return err
	}
	left, err := db.Query[gen.Task]().Count(ada)
	if err != nil {
		return err
	}
	say("Delete All: %d removed, %d left", n, left)
	return nil
}
