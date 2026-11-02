package apitest

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// shardSet is a DB on two shards: eu, a primary and a replica, and us, a
// primary alone. Its WithShardFor maps org 1 to eu, 2 to us and 3 to
// "mars", which is no shard, and fails for any other org.
type shardSet struct {
	euP, euR, usP *fakeconn.Exec
	db            *velox.DB
	lookups       atomic.Int32 // the WithShardFor calls
	mu            sync.Mutex
	routes        []velox.Route
}

var errShardLookup = errors.New("the org is being moved")

// openShards opens a shardSet; hooked adds an Around hook that records each
// statement's Route once it ran, a fallback included, so the statements take
// run's path instead of plain's.
func openShards(hooked bool, extra ...velox.Option) *shardSet {
	s := &shardSet{euP: &fakeconn.Exec{}, euR: &fakeconn.Exec{}, usP: &fakeconn.Exec{}}
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
		"eu": {Primary: fakeConn(s.euP), Replicas: []velox.Conn{fakeConn(s.euR)}},
		"us": {Primary: fakeConn(s.usP)},
	}, opts...)
	return s
}

func (s *shardSet) shardFor(ctx context.Context) (string, error) {
	s.lookups.Add(1)
	org, _ := testmodel.Org.From(ctx)
	switch org {
	case 1:
		return "eu", nil
	case 2:
		return "us", nil
	case 3:
		return "mars", nil
	case 0:
		return "", nil // the context has no org: it names no shard
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
	bg        = context.Background()
	euPrimary = velox.Route{Shard: "eu", Replica: -1}
	euR0      = velox.Route{Shard: "eu", Replica: 0}
	usPrimary = velox.Route{Shard: "us", Replica: -1}
)

// shardWork runs, under sctx, a read of invoices and one of items, an
// update of items and an insert of an invoice: Item has no org column.
func shardWork(t *testing.T, sctx context.Context, db *velox.DB) {
	t.Helper()
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(sctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Query[testmodel.Item]().Limit(1).All(sctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Update[testmodel.Item]().Set(It.Qty.Set(2)).Where(It.ID.EQ(1)).Exec(sctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Insert(&testmodel.Invoice{Number: "N"}).Exec(sctx); err != nil {
		t.Fatal(err)
	}
}

// TestShardFromOrg: a statement runs on the shard WithShardFor gives for
// the context's org, and on no other.
func TestShardFromOrg(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := openShards(hooked)
		s.euR.Results = [][][]any{{invoiceRow(1)}, {itemRow(1, 1, "A", 1)}}
		s.euP.Rows = [][]any{{int64(42)}} // the key the insert reads back
		shardWork(t, testmodel.Org.With(bg, int64(1)), s.db)
		s.check(t, hooked, [3]int{2, 2, 0}, euR0, euR0, euPrimary, euPrimary)

		s = openShards(hooked)
		s.usP.Results = [][][]any{{invoiceRow(1)}, {itemRow(1, 1, "A", 1)}, {{int64(42)}}}
		shardWork(t, testmodel.Org.With(bg, int64(2)), s.db)
		s.check(t, hooked, [3]int{0, 0, 4}, usPrimary, usPrimary, usPrimary, usPrimary)
	})
}

// TestShardWithLoads: a query's With loads run where the query ran, on its
// shard's replica, without asking WithShardFor again.
func TestShardWithLoads(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := openShards(hooked)
		s.euR.Results = [][][]any{{invoiceRow(1)}, {itemRow(10, 1, "A", 1)}}
		invs, err := s.db.Query[testmodel.Invoice]().With(I.Items).Limit(10).All(testmodel.Org.With(bg, int64(1)))
		if err != nil {
			t.Fatal(err)
		}
		if items, ok := invs[0].Items.Get(); !ok || len(items) != 1 {
			t.Errorf("items %v, loaded %v", items, ok)
		}
		s.check(t, hooked, [3]int{0, 2, 0}, euR0, euR0)
		if n := s.lookups.Load(); n != 1 {
			t.Errorf("WithShardFor called %d times, want 1", n)
		}
	})
}

