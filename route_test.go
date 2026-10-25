package velox_test

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

// replicaSet is a DB on a primary and two replica fakes, and the routes its
// statements took, when it has an Around hook to record them.
type replicaSet struct {
	p, r0, r1 *velox.FakeExec
	db        *velox.DB
	mu        sync.Mutex
	routes    []velox.Route
}

// openReplicas opens a replicaSet, with extra options; hooked adds an Around
// hook that records each statement's Route, so the statements take run's path
// instead of plain's.
func openReplicas(hooked bool, extra ...velox.Option) *replicaSet {
	s := &replicaSet{p: &velox.FakeExec{}, r0: &velox.FakeExec{}, r1: &velox.FakeExec{}}
	opts := append([]velox.Option{velox.WithReplicas(velox.FakeConn(s.r0), velox.FakeConn(s.r1))}, extra...)
	if hooked {
		opts = append(opts, velox.WithAround(s.record))
	}
	s.db = velox.OpenFake(velox.Postgres, s.p, opts...)
	return s
}

func (s *replicaSet) record(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
	s.mu.Lock()
	s.routes = append(s.routes, st.Route)
	s.mu.Unlock()
	return next(ctx)
}

// calls is how many statements each fake was sent: primary, replica 0,
// replica 1.
func (s *replicaSet) calls() [3]int { return [3]int{s.p.Calls, s.r0.Calls, s.r1.Calls} }

// check reports a difference from the calls and, when the DB is hooked, the
// routes wanted.
func (s *replicaSet) check(t *testing.T, hooked bool, calls [3]int, routes ...velox.Route) {
	t.Helper()
	if got := s.calls(); got != calls {
		t.Errorf("calls (primary, replica 0, replica 1) %v, want %v", got, calls)
	}
	if hooked && !reflect.DeepEqual(s.routes, routes) {
		t.Errorf("routes %+v, want %+v", s.routes, routes)
	}
}

var (
	onPrimary = velox.Route{Replica: -1}
	onR0      = velox.Route{Replica: 0}
	onR1      = velox.Route{Replica: 1}
)

func bothPaths(t *testing.T, f func(t *testing.T, hooked bool)) {
	for _, hooked := range []bool{false, true} {
		name := "plain"
		if hooked {
			name = "hooked"
		}
		t.Run(name, func(t *testing.T) { f(t, hooked) })
	}
}

// TestRouteTable: every row of the routing table, on plain's path and run's.
func TestRouteTable(t *testing.T) {
	t.Run("writes go to the primary", func(t *testing.T) {
		bothPaths(t, func(t *testing.T, hooked bool) {
			s := openReplicas(hooked)
			s.p.Rows = [][]any{{int64(42)}} // the keys the inserts read back
			inv := testmodel.Invoice{OrgID: 1, Number: "N"}
			if err := s.db.Insert(&inv).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.ID.EQ(1)).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Delete[testmodel.Invoice]().Where(I.ID.EQ(1)).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			up := testmodel.Invoice{OrgID: 1, Number: "N"}
			if err := s.db.Insert(&up).OnConflict(I.OrgID, I.Number).Update(I.Total).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			s.check(t, hooked, [3]int{4, 0, 0}, onPrimary, onPrimary, onPrimary, onPrimary)
		})
	})
	t.Run("every statement in a transaction goes to the primary", func(t *testing.T) {
		bothPaths(t, func(t *testing.T, hooked bool) {
			s := openReplicas(hooked)
			s.p.Rows = [][]any{invoiceRow(1)}
			err := s.db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
				if _, err := tx.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx); err != nil {
					return err
				}
				if _, err := tx.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.ID.EQ(1)).Exec(ctx); err != nil {
					return err
				}
				return tx.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
					_, err := tx.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx)
					return err
				})
			})
			if err != nil {
				t.Fatal(err)
			}
			// SELECT, UPDATE, SAVEPOINT, SELECT, RELEASE
			s.check(t, hooked, [3]int{5, 0, 0}, onPrimary, onPrimary, onPrimary)
			if s.p.Log[0] != "BEGIN" || s.p.Log[len(s.p.Log)-1] != "COMMIT" {
				t.Errorf("the transaction is not on the primary: %q", s.p.Log)
			}
		})
	})
	t.Run("a read under Primary goes to the primary", func(t *testing.T) {
		bothPaths(t, func(t *testing.T, hooked bool) {
			s := openReplicas(hooked)
			s.p.Results = [][][]any{{invoiceRow(1)}, {invoiceRow(1)}, {{int64(1)}}, {invoiceRow(1)}}
			pctx := velox.WithPrimaryReads(ctx)
			readAll(t, s.db, pctx)
			s.check(t, hooked, [3]int{4, 0, 0}, onPrimary, onPrimary, onPrimary, onPrimary)
		})
	})
	t.Run("reads go to the replicas in turn", func(t *testing.T) {
		bothPaths(t, func(t *testing.T, hooked bool) {
			s := openReplicas(hooked)
			s.r0.Results = [][][]any{{invoiceRow(1)}, {{int64(1)}}}    // All, Count
			s.r1.Results = [][][]any{{invoiceRow(1)}, {invoiceRow(1)}} // One, Iter
			readAll(t, s.db, ctx)
			s.check(t, hooked, [3]int{0, 2, 2}, onR0, onR1, onR0, onR1)
		})
	})
}

