// Command policy shows row-level policy: who may read, change and insert
// rows is decided from the context, as conditions velox adds to every
// statement, and a child entity inherits its parent's visibility.
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
	"strings"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/policy/auth"
	"github.com/syssam/velox/examples/policy/gen"
	_ "github.com/syssam/velox/examples/policy/gen/runtime" // binds the schema's policies into gen
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
//	go tool velox-gen migrate diff -dialect sqlite -name init -dir ./policy/migrations ./policy/schema
//
// and applied with migrate.Up. The temporary SQLite database is this demo's;
// a real application opens its own.
//
//go:embed migrations
var migrations embed.FS

func run(ctx context.Context, w io.Writer) error {
	dir, err := os.MkdirTemp("", "velox-policy-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	db, err := velox.Connect("sqlite", filepath.Join(dir, "policy.db")+"?_pragma=foreign_keys(1)")
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
	D, C := gen.Docs, gen.Comments
	say := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format+"\n", args...) }

	alice, bob, admin := auth.WithUser(ctx, "alice"), auth.WithUser(ctx, "bob"), auth.WithUser(ctx, "admin")

	// Each owner inserts their own docs; the Insert policy checks the row.
	docs := map[string]*gen.Doc{
		"Roadmap":  {Owner: "alice", Title: "Roadmap", Shared: true},
		"Salaries": {Owner: "alice", Title: "Salaries"},
		"Notes":    {Owner: "bob", Title: "Notes"},
	}
	for _, title := range []string{"Roadmap", "Salaries", "Notes"} {
		d := docs[title]
		if err := db.Insert(d).Exec(auth.WithUser(ctx, d.Owner)); err != nil {
			return err
		}
	}
	err = db.InsertAll([]gen.Comment{
		{DocID: docs["Roadmap"].ID, Body: "Ship it"},
		{DocID: docs["Salaries"].ID, Body: "Confidential"},
		{DocID: docs["Notes"].ID, Body: "Todo"},
	}).Exec(admin) // Comment has no Insert check: a policy function gets no *DB to look up the doc
	if err != nil {
		return err
	}

	// The same query reads different rows for different users. SQL(ctx)
	// shows the condition the policy adds for that context.
	byTitle := db.Query[gen.Doc]().OrderBy(D.Title.Asc()).Limit(10)
	for _, c := range []struct {
		who string
		ctx context.Context
	}{{"alice", alice}, {"bob", bob}, {"admin", admin}} {
		got, err := byTitle.All(c.ctx)
		if err != nil {
			return err
		}
		text, args, err := byTitle.Columns(D.Title).SQL(c.ctx)
		if err != nil {
			return err
		}
		say("%s sees %s\n  %s %v", c.who, titles(got), text, args)
	}

	// A comment is visible when its doc is: Comment's Read is Doc.Has(), and
	// velox applies Doc's Read inside that EXISTS.
	comments, err := db.Query[gen.Comment]().OrderBy(C.ID.Asc()).Limit(10).All(bob)
	if err != nil {
		return err
	}
	for _, c := range comments {
		say("bob sees comment %q", c.Body)
	}
	text, args, err := db.Query[gen.Comment]().Columns(C.Body).Limit(10).SQL(bob)
	if err != nil {
		return err
	}
	say("  %s %v", text, args)

	// Write filters: bob's update of alice's doc matches no row. It is not
	// an error, as an update whose Where matches nothing is not.
	for _, title := range []string{"Roadmap", "Notes"} {
		n, err := db.Update[gen.Doc]().Set(D.Shared.Set(false)).Where(D.Title.EQ(title)).Exec(bob)
		if err != nil {
			return err
		}
		say("bob unshares %s: rows changed=%d", title, n)
	}

	// An error from a policy function denies the statement: a *PolicyError
	// that matches ErrDenied.
	_, err = db.Query[gen.Doc]().Count(ctx)
	if pe, ok := errors.AsType[*velox.PolicyError](err); ok {
		say("no user: ErrDenied=%t, entity %s, op %s, reason %q", errors.Is(err, velox.ErrDenied), pe.Entity, pe.Op, pe.Err)
	}
	err = db.Insert(&gen.Doc{Owner: "alice", Title: "Forged"}).Exec(bob)
	say("bob inserts a doc as alice: %v", err)

	// WithoutPolicy switches every policy off for one context, with a reason
	// that hooks see as Statement.PolicyBypass. It lifts a scope (the scope package) too,
	// as that is a policy.
	n, err := db.Query[gen.Doc]().Count(velox.WithoutPolicy(ctx, "nightly backup"))
	if err != nil {
		return err
	}
	say("WithoutPolicy: %d docs", n)
	return nil
}

func titles(ds []gen.Doc) string {
	var s []string
	for _, d := range ds {
		s = append(s, d.Title)
	}
	return "[" + strings.Join(s, ", ") + "]"
}
