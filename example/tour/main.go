// Command tour walks through velox2 on an in-memory SQLite database: it
// inserts, reads and changes the invoices of package gen as two
// organizations, and prints what each step did.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/example/tour/gen"
)

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// ddl creates the tables by hand. Planning migrations from the schemas is a
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
	I, L := gen.Invoices, gen.Lines
	say := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format+"\n", args...) }

	// 1. The context says who is asking: the tenant for velox, the role for
	// the policy. Middleware would set both.
	say("\n== 1. Contexts and inserts")
	acct := gen.WithRole(velox.WithTenant(ctx, acme), gen.Accountant)
	viewer := gen.WithRole(velox.WithTenant(ctx, acme), gen.Viewer)
	globexAcct := gen.WithRole(velox.WithTenant(ctx, globex), gen.Accountant)

	// Insert fills OrgID from the context and reads the generated ID back.
	inv := gen.Invoice{Number: "INV-001", Customer: "Initech", Status: gen.InvoiceStatusSent, TotalCents: 150000}
	if err := db.Insert(&inv).Exec(acct); err != nil {
		return err
	}
	say("Insert: %s has id %d and org_id %d", inv.Number, inv.ID, inv.OrgID)

	// InsertAll sends one statement for many rows and fills each row's OrgID.
	// It reads no IDs back.
	lines := []gen.Line{
		{InvoiceID: inv.ID, Description: "Consulting", AmountCents: 120000},
		{InvoiceID: inv.ID, Description: "Travel", AmountCents: 30000},
		{InvoiceID: inv.ID, Description: "Parking", AmountCents: 0},
	}
	if err := db.InsertAll(lines).Exec(acct); err != nil {
		return err
	}
	say("InsertAll: %d lines, each with org_id %d", len(lines), lines[2].OrgID)

	more := []gen.Invoice{
		{Number: "INV-002", Customer: "Umbrella", Status: gen.InvoiceStatusDraft},
		{Number: "INV-003", Customer: "Initech", Status: gen.InvoiceStatusPaid, TotalCents: 45000},
	}
	if err := db.InsertAll(more).Exec(acct); err != nil {
		return err
	}
	// The same number in another organization: the unique key includes OrgID.
	theirs := []gen.Invoice{{Number: "INV-001", Customer: "Hooli", Status: gen.InvoiceStatusSent, TotalCents: 99000}}
	if err := db.InsertAll(theirs).Exec(globexAcct); err != nil {
		return err
	}
	say("InsertAll: INV-002 and INV-003 for org 1, INV-001 for org 2")

	// Invoice's insert hook validates the number.
	err = db.Insert(&gen.Invoice{Number: "2026-17", Customer: "Initech", Status: gen.InvoiceStatusDraft}).Exec(acct)
	say("insert hook refused: %v", err)

	// 2. Reading. A query is a value: build it, then run it with All, One,
	// Iter or Count.
	say("\n== 2. Reading")
	unpaid, err := db.Query[gen.Invoice]().
		Where(I.Status.NEQ(gen.InvoiceStatusPaid)).
		OrderBy(I.Number.Asc()).
		Limit(10).
		All(acct)
	if err != nil {
		return err
	}
	for _, x := range unpaid {
		say("All: %s %s %s", x.Number, x.Customer, x.Status)
	}

	paid, err := db.Query[gen.Invoice]().Where(I.Status.EQ(gen.InvoiceStatusPaid)).One(acct)
	if err != nil {
		return err
	}
	say("One: %s %s", paid.Number, money(paid.TotalCents))

	n, err := db.Query[gen.Invoice]().Where(I.Customer.EQ("Initech")).Count(acct)
	if err != nil {
		return err
	}
	say("Count: %d invoices for Initech", n)

	// Iter holds one row at a time. Unbounded says on purpose that there is
	// no Limit.
	var numbers []string
	for x, err := range db.Query[gen.Invoice]().OrderBy(I.ID.Asc()).Unbounded().Iter(acct) {
		if err != nil {
			return err
		}
		numbers = append(numbers, x.Number)
	}
	say("Iter: %s", strings.Join(numbers, " "))

	// With loads a relation with one more query; nothing is loaded lazily.
	first, err := db.Query[gen.Invoice]().
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
	withLines, err := db.Query[gen.Invoice]().Where(I.Lines.Has()).Limit(10).All(acct)
	if err != nil {
		return err
	}
	say("Has: %d invoice with lines, %s", len(withLines), withLines[0].Number)

	// 3. SQL(ctx) prints the statement a query would send for that context,
	// with the tenant and policy conditions, without sending it.
	say("\n== 3. SQL(ctx)")
	q := db.Query[gen.Invoice]().Where(I.Customer.EQ("Initech")).Limit(10)
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
	n, err = db.Update[gen.Invoice]().
		Set(I.Status.Set(gen.InvoiceStatusPaid)).
		Where(I.Number.EQ("INV-001")).
		Exec(acct)
	if err != nil {
		return err
	}
	say("Update: %d row marked paid", n)

	// Save writes the named columns of a loaded struct, and UpdatedAt through
	// the mixin's UpdateDefault.
	draft, err := db.Query[gen.Invoice]().Where(I.Number.EQ("INV-002")).One(acct)
	if err != nil {
		return err
	}
	draft.Customer = "Umbrella Corp"
	if err := db.Save(draft, I.Customer).Exec(acct); err != nil {
		return err
	}
	say("Save: %s customer is now %s", draft.Number, draft.Customer)

	n, err = db.Delete[gen.Line]().Where(L.AmountCents.EQ(0)).Exec(acct)
	if err != nil {
		return err
	}
	say("Delete: %d empty line removed", n)

	// 5. Guards are always on: a read without a Limit, and an update or
	// delete without a Where, are refused before anything is sent.
	say("\n== 5. Guards")
	_, err = db.Query[gen.Invoice]().All(acct)
	say("no Limit: ErrUnbounded=%t: %v", errors.Is(err, velox.ErrUnbounded), err)
	_, err = db.Delete[gen.Line]().Exec(acct)
	say("no Where: ErrNoWhere=%t: %v", errors.Is(err, velox.ErrNoWhere), err)

	// 6. Policy and tenant isolation.
	say("\n== 6. Policy and tenants")
	_, err = db.Update[gen.Invoice]().
		Set(I.Status.Set(gen.InvoiceStatusSent)).
		Where(I.Number.EQ("INV-002")).
		Exec(viewer)
	say("viewer's update: ErrDenied=%t: %v", errors.Is(err, velox.ErrDenied), err)

	seen, err := db.Query[gen.Invoice]().Count(viewer)
	if err != nil {
		return err
	}
	say("viewer sees %d invoices of org 1 (not the draft)", seen)

	seen, err = db.Query[gen.Invoice]().Count(globexAcct)
	if err != nil {
		return err
	}
	say("org 2 sees %d invoice", seen)
	_, err = db.Query[gen.Invoice]().Where(I.Number.EQ("INV-003")).One(globexAcct)
	say("org 2 asks for org 1's INV-003: ErrNotFound=%t", errors.Is(err, velox.ErrNotFound))
	_, err = db.Query[gen.Invoice]().Count(ctx)
	say("no tenant in the context: ErrNoTenant=%t: %v", errors.Is(err, velox.ErrNoTenant), err)

	// 7. A transaction: fn's error rolls it back, nil commits. tx is a
	// *velox.DB like db, and ctx is the transaction's: a statement on db
	// under it fails with ErrOutsideTx. A nested Tx is a savepoint, so a
	// failed statement undoes only its own work and the transaction goes on
	// (on PostgreSQL a failed statement would otherwise abort the whole
	// transaction).
	say("\n== 7. Transactions")
	err = db.Tx(acct, func(ctx context.Context, tx *velox.DB) error {
		next := gen.Invoice{Number: "INV-003", Customer: "Initech", Status: gen.InvoiceStatusSent, TotalCents: 80000}
		err := tx.Tx(ctx, func(ctx context.Context, tx *velox.DB) error { return tx.Insert(&next).Exec(ctx) })
		if ce := (*velox.ConstraintError)(nil); errors.As(err, &ce) && ce.Kind == velox.ConstraintUnique {
			say("%s is taken (%s constraint on %s); using INV-004", next.Number, ce.Kind, ce.Constraint)
			next.Number = "INV-004"
			err = tx.Insert(&next).Exec(ctx)
		}
		if err != nil {
			return err
		}
		return tx.InsertAll([]gen.Line{
			{InvoiceID: next.ID, Description: "Licences", AmountCents: 80000},
		}).Exec(ctx)
	})
	if err != nil {
		return err
	}
	n, err = db.Query[gen.Invoice]().Count(acct)
	if err != nil {
		return err
	}
	say("committed: org 1 has %d invoices", n)

	// 8. AllTenants lifts the tenant scope for one context, and requires a
	// reason. The policy still applies, so the report runs as an accountant.
	say("\n== 8. AllTenants and Raw")
	report := gen.WithRole(velox.AllTenants(ctx, "monthly revenue report"), gen.Accountant)
	text, _, err := db.Query[gen.Invoice]().Where(I.Status.NEQ(gen.InvoiceStatusDraft)).Unbounded().SQL(report)
	if err != nil {
		return err
	}
	say("%s", text)
	// Sum adds up one column in the database, under the same scope as any
	// query: here all tenants, and the accountant's policy.
	paidTotal, err := velox.Sum(report, db.Query[gen.Invoice]().Where(I.Status.EQ(gen.InvoiceStatusPaid)), I.TotalCents)
	if err != nil {
		return err
	}
	say("paid in all: %s", money(paidTotal))
	// Raw SQL is the escape hatch for what the builder cannot express, here
	// a GROUP BY, per tenant. velox applies neither the tenant scope nor the
	// policy to it, so it requires a reason too, and its SQL says what they
	// would: no drafts. Its rows scan into Invoice by column name; the fields
	// of the columns it does not return stay zero.
	totals, err := db.Raw[gen.Invoice]("monthly revenue report",
		"SELECT org_id, SUM(total_cents) AS total_cents FROM invoices WHERE status <> ? GROUP BY org_id ORDER BY org_id",
		gen.InvoiceStatusDraft).All(ctx)
	if err != nil {
		return err
	}
	for _, x := range totals {
		say("org %d: %s", x.OrgID, money(x.TotalCents))
	}

	// 9. Deadlines. TxTimeout bounds one transaction, within WithDeadlines'
	// MaxTx; fn's ctx carries the transaction's deadline, and every statement
	// in it gets that deadline, whatever context it is given.
	say("\n== 9. Deadlines")
	err = db.Tx(acct, func(ctx context.Context, tx *velox.DB) error {
		time.Sleep(50 * time.Millisecond) // slow work while the transaction is open
		_, err := tx.Query[gen.Invoice]().Count(ctx)
		return err
	}, velox.TxTimeout(10*time.Millisecond))
	say("transaction past its TxTimeout: ErrTimeout=%t, context.DeadlineExceeded=%t\n  %v",
		errors.Is(err, velox.ErrTimeout), errors.Is(err, context.DeadlineExceeded), err)
	return nil
}

// money formats cents as a decimal amount.
func money(cents int64) string { return fmt.Sprintf("%d.%02d", cents/100, cents%100) }
