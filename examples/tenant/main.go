// Command tenant shows tenant scoping with the scope package: the context
// names the organization, and every read, write and insert on a scoped entity
// is confined to it. The scope is a Policy and a hook (gen/scope_velox.go, from the schema's Scoped), not
// something the engine knows about, so a rule of another shape is written the
// same way.
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
	"github.com/syssam/velox/examples/tenant/gen"
	"github.com/syssam/velox/migrate"
	"github.com/syssam/velox/scope"
)

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// The tables come from migrations/, made, in the example module, by
//
//	go tool velox-gen migrate diff -dialect sqlite -name init -dir ./tenant/migrations ./tenant/schema
//
// and applied with migrate.Up. The temporary SQLite database is this demo's;
// a real application opens its own.
//
//go:embed migrations
var migrations embed.FS

// The organizations, of the scope's Go type: int64.
const (
	acme   int64 = 1
	globex int64 = 2
)

// fsSub is the migrations directory's files.
func fsSub() (fs.FS, error) { return fs.Sub(migrations, "migrations") }

func run(ctx context.Context, w io.Writer) error {
	dir, err := os.MkdirTemp("", "velox-tenant-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	db, err := velox.Connect("sqlite", filepath.Join(dir, "tenant.db"))
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	sub, err := fsSub()
	if err != nil {
		return err
	}
	if _, err := migrate.Up(ctx, velox.Pool(db), sub); err != nil {
		return err
	}
	P := gen.Projects
	say := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format+"\n", args...) }

	// Middleware would set the organization once per request.
	acmeCtx := gen.OrgKey.With(ctx, acme)
	globexCtx := gen.OrgKey.With(ctx, globex)

	// Inserts fill OrgID from the context, into the caller's structs too.
	rocket := gen.Project{Name: "Rocket", Status: "active"}
	if err := db.Insert(&rocket).Exec(acmeCtx); err != nil {
		return err
	}
	say("Insert: %s got org_id %d", rocket.Name, rocket.OrgID)
	more := []gen.Project{{Name: "Anvil", Status: "active"}, {Name: "Magnet", Status: "paused"}}
	if err := db.InsertAll(more).Exec(acmeCtx); err != nil {
		return err
	}
	// The same name in another organization: the unique key includes OrgID.
	if err := db.InsertAll([]gen.Project{{Name: "Rocket", Status: "active"}}).Exec(globexCtx); err != nil {
		return err
	}
	say("InsertAll: org 1 has Rocket, Anvil, Magnet; org 2 has Rocket")

	// Reads see only the context's organization.
	for _, c := range []struct {
		name string
		ctx  context.Context
	}{{"org 1", acmeCtx}, {"org 2", globexCtx}} {
		n, err := db.Query[gen.Project]().Count(c.ctx)
		if err != nil {
			return err
		}
		say("Count as %s: %d", c.name, n)
	}
	text, args, err := db.Query[gen.Project]().Where(P.Status.EQ("active")).Limit(10).SQL(acmeCtx)
	if err != nil {
		return err
	}
	say("SQL: %s %v", text, args)
	_, err = db.Query[gen.Project]().Where(P.Name.EQ("Magnet")).One(globexCtx)
	say("org 2 asks for Magnet: ErrNotFound=%t", velox.IsNotFound(err))

	// Writes too: org 2 pausing every Anvil changes nothing of org 1's.
	n, err := db.Update[gen.Project]().Set(P.Status.Set("paused")).Where(P.Name.EQ("Anvil")).Exec(globexCtx)
	if err != nil {
		return err
	}
	say("org 2 pauses Anvil: %d rows changed (Anvil is org 1's)", n)

	// Fail closed: no organization, a row naming another one, and moving a row
	// to another one are all refused.
	_, err = db.Query[gen.Project]().Count(ctx)
	say("no organization: ErrNoScope=%t", errors.Is(err, scope.ErrNoScope))
	err = db.Insert(&gen.Project{OrgID: globex, Name: "Spy", Status: "active"}).Exec(acmeCtx)
	say("insert for org 2 as org 1: ErrMismatch=%t", errors.Is(err, scope.ErrMismatch))
	_, err = db.Update[gen.Project]().Set(P.OrgID.Set(globex)).Where(P.Name.EQ("Rocket")).Exec(acmeCtx)
	say("move a row to org 2: ErrImmutable=%t", errors.Is(err, velox.ErrImmutable))

	// An upsert names the unique key, which includes OrgID. The scope is ANDed
	// into the update branch, so it can change only this organization's row.
	upsert := db.Insert(&gen.Project{Name: "Rocket", Status: "done"}).OnConflict(P.OrgID, P.Name).Update(P.Status)
	text, _, err = upsert.SQL(acmeCtx)
	if err != nil {
		return err
	}
	say("upsert on (org_id, name): %s", text)
	if err := upsert.Exec(acmeCtx); err != nil {
		return err
	}

	// velox.WithoutPolicy lifts the scope, as it lifts every policy, and needs
	// a reason, which hooks see as Statement.PolicyBypass. An empty reason is
	// ignored.
	report := velox.WithoutPolicy(ctx, "weekly status report")
	for p, err := range db.Query[gen.Project]().OrderBy(P.OrgID.Asc(), P.Name.Asc()).Unbounded().Iter(report) {
		if err != nil {
			return err
		}
		say("WithoutPolicy: org %d %s %s", p.OrgID, p.Name, p.Status)
	}
	_, err = db.Query[gen.Project]().Count(velox.WithoutPolicy(ctx, ""))
	say("WithoutPolicy with no reason: ErrNoScope=%t", errors.Is(err, scope.ErrNoScope))
	return nil
}
