// Package a holds the reads the unbounded analyzer reports, and those it
// leaves, on velox's own test model.
package a

import (
	"context"
	"errors"

	"github.com/syssam/velox"
	tm "github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/untyped"
)

var I = tm.Invoices

// Chains.

func chain(ctx context.Context, db *velox.DB) {
	db.Query[tm.Invoice]().All(ctx)                                                   // want `All reads a query with no Limit or AllowUnbounded`
	db.Query[tm.Invoice]().Where(I.Paid.EQ(true)).All(ctx)                            // want `All reads a query`
	db.Query[tm.Invoice]().OrderBy(I.ID.Asc()).Iter(ctx)                              // want `Iter reads a query`
	db.Query[tm.Invoice]().Values(ctx, I.Number)                                      // want `Values reads a query`
	db.Query[tm.Invoice]().IDs(ctx)                                                   // want `IDs reads a query`
	db.Query[tm.Invoice]().Select(I.Number).Rows(ctx)                                 // want `Rows reads a query`
	db.Query[tm.Invoice]().SQL(ctx)                                                   // want `SQL reads a query`
	velox.Scan[struct{ Number string }](ctx, db.Query[tm.Invoice]().Select(I.Number)) // want `velox.Scan reads a query`
	db.Query[tm.Invoice]().Where(I.Paid.EQ(true)).Limit(10).All(ctx)
	db.Query[tm.Invoice]().Limit(10).Where(I.Paid.EQ(true)).Values(ctx, I.Number)
	db.Query[tm.Invoice]().AllowUnbounded().OrderBy(I.ID.Asc()).All(ctx)
	velox.Scan[struct{ Number string }](ctx, db.Query[tm.Invoice]().Select(I.Number).Limit(5))
}

// A comment in the middle of a chain changes nothing.
func commented(ctx context.Context, db *velox.DB) {
	db.Query[tm.Invoice]().
		// the unpaid ones
		Where(I.Paid.EQ(false)).
		/* newest first */ OrderBy(I.ID.Desc()).
		All(ctx) // want `All reads a query`
	db.Query[tm.Invoice]().
		// a page
		Limit(20).
		All(ctx)
}

// One, First, Count, Exists, ScanOne and a plain aggregate set or need no
// Limit.
func singles(ctx context.Context, db *velox.DB) {
	q := db.Query[tm.Invoice]().Where(I.Paid.EQ(true))
	q.One(ctx)
	q.First(ctx)
	q.Count(ctx)
	q.Exists(ctx)
	velox.ScanOne[struct{ Number string }](ctx, q.Select(I.Number))
	velox.Sum(ctx, q, I.Total)
	velox.Max(ctx, q, I.Total)
}

// Variables and reassignments.

func variables(ctx context.Context, db *velox.DB) {
	q := db.Query[tm.Invoice]()
	q = q.Where(I.Paid.EQ(true))
	q.All(ctx) // want `All reads a query`

	b := db.Query[tm.Invoice]()
	b = b.Limit(10)
	b.All(ctx)

	base := db.Query[tm.Invoice]()
	page := base.Limit(10)
	page.All(ctx) // Limit returned a new query: base is as it was
	base.All(ctx) // want `All reads a query`
}

// Branches: a read is reported only when no path bounds its query.

func oneBranchBounded(ctx context.Context, db *velox.DB, c bool) {
	q := db.Query[tm.Invoice]()
	if c {
		q = q.Limit(10)
	}
	q.All(ctx)
}

func noBranchBounded(ctx context.Context, db *velox.DB, c bool) {
	q := db.Query[tm.Invoice]()
	if c {
		q = q.Where(I.Paid.EQ(true))
	} else {
		q = q.OrderBy(I.ID.Asc())
	}
	q.All(ctx) // want `All reads a query`
}

func everyBranchBounded(ctx context.Context, db *velox.DB, c bool) {
	q := db.Query[tm.Invoice]()
	if c {
		q = q.Limit(10)
	} else {
		q = q.AllowUnbounded()
	}
	q.All(ctx)
}

func boundedInLoop(ctx context.Context, db *velox.DB, n int) {
	q := db.Query[tm.Invoice]()
	for range n {
		q = q.Limit(10)
	}
	q.All(ctx)
}

func unboundedInLoop(ctx context.Context, db *velox.DB, ps []velox.Pred[tm.Invoice]) {
	q := db.Query[tm.Invoice]()
	for _, p := range ps {
		q = q.Where(p)
	}
	q.All(ctx) // want `All reads a query`
}

