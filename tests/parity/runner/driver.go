package runner

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"velox.test/parity/compare"
	"velox.test/parity/model"
	"velox.test/parity/op"
)

// Backend selects the database engine the three-way driver runs against.
type Backend int

const (
	// SQLite runs the parity programs against in-memory SQLite (velox via the
	// modernc "sqlite" driver, ent via the same). Always available — no env.
	SQLite Backend = iota
	// Postgres runs against the server named by VELOX_TEST_POSTGRES (velox→base
	// db, ent→"<base>_ent" db). Skipped when that env var is unset/unreachable.
	Postgres
	// MySQL runs against the server named by VELOX_TEST_MYSQL, same
	// separate-database convention as Postgres.
	MySQL
)

func (b Backend) String() string {
	switch b {
	case SQLite:
		return "sqlite"
	case Postgres:
		return "postgres"
	case MySQL:
		return "mysql"
	default:
		return fmt.Sprintf("backend(%d)", int(b))
	}
}

// HasBackend reports whether the given backend can run: SQLite is always
// available; Postgres / MySQL require their DSN env var to be set (the actual
// reachability check happens when RunParity opens the clients, which skips the
// test on failure). This lets callers cheaply branch on configuration before
// spending a connection attempt.
func HasBackend(b Backend) bool {
	switch b {
	case SQLite:
		return true
	case Postgres:
		return os.Getenv(pgEnvVar) != ""
	case MySQL:
		return os.Getenv(mysqlEnvVar) != ""
	default:
		return false
	}
}

// timestampFields are excluded from row comparison: their VALUES are
// implementation-dependent wall-clock-ish data whose only parity-relevant
// effect is ORDERING, which the deterministic parity clock already pins. The
// executors still EMIT them (so ORDER BY created_at works); the driver strips
// them before Diff so timestamp noise never masquerades as a divergence.
var timestampFields = map[string]bool{
	"created_at": true,
	"updated_at": true,
}

// sqlSink captures SQL statements keyed by the op index that produced them. A
// single sink is shared by one ORM's traced client; the executor advances
// op before each op so statements land in the right bucket. It is ORM-free
// (the ORM debug driver hands it plain strings), so it lives in this file.
type sqlSink struct {
	mu  sync.Mutex
	op  int
	log map[int][]string
}

func newSQLSink() *sqlSink {
	return &sqlSink{log: map[int][]string{}}
}

// setOp marks which op index subsequent statements belong to.
func (s *sqlSink) setOp(i int) {
	s.mu.Lock()
	s.op = i
	s.mu.Unlock()
}

// clear resets the sink to empty so a long-lived (cached) ORM client's sink can
// be reused for the next program without carrying over prior statements. Used by
// the dialect driver, whose clients live for the whole test.
func (s *sqlSink) clear() {
	s.mu.Lock()
	s.op = 0
	s.log = map[int][]string{}
	s.mu.Unlock()
}

// record appends one statement string under the current op index. It is the
// log function handed to velox.Log / ent.Log (which pass a single formatted
// "driver.Exec: query=... args=..." string).
func (s *sqlSink) record(v ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log[s.op] = append(s.log[s.op], strings.TrimSpace(fmt.Sprint(v...)))
}

// forOp returns the statements captured for op i.
func (s *sqlSink) forOp(i int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.log[i]
}

// OpReport is the per-op verdict plus the structured mismatches that justify it.
type OpReport struct {
	OpIndex   int
	Op        op.Op
	Verdict   compare.Verdict
	VeloxDiff []compare.Mismatch // velox vs reference
	EntDiff   []compare.Mismatch // ent vs reference
	VeloxSQL  []string
	EntSQL    []string
}

// Report is the full three-way result of running one program on one backend.
type Report struct {
	Backend Backend
	Prog    op.Program
	Ops     []OpReport
}

// dialectCacheKey identifies the (velox, ent) client pair for one (test,
// backend) scope. The cache lets multiple RunParity calls within the SAME test
// scope share one migrated pair, with tables truncated between programs. In
// practice TestCuratedSuite gives each case its own t.Run, so each case opens a
// FRESH pair (stored then Cleanup-deleted per case); the reuse path is correct
// and harmless but not exercised by the suite.
type dialectCacheKey struct {
	tb      testing.TB
	backend Backend
}

// dialectCache holds the (velox, ent) client pairs keyed by (test, backend)
// scope. It is a startup-style registry: written on the first RunParity for a
// scope and read by any later calls in the SAME scope. The entry's clients
// register their own t.Cleanup via openDialectPair, and the cache entry is
// dropped on scope end — so a per-case t.Run gets a fresh pair. The real
// isolation comes from the open-time truncate plus the per-program truncate, not
// from cross-program client reuse.
var dialectCache sync.Map // map[dialectCacheKey]*dialectPair

