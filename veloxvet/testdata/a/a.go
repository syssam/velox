// Package a holds the reads the unbounded analyzer reports, and those it
// leaves, on velox's own test model.
package a

import (
	"context"

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
