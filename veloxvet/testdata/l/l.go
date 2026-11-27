// Package l holds the locking reads the locktx analyzer reports, and those
// it leaves, on velox's own test model.
package l

import (
	"context"
	"database/sql"

	"github.com/syssam/velox"
	tm "github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/untyped"
	"github.com/syssam/velox/veloxvet/testdata/lhelp"
)

var I = tm.Invoices

type Service struct{ client *velox.DB }

var global *velox.DB

// lockReturn locks on its parameter, as nextapp's lockPurchaseReturnRow.
func lockReturn(ctx context.Context, client *velox.DB, id int64) error { // want lockReturn:`LocksOnParam\(p1\)`
	_, err := client.Query[tm.Invoice]().Where(I.ID.EQ(id)).ForUpdate().All(ctx)
	return err
}

// creditable passes its parameter on, as CreditableReturnQuantities.
func creditable(ctx context.Context, db *velox.DB, id int64) error { // want creditable:`LocksOnParam\(p1\)`
	return lockReturn(ctx, db, id)
}

// Complete is the bug: it locks on its field before its transaction.
func (s *Service) Complete(ctx context.Context, id int64) error {
	if err := creditable(ctx, s.client, id); err != nil { // want `creditable takes a row lock \(ForUpdate or ForShare\) on its parameter db, given the field client, which is not provably a transaction's handle: call it in db.Tx with the closure's tx, or take the handle as a parameter`
		return err
	}
	return s.client.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		return creditable(ctx, tx, id)
	})
}

// creditableTx is the fix: it wraps its own body in a transaction.
func creditableTx(ctx context.Context, db *velox.DB, id int64) error {
	return db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error { return lockReturn(ctx, tx, id) })
}

func (s *Service) Fixed(ctx context.Context, id int64) error {
	return creditableTx(ctx, s.client, id)
}

// A read straight on the closure's tx, or on a DB made from it.
func (s *Service) direct(ctx context.Context, id int64) error {
	return s.client.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		if _, err := tx.Query[tm.Invoice]().Where(I.ID.EQ(id)).ForUpdate().One(ctx); err != nil {
			return err
		}
		q := tx.WithoutPolicy("audit").Query[tm.Invoice]()
		q = q.Where(I.Paid.EQ(true))
		if _, err := q.ForShare().First(ctx); err != nil {
			return err
		}
		_, err := untyped.Query(tx, untyped.TableOf[tm.Invoice]()).ForUpdate().All(ctx)
		return err
	})
}

// Strict: a field inside the closure is not the closure's tx, though velox
// would run it in the transaction, as ctx joins.
func (s *Service) fieldInTx(ctx context.Context, id int64) error {
	return s.client.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		_, err := s.client.Query[tm.Invoice]().Where(I.ID.EQ(id)).ForUpdate().All(ctx) // want `All takes a row lock \(ForUpdate or ForShare\) on the field client, which is not provably a transaction's handle: run it in db.Tx on the closure's tx, or take the handle as a parameter`
		return err
	})
}

// Other handles that are not a transaction's.
func others(ctx context.Context, inv *tm.Invoice) {
	global.Query[tm.Invoice]().ForUpdate().All(ctx)                                  // want `on the package variable global`
	inv.QueryItems().ForUpdate().All(ctx)                                            // want `on the query QueryItems returns`
	untyped.Query(global, untyped.TableOf[tm.Invoice]()).ForShare().Exists(ctx)      // want `Exists takes a row lock`
	velox.Scan[struct{ Number string }](ctx, global.Query[tm.Invoice]().ForUpdate()) // want `velox.Scan takes a row lock`
	global.Query[tm.Invoice]().ForUpdate().Count(ctx)                                // Count refuses a lock whatever the handle
	global.Query[tm.Invoice]().Where(I.Paid.EQ(true)).All(ctx)                       // no lock
	lit := func(db *velox.DB) {
		db.Query[tm.Invoice]().ForUpdate().All(ctx) // want `on a function literal's parameter db`
	}
	lit(global)
}