// TestShardIterCountExists: Iter, Count and Exists run on the org's shard,
// a read on its replica when it has one, and ask WithShardFor once each; a
// org on no shard is refused.
func TestShardIterCountExists(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := openShards(hooked)
		s.usP.Results = [][][]any{{invoiceRow(1), invoiceRow(2)}, {{int64(2)}}, {{int64(1)}}}
		uctx := testmodel.Org.With(bg, int64(2))
		rows := 0
		for _, err := range s.db.Query[testmodel.Invoice]().Limit(5).Iter(uctx) {
			if err != nil {
				t.Fatal(err)
			}
			rows++
		}
		if rows != 2 {
			t.Errorf("Iter yielded %d rows, want 2", rows)
		}
		if n, err := s.db.Query[testmodel.Invoice]().Count(uctx); err != nil || n != 2 {
			t.Errorf("Count on us: %d, %v, want 2", n, err)
		}
		if ok, err := s.db.Query[testmodel.Invoice]().Exists(uctx); err != nil || !ok {
			t.Errorf("Exists on us: %v, %v, want true", ok, err)
		}
		s.check(t, hooked, [3]int{0, 0, 3}, usPrimary, usPrimary, usPrimary)
		if n := s.lookups.Load(); n != 3 {
			t.Errorf("WithShardFor called %d times, want 3", n)
		}

		s = openShards(hooked)
		s.euR.Results = [][][]any{{invoiceRow(1)}, {{int64(3)}}, {{int64(1)}}}
		ectx := testmodel.Org.With(bg, int64(1))
		for _, err := range s.db.Query[testmodel.Invoice]().Limit(5).Iter(ectx) {
			if err != nil {
				t.Fatal(err)
			}
		}
		if n, err := s.db.Query[testmodel.Invoice]().Count(ectx); err != nil || n != 3 {
			t.Errorf("Count on eu: %d, %v, want 3", n, err)
		}
		if ok, err := s.db.Query[testmodel.Invoice]().Exists(ectx); err != nil || !ok {
			t.Errorf("Exists on eu: %v, %v, want true", ok, err)
		}
		s.check(t, hooked, [3]int{0, 3, 0}, euR0, euR0, euR0)

		s = openShards(hooked)
		_, err := s.db.Query[testmodel.Invoice]().Count(testmodel.Org.With(bg, int64(3)))
		const want = `velox: testmodel.Invoice: no shard for the statement: WithShardFor gave "mars", which is not a shard of the DB`
		if !errors.Is(err, velox.ErrNoShard) || err.Error() != want {
			t.Errorf("Count on org 3: %v, want ErrNoShard %q", err, want)
		}
		s.check(t, hooked, [3]int{0, 0, 0})
	})
}

// TestShardFailoverStaysInShard: a replica's failed read falls over to its
// own shard's primary.
func TestShardFailoverStaysInShard(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := openShards(hooked)
		s.euR.Err = driver.ErrBadConn
		s.euP.Rows = [][]any{invoiceRow(1)}
		if _, err := s.db.Query[testmodel.Invoice]().Limit(1).All(testmodel.Org.With(bg, int64(1))); err != nil {
			t.Fatal(err)
		}
		s.check(t, hooked, [3]int{1, 1, 0}, velox.Route{Shard: "eu", Replica: -1, Fallback: true})
	})
}

// TestWithShardWins: WithShard names the shard whatever the org, and is how
// a statement on an entity with no org column finds its shard.
func TestWithShardWins(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := openShards(hooked)
		s.usP.Rows = [][]any{invoiceRow(1)}
		if _, err := s.db.Query[testmodel.Invoice]().Limit(1).All(velox.WithShard(testmodel.Org.With(bg, int64(1)), "us")); err != nil {
			t.Fatal(err)
		}
		// WithShard on the outer context, under a later Org.With, still
		// wins: the marks and the org are separate.
		if _, err := s.db.Query[testmodel.Invoice]().Limit(1).All(testmodel.Org.With(velox.WithShard(bg, "us"), int64(1))); err != nil {
			t.Fatal(err)
		}
		s.euR.Rows = [][]any{itemRow(1, 1, "A", 1)}
		if _, err := s.db.Query[testmodel.Item]().Limit(1).All(velox.WithShard(bg, "eu")); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Delete[testmodel.Item]().Where(It.ID.EQ(1)).Exec(velox.WithShard(bg, "us")); err != nil {
			t.Fatal(err)
		}
		// A later WithShard replaces an earlier one, and keeps the other marks.
		pctx := velox.WithShard(velox.WithShard(velox.WithPrimaryReads(bg), "us"), "eu")
		if _, err := s.db.Query[testmodel.Item]().Limit(1).All(pctx); err != nil {
			t.Fatal(err)
		}
		s.check(t, hooked, [3]int{1, 1, 3}, usPrimary, usPrimary, euR0, usPrimary, euPrimary)
		if n := s.lookups.Load(); n != 0 {
			t.Errorf("WithShardFor called %d times under WithShard, want 0", n)
		}
	})
}

