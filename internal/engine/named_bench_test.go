package engine_test

import (
	"context"
	"strings"
	"testing"
	"unsafe"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/engine"
)

// The entities of the by-name benchmarks: two of one shape, whose Read
// policies confine them to the context's org, one by the column's index and
// one by its name.

type bnIdx struct {
	ID    int64
	OrgID int64
	Title string
}

type bnName struct {
	ID    int64
	OrgID int64
	Title string
}

// bnOrg is the context key of the org the policies read.
type bnOrg struct{}

// bnBad makes bnName's policy, interceptor or hook name a column it does
// not have: the value says which.
type bnBad struct{}

func bnOrgOf(ctx context.Context) int64 { v, _ := ctx.Value(bnOrg{}).(int64); return v }

func bnBadOf(ctx context.Context) string { v, _ := ctx.Value(bnBad{}).(string); return v }

// bnIdx's policy holds a typed handle's condition, which names the column
// by its index, converted to a Cond: the statement does not resolve it.
func (*bnIdx) Policy() velox.Policy {
	org := velox.NumberCol[bnIdx, int64]{I: 1}
	return velox.Policy{Read: func(ctx context.Context) (velox.Predicate, error) {
		return velox.Predicate(org.EQ(bnOrgOf(ctx))), nil
	}}
}

func (*bnName) Policy() velox.Policy {
	return velox.Policy{
		Read: func(ctx context.Context) (velox.Predicate, error) {
			if bnBadOf(ctx) == "policy" {
				return velox.FieldEQ("orgg_id", 1), nil
			}
			return velox.FieldEQ("org_id", bnOrgOf(ctx)), nil
		},
		Insert: func(ctx context.Context, r velox.Row) error {
			if r.Value("org_id") != bnOrgOf(ctx) {
				return velox.Deny("another org")
			}
			return nil
		},
	}
}

func (*bnName) Interceptors() []velox.Interceptor {
	return []velox.Interceptor{{Filter: func(ctx context.Context) (velox.Predicate, error) {
		if bnBadOf(ctx) == "interceptor" {
			return velox.FieldNEQ("titel", ""), nil
		}
		return velox.Allow(), nil
	}}}
}

func (*bnName) Hooks() []velox.Hook {
	return []velox.Hook{velox.Before(func(ctx context.Context, m *velox.Mutation) error {
		name := "org_id"
		if bnBadOf(ctx) == "hook" {
			name = "orgid"
		}
		_ = m.Fill(name, bnOrgOf(ctx)) // a misuse is recorded, and fails the write
		return nil
	}, velox.OpInsert)}
}

var (
	bnIdxMeta  velox.MetaOnce[bnIdx]
	bnNameMeta velox.MetaOnce[bnName]
)

func (*bnIdx) VeloxMeta() *velox.Meta[bnIdx] {
	return bnIdxMeta.Load(func() velox.Descriptor[bnIdx] {
		return velox.Descriptor[bnIdx]{
			Table:      "bn_idx",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id"}, {Field: "Title", Name: "title"}},
			PrimaryKey: []int{0},
			Schema:     (*bnIdx)(nil),
			FieldPtr:   func(x *bnIdx, i int) any { return [...]any{&x.ID, &x.OrgID, &x.Title}[i] },
		}
	})
}

func (*bnName) VeloxMeta() *velox.Meta[bnName] {
	return bnNameMeta.Load(func() velox.Descriptor[bnName] {
		return velox.Descriptor[bnName]{
			Table:      "bn_name",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id"}, {Field: "Title", Name: "title"}},
			PrimaryKey: []int{0},
			Schema:     (*bnName)(nil),

			FieldPtr: func(x *bnName, i int) any { return [...]any{&x.ID, &x.OrgID, &x.Title}[i] },
		}
	})
}

// bnDB is an in-memory SQLite database holding both tables, each with rows of
// two orgs.
func bnDB(tb testing.TB) *velox.DB {
	tb.Helper()
	db, err := velox.Connect("sqlite", ":memory:")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = db.Close() })
	bg := context.Background()
	for _, table := range []string{"bn_idx", "bn_name"} {
		if _, err := db.Exec(bg, "test schema", "CREATE TABLE "+table+" (id INTEGER PRIMARY KEY, org_id INTEGER NOT NULL, title TEXT NOT NULL)"); err != nil {
			tb.Fatal(err)
		}
		for i := range 20 {
			if _, err := db.Exec(bg, "test rows", "INSERT INTO "+table+" (org_id, title) VALUES (?, ?)", i%2+1, "t"); err != nil {
				tb.Fatal(err)
			}
		}
	}
	return db
}

