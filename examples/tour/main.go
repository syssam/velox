// Command tour walks through velox on a SQLite file made by its migrations: it
// inserts, reads and changes the invoices of package veloxgen as two
// organizations, and prints what each step did.
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
	"time"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/tour/role"
	"github.com/syssam/velox/examples/tour/veloxgen"
	"github.com/syssam/velox/examples/tour/veloxschema"
	"github.com/syssam/velox/migrate"
	"github.com/syssam/velox/schema/mixin/softdelete"
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
//	go tool velox-gen migrate diff -dialect sqlite -name init -dir ./tour/migrations ./tour/veloxschema
//
// and applied with migrate.Up. The temporary SQLite database is this demo's;
// a real application opens its own.
//
//go:embed migrations
var migrations embed.FS

// The two organizations, of the scope key's Go type: int64.
const (
	acme   int64 = 1
	globex int64 = 2
)

func run(ctx context.Context, w io.Writer) error {
	dir, err := os.MkdirTemp("", "velox-tour-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	db, err := open(ctx, dir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	t := &tour{
		ctx: ctx, db: db, w: w,
		acct:       role.With(veloxschema.OrgKey.With(ctx, acme), role.Accountant),
		viewer:     role.With(veloxschema.OrgKey.With(ctx, acme), role.Viewer),
		globexAcct: role.With(veloxschema.OrgKey.With(ctx, globex), role.Accountant),
	}
	for _, step := range []func() error{
		t.inserts, t.reading, t.sqlOf, t.writing, t.guards,
		t.policy, t.transactions, t.withoutPolicy, t.deadlines,
	} {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

// open makes the tour's SQLite database in dir and applies its migrations.
// Every statement gets a deadline when its context has none, and no
// statement or transaction may run longer than the maximum (step 9).
func open(ctx context.Context, dir string) (*velox.DB, error) {
	db, err := velox.Connect("sqlite", filepath.Join(dir, "tour.db")+"?_pragma=foreign_keys(1)", velox.WithDeadlines(velox.Deadlines{
		Statement:    2 * time.Second,
		MaxStatement: 10 * time.Second,
		Tx:           5 * time.Second,
		MaxTx:        30 * time.Second,
	}))
	if err != nil {
		return nil, err
	}
	sub, err := fs.Sub(migrations, "migrations")
	if err == nil {
		_, err = migrate.Up(ctx, velox.Pool(db), sub)
	}
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// tour is the database and the contexts the steps share. The contexts say
// who is asking: the organization and the role, which the policies read.
// Middleware would set both.
type tour struct {
	ctx                      context.Context
	db                       *velox.DB
	w                        io.Writer
	acct, viewer, globexAcct context.Context
}

// say prints one line of the tour.
func (t *tour) say(format string, args ...any) { _, _ = fmt.Fprintf(t.w, format+"\n", args...) }

// inserts inserts the invoices and lines of both organizations, and shows
// the insert hook's refusal.
func (t *tour) inserts() error {
	db, say, acct, globexAcct := t.db, t.say, t.acct, t.globexAcct
	// 1. The tour's contexts say who is asking (see tour).
	say("\n== 1. Contexts and inserts")

	// Insert fills OrgID from the context and reads the generated ID back.
	inv := veloxgen.Invoice{Number: "INV-001", Customer: "Initech", Status: veloxgen.InvoiceStatusSent, TotalCents: 150000}
	if err := db.Insert(&inv).Exec(acct); err != nil {
		return err
	}
	say("Insert: %s has id %d and org_id %d", inv.Number, inv.ID, inv.OrgID)

	// InsertAll sends one statement for many rows and fills each row's OrgID.
	// It reads no IDs back.
	lines := []veloxgen.Line{
		{InvoiceID: inv.ID, Description: "Consulting", AmountCents: 120000},
		{InvoiceID: inv.ID, Description: "Travel", AmountCents: 30000},
		{InvoiceID: inv.ID, Description: "Parking", AmountCents: 0},
	}
	if err := db.InsertAll(lines).Exec(acct); err != nil {
		return err
	}
	say("InsertAll: %d lines, each with org_id %d", len(lines), lines[2].OrgID)

	more := []veloxgen.Invoice{
		{Number: "INV-002", Customer: "Umbrella", Status: veloxgen.InvoiceStatusDraft},
		{Number: "INV-003", Customer: "Initech", Status: veloxgen.InvoiceStatusPaid, TotalCents: 45000},
	}
	if err := db.InsertAll(more).Exec(acct); err != nil {
		return err
	}
	// The same number in another organization: the unique key includes OrgID.
	theirs := []veloxgen.Invoice{{Number: "INV-001", Customer: "Hooli", Status: veloxgen.InvoiceStatusSent, TotalCents: 99000}}
	if err := db.InsertAll(theirs).Exec(globexAcct); err != nil {
		return err
	}
	say("InsertAll: INV-002 and INV-003 for org 1, INV-001 for org 2")

	// Invoice's insert hook validates the number.
	err := db.Insert(&veloxgen.Invoice{Number: "2026-17", Customer: "Initech", Status: veloxgen.InvoiceStatusDraft}).Exec(acct)
	say("insert hook refused: %v", err)
	return nil
}

// reading reads with All, One, Count, Iter, With and Has.
func (t *tour) reading() error {
	db, say, acct := t.db, t.say, t.acct
	I, L := veloxgen.Invoices, veloxgen.Lines
	// 2. Reading. A query is a value: build it, then run it with All, One,
	// Iter or Count.
	say("\n== 2. Reading")
	unpaid, err := db.Query[veloxgen.Invoice]().
		Where(I.Status.NEQ(veloxgen.InvoiceStatusPaid)).
		OrderBy(I.Number.Asc()).
		Limit(10).
		All(acct)
	if err != nil {
		return err
	}
	for _, x := range unpaid {
		say("All: %s %s %s", x.Number, x.Customer, x.Status)
	}

	paid, err := db.Query[veloxgen.Invoice]().Where(I.Status.EQ(veloxgen.InvoiceStatusPaid)).One(acct)
	if err != nil {
		return err
	}
	say("One: %s %s", paid.Number, money(paid.TotalCents))

	n, err := db.Query[veloxgen.Invoice]().Where(I.Customer.EQ("Initech")).Count(acct)
	if err != nil {
		return err
	}
	say("Count: %d invoices for Initech", n)

	// Iter holds one row at a time. AllowUnbounded says on purpose that there is
	// no Limit.
	var numbers []string
	for x, err := range db.Query[veloxgen.Invoice]().OrderBy(I.ID.Asc()).AllowUnbounded().Iter(acct) {
		if err != nil {
			return err
		}
		numbers = append(numbers, x.Number)
	}
	say("Iter: %s", strings.Join(numbers, " "))

	// With loads a relation with one more query; nothing is loaded lazily.
	first, err := db.Query[veloxgen.Invoice]().
		Where(I.Number.EQ("INV-001")).
		With(I.Lines.OrderBy(L.AmountCents.Desc())).
		One(acct)
	if err != nil {
		return err
	}
	loaded, _ := first.Edges.Lines.Get()
	for _, l := range loaded {
		say("With: %s line %q %s", first.Number, l.Description, money(l.AmountCents))
	}

	// Has is an EXISTS on the relation.
	withLines, err := db.Query[veloxgen.Invoice]().Where(I.Lines.Has()).Limit(10).All(acct)
	if err != nil {
		return err
	}
	say("Has: %d invoice with lines, %s", len(withLines), withLines[0].Number)
	return nil
}

// sqlOf prints the SQL a query would send for two roles.
func (t *tour) sqlOf() error {
	db, say, acct, viewer := t.db, t.say, t.acct, t.viewer
	I := veloxgen.Invoices
	// 3. SQL(ctx) prints the statement a query would send for that context,
	// with the organization and policy conditions, without sending it.
	say("\n== 3. SQL(ctx)")
	q := db.Query[veloxgen.Invoice]().Where(I.Customer.EQ("Initech")).Limit(10)
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
	return nil
}

// writing updates, saves, deletes, soft-deletes and restores.
func (t *tour) writing() error {
	db, say, acct := t.db, t.say, t.acct
	I, L := veloxgen.Invoices, veloxgen.Lines
	// 4. Writing. Updates are set-based or name their columns; there is no
	// write-back of a whole struct.
	say("\n== 4. Writing")
	n, err := db.Update[veloxgen.Invoice]().
		Set(I.Status.Set(veloxgen.InvoiceStatusPaid)).
		Where(I.Number.EQ("INV-001")).
		Exec(acct)
	if err != nil {
		return err
	}
	say("Update: %d row marked paid", n)

	// Save writes the named columns of a loaded struct, and UpdatedAt through
	// the mixin's UpdateDefault.
	draft, err := db.Query[veloxgen.Invoice]().Where(I.Number.EQ("INV-002")).One(acct)
	if err != nil {
		return err
	}
	draft.Customer = "Umbrella Corp"
	if err := db.Save(draft, I.Customer).Exec(acct); err != nil {
		return err
	}
	say("Save: %s customer is now %s", draft.Number, draft.Customer)

	n, err = db.Delete[veloxgen.Line]().Where(L.AmountCents.EQ(0)).Exec(acct)
	if err != nil {
		return err
	}
	say("Delete: %d empty line removed", n)

	// softdelete.Mixin makes Invoice's delete an update that sets deleted_at,
	// and hides the rows it is set on. softdelete.Skip shows them, and a
	// restore is an update under it.
	n, err = db.Delete[veloxgen.Invoice]().Where(I.Number.EQ("INV-002")).Exec(acct)
	if err != nil {
		return err
	}
	live, err := db.Query[veloxgen.Invoice]().Count(acct)
	if err != nil {
		return err
	}
	all, err := db.Query[veloxgen.Invoice]().Count(softdelete.Skip(acct))
	if err != nil {
		return err
	}
	say("soft delete: %d invoice deleted; org 1 sees %d, and %d under softdelete.Skip", n, live, all)
	n, err = db.Update[veloxgen.Invoice]().Where(I.Number.EQ("INV-002")).Set(I.DeletedAt.SetNull()).Exec(softdelete.Skip(acct))
	if err != nil {
		return err
	}
	say("restore under softdelete.Skip: %d invoice", n)
	return nil
}

// guards shows the guards that refuse a read without a Limit and a
// write without a Where.
func (t *tour) guards() error {
	db, say, acct := t.db, t.say, t.acct
	// 5. Guards are always on: a read without a Limit, and an update or
	// delete without a Where, are refused before anything is sent.
	say("\n== 5. Guards")
	_, err := db.Query[veloxgen.Invoice]().All(acct)
	say("no Limit: ErrUnbounded=%t: %v", errors.Is(err, velox.ErrUnbounded), err)
	_, err = db.Delete[veloxgen.Line]().Exec(acct)
	say("no Where: ErrNoWhere=%t: %v", errors.Is(err, velox.ErrNoWhere), err)
	return nil
}

// policy shows the policy and the organizations' isolation.
func (t *tour) policy() error {
	db, say, viewer, globexAcct, ctx := t.db, t.say, t.viewer, t.globexAcct, t.ctx
	I := veloxgen.Invoices
	// 6. Policy and organization isolation.
	say("\n== 6. Policy and organizations")
	_, err := db.Update[veloxgen.Invoice]().
		Set(I.Status.Set(veloxgen.InvoiceStatusSent)).
		Where(I.Number.EQ("INV-002")).
		Exec(viewer)
	say("viewer's update: ErrDenied=%t: %v", errors.Is(err, velox.ErrDenied), err)

	seen, err := db.Query[veloxgen.Invoice]().Count(viewer)
	if err != nil {
		return err
	}
	say("viewer sees %d invoices of org 1 (not the draft)", seen)

	seen, err = db.Query[veloxgen.Invoice]().Count(globexAcct)
	if err != nil {
		return err
	}
	say("org 2 sees %d invoice", seen)
	_, err = db.Query[veloxgen.Invoice]().Where(I.Number.EQ("INV-003")).One(globexAcct)
	say("org 2 asks for org 1's INV-003: ErrNotFound=%t", velox.IsNotFound(err))
	_, err = db.Query[veloxgen.Invoice]().Count(ctx)
	say("no organization in the context: ErrNoScope=%t: %v", errors.Is(err, scope.ErrNoScope), err)
	return nil
}

// transactions runs a transaction with a savepoint.
func (t *tour) transactions() error {
	db, say, acct := t.db, t.say, t.acct
	// 7. A transaction: fn's error rolls it back, nil commits. tx is a
	// *velox.DB like db, and ctx is the transaction's: a statement on db
	// under it fails with ErrOutsideTx. A nested Tx is a savepoint, so a
	// failed statement undoes only its own work and the transaction goes on
	// (on PostgreSQL a failed statement would otherwise abort the whole
	// transaction).
	say("\n== 7. Transactions")
	err := db.Tx(acct, func(ctx context.Context, tx *velox.DB) error {
		next := veloxgen.Invoice{Number: "INV-003", Customer: "Initech", Status: veloxgen.InvoiceStatusSent, TotalCents: 80000}
		err := tx.Tx(ctx, func(ctx context.Context, tx *velox.DB) error { return tx.Insert(&next).Exec(ctx) })
		if ce, ok := errors.AsType[*velox.ConstraintError](err); ok && ce.Kind == velox.ConstraintUnique {
			say("%s is taken (%s constraint on %s); using INV-004", next.Number, ce.Kind, ce.Constraint)
			next.Number = "INV-004"
			err = tx.Insert(&next).Exec(ctx)
		}
		if err != nil {
			return err
		}
		return tx.InsertAll([]veloxgen.Line{
			{InvoiceID: next.ID, Description: "Licences", AmountCents: 80000},
		}).Exec(ctx)
	})
	if err != nil {
		return err
	}
	n, err := db.Query[veloxgen.Invoice]().Count(acct)
	if err != nil {
		return err
	}
	say("committed: org 1 has %d invoices", n)
	return nil
}

// withoutPolicy reports across organizations under WithoutPolicy.
func (t *tour) withoutPolicy() error {
	db, say, ctx := t.db, t.say, t.ctx
	I := veloxgen.Invoices
	// 8. db.WithoutPolicy returns a DB that lifts every policy, the
	// organization's scope among them, and requires a reason. A report across
	// organizations needs it, and so does its caller's care. Only the code
	// given that DB is unscoped; ctx carries no bypass.
	say("\n== 8. WithoutPolicy")
	report := db.WithoutPolicy("monthly revenue report")
	text, _, err := report.Query[veloxgen.Invoice]().Where(I.Status.NEQ(veloxgen.InvoiceStatusDraft)).AllowUnbounded().SQL(ctx)
	if err != nil {
		return err
	}
	say("%s", text)
	// Sum adds up one column in the database, under the same scope as any
	// query: here all organizations, and no policy.
	paidTotal, err := velox.Sum(ctx, report.Query[veloxgen.Invoice]().Where(I.Status.EQ(veloxgen.InvoiceStatusPaid)), I.TotalCents)
	if err != nil {
		return err
	}
	say("paid in all: %s", money(paidTotal))
	// SumBy does so per value of a key column, here per organization: no drafts.
	// AllowUnbounded says that the groups may be as many as the rows; the row cap,
	// if any, still applies.
	totals, err := velox.SumBy(ctx, report.Query[veloxgen.Invoice]().Where(I.Status.NEQ(veloxgen.InvoiceStatusDraft)).AllowUnbounded(), I.OrgID.Key(), I.TotalCents)
	if err != nil {
		return err
	}
	for _, x := range totals {
		say("org %d: %s", x.Key, money(x.Value))
	}
	return nil
}

// deadlines runs a transaction past its TxTimeout.
func (t *tour) deadlines() error {
	db, say, acct := t.db, t.say, t.acct
	// 9. Deadlines. TxTimeout bounds one transaction, within WithDeadlines'
	// MaxTx; fn's ctx carries the transaction's deadline, and every statement
	// in it gets that deadline, whatever context it is given.
	say("\n== 9. Deadlines")
	err := db.Tx(acct, func(ctx context.Context, tx *velox.DB) error {
		time.Sleep(600 * time.Millisecond) // slow work while the transaction is open
		_, err := tx.Query[veloxgen.Invoice]().Count(ctx)
		return err
	}, velox.TxTimeout(300*time.Millisecond)) // room for BEGIN on a loaded machine
	say("transaction past its TxTimeout: ErrTimeout=%t, context.DeadlineExceeded=%t\n  %v",
		errors.Is(err, velox.ErrTimeout), errors.Is(err, context.DeadlineExceeded), err)
	return nil
}

// money formats cents as a decimal amount.
func money(cents int64) string { return fmt.Sprintf("%d.%02d", cents/100, cents%100) }
