package velox

import (
	"context"
	"database/sql"
	"reflect"
	"time"

	"github.com/syssam/velox2/internal/conn"
	"github.com/syssam/velox2/internal/fakeconn"
)

// A JSON column's destination must stay unwrapped: conn wraps no sql.Scanner.
var _ sql.Scanner = (*codecScanner)(nil)

// FakeExec is the tests' executor.
type FakeExec = fakeconn.Exec

// SetRollbackTimeout sets rollbackTimeout to d until restore is called.
func SetRollbackTimeout(d time.Duration) (restore func()) {
	old := rollbackTimeout
	rollbackTimeout = d
	return func() { rollbackTimeout = old }
}

// render is renderStmt for the tests that render statements that cannot fail.
func render(d Dialect, st *Statement) (string, []any) {
	text, args, err := renderStmt(d, st)
	if err != nil {
		panic(err)
	}
	return text, args
}

// render is get for the tests that look only at the text.
func (c *renderCache) render(d Dialect, st *Statement) (string, []any) {
	e, args, err := c.get(d, st)
	if err != nil {
		panic(err)
	}
	return e.text, args
}

// FakeConn returns a Conn that runs statements on e.
func FakeConn(e *FakeExec) Conn { return newConn(e, 0) }

// SetReplicaDown puts db's replica i in back-off until until.
func SetReplicaDown(db *DB, i int, until time.Time) {
	db.rt.one.replicas[i].downUntil.Store(until.UnixNano())
}

// ReplicaDownUntil returns the end of db's replica i's back-off; zero when
// it has none.
func ReplicaDownUntil(db *DB, i int) time.Time {
	if u := db.rt.one.replicas[i].downUntil.Load(); u != 0 {
		return time.Unix(0, u)
	}
	return time.Time{}
}

// DryRun prepares and routes st as the SQL(ctx) methods do.
func DryRun(ctx context.Context, db *DB, st *Statement) (string, []any, error) {
	return db.dryRun(ctx, st)
}

// OpenFake returns a DB of dialect d that runs statements on e.
func OpenFake(d Dialect, e *FakeExec, opts ...Option) *DB { return newDB(d, e, opts) }

// RawRows runs sql on db's executor, bypassing the pipeline: the baseline of
// hand-written code.
func RawRows(db *DB, sql string, args []any) (interface {
	Next() bool
	Scan(...any) error
	Close()
}, error) {
	return db.ex.Query(context.Background(), sql, args)
}

// SetMaxParams lowers a dialect's parameter limit for a test.
func SetMaxParams(d Dialect, n int) (restore func()) {
	old := maxParams[d]
	maxParams[d] = n
	return func() { maxParams[d] = old }
}

// CacheSize returns the number of shapes db's render cache holds.
func CacheSize(db *DB) int { return db.cfg.cache.size() }

// TenantOf returns the context's tenant state, as scope reads it.
func TenantOf(ctx context.Context) (id any, all string) { return tenantOf(ctx) }

// TagsOf returns the context's WithTags tags, as observe reads them.
func TagsOf(ctx context.Context) []Tag { return tagsOf(ctx) }

// PolicyBypass returns the context's WithoutPolicy reason.
func PolicyBypass(ctx context.Context) string { return policyBypass(ctx) }

// InfoOf returns entity E's metadata.
func InfoOf[E any, PE Entity[E]]() *EntityInfo { return PE(nil).VeloxMeta().info }

// Prepare runs db's stages before execution on st, as run does: guard,
// entity hooks, Rewrite hooks, scope, row cap and rendering.
func Prepare(ctx context.Context, db *DB, st *Statement) error { return db.prepare(ctx, st) }

// Kids returns p's sub-conditions: the operands of an And, Or or Not, or the
// conditions an EXISTS puts on its target.
func Kids(p Predicate) []Predicate {
	out := make([]Predicate, len(predNode(p).kids))
	for i, k := range predNode(p).kids {
		out[i] = newPredicate(k)
	}
	return out
}

// ExistsOn returns the target of an EXISTS; nil for any other condition.
func ExistsOn(p Predicate) *EntityInfo {
	if predNode(p).op != opExists {
		return nil
	}
	l, ok := predNode(p).v.(link)
	if !ok {
		return nil
	}
	return l.target
}

// TargetOf is the target of a relation to T as a use site names it: what Rel
// puts in a load or a condition.
func TargetOf[T any]() UseTarget { return targetOf[T]() }

// UseTarget is a relation's target as a use site names it.
type UseTarget = useTarget

// LoadDesc builds entity E's metadata from d, as MetaOnce.Load does on the
// entity's first use, then resolves its relations, as CheckRelation does:
// relation i of an EdgeTo takes the target targets[i], which a use site would
// name. A relation with no target there is left unresolved.
func LoadDesc[E any](d Desc[E], targets ...UseTarget) (*EntityInfo, error) {
	var o MetaOnce[E]
	m := o.Load(func() Desc[E] { return d })
	if m.err != nil {
		return nil, m.err
	}
	for i, r := range infoRels(m.info) {
		var use useTarget
		if i < len(targets) {
			use = targets[i]
		}
		if use.get == nil {
			continue
		}
		if _, err := r.resolve(m.info, use); err != nil {
			return m.info, err
		}
	}
	return m.info, nil
}

// SamePreds reports whether a and b are the same conditions. A relation not
// yet resolved is a relRef, which holds a function: reflect.DeepEqual never
// finds two equal, so relRefs are compared by their relation index.
func SamePreds(a, b []Predicate) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameNode(predNode(a[i]), predNode(b[i])) {
			return false
		}
	}
	return true
}

func sameNode(a, b node) bool {
	if a.op != b.op || a.col != b.col || len(a.kids) != len(b.kids) || !reflect.DeepEqual(a.w, b.w) {
		return false
	}
	ra, aok := a.v.(relRef)
	rb, bok := b.v.(relRef)
	switch {
	case aok != bok || aok && ra.i != rb.i:
		return false
	case !aok && !reflect.DeepEqual(a.v, b.v):
		return false
	}
	for i := range a.kids {
		if !sameNode(a.kids[i], b.kids[i]) {
			return false
		}
	}
	return true
}

// OpenColumnScan returns a DB on db whose rows are colRows when wrap is set,
// whatever db's driver.
func OpenColumnScan(d Dialect, db *sql.DB, wrap bool) *DB {
	return newDB(d, conn.NewSQLCols(db, wrap), nil)
}

// PgxStdlib reports whether db's driver is pgx's stdlib driver.
func PgxStdlib(db *sql.DB) bool { return conn.PgxStdlib(db) }

// RowsAreColumnScanning runs SELECT 1 on db, a DB made by Open, and reports
// whether its rows are colRows.
func RowsAreColumnScanning(db *DB) bool {
	r, err := db.ex.Query(context.Background(), "SELECT 1", nil)
	if err != nil {
		panic(err)
	}
	defer r.Close()
	return conn.RowsColumnScanning(r)
}

// ConnColumnScanning reports whether c's executor makes colRows.
func ConnColumnScanning(c Conn) bool { return connExec(c).(conn.SQL).ColumnScanning() }