// A gate on a bool parameter.
func load(ctx context.Context, db *velox.DB, id int64, lock bool) error { // want load:`LocksOnParam\(p1 when p3==true\)`
	q := db.Query[tm.Invoice]().Where(I.ID.EQ(id))
	if lock {
		q = q.ForUpdate()
	}
	_, err := q.All(ctx)
	return err
}

func (s *Service) gated(ctx context.Context, id int64) {
	load(ctx, s.client, id, false)
	load(ctx, s.client, id, true) // want `load takes a row lock`
	s.client.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		return load(ctx, tx, id, true)
	})
}

// A gate on the read, by an early return under the negated parameter.
func early(ctx context.Context, db *velox.DB, lock bool) error { // want early:`LocksOnParam\(p1 when p2==true\)`
	q := db.Query[tm.Invoice]().ForUpdate()
	if !lock {
		return nil
	}
	_, err := q.All(ctx)
	return err
}

func (s *Service) earlyCalls(ctx context.Context) {
	early(ctx, s.client, false)
	early(ctx, s.client, true) // want `early takes a row lock`
}

// A gate of a named bool type compared with its constants, as nextapp's
// salesorder lockMode, passed through to a callee.
type lockMode bool

const (
	lockRows lockMode = true
	readOnly lockMode = false
)

func loadBound(ctx context.Context, client *velox.DB, ids []int64, mode lockMode) error { // want loadBound:`LocksOnParam\(p1 when p3==true\)`
	q := client.Query[tm.Invoice]().Where(I.ID.In(ids...))
	if mode == lockRows {
		q = q.ForUpdate()
	}
	if _, err := q.All(ctx); err != nil {
		return err
	}
	return addBindings(ctx, client, mode)
}

func addBindings(ctx context.Context, client *velox.DB, mode lockMode) error { // want addBindings:`LocksOnParam\(p1 when p2==true\)`
	q := client.Query[tm.Invoice]()
	if mode == lockRows {
		q = q.ForUpdate()
	}
	_, err := q.All(ctx)
	return err
}

func (s *Service) bound(ctx context.Context, ids []int64) error {
	if err := loadBound(ctx, s.client, ids, readOnly); err != nil {
		return err
	}
	loadBound(ctx, s.client, ids, lockRows) // want `loadBound takes a row lock`
	return s.client.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		return loadBound(ctx, tx, ids, lockRows)
	})
}

// An integer mode, compared with != and in a switch.
type mode int

const (
	modeRead mode = iota
	modeLock
	modeShare
)

func byMode(ctx context.Context, db *velox.DB, m mode) error { // want byMode:`LocksOnParam\(p1 when p2!=0 and p2!=2 or p2!=0 and p2==2\)`
	q := db.Query[tm.Invoice]()
	if m != modeRead {
		switch m {
		case modeShare:
			q = q.ForShare()
		default:
			q = q.ForUpdate()
		}
	}
	_, err := q.All(ctx)
	return err
}

func (s *Service) modes(ctx context.Context) {
	byMode(ctx, s.client, modeRead)
	byMode(ctx, s.client, modeLock)  // want `byMode takes a row lock`
	byMode(ctx, s.client, modeShare) // want `byMode takes a row lock`
}

// A gate that is not a constant counts as locking; a waiver hides it.
func lockIf(ctx context.Context, db *velox.DB, id int64, inTx bool) error { // want lockIf:`LocksOnParam\(p1 when p3==true\)`
	q := db.Query[tm.Invoice]().Where(I.ID.EQ(id))
	if inTx {
		q = q.ForUpdate()
	}
	_, err := q.All(ctx)
	return err
}

func (s *Service) maybe(ctx context.Context, tx *velox.DB, id int64) error { // want maybe:`LocksOnParam\(p2\)`
	db := s.client
	if tx != nil {
		db = tx
	}
	return lockIf(ctx, db, id, tx != nil) // want `lockIf takes a row lock \(ForUpdate or ForShare\) on its parameter db, given the field client`
}

func (s *Service) maybeWaived(ctx context.Context, tx *velox.DB, id int64) error {
	db := s.client
	if tx != nil {
		db = tx
	}
	return lockIf(ctx, db, id, tx != nil) //velox:lock-ok ctx carries the caller's transaction
}