// readAll runs All, One, Count and Iter, in that order.
func readAll(t *testing.T, db *velox.DB, ctx context.Context) {
	t.Helper()
	q := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1)
	if _, err := q.All(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := q.One(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Count(ctx); err != nil {
		t.Fatal(err)
	}
	for _, err := range q.Iter(ctx) {
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestRouteSkipsADownReplica: a replica in back-off is skipped until its
// back-off ends; with every replica in back-off, reads go to the primary.
func TestRouteSkipsADownReplica(t *testing.T) {
	s := openReplicas(true)
	for _, e := range []*velox.FakeExec{s.p, s.r0, s.r1} {
		e.Rows = [][]any{invoiceRow(1)}
	}
	read := func() {
		t.Helper()
		if _, err := s.db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx); err != nil {
			t.Fatal(err)
		}
	}
	velox.SetReplicaDown(s.db, 0, time.Now().Add(time.Hour))
	read()
	read()
	velox.SetReplicaDown(s.db, 1, time.Now().Add(time.Hour))
	read()
	velox.SetReplicaDown(s.db, 0, time.Now().Add(-time.Second)) // its back-off has ended
	read()
	s.check(t, true, [3]int{1, 1, 2}, onR1, onR1, onPrimary, onR0)
}

// TestRouteDryRunTakesNoTurn: SQL(ctx) runs the route stage but sends
// nothing, so it does not move the round-robin on.
func TestRouteDryRunTakesNoTurn(t *testing.T) {
	s := openReplicas(false)
	s.r0.Rows, s.r1.Rows = [][]any{invoiceRow(1)}, [][]any{invoiceRow(1)}
	q := s.db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1)
	for range 3 {
		if _, err := q.All(ctx); err != nil {
			t.Fatal(err)
		}
		if _, _, err := q.SQL(ctx); err != nil {
			t.Fatal(err)
		}
	}
	s.check(t, false, [3]int{0, 2, 1})
}

// TestNoReplicasIsTheOldPath: a DB opened without WithReplicas sends every
// statement to its one connection, as before routing.
func TestNoReplicasIsTheOldPath(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		e := &velox.FakeExec{Results: [][][]any{{invoiceRow(1)}, {invoiceRow(1)}, {{int64(1)}}, {invoiceRow(1)}}}
		var routes []velox.Route
		var opts []velox.Option
		if hooked {
			opts = append(opts, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
				routes = append(routes, st.Route)
				return next(ctx)
			}))
		}
		db := velox.OpenFake(velox.Postgres, e, opts...)
		readAll(t, db, ctx)
		if _, err := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.ID.EQ(1)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if e.Calls != 5 {
			t.Errorf("%d statements sent, want 5", e.Calls)
		}
		if hooked && !reflect.DeepEqual(routes, []velox.Route{onPrimary, onPrimary, onPrimary, onPrimary, onPrimary}) {
			t.Errorf("routes %+v, want Replica -1 for each", routes)
		}
	})
}

// TestWithLoadsRunWhereTheQueryRan pins that a query's With loads run on
// the replica the query read, so parents and children agree.
func TestWithLoadsRunWhereTheQueryRan(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := openReplicas(hooked)
		run := [][][]any{
			{invoiceRow(1), invoiceRow(2)},
			{itemRow(10, 1, "A", 1), itemRow(11, 2, "B", 1)},
		}
		s.r0.Results = append(append([][][]any{}, run...), run...)
		s.r1.Results = append(append([][][]any{}, run...), run...)
		for range 4 {
			invs, err := s.db.Query[testmodel.Invoice]().With(I.Items).Limit(10).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if items, ok := invs[1].Items.Get(); !ok || len(items) != 1 || items[0].SKU != "B" {
				t.Fatalf("items %v, loaded %v", items, ok)
			}
		}
		s.check(t, hooked, [3]int{0, 4, 4}, onR0, onR0, onR1, onR1, onR0, onR0, onR1, onR1)
		for i, e := range []*velox.FakeExec{s.r0, s.r1} {
			want := []string{"invoices", "items", "invoices", "items"}
			var got []string
			for _, q := range e.Log {
				_, after, _ := strings.Cut(q, "FROM ")
				got = append(got, strings.Trim(strings.Fields(after)[0], `"`))
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("replica %d read %v, want %v", i, got, want)
			}
		}
	})
}