// TestNoShard: a statement whose shard cannot be determined is refused, and
// nothing is sent.
func TestNoShard(t *testing.T) {
	cases := []struct {
		name string
		ctx  context.Context
		want []string // in the error's text, after the prefix
		is   error    // matched besides ErrNoShard
	}{
		{"no scope value and no WithShard", bg, nil, nil},
		{"WithoutPolicy without WithShard", velox.WithoutPolicy(bg, "report"), nil, nil},
		{"an unknown shard from WithShardFor", testmodel.Org.With(bg, int64(3)), []string{`"mars"`}, nil},
		{"an unknown shard from WithShard", velox.WithShard(testmodel.Org.With(bg, int64(1)), "nope"), []string{`"nope"`}, nil},
		{"WithShard with no name", velox.WithShard(testmodel.Org.With(bg, int64(1)), ""), []string{`""`}, nil},
		{"WithShardFor's error", testmodel.Org.With(bg, int64(4)), []string{errShardLookup.Error()}, errShardLookup},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bothPaths(t, func(t *testing.T, hooked bool) {
				s := openShards(hooked)
				for _, e := range []*fakeconn.Exec{s.euP, s.euR, s.usP} {
					e.Rows = [][]any{itemRow(1, 1, "A", 1)}
				}
				checkErr := func(what string, err error) {
					t.Helper()
					prefix := "velox: testmodel.Item: no shard for the statement: "
					switch {
					case !errors.Is(err, velox.ErrNoShard):
						t.Errorf("%s: %v, want ErrNoShard", what, err)
					case !strings.HasPrefix(err.Error(), prefix):
						t.Errorf("%s: %q, want it to start %q", what, err, prefix)
					case c.is != nil && !errors.Is(err, c.is):
						t.Errorf("%s: %v does not wrap %v", what, err, c.is)
					}
					for _, w := range c.want {
						if err != nil && !strings.Contains(err.Error(), w) {
							t.Errorf("%s: %q does not say %s", what, err, w)
						}
					}
				}
				_, err := s.db.Query[testmodel.Item]().Limit(1).All(c.ctx)
				checkErr("All", err)
				_, err = s.db.Query[testmodel.Item]().Where(It.ID.EQ(1)).One(c.ctx)
				checkErr("One", err)
				_, err = s.db.Update[testmodel.Item]().Set(It.Qty.Set(2)).Where(It.ID.EQ(1)).Exec(c.ctx)
				checkErr("Update", err)
				err = s.db.Insert(&testmodel.Item{ID: 1, InvoiceID: 1, SKU: "A"}).Exec(c.ctx)
				checkErr("Insert", err)
				s.check(t, hooked, [3]int{0, 0, 0})
			})
		})
	}
}

// TestTxPinsItsShard pins that a transaction runs on the shard its
// context names at begin, and refuses a statement for another.
func TestTxPinsItsShard(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := openShards(hooked)
		s.euP.Rows = [][]any{itemRow(1, 1, "A", 1)}
		tctx := testmodel.Org.With(bg, int64(1))
		var mismatch, lookup error
		err := s.db.Tx(tctx, func(ctx context.Context, tx *velox.DB) error {
			for _, sctx := range []context.Context{tctx, bg, velox.WithoutPolicy(bg, "report"), velox.WithShard(bg, "eu")} {
				if _, err := tx.Query[testmodel.Item]().Limit(1).All(sctx); err != nil {
					return err
				}
			}
			if _, err := tx.Update[testmodel.Item]().Set(It.Qty.Set(2)).Where(It.ID.EQ(1)).Exec(bg); err != nil {
				return err
			}
			_, mismatch = tx.Query[testmodel.Item]().Limit(1).All(velox.WithShard(tctx, "us"))
			_, lookup = tx.Query[testmodel.Item]().Limit(1).All(testmodel.Org.With(bg, int64(4)))
			return tx.Tx(bg, func(ctx context.Context, tx *velox.DB) error { // a savepoint, on eu too
				_, err := tx.Query[testmodel.Item]().Limit(1).All(bg)
				return err
			})
		})
		if err != nil {
			t.Fatal(err)
		}
		want := `velox: testmodel.Item: statement for shard "us" in a transaction on shard "eu"`
		if mismatch == nil || mismatch.Error() != want || errors.Is(mismatch, velox.ErrNoShard) {
			t.Errorf("a statement for us in a transaction on eu: %v, want %q, not ErrNoShard", mismatch, want)
		}
		if !errors.Is(lookup, errShardLookup) {
			t.Errorf("a statement whose WithShardFor failed: %v, want its error", lookup)
		}
		// BEGIN, 4 SELECT, UPDATE, SAVEPOINT, SELECT, RELEASE, COMMIT
		if s.euP.Log[0] != "BEGIN" || s.euP.Log[len(s.euP.Log)-1] != "COMMIT" || len(s.euP.Log) != 10 {
			t.Errorf("eu's primary was sent %q", s.euP.Log)
		}
		s.check(t, hooked, [3]int{8, 0, 0}, euPrimary, euPrimary, euPrimary, euPrimary, euPrimary, euPrimary)

		// A transaction WithShard names begins on that shard.
		s = openShards(hooked)
		if err := s.db.Tx(velox.WithShard(bg, "us"), func(ctx context.Context, tx *velox.DB) error {
			_, err := tx.Delete[testmodel.Item]().Where(It.ID.EQ(1)).Exec(bg)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(s.usP.Log, []string{"BEGIN", s.usP.SQL, "COMMIT"}) {
			t.Errorf("us's primary was sent %q", s.usP.Log)
		}
		s.check(t, hooked, [3]int{0, 0, 1}, usPrimary)
	})
}

