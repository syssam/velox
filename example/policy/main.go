// Command policy shows row-level policy: who may read, change and insert
// rows is decided from the context, as conditions velox adds to every
// statement, and a child entity inherits its parent's visibility.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/example/policy/model"
)

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// ddl creates the tables by hand: planning migrations from the structs is a
// later velox2 sub-project.
const ddl = `
CREATE TABLE docs     (id INTEGER PRIMARY KEY, owner TEXT NOT NULL, title TEXT NOT NULL, shared BOOLEAN NOT NULL);
CREATE TABLE comments (id INTEGER PRIMARY KEY, doc_id INTEGER NOT NULL REFERENCES docs (id), body TEXT NOT NULL);`

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
	D, C := model.Docs, model.Comments
	say := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format+"\n", args...) }

	alice, bob, admin := model.AsUser(ctx, "alice"), model.AsUser(ctx, "bob"), model.AsUser(ctx, "admin")

	// Each owner inserts their own docs; the Insert policy checks the row.
	docs := map[string]*model.Doc{
		"Roadmap":  {Owner: "alice", Title: "Roadmap", Shared: true},
		"Salaries": {Owner: "alice", Title: "Salaries"},
		"Notes":    {Owner: "bob", Title: "Notes"},
	}
	for _, title := range []string{"Roadmap", "Salaries", "Notes"} {
		d := docs[title]
		if err := db.Insert(d).Exec(model.AsUser(ctx, d.Owner)); err != nil {
			return err
		}
	}
	err = db.InsertAll([]model.Comment{
		{DocID: docs["Roadmap"].ID, Body: "Ship it"},
		{DocID: docs["Salaries"].ID, Body: "Confidential"},
		{DocID: docs["Notes"].ID, Body: "Todo"},
	}).Exec(admin) // Comment has no Insert check: a policy function gets no *DB to look up the doc
	if err != nil {
		return err
	}

	// The same query reads different rows for different users. SQL(ctx)
	// shows the condition the policy adds for that context.
	byTitle := db.Query[model.Doc]().OrderBy(D.Title.Asc()).Limit(10)
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
	comments, err := db.Query[model.Comment]().OrderBy(C.ID.Asc()).Limit(10).All(bob)
	if err != nil {
		return err
	}
	for _, c := range comments {
		say("bob sees comment %q", c.Body)
	}
	text, args, err := db.Query[model.Comment]().Columns(C.Body).Limit(10).SQL(bob)
	if err != nil {
		return err
	}
	say("  %s %v", text, args)

	// Write filters: bob's update of alice's doc matches no row. It is not
	// an error, as an update whose Where matches nothing is not.
	for _, title := range []string{"Roadmap", "Notes"} {
		n, err := db.Update[model.Doc]().Set(D.Shared.Set(false)).Where(D.Title.EQ(title)).Exec(bob)
		if err != nil {
			return err
		}
		say("bob unshares %s: rows changed=%d", title, n)
	}

	// An error from a policy function denies the statement: a *PolicyError
	// that matches ErrDenied.
	_, err = db.Query[model.Doc]().Count(ctx)
	var pe *velox.PolicyError
	if errors.As(err, &pe) {
		say("no user: ErrDenied=%t, entity %s, op %s, reason %q", errors.Is(err, velox.ErrDenied), pe.Entity, pe.Op, pe.Err)
	}
	err = db.Insert(&model.Doc{Owner: "alice", Title: "Forged"}).Exec(bob)
	say("bob inserts a doc as alice: %v", err)

	// SkipPolicy switches every policy off for one context, with a reason
	// that hooks see as Statement.PolicyBypass. It does not lift tenant
	// scoping (see AllTenants).
	n, err := db.Query[model.Doc]().Count(velox.SkipPolicy(ctx, "nightly backup"))
	if err != nil {
		return err
	}
	say("SkipPolicy: %d docs", n)
	return nil
}

func titles(ds []model.Doc) string {
	var s []string
	for _, d := range ds {
		s = append(s, d.Title)
	}
	return "[" + strings.Join(s, ", ") + "]"
}