// RunParity runs all three executors (reference, velox, ent) for the backend and
// classifies each op's outcome per the verdict table. SQLite uses fresh
// in-memory clients per call; Postgres/MySQL get a migrated (velox, ent) pair for
// the current test scope, truncating all tables on BOTH ORMs before each program
// so state never bleeds between programs. SQL from both ORMs is captured per op so
// a non-Pass op prints the failing op, the structured mismatches, and the two SQL
// statements.
func RunParity(t testing.TB, backend Backend, prog op.Program) Report {
	t.Helper()

	ref := Reference(t, prog)

	var veloxSink, entSink *sqlSink
	var veloxRes, entRes []model.Result

	switch backend {
	case SQLite:
		veloxSink = newSQLSink()
		entSink = newSQLSink()
		veloxRes = runVeloxTraced(t, veloxSink, prog)
		entRes = runEntTraced(t, entSink, prog)
	case Postgres, MySQL:
		// PG/MySQL reuse a per-test pair whose traced clients log into the
		// pair's own sinks; those sinks (cleared per program) carry the SQL.
		veloxRes, entRes, veloxSink, entSink = runDialectProgram(t, backend, prog)
	default:
		t.Fatalf("RunParity: unsupported backend %s", backend)
	}

	rep := Report{Backend: backend, Prog: prog, Ops: make([]OpReport, len(prog))}
	for i := range prog {
		vDiff := diffOp(ref, veloxRes, i)
		eDiff := diffOp(ref, entRes, i)
		verdict := compare.Classify(len(vDiff) == 0, len(eDiff) == 0)
		rep.Ops[i] = OpReport{
			OpIndex:   i,
			Op:        prog[i],
			Verdict:   verdict,
			VeloxDiff: vDiff,
			EntDiff:   eDiff,
			VeloxSQL:  veloxSink.forOp(i),
			EntSQL:    entSink.forOp(i),
		}
	}
	return rep
}

// runDialectProgram runs prog against the (velox, ent) pair for the current
// test scope on a PG/MySQL backend. It lazily opens (and caches within the
// scope) the pair on the first call, truncates BOTH databases before running so
// the program starts from empty, executes the program on both ORMs, and returns
// the normalized results plus the pair's SQL-trace sinks (cleared per program).
// If the backend is configured but unreachable / unprivileged, the pair open
// path skips the test rather than failing.
func runDialectProgram(t testing.TB, backend Backend, prog op.Program) (veloxRes, entRes []model.Result, veloxSink, entSink *sqlSink) {
	t.Helper()
	pair := getDialectPair(t, backend)
	if pair == nil {
		t.Skipf("%s backend not reachable; skipping", backend)
	}

	// Isolate this program from the previous one on the shared, long-lived pair.
	ctx := context.Background()
	if err := pair.reset(ctx); err != nil {
		t.Fatalf("%s: truncate between programs failed: %v", backend, err)
	}
	pair.veloxSink.clear()
	pair.entSink.clear()

	veloxRes = runVeloxOnClient(pair.velox, pair.veloxSink, prog)
	entRes = runEntOnClient(pair.ent, pair.entSink, prog)
	return veloxRes, entRes, pair.veloxSink, pair.entSink
}

// getDialectPair returns the cached (velox, ent) pair for (t, backend), opening
// it on first use. Returns nil when the backend is configured but cannot be
// opened (caller skips). The cache entry is dropped on test cleanup so a later
// test with the same testing.TB pointer (subtests reuse parents) re-opens fresh.
func getDialectPair(t testing.TB, backend Backend) *dialectPair {
	t.Helper()
	key := dialectCacheKey{tb: t, backend: backend}
	if v, ok := dialectCache.Load(key); ok {
		return v.(*dialectPair)
	}
	pair, ok := newTracedDialectPair(t, backend)
	if !ok {
		return nil
	}
	dialectCache.Store(key, pair)
	t.Cleanup(func() { dialectCache.Delete(key) })
	return pair
}

// diffOp compares one op's results (reference vs other), after stripping
// timestamp VALUES so only their ordering effect — already pinned by the parity
// clock — matters. It compares a single-element slice so Diff's op-index is 0;
// callers care only about whether the slice is empty.
func diffOp(ref, other []model.Result, i int) []compare.Mismatch {
	a := stripTimestamps(ref[i])
	b := stripTimestamps(other[i])
	return compare.Diff([]model.Result{a}, []model.Result{b})
}

// stripTimestamps returns a shallow copy of r with created_at / updated_at
// removed from every row, leaving the rest of the structure intact.
func stripTimestamps(r model.Result) model.Result {
	if r.Rows == nil {
		return r
	}
	rows := make([]model.Row, len(r.Rows))
	for i, row := range r.Rows {
		nr := make(model.Row, len(row))
		for k, v := range row {
			if timestampFields[k] {
				continue
			}
			nr[k] = v
		}
		rows[i] = nr
	}
	return model.Result{Rows: rows, Scalar: r.Scalar, Page: r.Page, Err: r.Err}
}

// AllPass reports whether every op's verdict is Pass.
func (r Report) AllPass() bool {
	for _, o := range r.Ops {
		if o.Verdict != compare.Pass {
			return false
		}
	}
	return true
}

// CountVeloxBugs returns how many ops were classified VeloxBug (ent matches the
// reference, velox does not). This is the velox-focused failure signal.
func (r Report) CountVeloxBugs() int {
	return r.countVerdict(compare.VeloxBug)
}