// A waiver on a read.
func waived(ctx context.Context) {
	//velox:lock-ok a demo of the refusal
	global.Query[tm.Invoice]().ForUpdate().All(ctx)
}

// A handle of the receiver's field, gated by a parameter: the callers decide.
func (s *Service) own(ctx context.Context, lock bool) error { // want own:`LocksOnParam\(own when p2==true\)`
	q := s.client.Query[tm.Invoice]()
	if lock {
		q = q.ForUpdate()
	}
	_, err := q.All(ctx)
	return err
}

func (s *Service) ownCalls(ctx context.Context) {
	s.own(ctx, false)
	s.own(ctx, true) // want `own takes a row lock \(ForUpdate or ForShare\) on its own handle, the field client`
}

// A query parameter is the caller's handle.
func lockQuery(ctx context.Context, q velox.Query[tm.Invoice]) error { // want lockQuery:`LocksOnParam\(p1\)`
	_, err := q.ForUpdate().All(ctx)
	return err
}

func (s *Service) queries(ctx context.Context) {
	lockQuery(ctx, s.client.Query[tm.Invoice]()) // want `lockQuery takes a row lock`
	s.client.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		return lockQuery(ctx, tx.Query[tm.Invoice]().Where(I.Paid.EQ(true)))
	})
}

// A handle captured from the enclosing function's parameter is that
// parameter, in a closure that is not a transaction's.
func each(ctx context.Context, db *velox.DB, ids []int64) error { // want each:`LocksOnParam\(p1\)`
	one := func(id int64) error { return lockReturn(ctx, db, id) }
	for _, id := range ids {
		if err := one(id); err != nil {
			return err
		}
	}
	return nil
}

// Other packages' facts.
func (s *Service) imported(ctx context.Context, id int64) error {
	lhelp.Forward(ctx, s.client, id) // want `Forward takes a row lock`
	lhelp.Load(ctx, s.client, id, false)
	lhelp.Load(ctx, s.client, id, true)       // want `Load takes a row lock`
	lhelp.Base(s.client).ForUpdate().All(ctx) // want `All takes a row lock \(ForUpdate or ForShare\) on the field client`
	lhelp.Locked(s.client).All(ctx)           // want `All takes a row lock`
	lhelp.Locked(s.client).Count(ctx)
	lhelp.WithTx(ctx, s.client, func(ctx context.Context, tx *velox.DB) error {
		if err := lhelp.LockRow(ctx, tx, id); err != nil {
			return err
		}
		_, err := lhelp.Base(tx).ForUpdate().All(ctx)
		return err
	})
	return s.client.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		if j, ok := velox.TxFrom(ctx, s.client); ok {
			return lhelp.Forward(ctx, j, id)
		}
		return lhelp.Forward(ctx, tx, id)
	})
}

// A function literal stored in a variable, then given to Tx.
func (s *Service) stored(ctx context.Context, id int64) error {
	fn := func(ctx context.Context, tx *velox.DB) error { return lockReturn(ctx, tx, id) }
	return s.client.Tx(ctx, fn)
}

// A function literal also called outside Tx is not a transaction's closure.
func (s *Service) twice(ctx context.Context, id int64) error {
	fn := func(ctx context.Context, tx *velox.DB) error {
		return lockReturn(ctx, tx, id) // want `lockReturn takes a row lock \(ForUpdate or ForShare\) on its parameter client, given a function literal's parameter tx`
	}
	if err := s.client.Tx(ctx, fn); err != nil {
		return err
	}
	return fn(ctx, s.client)
}

// A wrapper of this package whose callback takes more than the tx, as
// nextapp's mutateLocked: its literal is a transaction's closure.
func (s *Service) mutateLocked(ctx context.Context, id int64, fn func(ctx context.Context, tx *velox.DB, rec *tm.Invoice) error) error { // want mutateLocked:`TxCallback\(p3\)`
	return s.client.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		rec, err := tx.Query[tm.Invoice]().Where(I.ID.EQ(id)).ForUpdate().One(ctx)
		if err != nil {
			return err
		}
		return fn(ctx, tx, rec)
	})
}

