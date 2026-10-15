// Command example walks through velox2 on an in-memory SQLite database: it
// inserts, reads and changes the invoices of package billing as two
// organizations, and prints what each step did.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/example/billing"
)

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// ddl creates the tables by hand. Planning migrations from the structs is a
// later velox2 sub-project.
const ddl = `
CREATE TABLE invoices (
	id          INTEGER PRIMARY KEY,
	org_id      INTEGER NOT NULL,
	number      TEXT NOT NULL,
	customer    TEXT NOT NULL,
	status      TEXT NOT NULL,
	total_cents INTEGER NOT NULL,
	created_at  DATETIME NOT NULL,
	updated_at  DATETIME NOT NULL,
	CONSTRAINT invoices_org_number UNIQUE (org_id, number)
);
CREATE TABLE lines (
	id           INTEGER PRIMARY KEY,
	org_id       INTEGER NOT NULL,
	invoice_id   INTEGER NOT NULL REFERENCES invoices (id),
	description  TEXT NOT NULL,
	amount_cents INTEGER NOT NULL
);`

// The two organizations, the tenants. A tenant's type must be the tenant
// column's Go type exactly: int64 here, so an untyped 1 (an int) would not do.
const (
	acme   int64 = 1
	globex int64 = 2
)

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

	// Every statement gets a deadline when its context has none, and no
	// statement or transaction may run longer than the maximum (step 9).
	db := velox.Open(velox.SQLite, sqlDB, velox.WithDeadlines(velox.Deadlines{
		Statement:    2 * time.Second,
		MaxStatement: 10 * time.Second,
		Tx:           5 * time.Second,
		MaxTx:        30 * time.Second,
	}))
	I, L := billing.Invoices, billing.Lines
	say := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format+"\n", args...) }

	// 1. The context says who is asking: the tenant for velox, the role for
	// billing's policy. Middleware would set both.
	say("\n== 1. Contexts and inserts")
	acct := billing.WithRole(velox.WithTenant(ctx, acme), billing.Accountant)
	viewer := billing.WithRole(velox.WithTenant(ctx, acme), billing.Viewer)
	globexAcct := billing.WithRole(velox.WithTenant(ctx, globex), billing.Accountant)

	// Insert fills OrgID from the context and reads the generated ID back.
	inv := billing.Invoice{Number: "INV-001", Customer: "Initech", Status: billing.Sent, TotalCents: 150000}
	if err := db.Insert(&inv).Exec(acct); err != nil {
		return err
	}
	say("Insert: %s has id %d and org_id %d", inv.Number, inv.ID, inv.OrgID)

	// InsertAll sends one statement for many rows and fills each row's OrgID.
	// It reads no IDs back.
	lines := []billing.Line{
		{InvoiceID: inv.ID, Description: "Consulting", AmountCents: 120000},
		{InvoiceID: inv.ID, Description: "Travel", AmountCents: 30000},
		{InvoiceID: inv.ID, Description: "Parking", AmountCents: 0},
	}
	if err := db.InsertAll(lines).Exec(acct); err != nil {
		return err
	}
	say("InsertAll: %d lines, each with org_id %d", len(lines), lines[2].OrgID)

	more := []billing.Invoice{
		{Number: "INV-002", Customer: "Umbrella", Status: billing.Draft},
		{Number: "INV-003", Customer: "Initech", Status: billing.Paid, TotalCents: 45000},
	}
	if err := db.InsertAll(more).Exec(acct); err != nil {
		return err
	}
	// The same number in another organization: the unique key includes OrgID.
	theirs := []billing.Invoice{{Number: "INV-001", Customer: "Hooli", Status: billing.Sent, TotalCents: 99000}}
	if err := db.InsertAll(theirs).Exec(globexAcct); err != nil {
		return err
	}
	say("InsertAll: INV-002 and INV-003 for org 1, INV-001 for org 2")

	// Invoice's BeforeInsert hook validates the number.
	err = db.Insert(&billing.Invoice{Number: "2026-17", Customer: "Initech", Status: billing.Draft}).Exec(acct)
	say("BeforeInsert refused: %v", err)

	// 2. Reading. A query is a value: build it, then run it with All, One,
	// Iter or Count.
	say("\n== 2. Reading")
	unpaid, err := db.Query[billing.Invoice]().
		Where(I.Status.NEQ(billing.Paid)).
		OrderBy(I.Number.Asc()).
		Limit(10).
		All(acct)
	if err != nil {
		return err
	}
	for _, x := range unpaid {
		say("All: %s %s %s", x.Number, x.Customer, x.Status)
	}

	paid, err := db.Query[billing.Invoice]().Where(I.Status.EQ(billing.Paid)).One(acct)
	if err != nil {
		return err
	}
	say("One: %s %s", paid.Number, money(paid.TotalCents))

	n, err := db.Query[billing.Invoice]().Where(I.Customer.EQ("Initech")).Count(acct)
	if err != nil {
		return err
	}
	say("Count: %d invoices for Initech", n)

	// Iter holds one row at a time. Unbounded says on purpose that there is
	// no Limit.
	var numbers []string
	for x, err := range db.Query[billing.Invoice]().OrderBy(I.ID.Asc()).Unbounded().Iter(acct) {
		if err != nil {
			return err
		}
		numbers = append(numbers, x.Number)
	}
	say("Iter: %s", strings.Join(numbers, " "))

	// With loads a relation with one more query; nothing is loaded lazily.
	first, err := db.Query[billing.Invoice]().
		Where(I.Number.EQ("INV-001")).
		With(I.Lines.OrderBy(L.AmountCents.Desc())).
		One(acct)
	if err != nil {
		return err
	}
	loaded, _ := first.Lines.Get()
	for _, l := range loaded {
		say("With: %s line %q %s", first.Number, l.Description, money(l.AmountCents))
	}

	// Has is an EXISTS on the relation.
	withLines, err := db.Query[billing.Invoice]().Where(I.Lines.Has()).Limit(10).All(acct)
	if err != nil {
		return err
	}
	say("Has: %d invoice with lines, %s", len(withLines), withLines[0].Number)

	// 3. SQL(ctx) prints the statement a query would send for that context,
	// with the tenant and policy conditions, without sending it.
	say("\n== 3. SQL(ctx)")
	q := db.Query[billing.Invoice]().Where(I.Customer.EQ("Initech")).Limit(10)
	for _, c := range []struct {
		who string
		ctx context.Context
	}{{"accountant", acct}, {"viewer", viewer}} {
		text, args, err := q.Columns(I.ID, I.Number).SQL(c.ctx)
		if err != nil {
			return err
		}
		say("as %s:\n  %s\n  %v", c.who, text, args)
	}

	// 4. Writing. Updates are set-based or name their columns; there is no
	// write-back of a whole struct.
	say("\n== 4. Writing")
	n, err = db.Update[billing.Invoice]().
		Set(I.Status.Set(billing.Paid)).
		Where(I.Number.EQ("INV-001")).
		Exec(acct)
	if err != nil {
		return err
	}
	say("Update: %d row marked paid", n)

	// Save writes the named columns of a loaded struct, and UpdatedAt through
	// the mixin's OnUpdate.
	draft, err := db.Query[billing.Invoice]().Where(I.Number.EQ("INV-002")).One(acct)
	if err != nil {
		return err
	}
	draft.Customer = "Umbrella Corp"
	if err := db.Save(acct, draft, I.Customer); err != nil {
		return err
	}
	say("Save: %s customer is now %s", draft.Number, draft.Customer)

	n, err = db.Delete[billing.Line]().Where(L.AmountCents.EQ(0)).Exec(acct)
	if err != nil {
		return err
	}
	say("Delete: %d empty line removed", n)

	// 5. Guards are always on: a read without a Limit, and an update or
	// delete without a Where, are refused before anything is sent.
	say("\n== 5. Guards")
	_, err = db.Query[billing.Invoice]().All(acct)
	say("no Limit: ErrUnbounded=%t: %v", errors.Is(err, velox.ErrUnbounded), err)
	_, err = db.Delete[billing.Line]().Exec(acct)
	say("no Where: ErrNoWhere=%t: %v", errors.Is(err, velox.ErrNoWhere), err)

	// 6. Policy and tenant isolation.
	say("\n== 6. Policy and tenants")
	_, err = db.Update[billing.Invoice]().
		Set(I.Status.Set(billing.Sent)).
		Where(I.Number.EQ("INV-002")).
		Exec(viewer)
	say("viewer's update: ErrDenied=%t: %v", errors.Is(err, velox.ErrDenied), err)

	seen, err := db.Query[billing.Invoice]().Count(viewer)
	if err != nil {
		return err
	}
	say("viewer sees %d invoices of org 1 (not the draft)", seen)

	seen, err = db.Query[billing.Invoice]().Count(globexAcct)
	if err != nil {
		return err
	}
	say("org 2 sees %d invoice", seen)
	_, err = db.Query[billing.Invoice]().Where(I.Number.EQ("INV-003")).One(globexAcct)
	say("org 2 asks for org 1's INV-003: ErrNotFound=%t", errors.Is(err, velox.ErrNotFound))
	_, err = db.Query[billing.Invoice]().Count(ctx)
	say("no tenant in the context: ErrNoTenant=%t: %v", errors.Is(err, velox.ErrNoTenant), err)

	// 7. A transaction: fn's error rolls it back, nil commits. tx is a
	// *velox.DB like db. A nested Tx is a savepoint, so a failed statement
	// undoes only its own work and the transaction goes on (on PostgreSQL a
	// failed statement would otherwise abort the whole transaction).
	say("\n== 7. Transactions")
	err = db.Tx(acct, func(tx *velox.DB) error {
		next := billing.Invoice{Number: "INV-003", Customer: "Initech", Status: billing.Sent, TotalCents: 80000}
		err := tx.Tx(acct, func(tx *velox.DB) error { return tx.Insert(&next).Exec(acct) })
		if ce := (*velox.ConstraintError)(nil); errors.As(err, &ce) && ce.Kind == velox.Unique {
			say("%s is taken (%s constraint on %s); using INV-004", next.Number, ce.Kind, ce.Constraint)
			next.Number = "INV-004"
			err = tx.Insert(&next).Exec(acct)
		}
		if err != nil {
			return err
		}
		return tx.InsertAll([]billing.Line{
			{InvoiceID: next.ID, Description: "Licences", AmountCents: 80000},
		}).Exec(acct)
	})
	if err != nil {
		return err
	}
	n, err = db.Query[billing.Invoice]().Count(acct)
	if err != nil {
		return err
	}
	say("committed: org 1 has %d invoices", n)

	// 8. AllTenants lifts the tenant scope for one context, and requires a
	// reason. The policy still applies, so the report runs as an accountant.
	say("\n== 8. AllTenants")
	report := billing.WithRole(velox.AllTenants(ctx, "monthly revenue report"), billing.Accountant)
	revenue := db.Query[billing.Invoice]().Where(I.Status.NEQ(billing.Draft)).Unbounded()
	text, _, err := revenue.SQL(report)
	if err != nil {
		return err
	}
	say("%s", text)
	totals := map[int64]int64{}
	for x, err := range revenue.Iter(report) {
		if err != nil {
			return err
		}
		totals[x.OrgID] += x.TotalCents
	}
	for _, org := range slices.Sorted(maps.Keys(totals)) {
		say("org %d: %s", org, money(totals[org]))
	}

	// 9. Deadlines. Timeout bounds one transaction, within WithDeadlines'
	// MaxTx; every statement in it gets the transaction's deadline, whatever
	// context it is given.
	say("\n== 9. Deadlines")
	err = db.Tx(acct, func(tx *velox.DB) error {
		time.Sleep(50 * time.Millisecond) // slow work while the transaction is open
		_, err := tx.Query[billing.Invoice]().Count(acct)
		return err
	}, velox.Timeout(10*time.Millisecond))
	say("transaction past its Timeout: ErrTimeout=%t, context.DeadlineExceeded=%t\n  %v",
		errors.Is(err, velox.ErrTimeout), errors.Is(err, context.DeadlineExceeded), err)
	return nil
}

// money formats cents as a decimal amount.
func money(cents int64) string { return fmt.Sprintf("%d.%02d", cents/100, cents%100) }