// What the check cannot see is not reported.

type repo struct {
	q velox.Query[tm.Invoice]
}

func unknown(ctx context.Context, q velox.Query[tm.Invoice], r repo, get func() velox.Query[tm.Invoice]) {
	q.All(ctx)
	q.Where(I.Paid.EQ(true)).All(ctx)
	r.q.All(ctx)
	get().All(ctx)
	var zero velox.Query[tm.Invoice]
	zero.All(ctx)
}

func captured(ctx context.Context, db *velox.DB) {
	q := db.Query[tm.Invoice]()
	func() { q = q.Limit(1) }()
	q.All(ctx)
	func() {
		q.All(ctx)
	}()
}

// Grouped aggregates need AllowUnbounded: a Limit does not stand in for it.

func grouped(ctx context.Context, db *velox.DB) {
	velox.CountBy(ctx, db.Query[tm.Invoice](), I.Paid)                  // want `velox.CountBy groups a query with no AllowUnbounded`
	velox.SumBy(ctx, db.Query[tm.Invoice]().Limit(10), I.Paid, I.Total) // want `velox.SumBy groups a query`
	velox.CountBy(ctx, db.Query[tm.Invoice]().AllowUnbounded(), I.Paid)
}

// A constant Limit that is not positive does not bound the query: Query's
// fails the read, TableQuery's removes the limit.

const none = 0

func nonPositive(ctx context.Context, db *velox.DB, n int) {
	db.Query[tm.Invoice]().Limit(0).All(ctx)                           // want `All reads a query`
	db.Query[tm.Invoice]().Limit(-1).All(ctx)                          // want `All reads a query`
	db.Query[tm.Invoice]().Limit(10).Limit(none).All(ctx)              // want `All reads a query`
	untyped.Query(db, untyped.TableOf[tm.Invoice]()).Limit(0).All(ctx) // want `All reads a query`
	db.Query[tm.Invoice]().Limit(0).AllowUnbounded().All(ctx)
	db.Query[tm.Invoice]().Limit(n).All(ctx)
	db.Query[tm.Invoice]().Limit(1).All(ctx)
}

// Untyped queries.

func untypedQueries(ctx context.Context, db *velox.DB) {
	t := untyped.TableOf[tm.Invoice]()
	untyped.Query(db, t).All(ctx)                                          // want `All reads a query`
	untyped.Query(db, t).Where(velox.Predicate(I.Paid.EQ(true))).Rows(ctx) // want `Rows reads a query`
	untyped.Query(db, t).Limit(5).All(ctx)
	untyped.Query(db, t).AllowUnbounded().All(ctx)
	untyped.Query(db, t).One(ctx)
	untyped.FromQuery(db.Query[tm.Invoice]()).All(ctx) // want `All reads a query`
	untyped.FromQuery(db.Query[tm.Invoice]().Limit(1)).All(ctx)
}

// Edge queries, from velox-gen's QueryX methods, and the package's own
// functions that start a query.

func edges(ctx context.Context, inv *tm.Invoice) {
	inv.QueryItems().All(ctx) // want `All reads a query`
	inv.QueryItems().Limit(10).All(ctx)
	inv.QueryItems().First(ctx)
}

func unpaid(db *velox.DB) velox.Query[tm.Invoice] { // want unpaid:"FreshQuery"
	return db.Query[tm.Invoice]().Where(I.Paid.EQ(false))
}

func unpaidPage(db *velox.DB) velox.Query[tm.Invoice] {
	return unpaid(db).Limit(10)
}

func either(db *velox.DB, c bool) velox.Query[tm.Invoice] {
	if c {
		return unpaid(db)
	}
	return unpaidPage(db)
}

func helpers(ctx context.Context, db *velox.DB, c bool) {
	unpaid(db).All(ctx) // want `All reads a query`
	unpaidPage(db).All(ctx)
	either(db, c).All(ctx)
}

// Deferred and started reads are checked too.

func deferred(ctx context.Context, db *velox.DB) {
	defer db.Query[tm.Invoice]().All(ctx) // want `All reads a query`
	go db.Query[tm.Invoice]().Limit(1).All(ctx)
}

// The directive waives a read that is meant to fail: at the end of its
// line, or alone on the line before it.
func waived(ctx context.Context, db *velox.DB) {
	db.Query[tm.Invoice]().All(ctx) //velox:unbounded-ok the test expects ErrUnbounded
	//velox:unbounded-ok
	db.Query[tm.Invoice]().All(ctx)

	db.Query[tm.Invoice]().All(ctx) // want `All reads a query`
	q := db.Query[tm.Invoice]()     //velox:unbounded-ok
	q.All(ctx)                      // want `All reads a query`
}

