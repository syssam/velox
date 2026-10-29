package bench

import (
	"context"
	"testing"
)

// setup opens a fresh database, creates the tables and seeds them.
func setup(tb testing.TB) *app {
	tb.Helper()
	a := newApp(tb, openDB(tb))
	if err := a.seed(context.Background()); err != nil {
		tb.Fatal(err)
	}
	return a
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
	if !prevIDIndexed(t, db) {
		t.Error("e1s has no index on prev_id")
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
	if _, name, err := a.get(ctx, 7); err != nil || name != "p7" {
		t.Errorf("get(7) = %q, %v; want p7", name, err)
	}
	counts, err := a.query(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 100 || counts[0] != 499 {
		t.Errorf("query: %d values, the first %v; want 100, the first 499", len(counts), counts[:min(1, len(counts))])
	}
	for i := 1; i < len(counts); i++ {
		if counts[i] >= counts[i-1] {
			t.Errorf("query: counts[%d] = %d after %d, not strictly descending", i, counts[i], counts[i-1])
			break
		}
	}
	if parents, children, err := a.eager(ctx); err != nil || parents != 100 || children != 500 {
		t.Errorf("eager = %d parents, %d children, %v; want 100, 500", parents, children, err)
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