// TestTxWithNoShard: a transaction whose context names no shard is refused
// before it begins.
func TestTxWithNoShard(t *testing.T) {
	for name, c := range map[string]struct {
		ctx  context.Context
		want string
		is   error
	}{
		"no org":                          {bg, "", nil},
		"unknown shard":                   {testmodel.Org.With(bg, int64(3)), `"mars"`, nil},
		"an unknown WithShard":            {velox.WithShard(bg, "nope"), `"nope"`, nil},
		"WithoutPolicy without WithShard": {velox.WithoutPolicy(bg, "report"), "", nil},
		"a failed lookup":                 {testmodel.Org.With(bg, int64(4)), "", errShardLookup},
	} {
		t.Run(name, func(t *testing.T) {
			hooks := 0
			s := openShards(false, velox.WithTxAround(func(ctx context.Context, _ *velox.TxInfo, next func(context.Context) error) error {
				hooks++
				return next(ctx)
			}))
			ran := false
			err := s.db.Tx(c.ctx, func(context.Context, *velox.DB) error { ran = true; return nil })
			if hooks != 0 {
				t.Errorf("%d TxAround hooks ran; a refused Tx runs none", hooks)
			}
			prefix := "velox: transaction: no shard for the statement: "
			if !errors.Is(err, velox.ErrNoShard) || !strings.HasPrefix(err.Error(), prefix) || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Tx: %v, want ErrNoShard as %q…%s", err, prefix, c.want)
			}
			if c.is != nil && !errors.Is(err, c.is) {
				t.Errorf("Tx: %v does not wrap %v", err, c.is)
			}
			if ran {
				t.Error("fn ran")
			}
			if n := len(s.euP.Log) + len(s.euR.Log) + len(s.usP.Log); n != 0 {
				t.Errorf("%d statements sent", n)
			}
		})
	}
}

