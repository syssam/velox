package velox_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/apitest/fixture"
	"github.com/syssam/velox2/internal/testmodel"
)

// dryContext returns a context that IsDryRun reports true for, as SQL(ctx)
// gives to everything it runs.
func dryContext(t *testing.T, e *velox.FakeExec) (context.Context, *velox.DB) {
	t.Helper()
	var dry context.Context
	db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(func(ctx context.Context, st *velox.Statement) error {
		if st.DryRun {
			dry = ctx
		}
		return nil
	}))
	if _, _, err := db.Query[fixture.Dnote]().Limit(1).SQL(context.Background()); err != nil {
		t.Fatal(err)
	}
	if dry == nil || !velox.IsDryRun(dry) {
		t.Fatal("no dry-run context")
	}
	return dry, db
}

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
	for _, e := range []*velox.FakeExec{s.p, s.r0, s.r1} {
		e.Rows = [][]any{invoiceRow(1)}
	}
	return s
}

var fellOver = velox.Route{Replica: -1, Fallback: true}

// invoiceRow is declared in query_test.go; It is declared there too.

func itemRow(id, invoice int64, sku string, qty int64) []any {
	return []any{id, invoice, sku, qty}
}

var (
	ctx = velox.WithAllTenants(context.Background(), "velox tests")
	t0  = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	I   = testmodel.Invoices
	It  = testmodel.Items
)

func invoiceRow(id int64) []any {
	return []any{id, t0, t0, int64(1), fmt.Sprintf("INV-%d", id), 12.5, true, t0, "", []byte("d")}
}

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

// seeded opens a replicaSet whose primary and replicas each hold an invoice.
func seeded(hooked bool, opts ...velox.Option) *replicaSet {
	s := openReplicas(hooked, opts...)
	s.p.Rows = [][]any{invoiceRow(1)}
	s.r0.Rows = [][]any{invoiceRow(1)}
	s.r1.Rows = [][]any{invoiceRow(1)}
	return s
}

func readOne(t *testing.T, ctx context.Context, db *velox.DB) {
	t.Helper()
	if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx); err != nil {
		t.Fatal(err)
	}
}

// shardSet is a DB on two shards: eu, a primary and a replica, and us, a
// primary alone. Its WithShardFor maps tenant 1 to eu, 2 to us and 3 to
// "mars", which is no shard, and fails for any other tenant.
type shardSet struct {
	euP, euR, usP *velox.FakeExec
	db            *velox.DB
	lookups       atomic.Int32 // the WithShardFor calls
	mu            sync.Mutex
	routes        []velox.Route
}

var errShardLookup = errors.New("the tenant is being moved")

// openShards opens a shardSet; hooked adds an Around hook that records each
// statement's Route once it ran, a fallback included, so the statements take
// run's path instead of plain's.
func openShards(hooked bool, extra ...velox.Option) *shardSet {
	s := &shardSet{euP: &velox.FakeExec{}, euR: &velox.FakeExec{}, usP: &velox.FakeExec{}}
	opts := append([]velox.Option{velox.WithShardFor(s.shardFor)}, extra...)
	if hooked {
		opts = append(opts, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
			err := next(ctx)
			s.mu.Lock()
			s.routes = append(s.routes, st.Route)
			s.mu.Unlock()
			return err
		}))
	}
	s.db = velox.OpenShards(velox.Postgres, map[string]velox.Shard{
		"eu": {Primary: velox.FakeConn(s.euP), Replicas: []velox.Conn{velox.FakeConn(s.euR)}},
		"us": {Primary: velox.FakeConn(s.usP)},
	}, opts...)
	return s
}

func (s *shardSet) shardFor(_ context.Context, tenant any) (string, error) {
	s.lookups.Add(1)
	switch tenant {
	case int64(1):
		return "eu", nil
	case int64(2):
		return "us", nil
	case int64(3):
		return "mars", nil
	}
	return "", errShardLookup
}

// calls is how many statements each fake was sent: eu's primary, eu's
// replica, us's primary.
func (s *shardSet) calls() [3]int { return [3]int{s.euP.Calls, s.euR.Calls, s.usP.Calls} }

// check reports a difference from the calls and, when the DB is hooked, the
// routes wanted.
func (s *shardSet) check(t *testing.T, hooked bool, calls [3]int, routes ...velox.Route) {
	t.Helper()
	if got := s.calls(); got != calls {
		t.Errorf("calls (eu primary, eu replica, us primary) %v, want %v", got, calls)
	}
	if hooked && !reflect.DeepEqual(s.routes, routes) {
		t.Errorf("routes %+v, want %+v", s.routes, routes)
	}
}

var (
	bg   = context.Background()
	euR0 = velox.Route{Shard: "eu", Replica: 0}
)

var Tk = testmodel.Tickets

// tenant returns a context scoped to tenant id, with the policy skipped: the
// tenant tests check the scope alone.
func tenant(id any) context.Context {
	return velox.WithTenant(velox.WithoutPolicy(context.Background(), "tenant tests"), id)
}

func tagNames(t *testing.T, m velox.Many[fixture.DTag]) []string {
	t.Helper()
	tags, ok := m.Get()
	if !ok || tags == nil {
		t.Fatalf("tags not loaded: %v %v", tags, ok)
	}
	names := []string{}
	for _, g := range tags {
		names = append(names, g.Name)
	}
	return names
}

func touch(ctx context.Context, tx *velox.DB) error {
	_, err := tx.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.ID.EQ(1)).Exec(ctx)
	return err
}
