package bench

import (
	"context"
	"maps"
	"testing"
)

// setup opens a fresh database, creates the tables, seeds them and analyzes them.
func setup(tb testing.TB) *app {
	tb.Helper()
	db := openDB(tb)
	a := newApp(tb, db)
	if err := a.seed(context.Background()); err != nil {
		tb.Fatal(err)
	}
	analyze(tb, db)
	return a
}

// wantShape is the number of indexes and foreign keys of each table, the same
// for every ORM: e1s..e5s have the prev_id index and foreign key, and e4s and
// e5s one more index each, for the where and page benchmarks. On postgres the
// primary key's index counts too.
func wantShape() map[string]tableShape {
	pk := 0
	if dbDialect() == "postgres" {
		pk = 1
	}
	return map[string]tableShape{
		"e0s": {Indexes: pk}, "e1s": {Indexes: pk + 1, FKs: 1},
		"e2s": {Indexes: pk + 1, FKs: 1}, "e3s": {Indexes: pk + 1, FKs: 1},
		"e4s": {Indexes: pk + 2, FKs: 1}, "e5s": {Indexes: pk + 2, FKs: 1},
	}
}

// TestGuard checks that each operation does its work, so that no ORM wins by
// doing less (Ruling 7).
func TestGuard(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	a := newApp(t, db)
	if err := a.seed(ctx); err != nil {
		t.Fatal(err)
	}
	analyze(t, db)
	if !prevIDIndexed(t, db) {
		t.Error("e1s has no index on prev_id")
	}
	if got, want := ddlShape(t, db), wantShape(); !maps.Equal(got, want) {
		t.Errorf("the tables' indexes and foreign keys are %v, want %v", got, want)
	}
	if err := a.insertOne(ctx); err != nil {
		t.Fatal(err)
	}
	if _, e2, _, err := a.rows(ctx); err != nil || e2 != 2 {
		t.Errorf("after insertOne: e2 rows %d, %v; want 2", e2, err)
	}
	if err := a.insertBulk(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, e3, err := a.rows(ctx); err != nil || e3 != 100 {
		t.Errorf("after insertBulk: e3 rows %d, %v; want 100", e3, err)
	}
	if id, name, err := a.get(ctx, 7); err != nil || id != a.seededID(7) || name != "p7" {
		t.Errorf("get(7) = %d, %q, %v; want %d, p7", id, name, err, a.seededID(7))
	}
	counts, err := a.query(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 100 || counts[0] != 449 {
		t.Errorf("query: %d values, the first %v; want 100, the first 449", len(counts), counts[:min(1, len(counts))])
	}
	for i := 1; i < len(counts); i++ {
		if counts[i] >= counts[i-1] {
			t.Errorf("query: counts[%d] = %d after %d, not strictly descending", i, counts[i], counts[i-1])
			break
		}
	}
	parents, children, err := a.eager(ctx)
	if err != nil || len(parents) != 100 || children != 500 {
		t.Errorf("eager = %d parents, %d children, %v; want 100, 500", len(parents), children, err)
	}
	for i := 1; i < len(parents); i++ {
		if parents[i] <= parents[i-1] {
			t.Errorf("eager: parent %d has id %d after %d, not strictly ascending", i, parents[i], parents[i-1])
			break
		}
	}
	if err := a.update(ctx, 7, "u"); err != nil {
		t.Fatal(err)
	}
	if _, name, err := a.get(ctx, 7); err != nil || name != "u" {
		t.Errorf("get(7) after update = %q, %v; want u", name, err)
	}
	if n, err := a.count(ctx); err != nil || n != 250 {
		t.Errorf("count = %d, %v; want 250", n, err)
	}
	if e1, _, _, err := a.rows(ctx); err != nil || e1 != 500 {
		t.Errorf("e1 rows %d, %v; want 500", e1, err)
	}
	guardPages(t, a)
}

func BenchmarkInsertOne(b *testing.B) {
	ctx, a := context.Background(), setup(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := a.insertOne(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkInsertBulk100(b *testing.B) {
	ctx, a := context.Background(), setup(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := a.insertBulk(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGetByID(b *testing.B) {
	ctx, a := context.Background(), setup(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		if _, _, err := a.get(ctx, i); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkQuery100(b *testing.B) {
	ctx, a := context.Background(), setup(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := a.query(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEagerO2M100(b *testing.B) {
	ctx, a := context.Background(), setup(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, _, err := a.eager(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkUpdateByID(b *testing.B) {
	ctx, a := context.Background(), setup(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		if err := a.update(ctx, i, "u"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCount(b *testing.B) {
	ctx, a := context.Background(), setup(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := a.count(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWhereAnd(b *testing.B) {
	ctx, a := context.Background(), setupPages(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := a.whereAnd(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWhereHasEdge(b *testing.B) {
	ctx, a := context.Background(), setupPages(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := a.whereHasEdge(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPageOffset(b *testing.B) {
	ctx, a := context.Background(), setupPages(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := a.pageOffset(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPageKeyset(b *testing.B) {
	ctx, a := context.Background(), setupPages(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := a.pageKeyset(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
