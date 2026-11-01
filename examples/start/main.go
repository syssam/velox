// Command start shows create, read, update and delete on one entity,
// gen.User, against a SQLite file made by its migrations.
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
	"github.com/syssam/velox/examples/start/gen"
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
//	go tool velox-gen migrate diff -dialect sqlite -name init -dir ./start/migrations ./start/schema
//
// and applied with migrate.Up. The temporary SQLite database is this demo's;
// a real application opens its own.
//
//go:embed migrations
var migrations embed.FS

func run(ctx context.Context, w io.Writer) error {
	dir, err := os.MkdirTemp("", "velox-start-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	db, err := velox.Connect("sqlite", filepath.Join(dir, "start.db"))
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

	// U holds the typed column handles velox-gen wrote in gen/user_velox.go.
	U := gen.Users
	say := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format+"\n", args...) }

	// Create. Insert reads the generated ID back into the struct.
	ada := gen.User{Name: "Ada", Email: "ada@example.com", Age: 36}
	if err := db.Insert(&ada).Exec(ctx); err != nil {
		return err
	}
	say("Insert: %s has id %d", ada.Name, ada.ID)

	// InsertAll sends one statement for many rows. It reads no IDs back.
	err = db.InsertAll([]gen.User{
		{Name: "Grace", Email: "grace@example.com", Age: 45},
		{Name: "Linus", Email: "linus@example.com", Age: 28},
		{Name: "Barbara", Email: "barbara@example.com", Age: 52},
	}).Exec(ctx)
	if err != nil {
		return err
	}
	say("InsertAll: 3 users")

	// Read. A query is a value: build it with Where, OrderBy and Limit, then
	// run it with All, One, Count or Iter.
	over30, err := db.Query[gen.User]().
		Where(U.Age.GT(30)).
		OrderBy(U.Age.Desc()).
		Limit(10).
		All(ctx)
	if err != nil {
		return err
	}
	for _, u := range over30 {
		say("All: %s, %d", u.Name, u.Age)
	}

	// One returns exactly one row, ErrNotFound for none.
	grace, err := db.Query[gen.User]().Where(U.Email.EQ("grace@example.com")).One(ctx)
	if err != nil {
		return err
	}
	say("One: %s has id %d", grace.Name, grace.ID)
	_, err = db.Query[gen.User]().Where(U.Name.EQ("Nobody")).One(ctx)
	say("One: ErrNotFound=%t", velox.IsNotFound(err))

	n, err := db.Query[gen.User]().Where(U.Name.HasPrefix("B")).Count(ctx)
	if err != nil {
		return err
	}
	say("Count: %d name starts with B", n)

	// Iter holds one row at a time. Unbounded says there is no Limit on
	// purpose.
	for u, err := range db.Query[gen.User]().OrderBy(U.Name.Asc()).Unbounded().Iter(ctx) {
		if err != nil {
			return err
		}
		say("Iter: %s", u.Name)
	}

	// SQL(ctx) prints the statement a query would send, without sending it.
	text, args, err := db.Query[gen.User]().Where(U.Age.Between(30, 50)).Limit(5).SQL(ctx)
	if err != nil {
		return err
	}
	say("SQL: %s %v", text, args)

	// Update. A set-based Update returns the number of rows it changed.
	n, err = db.Update[gen.User]().
		Set(U.Age.Add(1)).
		Where(U.Name.In("Ada", "Linus")).
		Exec(ctx)
	if err != nil {
		return err
	}
	say("Update: %d birthdays", n)

	// Save writes the named columns of a loaded struct, by its primary key.
	// There is no Save of every column: writing back a stale struct would
	// lose concurrent updates.
	grace.Email = "grace.hopper@example.com"
	if err := db.Save(grace, U.Email).Exec(ctx); err != nil {
		return err
	}
	say("Save: %s's email is %s", grace.Name, grace.Email)

	// Delete returns the number of rows it removed.
	n, err = db.Delete[gen.User]().Where(U.Age.LT(30)).Exec(ctx)
	if err != nil {
		return err
	}
	say("Delete: %d user under 30", n)

	n, err = db.Query[gen.User]().Count(ctx)
	if err != nil {
		return err
	}
	say("Count: %d users left", n)

	// Guards are always on: a read with no Limit, and an update or delete
	// with no Where, fail before anything is sent. Unbounded() and All() are
	// the per-query escapes.
	_, err = db.Query[gen.User]().All(ctx)
	say("no Limit: ErrUnbounded=%t", errors.Is(err, velox.ErrUnbounded))
	_, err = db.Delete[gen.User]().Exec(ctx)
	say("no Where: ErrNoWhere=%t", errors.Is(err, velox.ErrNoWhere))
	return nil
}
