package runner

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"velox.test/parity/compare"
	"velox.test/parity/model"
	"velox.test/parity/op"
)

// Backend selects the database engine the three-way driver runs against. Only
// SQLite is wired in A3a; Postgres and MySQL are added in A3b.
type Backend int

const (
	// SQLite runs the parity programs against in-memory SQLite (velox via the
	// modernc "sqlite" driver, ent via the same).
	SQLite Backend = iota
)

func (b Backend) String() string {
	switch b {
	case SQLite:
		return "sqlite"
	default:
		return fmt.Sprintf("backend(%d)", int(b))
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

// RunParity runs all three executors (reference, velox, ent) on fresh, isolated
// clients for the backend and classifies each op's outcome per the verdict
// table. SQL from both ORMs is captured per op so a non-Pass op prints the
// failing op, the structured mismatches, and the two SQL statements.
func RunParity(t testing.TB, backend Backend, prog op.Program) Report {
	t.Helper()
	if backend != SQLite {
		t.Fatalf("RunParity: unsupported backend %s (A3b adds Postgres/MySQL)", backend)
	}

	ref := Reference(t, prog)

	veloxSink := newSQLSink()
	veloxRes := runVeloxTraced(t, veloxSink, prog)

	entSink := newSQLSink()
	entRes := runEntTraced(t, entSink, prog)

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

func writeSQL(b *strings.Builder, label string, stmts []string) {
	if len(stmts) == 0 {
		fmt.Fprintf(b, "   %s: (none)\n", label)
		return
	}
	for _, s := range stmts {
		fmt.Fprintf(b, "   %s: %s\n", label, s)
	}
}
