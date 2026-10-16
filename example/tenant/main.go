// Command tenant shows tenant scoping: the context names the tenant, and
// every read, write and insert on a tenant-scoped entity is confined to it.
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
	"github.com/syssam/velox2/example/tenant/gen"
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
CREATE TABLE projects (
	id     INTEGER PRIMARY KEY,
	org_id INTEGER NOT NULL,
	name   TEXT NOT NULL,
	status TEXT NOT NULL,
	UNIQUE (org_id, name)
)`

// The tenants. A tenant must have the tenant column's Go type exactly:
// int64 here, so these are typed constants. An untyped 1 would be an int.
const (
	acme   int64 = 1
	globex int64 = 2
)

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
	say("org 2 asks for Magnet: ErrNotFound=%t", errors.Is(err, velox.ErrNotFound))

	// Writes too: org 2 pausing every Anvil changes nothing of org 1's.
	n, err := db.Update[gen.Project]().Set(P.Status.Set("paused")).Where(P.Name.EQ("Anvil")).Exec(globexCtx)
	if err != nil {
		return err
	}
	say("org 2 pauses Anvil: %d rows changed (Anvil is org 1's)", n)

	// Fail closed: no tenant, the wrong Go type, a row naming another
	// tenant, and moving a row to another tenant are all refused.
	_, err = db.Query[gen.Project]().Count(ctx)
	say("no tenant: ErrNoTenant=%t", errors.Is(err, velox.ErrNoTenant))
	_, err = db.Query[gen.Project]().Count(velox.WithTenant(ctx, 1)) // an int, not an int64
	say("tenant of type int: ErrTenantMismatch=%t", errors.Is(err, velox.ErrTenantMismatch))
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

	// AllTenants lifts the scope for one context and needs a reason, which
	// hooks see as Statement.TenantBypass. An empty reason is ignored.
	report := velox.AllTenants(ctx, "weekly status report")
	for p, err := range db.Query[gen.Project]().OrderBy(P.OrgID.Asc(), P.Name.Asc()).Unbounded().Iter(report) {
		if err != nil {
			return err
		}
		say("AllTenants: org %d %s %s", p.OrgID, p.Name, p.Status)
	}
	_, err = db.Query[gen.Project]().Count(velox.AllTenants(ctx, ""))
	say("AllTenants with no reason: ErrNoTenant=%t", errors.Is(err, velox.ErrNoTenant))
	return nil
}
