// Command hooks shows entity hooks: the Hooks methods of schema.Task and of
// its CreatedBy mixin, an insert hook that validates and derives a field, an
// update hook that works through a Mutation, a delete hook that narrows, and
// Save storing what hooks set. A run-time hook, from velox.WithHooks, writes
// an audit row on the write's DB (inside db.Tx, it commits or rolls back with
// the write), and a dry run refuses that write.
package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/hooks/auth"
	"github.com/syssam/velox/examples/hooks/veloxgen"
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
//	go tool velox-gen migrate diff -dialect sqlite -name init -dir ./hooks/migrations ./hooks/veloxschema
//
// and applied with migrate.Up. The temporary SQLite database is this demo's;
// a real application opens its own.
//
//go:embed migrations
var migrations embed.FS

func run(ctx context.Context, w io.Writer) error {
	dir, err := os.MkdirTemp("", "velox-hooks-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	db, err := velox.Connect("sqlite", filepath.Join(dir, "hooks.db"),
		velox.WithHooks[veloxgen.Task](velox.On(auditRename, velox.OpUpdate)))
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
	T := veloxgen.Tasks
	say := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format+"\n", args...) }
	// trail lists the audit rows, written by the update hook.
	trail := func() ([]string, error) {
		var rows []string
		for a, err := range db.Query[veloxgen.Audit]().OrderBy(veloxgen.Audits.ID.Asc()).AllowUnbounded().Iter(ctx) {
			if err != nil {
				return nil, err
			}
			rows = append(rows, a.Actor+": "+a.Message)
		}
		return rows, nil
	}

	ada := auth.WithUser(ctx, "ada")

	// The insert hooks: the CreatedBy mixin's fills created_by from the
	// context's user; deriveSlug checks the title and derives the slug, in
	// the caller's struct.
	err = db.Insert(&veloxgen.Task{}).Exec(ada)
	say("Insert with no title: %v", err)
	readme := veloxgen.Task{Title: "Write the README"}
	if err := db.Insert(&readme).Exec(ada); err != nil {
		return err
	}
	say("Insert: id %d, slug %q, created by %s", readme.ID, readme.Slug, readme.CreatedBy)
	more := []veloxgen.Task{{Title: "Fix CI", Done: true}, {Title: "Tag v1", Done: false}}
	if err := db.InsertAll(more).Exec(ada); err != nil {
		return err
	}
	say("InsertAll: slugs %q and %q", more[0].Slug, more[1].Slug)

	// The update hook adds the slug to an update that sets the title. SQL(ctx)
	// runs the hooks too, so the statement it prints has the slug;
	// auditRename's write is refused (velox.ErrDryRun), so it records nothing.
	rename := db.Update[veloxgen.Task]().Set(T.Title.Set("Tag v1.0")).Where(T.Slug.EQ("tag-v1"))
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

	_, err = db.Update[veloxgen.Task]().Set(T.Title.Set("")).Where(T.ID.EQ(readme.ID)).Exec(ada)
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
		_, err := tx.Update[veloxgen.Task]().Set(T.Title.Set("Ship it")).Where(T.ID.EQ(readme.ID)).Exec(ctx)
		if err != nil {
			return err
		}
		inside, err := tx.Query[veloxgen.Audit]().Count(ctx)
		if err != nil {
			return err
		}
		say("in the transaction: %d audit rows", inside)
		return errAbort
	})
	again, err := db.Query[veloxgen.Task]().Where(T.ID.EQ(readme.ID)).One(ada)
	if err != nil {
		return err
	}
	if rows, err = trail(); err != nil {
		return err
	}
	say("rolled back (%v): title %q, %d audit rows", txErr, again.Title, len(rows))

	// The delete hook narrows even a delete of every row to the finished
	// tasks.
	wipe := db.Delete[veloxgen.Task]().All()
	text, _, err = wipe.SQL(ada)
	if err != nil {
		return err
	}
	say("SQL: %s", text)
	n, err := wipe.Exec(ada)
	if err != nil {
		return err
	}
	left, err := db.Query[veloxgen.Task]().Count(ada)
	if err != nil {
		return err
	}
	say("Delete All: %d removed, %d left", n, left)
	return nil
}

// auditRename writes an Audit row when an update renames a task, after the
// update succeeded, on the write's DB: inside db.Tx it commits or rolls back
// with the update. It is a run-time hook because it inserts a veloxgen.Audit,
// which the schema cannot name. Under SQL(ctx) the insert is refused with
// velox.ErrDryRun, so printing the statement records nothing.
func auditRename(next velox.Mutator) velox.Mutator {
	return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
		title, renamed := m.Value("title")
		if err := next.Mutate(ctx, m); err != nil || !renamed {
			return err
		}
		a := veloxgen.Audit{Actor: auth.UserFromContext(ctx), Message: "renamed a task to " + title.(string)}
		return m.DB().Insert(&a).Exec(ctx)
	})
}
