package apitest

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// openFailover opens a replicaSet whose primary and replicas each hold an
// invoice. Hooked, it records each statement's Route after next returns, as
// failover leaves it.
func openFailover(hooked bool, extra ...velox.Option) *replicaSet {
	s := &replicaSet{}
	if hooked {
		extra = append(extra, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
			err := next(ctx)
			s.mu.Lock()
			s.routes = append(s.routes, st.Route)
			s.mu.Unlock()
			return err
		}))
	}
	o := openReplicas(false, extra...)
	s.p, s.r0, s.r1, s.db = o.p, o.r0, o.r1, o.db
	for _, e := range []*fakeconn.Exec{s.p, s.r0, s.r1} {
		e.Rows = [][]any{invoiceRow(1)}
	}
	return s
}

var fellOver = velox.Route{Replica: -1, Fallback: true}

// TestAllReplicasDown: with every replica in back-off, reads go to the
// primary, and are not fallbacks.
func TestAllReplicasDown(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := openFailover(hooked)
		s.r0.Err, s.r1.Err = driver.ErrBadConn, driver.ErrBadConn
		for range 4 {
			readOne(t, ctx, s.db)
		}
		s.check(t, hooked, [3]int{4, 1, 1}, fellOver, fellOver, onPrimary, onPrimary)
	})
}

// TestRetryFailsReturnsPrimaryError: when the primary fails the retry too,
// the caller gets the primary's error.
func TestRetryFailsReturnsPrimaryError(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := openFailover(hooked)
		x := &pgconn.PgError{Code: "57P03", Message: "the database system is starting up"}
		s.r0.Errs, s.p.Err = []error{driver.ErrBadConn}, x
		_, err := s.db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx)
		if !errors.Is(err, x) || errors.Is(err, driver.ErrBadConn) || !strings.HasPrefix(err.Error(), "velox: testmodel.Invoice: ") {
			t.Fatalf("got %v, want the primary's error, named", err)
		}
		s.check(t, hooked, [3]int{1, 1, 0}, fellOver)
	})
}

// TestLoadsFollowAFallback: a query that fell over to the primary loads its
// With relations there too.
func TestLoadsFollowAFallback(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := openFailover(hooked)
		s.r0.Errs = []error{driver.ErrBadConn}
		s.p.Results = [][][]any{{invoiceRow(1), invoiceRow(2)}, {itemRow(10, 1, "A", 1), itemRow(11, 2, "B", 1)}}
		invs, err := s.db.Query[testmodel.Invoice]().With(I.Items).Limit(10).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if items, ok := invs[1].Edges.Items.Get(); !ok || len(items) != 1 || items[0].SKU != "B" {
			t.Fatalf("items %v, loaded %v", items, ok)
		}
		s.check(t, hooked, [3]int{2, 1, 0}, fellOver, onPrimary)
	})
}

// TestAroundRetryAfterFallbackStaysOnThePrimary: an Around hook that retries
// after a fallback whose retry failed sends the statement where its Route
// says, the primary, not back to the failed replica.
func TestAroundRetryAfterFallbackStaysOnThePrimary(t *testing.T) {
	var routes []velox.Route
	s := openFailover(false, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		err := next(ctx)
		if err != nil {
			err = next(ctx)
		}
		routes = append(routes, st.Route)
		return err
	}))
	s.r0.Errs, s.p.Errs = []error{driver.ErrBadConn}, []error{errors.New("primary hiccup")}
	readOne(t, ctx, s.db)
	s.check(t, false, [3]int{2, 1, 0})
	if len(routes) != 1 || routes[0] != fellOver {
		t.Errorf("routes %+v, want one fallback", routes)
	}
}