// CountEntDivergent returns how many ops were classified EntDivergent.
func (r Report) CountEntDivergent() int {
	return r.countVerdict(compare.EntDivergent)
}

// CountReferenceSuspect returns how many ops were classified ReferenceSuspect.
func (r Report) CountReferenceSuspect() int {
	return r.countVerdict(compare.ReferenceSuspect)
}

func (r Report) countVerdict(v compare.Verdict) int {
	n := 0
	for _, o := range r.Ops {
		if o.Verdict == v {
			n++
		}
	}
	return n
}

// String renders a human-readable report. Pass ops are summarized in one line;
// every non-Pass op gets the failing op (op.Format-style), its verdict, the
// structured mismatches against the reference for BOTH ORMs, and the SQL each
// ORM emitted for that op — so a VeloxBug makes the failing op, the three
// values, and the two SQL statements obvious.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "parity report [backend=%s] %d ops\n", r.Backend, len(r.Ops))
	pass := 0
	for _, o := range r.Ops {
		if o.Verdict == compare.Pass {
			pass++
			continue
		}
		fmt.Fprintf(&b, "\n── op %d: %s\n", o.OpIndex, formatOp(o.Op))
		fmt.Fprintf(&b, "   verdict: %s\n", o.Verdict)
		if len(o.VeloxDiff) > 0 {
			fmt.Fprintf(&b, "   velox≠ref: %s\n", formatMismatches(o.VeloxDiff))
		}
		if len(o.EntDiff) > 0 {
			fmt.Fprintf(&b, "   ent≠ref:   %s\n", formatMismatches(o.EntDiff))
		}
		writeValueTriples(&b, o.VeloxDiff, o.EntDiff)
		writeSQL(&b, "velox SQL", o.VeloxSQL)
		writeSQL(&b, "ent SQL  ", o.EntSQL)
	}
	fmt.Fprintf(&b, "\nsummary: %d pass, %d velox_bug, %d ent_divergent, %d reference_suspect\n",
		pass, r.CountVeloxBugs(), r.CountEntDivergent(), r.CountReferenceSuspect())
	return b.String()
}

// formatOp renders an op the same way op.Format renders a whole program line.
func formatOp(o op.Op) string {
	prog := op.Program{o}
	line := op.Format(prog)
	// op.Format prefixes "0: " and a trailing newline; strip both.
	line = strings.TrimSuffix(line, "\n")
	return strings.TrimPrefix(line, "0: ")
}

func formatMismatches(ms []compare.Mismatch) string {
	parts := make([]string, len(ms))
	for i, m := range ms {
		if m.RowIndex >= 0 {
			parts[i] = fmt.Sprintf("[row %d %s: %v vs %v]", m.RowIndex, m.Field, m.A, m.B)
		} else {
			parts[i] = fmt.Sprintf("[%s: %v vs %v]", m.Field, m.A, m.B)
		}
	}
	return strings.Join(parts, " ")
}

// writeValueTriples prints, per mismatched field, a one-line `ref / velox / ent`
// value triple so a VeloxBug is instantly legible without mentally joining the
// separate velox≠ref and ent≠ref lines. veloxDiff/entDiff are both diffed
// against the reference, so each Mismatch's A is the reference value and B is the
// ORM value; a field absent from a diff means that ORM matched the reference
// (printed as "ref"). Fields are joined by (rowIndex, field) and emitted in a
// stable order.
func writeValueTriples(b *strings.Builder, veloxDiff, entDiff []compare.Mismatch) {
	type key struct {
		row   int
		field string
	}
	refVal := map[key]any{}
	veloxVal := map[key]any{}
	entVal := map[key]any{}
	var order []key
	seen := map[key]bool{}

	note := func(m compare.Mismatch, vals map[key]any) {
		k := key{row: m.RowIndex, field: m.Field}
		if !seen[k] {
			seen[k] = true
			order = append(order, k)
		}
		refVal[k] = m.A
		vals[k] = m.B
	}
	for _, m := range veloxDiff {
		note(m, veloxVal)
	}
	for _, m := range entDiff {
		note(m, entVal)
	}
	if len(order) == 0 {
		return
	}

	dump := func(vals map[key]any, k key) string {
		if v, ok := vals[k]; ok {
			return fmt.Sprintf("%v", v)
		}
		return "ref" // this ORM matched the reference for this field
	}
	for _, k := range order {
		label := k.field
		if k.row >= 0 {
			label = fmt.Sprintf("row %d %s", k.row, k.field)
		}
		fmt.Fprintf(b, "   values [%s]: ref=%v / velox=%s / ent=%s\n",
			label, refVal[k], dump(veloxVal, k), dump(entVal, k))
	}
}

func writeSQL(b *strings.Builder, label string, stmts []string) {
	if len(stmts) == 0 {
		fmt.Fprintf(b, "   %s: (none)\n", label)
		return
	}
	for _, s := range stmts {
		fmt.Fprintf(b, "   %s: %s\n", label, s)
	}
}