// TestSQLiteFilePerShard: two SQLite files as two shards; each org's rows
// are in its own file only.
func TestSQLiteFilePerShard(t *testing.T) {
	dir := t.TempDir()
	open := func(name string) *sql.DB {
		t.Helper()
		raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, name+".db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = raw.Close() })
		if _, err := raw.Exec(`CREATE TABLE invoices (created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, id INTEGER PRIMARY KEY,
			org_id INTEGER NOT NULL, number TEXT NOT NULL, total REAL NOT NULL, paid BOOLEAN NOT NULL,
			paid_at DATETIME, note TEXT, data BLOB NOT NULL, CONSTRAINT invoices_org_number_uq UNIQUE (org_id, number))`); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	eu, us := open("eu"), open("us")
	db := velox.OpenShards(velox.SQLite, map[string]velox.Shard{
		"eu": {Primary: velox.SQLConn(eu)},
		"us": {Primary: velox.SQLConn(us)},
	}, velox.WithShardFor(func(ctx context.Context) (string, error) {
		org, _ := testmodel.Org.From(ctx)
		switch org {
		case 1:
			return "eu", nil
		case 2:
			return "us", nil
		}
		return "", errShardLookup
	}))
	for org, numbers := range map[int64][]string{1: {"E1", "E2"}, 2: {"U1"}} {
		tctx := testmodel.Org.With(bg, org)
		for _, n := range numbers {
			if err := db.Insert(&testmodel.Invoice{Number: n, Data: []byte("d")}).Exec(tctx); err != nil {
				t.Fatal(err)
			}
		}
		got, err := db.Query[testmodel.Invoice]().OrderBy(I.Number.Asc()).Limit(10).All(tctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(numbers) {
			t.Errorf("org %d read %d invoices, want %d", org, len(got), len(numbers))
		}
	}
	for name, c := range map[string]struct {
		raw  *sql.DB
		want []string
	}{"eu": {eu, []string{"1 E1", "1 E2"}}, "us": {us, []string{"2 U1"}}} {
		rows, err := c.raw.Query(`SELECT org_id, number FROM invoices ORDER BY number`)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for rows.Next() {
			var org int64
			var n string
			if err := rows.Scan(&org, &n); err != nil {
				t.Fatal(err)
			}
			got = append(got, fmt.Sprintf("%d %s", org, n))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		_ = rows.Close()
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s's file holds %q, want %q", name, got, c.want)
		}
	}
}

// TestSavepointChecksItsShard: a nested Tx whose context names another shard,
// or one that is not a shard, is refused before SAVEPOINT; one whose context
// names none runs on the transaction's.
func TestSavepointChecksItsShard(t *testing.T) {
	s := openShards(false)
	var other, unknown error
	ran, plain := false, false
	err := s.db.Tx(velox.WithShard(bg, "eu"), func(ctx context.Context, tx *velox.DB) error {
		other = tx.Tx(velox.WithShard(ctx, "us"), func(context.Context, *velox.DB) error { ran = true; return nil })
		unknown = tx.Tx(velox.WithShard(ctx, "zz"), func(context.Context, *velox.DB) error { ran = true; return nil })
		return tx.Tx(bg, func(context.Context, *velox.DB) error { plain = true; return nil })
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := `velox: savepoint: for shard "us" in a transaction on shard "eu"`; fmt.Sprint(other) != want {
		t.Errorf("a savepoint for us: %v, want %q", other, want)
	}
	if !errors.Is(unknown, velox.ErrNoShard) {
		t.Errorf("a savepoint for zz: %v, want ErrNoShard", unknown)
	}
	if ran || !plain {
		t.Errorf("refused fn ran: %v; fn under no shard ran: %v", ran, plain)
	}
	// BEGIN, the third's SAVEPOINT and RELEASE, COMMIT
	if want := []string{"BEGIN", "SAVEPOINT velox_1", "RELEASE SAVEPOINT velox_1", "COMMIT"}; !reflect.DeepEqual(s.euP.Log, want) {
		t.Errorf("eu's primary was sent %q, want %q", s.euP.Log, want)
	}
}

// TestShardForCallsPerWrite: one write can call WithShardFor more than once.
// An Insert asks once. An InsertAll split into two statements asks three
// times, for its transaction and for each statement. A Tx asks when it
// begins and for each statement in it.
func TestShardForCallsPerWrite(t *testing.T) {
	s := openShards(false)
	tctx := testmodel.Org.With(bg, int64(1))
	s.euP.Rows = [][]any{{int64(1)}}

	reset := func() {
		s.lookups.Store(0)
		s.euP.Calls = 0
	}
	check := func(name string, lookups int32, statements int) {
		t.Helper()
		if n := s.lookups.Load(); n != lookups {
			t.Errorf("%s: WithShardFor called %d times, want %d", name, n, lookups)
		}
		if s.euP.Calls != statements {
			t.Errorf("%s: %d statements, want %d", name, s.euP.Calls, statements)
		}
	}

	reset()
	if err := s.db.Insert(&testmodel.Invoice{Number: "N"}).Exec(tctx); err != nil {
		t.Fatal(err)
	}
	check("Insert", 1, 1)

	reset()
	rows := make([]testmodel.Invoice, 8000)
	for i := range rows {
		rows[i] = testmodel.Invoice{Number: "N", Data: []byte{}}
	}
	if err := s.db.InsertAll(rows).Exec(tctx); err != nil {
		t.Fatal(err)
	}
	check("InsertAll of 8000", 3, 2)

	reset()
	err := s.db.Tx(tctx, func(ctx context.Context, tx *velox.DB) error {
		return tx.Insert(&testmodel.Invoice{Number: "N"}).Exec(ctx)
	})
	if err != nil {
		t.Fatal(err)
	}
	check("Tx with one insert", 2, 1)
}