func (s *Service) adjust(ctx context.Context, id int64) error {
	return s.mutateLocked(ctx, id, func(ctx context.Context, tx *velox.DB, rec *tm.Invoice) error {
		_, err := tx.Query[tm.Invoice]().Where(I.ID.EQ(rec.ID)).ForUpdate().One(ctx)
		return err
	})
}

// A wrapper that begins the transaction in a goroutine, as a test's Hold.
func (s *Service) held(ctx context.Context, id int64) {
	lhelp.Hold(ctx, s.client, func(ctx context.Context, tx *velox.DB) error {
		_, err := tx.Query[tm.Invoice]().Where(I.ID.EQ(id)).ForUpdate().One(ctx)
		return err
	})
}

// A gate and a handle captured by a function literal.
func gatedEach(ctx context.Context, db *velox.DB, ids []int64, lock bool) error { // want gatedEach:`LocksOnParam\(p1 when p3==true\)`
	one := func(id int64) error {
		q := db.Query[tm.Invoice]().Where(I.ID.EQ(id))
		if lock {
			q = q.ForUpdate()
		}
		_, err := q.All(ctx)
		return err
	}
	for _, id := range ids {
		if err := one(id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) gatedEachCalls(ctx context.Context, ids []int64) {
	gatedEach(ctx, s.client, ids, false)
	gatedEach(ctx, s.client, ids, true) // want `gatedEach takes a row lock`
}

// A handle in a field of a struct parameter, as nextapp's input.Tx.
type ApplyInput struct {
	Tx     *velox.DB
	Amount int64
}

func apply(ctx context.Context, in ApplyInput) error { // want apply:`LocksOnParam\(p1.Tx\)`
	_, err := in.Tx.Query[tm.Invoice]().ForUpdate().All(ctx)
	return err
}

func applyPtr(ctx context.Context, in *ApplyInput) error { // want applyPtr:`LocksOnParam\(p1.Tx\)`
	return lockReturn(ctx, in.Tx, in.Amount)
}

func (s *Service) applies(ctx context.Context) error {
	apply(ctx, ApplyInput{Tx: s.client}) // want `apply takes a row lock \(ForUpdate or ForShare\) on its parameter in's field Tx, given the field client`
	in := &ApplyInput{Amount: 1}
	in.Tx = global
	applyPtr(ctx, in) // want `applyPtr takes a row lock .* given the package variable global`
	return s.client.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		if err := apply(ctx, ApplyInput{Tx: tx, Amount: 1}); err != nil {
			return err
		}
		return applyPtr(ctx, &ApplyInput{Tx: tx})
	})
}

// A field of the receiver is not followed, even when the receiver is made
// from a transaction's handle for the call.
type txStore struct{ tx *velox.DB }

func (r txStore) lock(ctx context.Context) error {
	_, err := r.tx.Query[tm.Invoice]().ForUpdate().First(ctx) // want `First takes a row lock \(ForUpdate or ForShare\) on the field tx`
	return err
}

// A wrapper that checks its callback for nil, as nextapp's applyTransition.
func (s *Service) transition(ctx context.Context, extra func(ctx context.Context, tx *velox.DB) error) error { // want transition:`TxCallback\(p2\)`
	return s.client.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		if extra != nil {
			return extra(ctx, tx)
		}
		return nil
	})
}

func (s *Service) transitions(ctx context.Context, id int64) error {
	return s.transition(ctx, func(ctx context.Context, tx *velox.DB) error { return lockReturn(ctx, tx, id) })
}

// A function literal that wraps Tx, as a test's inTx.
func (s *Service) literalWrapper(ctx context.Context, id int64) {
	inTx := func(fn func(tx *velox.DB) error) {
		_ = s.client.Tx(ctx, func(ctx context.Context, tx *velox.DB) error { return fn(tx) })
	}
	inTx(func(tx *velox.DB) error { return lockReturn(ctx, tx, id) })
}

// A handle two fields deep, the outer struct built from the caller's
// parameter, as nextapp's legs.in.Tx.
type legs struct {
	in   ApplyInput
	note string
}

func clawBack(ctx context.Context, l legs) error { // want clawBack:`LocksOnParam\(p1.in.Tx\)`
	return lockReturn(ctx, l.in.Tx, 1)
}

