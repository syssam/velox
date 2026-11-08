package veloxotel

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// captured is the Statement of a dry run of a one-row read, rendered, so
// that it has a fingerprint.
func captured(t *testing.T) *velox.Statement {
	t.Helper()
	ctx := context.Background()
	var st *velox.Statement
	db := velox.Open(velox.Postgres, sql.OpenDB(unreachable{}), velox.WithRewrite(func(_ context.Context, s *velox.Statement) error {
		st = s
		return nil
	})).WithoutPolicy("otel tests")
	if _, _, err := db.Query[testmodel.Invoice]().Limit(1).SQL(ctx); err != nil {
		t.Fatal(err)
	}
	if st == nil || st.Fingerprint == "" {
		t.Fatalf("statement %+v: no fingerprint", st)
	}
	return st
}

func noopAdapter(cfg Config) *adapter {
	cfg.TracerProvider, cfg.MeterProvider = tracenoop.NewTracerProvider(), metricnoop.NewMeterProvider()
	return newAdapter(cfg)
}

// TestShapeCache: a builder statement's shape is described once and shared
// by its executions; its success set is built only without TargetFor; a
// fingerprint that another entity's statement reaches is not shared; and the
// cache stops growing at maxShapes.
func TestShapeCache(t *testing.T) {
	st := captured(t)
	t.Run("hit", func(t *testing.T) {
		a := noopAdapter(Config{})
		first := a.shapeOf(st)
		if first.coll != "invoices" || first.sum != "SELECT invoices" || first.ok == nil {
			t.Fatalf("shape %+v", first)
		}
		if again := a.shapeOf(st); again != first {
			t.Error("the second execution described the shape again")
		}
		if n := testing.AllocsPerRun(100, func() { a.shapeOf(st) }); n != 0 {
			t.Errorf("a hit: %v allocations, want 0", n)
		}
	})
	t.Run("TargetFor", func(t *testing.T) {
		a := noopAdapter(Config{TargetFor: func(velox.Route) Target { return Target{} }})
		if sh := a.shapeOf(st); sh.ok != nil {
			t.Error("a success set was built with TargetFor, which names the server per route")
		}
	})
	t.Run("another entity", func(t *testing.T) {
		a := noopAdapter(Config{})
		first := a.shapeOf(st)
		other := *st
		e := *st.Entity
		other.Entity = &e
		if sh := a.shapeOf(&other); sh == first || sh.entity != &e {
			t.Error("a statement of another entity got the cached shape")
		}
	})
	t.Run("bound", func(t *testing.T) {
		a := noopAdapter(Config{})
		for i := range maxShapes + 10 {
			s := *st
			s.Fingerprint = fmt.Sprint(i)
			a.shapeOf(&s)
		}
		if n := len(*a.shapes.m.Load()); n != maxShapes {
			t.Errorf("%d shapes cached, want %d", n, maxShapes)
		}
		s := *st
		s.Fingerprint = "past the bound"
		if a.shapeOf(&s).ok != nil {
			t.Error("a shape past the bound built a success set it cannot keep")
		}
	})
	t.Run("concurrent", func(t *testing.T) {
		a := noopAdapter(Config{})
		var wg sync.WaitGroup
		for i := range 8 {
			wg.Go(func() {
				for j := range 50 {
					s := *st
					s.Fingerprint = fmt.Sprint((i + j) % 20)
					if sh := a.shapeOf(&s); sh.sum != "SELECT invoices" {
						t.Errorf("shape %+v", sh)
					}
				}
			})
		}
		wg.Wait()
		if n := len(*a.shapes.m.Load()); n != 20 {
			t.Errorf("%d shapes cached, want 20", n)
		}
	})
}