// TestRoundRobinUnderConcurrency pins Review Focus 3: reads from many
// goroutines are spread evenly over the replicas.
func TestRoundRobinUnderConcurrency(t *testing.T) {
	s := openReplicas(false)
	s.r0.Rows, s.r1.Rows = [][]any{invoiceRow(1)}, [][]any{invoiceRow(1)}
	const goroutines, reads = 8, 250
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for range goroutines {
		wg.Go(func() {
			for range reads {
				if _, err := s.db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx); err != nil {
					errs <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	each := goroutines * reads / 2
	for i, n := range []int{s.r0.Calls, s.r1.Calls} {
		if n < each-1 || n > each+1 {
			t.Errorf("replica %d read %d times, want %d ± 1", i, n, each)
		}
	}
	if s.p.Calls != 0 {
		t.Errorf("the primary read %d times", s.p.Calls)
	}
}

// TestRoutingMisconfigurationPanics: misconfigured connections panic when the
// DB is opened, not at its first statement.
func TestRoutingMisconfigurationPanics(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	fake := func() velox.Conn { return velox.FakeConn(&velox.FakeExec{}) }
	shardFor := velox.WithShardFor(func(context.Context, any) (string, error) { return "eu", nil })
	for name, c := range map[string]struct {
		open func()
		want string // in the panic's message
	}{
		"a zero Conn replica": {func() {
			velox.OpenFake(velox.Postgres, &velox.FakeExec{}, velox.WithReplicas(velox.Conn{}))
		}, "replica 0 is the zero Conn"},
		"a pgx replica on MySQL": {func() {
			velox.Open(velox.MySQL, sqlDB, velox.WithReplicas(velox.SQLConn(sqlDB), velox.PgxConn(new(pgxpool.Pool))))
		}, "replica 1 is a PgxConn, valid only for PostgreSQL"},
		"SQLConn(nil)":                {func() { velox.SQLConn(nil) }, "velox: SQLConn(nil)"},
		"PgxConn(nil)":                {func() { velox.PgxConn(nil) }, "velox: PgxConn(nil)"},
		"a negative replica back-off": {func() { velox.WithReplicaBackoff(-1) }, "velox: WithReplicaBackoff(-1ns): the back-off must not be negative"},
		"OpenShards without WithShardFor": {func() {
			velox.OpenShards(velox.Postgres, map[string]velox.Shard{"eu": {Primary: fake()}})
		}, "OpenShards: WithShardFor is required"},
		"OpenShards with no shards": {func() {
			velox.OpenShards(velox.Postgres, map[string]velox.Shard{}, shardFor)
		}, "OpenShards: no shards"},
		"OpenShards with a zero Primary": {func() {
			velox.OpenShards(velox.Postgres, map[string]velox.Shard{"eu": {Primary: fake()}, "us": {}}, shardFor)
		}, `shard "us"'s Primary is the zero Conn`},
		"OpenShards with a zero Conn replica": {func() {
			velox.OpenShards(velox.Postgres, map[string]velox.Shard{"eu": {Primary: fake(), Replicas: []velox.Conn{fake(), {}}}}, shardFor)
		}, `shard "eu"'s replica 1 is the zero Conn`},
		"OpenShards with a pgx primary on MySQL": {func() {
			velox.OpenShards(velox.MySQL, map[string]velox.Shard{"eu": {Primary: velox.PgxConn(new(pgxpool.Pool))}}, shardFor)
		}, `shard "eu"'s Primary is a PgxConn, valid only for PostgreSQL`},
		"OpenShards with a pgx replica on MySQL": {func() {
			velox.OpenShards(velox.MySQL, map[string]velox.Shard{"eu": {Primary: fake(), Replicas: []velox.Conn{velox.PgxConn(new(pgxpool.Pool))}}}, shardFor)
		}, `shard "eu"'s replica 0 is a PgxConn, valid only for PostgreSQL`},
		"OpenShards with an unnamed shard": {func() {
			velox.OpenShards(velox.Postgres, map[string]velox.Shard{"": {Primary: fake()}}, shardFor)
		}, "OpenShards: a shard's name must not be empty"},
		"WithReplicas given to OpenShards": {func() {
			velox.OpenShards(velox.Postgres, map[string]velox.Shard{"eu": {Primary: fake()}}, shardFor, velox.WithReplicas(fake()))
		}, "OpenShards: WithReplicas is for Open and OpenPgx"},
		"WithShardFor given to Open": {func() {
			velox.Open(velox.SQLite, sqlDB, shardFor)
		}, "WithShardFor is for OpenShards"},
		"WithShardFor given to OpenPgx": {func() {
			velox.OpenPgx(new(pgxpool.Pool), shardFor)
		}, "WithShardFor is for OpenShards"},
		"WithShardFor(nil)": {func() { velox.WithShardFor(nil) }, "velox: WithShardFor(nil)"},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				r := recover()
				if s, ok := r.(string); !ok || !strings.HasPrefix(s, "velox: ") || !strings.Contains(s, c.want) {
					t.Errorf("recovered %v, want a velox panic saying %q", r, c.want)
				}
			}()
			c.open()
		})
	}
}