func reverse(ctx context.Context, in ApplyInput) error { // want reverse:`LocksOnParam\(p1.Tx\)`
	return clawBack(ctx, legs{in: in, note: "x"})
}

func (s *Service) reverses(ctx context.Context) error {
	reverse(ctx, ApplyInput{Tx: s.client})          // want `reverse takes a row lock .* given the field client`
	clawBack(ctx, legs{in: ApplyInput{Tx: global}}) // want `clawBack takes a row lock .* given the package variable global`
	return s.client.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		return reverse(ctx, ApplyInput{Tx: tx})
	})
}

// A local copy of the field struct, as nextapp's in := legs.in.
func clawBackCopy(ctx context.Context, l *legs) error { // want clawBackCopy:`LocksOnParam\(p1.in.Tx\)`
	in := l.in
	in.Amount++
	return lockReturn(ctx, in.Tx, in.Amount)
}

func (s *Service) clawBackCopies(ctx context.Context) {
	clawBackCopy(ctx, &legs{in: ApplyInput{Tx: s.client}}) // want `clawBackCopy takes a row lock .* given the field client`
}

// Review 1A: a query that loops back through Where; the unlocked read in
// the loop must not cache a partial answer for the locked one.
func cycleQuery(ctx context.Context, ids []int64) {
	q := global.Query[tm.Invoice]()
	for _, id := range ids {
		q.First(ctx)
		q = q.Where(I.ID.EQ(id))
		q.ForUpdate().First(ctx) // want `First takes a row lock .* on the package variable global`
	}
}

// Review 1B: a handle that loops back through WithoutPolicy.
func (s *Service) cycleHandle(ctx context.Context, ids []int64) {
	db := s.client
	for _, id := range ids {
		load(ctx, db, id, false)
		db = db.WithoutPolicy("x")
		load(ctx, db, id, true) // want `load takes a row lock .* given the field client`
	}
}

// Review 2: many identical paths before the lock must not crowd it out.
func manyPaths(ctx context.Context, db *velox.DB, a, b, c, d, e, f, lock bool) error { // want manyPaths:`LocksOnParam\(p1 when p8==true\)`
	q := db.Query[tm.Invoice]()
	if a {
		q = q.Where(I.Paid.EQ(true))
	}
	if b {
		q = q.Where(I.Paid.EQ(true))
	}
	if c {
		q = q.Where(I.Paid.EQ(true))
	}
	if d {
		q = q.Where(I.Paid.EQ(true))
	}
	if e {
		q = q.Where(I.Paid.EQ(true))
	}
	if f {
		q = q.Where(I.Paid.EQ(true))
	}
	if lock {
		q = q.ForUpdate()
	}
	_, err := q.All(ctx)
	return err
}

// Review 3: a query helper whose lock is gated on a parameter.
func gatedBase(db *velox.DB, lock bool) velox.Query[tm.Invoice] { // want gatedBase:`QueryFrom\(p0 locked when p1==true\)`
	q := db.Query[tm.Invoice]()
	if lock {
		q = q.ForUpdate()
	}
	return q
}

func (s *Service) gatedBases(ctx context.Context) {
	gatedBase(s.client, false).All(ctx)
	gatedBase(s.client, true).All(ctx) // want `All takes a row lock .* on the field client`
}

// Review 4: a struct whose field the check cannot see written.
func newInput(db *velox.DB) ApplyInput { return ApplyInput{Tx: db} }

func fill(in *ApplyInput, db *velox.DB) { in.Tx = db }

func (s *Service) unseenWrites(ctx context.Context) {
	in := newInput(s.client)
	in.Amount = 1
	apply(ctx, in) // want `apply takes a row lock .* cannot follow`
	var in2 ApplyInput
	fill(&in2, s.client)
	apply(ctx, in2) // want `apply takes a row lock .* cannot follow`
}

// Review 6: mutual recursion, declared callee first, and self-recursion.
func recF(ctx context.Context, db *velox.DB, n int) error { // want recF:`LocksOnParam\(p1\)`
	if n > 0 {
		return (&Service{}).recG(ctx, n-1)
	}
	_, err := db.Query[tm.Invoice]().ForUpdate().All(ctx)
	return err
}

func (s *Service) recG(ctx context.Context, n int) error {
	return recF(ctx, s.client, n) // want `recF takes a row lock .* given the field client`
}