// The directive waives the statement that holds the read: on any of its
// lines, or alone on the line before its first.
func waivedSpanning(ctx context.Context, db *velox.DB, c bool) {
	//velox:unbounded-ok
	db.Query[tm.Invoice]().
		Where(I.Paid.EQ(true)).
		All(ctx)
	_, _ = db.Query[tm.Invoice]().All(
		ctx,
	) //velox:unbounded-ok
	db.Query[tm.Invoice]().
		//velox:unbounded-ok
		All(ctx)
	defer db.Query[tm.Invoice]().
		All(ctx) //velox:unbounded-ok

	//velox:unbounded-ok
	q := db.Query[tm.Invoice]()
	q.
		All(ctx) // want `All reads a query`

	// A header's directive waives the header, not the body.
	if _, err := db.Query[tm.Invoice]().All(ctx); err != nil { //velox:unbounded-ok
		db.Query[tm.Invoice]().All(ctx) // want `All reads a query`
	}
	if c {
		db.Query[tm.Invoice]().All(ctx) // want `All reads a query`
	} //velox:unbounded-ok
	switch _, err := db.Query[tm.Invoice]().All(ctx); err { // want `All reads a query`
	case nil: //velox:unbounded-ok
	}
	for first(db.Query[tm.Invoice]().All(ctx)) == nil { //velox:unbounded-ok
		db.Query[tm.Invoice]().All(ctx) // want `All reads a query`
	}
	for range db.Query[tm.Invoice]().Iter(ctx) { //velox:unbounded-ok
		db.Query[tm.Invoice]().All(ctx) // want `All reads a query`
	}
	switch len(first(db.Query[tm.Invoice]().All(ctx))) { //velox:unbounded-ok
	case 0:
		db.Query[tm.Invoice]().All(ctx) // want `All reads a query`
	}
	switch {
	case first(db.Query[tm.Invoice]().All(ctx)) == nil: //velox:unbounded-ok
		db.Query[tm.Invoice]().All(ctx) // want `All reads a query`
	}
}

func first[T any](v T, _ error) T { return v }

// A function literal's directive waives the literal's own statement, not
// the statement outside it, and the outer statement's directive does not
// waive a read in the literal.
func waivedClosure(ctx context.Context, db *velox.DB) error {
	err := errors.Join(second(db.Query[tm.Invoice]().All(ctx)), func() error { // want `All reads a query`
		_, err := db.Query[tm.Invoice]().All(ctx) //velox:unbounded-ok
		return err
	}())
	return errors.Join(err, second(db.Query[tm.Invoice]().All(ctx)), func() error { //velox:unbounded-ok
		_, err := db.Query[tm.Invoice]().All(ctx) // want `All reads a query`
		return err
	}())
}

func second[T any](_ T, err error) error { return err }

//velox:unbounded-ok
var _, _ = unpaid(nil).
	All(context.Background())

// A function literal is checked as a function of its own.
func literal(db *velox.DB) func(context.Context) {
	return func(ctx context.Context) {
		db.Query[tm.Invoice]().All(ctx) // want `All reads a query`
	}
}

// A package variable's initializer is checked too.
var _ = func() error {
	_, err := unpaid(nil).All(context.Background()) // want `All reads a query`
	return err
}

// A method value is not followed: its receiver is bound in a closure.
func methodValues(ctx context.Context, db *velox.DB) {
	q := db.Query[tm.Invoice]()
	w := q.Writable[*tm.Invoice]
	w().All(ctx)
	allow := q.AllowUnbounded[*tm.Invoice]
	allow().All(ctx)
	limit := q.Limit[*tm.Invoice]
	limit(5).All(ctx)
	f := q.All[*tm.Invoice]
	f(ctx)
	scan := velox.Scan[struct{ Number string }, tm.Invoice]
	scan(ctx, q) // want `velox.Scan reads a query`
}

// A helper that calls itself, or never returns, is not known to return a
// new query: its reads are not reported.
func recursive(db *velox.DB, n int) velox.Query[tm.Invoice] {
	if n > 0 {
		return recursive(db, n-1).Where(I.Paid.EQ(true))
	}
	return db.Query[tm.Invoice]()
}

func neverReturns() velox.Query[tm.Invoice] { panic("never") }

func notFresh(ctx context.Context, db *velox.DB) {
	recursive(db, 2).All(ctx)
	neverReturns().All(ctx)
}
