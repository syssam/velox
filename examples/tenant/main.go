// Command tenant shows tenant scoping: the context names the tenant, and
// every read, write and insert on a tenant-scoped entity is confined to it.
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

// The tenants, of the tenant column's Go type: int64. An untyped 1 is an int,
// which WithTenant converts to it; an id of another named type it refuses.
const (
	acme   int64 = 1
	globex int64 = 2
)

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
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	if _, err := migrate.Up(ctx, velox.Pool(db), sub); err != nil {
		return err
	}
	P := gen.Projects
	say := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format+"\n", args...) }

	// Middleware would set the tenant once per request.
	acmeCtx := velox.WithTenant(ctx, acme)
	globexCtx := velox.WithTenant(ctx, globex)

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
	// The same name in another tenant: the unique key includes OrgID.
	if err := db.InsertAll([]gen.Project{{Name: "Rocket", Status: "active"}}).Exec(globexCtx); err != nil {
		return err
	}
	say("InsertAll: org 1 has Rocket, Anvil, Magnet; org 2 has Rocket")

	// Reads see only the context's tenant.
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

	// Fail closed: no tenant, a named type other than the column's, a row
	// naming another tenant, and moving a row to another tenant are all
	// refused.
	_, err = db.Query[gen.Project]().Count(ctx)
	say("no tenant: ErrNoTenant=%t", errors.Is(err, velox.ErrNoTenant))
	n, err = db.Query[gen.Project]().Count(velox.WithTenant(ctx, 1)) // an int, converted to the column's int64
	say("tenant 1 as an int: %d projects, err=%v", n, err)
	type userID int64
	_, err = db.Query[gen.Project]().Count(velox.WithTenant(ctx, userID(1)))
	say("tenant of another named type: ErrTenantMismatch=%t", errors.Is(err, velox.ErrTenantMismatch))
	err = db.Insert(&gen.Project{OrgID: globex, Name: "Spy", Status: "active"}).Exec(acmeCtx)
	say("insert for org 2 as org 1: ErrTenantMismatch=%t", errors.Is(err, velox.ErrTenantMismatch))
	_, err = db.Update[gen.Project]().Set(P.OrgID.Set(globex)).Where(P.Name.EQ("Rocket")).Exec(acmeCtx)
	say("move a row to org 2: ErrTenantImmutable=%t", errors.Is(err, velox.ErrTenantImmutable))

	// An upsert that updates must name the tenant column in OnConflict, so a
	// conflict on another tenant's key fails instead of changing its row.
	err = db.Insert(&gen.Project{Name: "Rocket", Status: "done"}).
		OnConflict(P.Name).Update(P.Status).Exec(acmeCtx)
	say("upsert on (name): ErrTenantUpsert=%t", errors.Is(err, velox.ErrTenantUpsert))
	upsert := db.Insert(&gen.Project{Name: "Rocket", Status: "done"}).OnConflict(P.OrgID, P.Name).Update(P.Status)
	text, _, err = upsert.SQL(acmeCtx)
	if err != nil {
		return err
	}
	say("upsert on (org_id, name): %s", text)
	if err := upsert.Exec(acmeCtx); err != nil {
		return err
	}

	// WithAllTenants lifts the scope for one context and needs a reason, which
	// hooks see as Statement.TenantBypass. An empty reason is ignored.
	report := velox.WithAllTenants(ctx, "weekly status report")
	for p, err := range db.Query[gen.Project]().OrderBy(P.OrgID.Asc(), P.Name.Asc()).Unbounded().Iter(report) {
		if err != nil {
			return err
		}
		say("WithAllTenants: org %d %s %s", p.OrgID, p.Name, p.Status)
	}
	_, err = db.Query[gen.Project]().Count(velox.WithAllTenants(ctx, ""))
	say("WithAllTenants with no reason: ErrNoTenant=%t", errors.Is(err, velox.ErrNoTenant))
	return nil
}