func selfRec(ctx context.Context, db *velox.DB, n int) error { // want selfRec:`LocksOnParam\(p1\)`
	if n > 0 {
		return selfRec(ctx, global, n-1) // want `selfRec takes a row lock .* package variable global`
	}
	_, err := db.Query[tm.Invoice]().ForUpdate().All(ctx)
	return err
}

// Review 5: captured function variables swapped; both reach Tx.
func (s *Service) swapped(ctx context.Context) {
	a := func(ctx context.Context, tx *velox.DB) error {
		_, err := tx.Query[tm.Invoice]().ForUpdate().All(ctx)
		return err
	}
	b := func(ctx context.Context, tx *velox.DB) error { return nil }
	a, b = b, a
	run := func() {
		_ = s.client.Tx(ctx, a)
		_ = s.client.Tx(ctx, b)
	}
	run()
}

// Not TxCallbacks: a wrapper that may call its function with the handle it
// was given, and one that stores its function in a field.
func maybeTx(ctx context.Context, db *velox.DB, x bool, fn func(ctx context.Context, tx *velox.DB) error) error {
	if x {
		return fn(ctx, db)
	}
	return db.Tx(ctx, fn)
}

type holder struct {
	fn func(ctx context.Context, tx *velox.DB) error
}

func (h *holder) keep(fn func(ctx context.Context, tx *velox.DB) error) { h.fn = fn }

func (s *Service) notCallbacks(ctx context.Context, id int64) {
	maybeTx(ctx, s.client, true, func(ctx context.Context, tx *velox.DB) error {
		return lockReturn(ctx, tx, id) // want `lockReturn takes a row lock .* given a function literal's parameter tx`
	})
	var h holder
	h.keep(func(ctx context.Context, tx *velox.DB) error {
		return lockReturn(ctx, tx, id) // want `lockReturn takes a row lock .* given a function literal's parameter tx`
	})
}

// A callee that writes its parameter's field: the field is not the
// caller's, here or in a literal.
func overwrite(ctx context.Context, in *ApplyInput) error {
	in.Tx = global
	return lockReturn(ctx, in.Tx, 1) // want `lockReturn takes a row lock .* given the field Tx`
}

func overwriteInLiteral(ctx context.Context, in *ApplyInput) error {
	func() { in.Tx = global }()
	return lockReturn(ctx, in.Tx, 1) // want `lockReturn takes a row lock .* given the field Tx`
}

// A report names every handle of the paths that lock under one condition.
func (s *Service) twoHandles(ctx context.Context, c bool) {
	q := global.Query[tm.Invoice]()
	if c {
		q = s.client.Query[tm.Invoice]()
	}
	q.ForUpdate().All(ctx) // want `on (the package variable global or the field client|the field client or the package variable global),`
}

// UseTx's and Record's handles, a generic helper, a deferred call, and the
// calls the check does not follow: a function value and an interface.
func lockGeneric[E any](ctx context.Context, db *velox.DB) error { // want lockGeneric:`LocksOnParam\(p1\)`
	_, err := db.Query[tm.Invoice]().ForUpdate().All(ctx)
	return err
}

var lockFn = lockReturn

type locker interface {
	lockIt(ctx context.Context, db *velox.DB) error
}

type lockerImpl struct{}

func (lockerImpl) lockIt(ctx context.Context, db *velox.DB) error { // want lockIt:`LocksOnParam\(p2\)`
	return lockReturn(ctx, db, 1)
}

func (s *Service) misc(ctx context.Context, sqltx *sql.Tx, l locker, id int64) error {
	if vtx, err := velox.UseTx(s.client, sqltx); err == nil {
		lockReturn(ctx, vtx, id)
	}
	_ = s.client.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		return lockReturn(ctx, tx.Record(&velox.Recorder{}), id)
	})
	lockGeneric[tm.Invoice](ctx, s.client) // want `lockGeneric takes a row lock`
	defer lockReturn(ctx, s.client, id)    // want `lockReturn takes a row lock`
	lockFn(ctx, s.client, id)
	l.lockIt(ctx, s.client)
	m := s.own
	return m(ctx, true)
}