// TestPolicyByName: a policy's and an interceptor's conditions by name
// resolve against the entity, a hook's Fill by name sets the rows, and a
// wrong name fails the statement or the write, naming who named it.
func TestPolicyByName(t *testing.T) {
	db := bnDB(t)
	org := context.WithValue(context.Background(), bnOrg{}, int64(2))
	rows, err := db.Query[bnName]().AllowUnbounded().All(org)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 10 {
		t.Errorf("%d rows of org 2, want 10", len(rows))
	}
	for _, r := range rows {
		if r.OrgID != 2 {
			t.Errorf("row of org %d", r.OrgID)
		}
	}
	x := bnName{Title: "new"}
	if err := db.Insert(&x).Exec(org); err != nil {
		t.Fatal(err)
	}
	if x.OrgID != 2 {
		t.Errorf("Fill set org %d", x.OrgID)
	}
	for _, tc := range []struct{ bad, err string }{
		{"policy", `velox: engine_test.bnName: the Read policy's condition: no column "orgg_id"`},
		{"interceptor", `velox: engine_test.bnName: interceptor [0]'s condition: no column "titel"`},
	} {
		ctx := context.WithValue(org, bnBad{}, tc.bad)
		if _, err := db.Query[bnName]().AllowUnbounded().All(ctx); err == nil || !strings.Contains(err.Error(), tc.err) {
			t.Errorf("%s: %v, want %q", tc.bad, err, tc.err)
		}
	}
	bad := context.WithValue(org, bnBad{}, "hook")
	y := bnName{Title: "y"}
	if err := db.Insert(&y).Exec(bad); err == nil || !strings.Contains(err.Error(), `velox: engine_test.bnName: hook: Fill("orgid"): no column "orgid"`) {
		t.Errorf("hook: %v", err)
	}
	z := bnName{Title: "z", OrgID: 1}
	if err := db.Insert(&z).Exec(org); err == nil {
		t.Error("an insert into another org: no error")
	}
}

// The budget of a condition by name: a scoped SELECT whose policy names its
// column within 3% of the same SELECT whose policy uses the column's index,
// on SQLite in memory. Measured 2026-11-05 on darwin/arm64, twelve
// interleaved runs each, benchstat: no difference it could tell from noise
// (p=0.76), and the same 36 allocations and 1296 bytes per SELECT. The
// resolution allocates nothing: a leaf holds its name as a pointer, not a
// string in an any.

func BenchmarkPolicyByIndex(b *testing.B) {
	db := bnDB(b)
	ctx := context.WithValue(context.Background(), bnOrg{}, int64(1))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := db.Query[bnIdx]().Limit(5).All(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPolicyByName(b *testing.B) {
	db := bnDB(b)
	ctx := context.WithValue(context.Background(), bnOrg{}, int64(1))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := db.Query[bnName]().Limit(5).All(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

// bnRows is n rows of bnName as an insert's mutation holds them.
func bnRows(n int) (*engine.MutationState, []bnName) {
	rows := make([]bnName, n)
	mu := &engine.MutationState{Op: velox.OpInsert, In: infoOf[bnName]()}
	for i := range rows {
		mu.Rows = append(mu.Rows, unsafe.Pointer(&rows[i]))
	}
	return mu, rows
}

// The budget of a mutation by name: Set by name within 20 ns per row of a
// typed store through the field. Measured 2026-11-05 on darwin/arm64, 64
// rows: 1.5 to 2.5 ns per row by name, 0.9 to 1.7 typed, no allocation.

const bnN = 64

func BenchmarkSetByName(b *testing.B) {
	mu, _ := bnRows(bnN)
	v := any(int64(7))
	b.ReportAllocs()
	for b.Loop() {
		if err := mu.SetByName("org_id", v); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/bnN, "ns/row")
}

func BenchmarkSetTyped(b *testing.B) {
	mu, _ := bnRows(bnN)
	b.ReportAllocs()
	for b.Loop() {
		for _, p := range mu.Rows {
			(*bnName)(p).OrgID = 7
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/bnN, "ns/row")
}
