package velox_test

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

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

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
func openShards(hooked bool) *shardSet {
	s := &shardSet{euP: &velox.FakeExec{}, euR: &velox.FakeExec{}, usP: &velox.FakeExec{}}
	opts := []velox.Option{velox.WithShardFor(s.shardFor)}
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
	bg        = context.Background()
	euPrimary = velox.Route{Shard: "eu", Replica: -1}
	euR0      = velox.Route{Shard: "eu", Replica: 0}
	usPrimary = velox.Route{Shard: "us", Replica: -1}
)

// shardWork runs, under sctx, a read of invoices and one of items, an
// update of items and an insert of an invoice: Item has no tenant column.
func shardWork(t *testing.T, db *velox.DB, sctx context.Context) {
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

// TestShardFromTenant: a statement runs on the shard WithShardFor gives for
// the context's tenant, and on no other.
func TestShardFromTenant(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := openShards(hooked)
		s.euR.Results = [][][]any{{invoiceRow(1)}, {itemRow(1, 1, "A", 1)}}
		s.euP.Rows = [][]any{{int64(42)}} // the key the insert reads back
		shardWork(t, s.db, velox.WithTenant(bg, int64(1)))
		s.check(t, hooked, [3]int{2, 2, 0}, euR0, euR0, euPrimary, euPrimary)

		s = openShards(hooked)
		s.usP.Results = [][][]any{{invoiceRow(1)}, {itemRow(1, 1, "A", 1)}, {{int64(42)}}}
		shardWork(t, s.db, velox.WithTenant(bg, int64(2)))
		s.check(t, hooked, [3]int{0, 0, 4}, usPrimary, usPrimary, usPrimary, usPrimary)
	})
}

// TestShardWithLoads: a query's With loads run where the query ran, on its
// shard's replica, without asking WithShardFor again.
func TestShardWithLoads(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := openShards(hooked)
		s.euR.Results = [][][]any{{invoiceRow(1)}, {itemRow(10, 1, "A", 1)}}
		invs, err := s.db.Query[testmodel.Invoice]().With(I.Items).Limit(10).All(velox.WithTenant(bg, int64(1)))
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

// TestShardFailoverStaysInShard: a replica's failed read falls over to its
// own shard's primary (spec §5: §4 applies within a shard).
func TestShardFailoverStaysInShard(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := openShards(hooked)
		s.euR.Err = driver.ErrBadConn
		s.euP.Rows = [][]any{invoiceRow(1)}
		if _, err := s.db.Query[testmodel.Invoice]().Limit(1).All(velox.WithTenant(bg, int64(1))); err != nil {
			t.Fatal(err)
		}
		s.check(t, hooked, [3]int{1, 1, 0}, velox.Route{Shard: "eu", Replica: -1, Fallback: true})
	})
}

// TestOnShardWins: OnShard names the shard whatever the tenant, and is how a
// statement on an entity with no tenant column finds its shard.
func TestOnShardWins(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := openShards(hooked)
		s.usP.Rows = [][]any{invoiceRow(1)}
		if _, err := s.db.Query[testmodel.Invoice]().Limit(1).All(velox.OnShard(velox.WithTenant(bg, int64(1)), "us")); err != nil {
			t.Fatal(err)
		}
		// OnShard on the outer context, under a later WithTenant, still
		// wins: the marks and the tenant are separate.
		if _, err := s.db.Query[testmodel.Invoice]().Limit(1).All(velox.WithTenant(velox.OnShard(bg, "us"), int64(1))); err != nil {
			t.Fatal(err)
		}
		s.euR.Rows = [][]any{itemRow(1, 1, "A", 1)}
		if _, err := s.db.Query[testmodel.Item]().Limit(1).All(velox.OnShard(bg, "eu")); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Delete[testmodel.Item]().Where(It.ID.EQ(1)).Exec(velox.OnShard(bg, "us")); err != nil {
			t.Fatal(err)
		}
		// A later OnShard replaces an earlier one, and keeps the other marks.
		pctx := velox.OnShard(velox.OnShard(velox.Primary(bg), "us"), "eu")
		if _, err := s.db.Query[testmodel.Item]().Limit(1).All(pctx); err != nil {
			t.Fatal(err)
		}
		s.check(t, hooked, [3]int{1, 1, 3}, usPrimary, usPrimary, euR0, usPrimary, euPrimary)
		if n := s.lookups.Load(); n != 0 {
			t.Errorf("WithShardFor called %d times under OnShard, want 0", n)
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
		{"no tenant and no OnShard", bg, nil, nil},
		{"AllTenants without OnShard", velox.AllTenants(bg, "report"), []string{"AllTenants"}, nil},
		{"an unknown shard from WithShardFor", velox.WithTenant(bg, int64(3)), []string{`"mars"`}, nil},
		{"an unknown shard from OnShard", velox.OnShard(velox.WithTenant(bg, int64(1)), "nope"), []string{`"nope"`}, nil},
		{"OnShard with no name", velox.OnShard(velox.WithTenant(bg, int64(1)), ""), []string{`""`}, nil},
		{"WithShardFor's error", velox.WithTenant(bg, int64(4)), []string{errShardLookup.Error()}, errShardLookup},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bothPaths(t, func(t *testing.T, hooked bool) {
				s := openShards(hooked)
				for _, e := range []*velox.FakeExec{s.euP, s.euR, s.usP} {
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

// TestTxPinsItsShard pins Ruling 8: a transaction runs on the shard its
// context names at begin, and refuses a statement for another.
func TestTxPinsItsShard(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := openShards(hooked)
		s.euP.Rows = [][]any{itemRow(1, 1, "A", 1)}
		tctx := velox.WithTenant(bg, int64(1))
		var mismatch, lookup error
		err := s.db.Tx(tctx, func(tx *velox.DB) error {
			for _, sctx := range []context.Context{tctx, bg, velox.AllTenants(bg, "report"), velox.OnShard(bg, "eu")} {
				if _, err := tx.Query[testmodel.Item]().Limit(1).All(sctx); err != nil {
					return err
				}
			}
			if _, err := tx.Update[testmodel.Item]().Set(It.Qty.Set(2)).Where(It.ID.EQ(1)).Exec(bg); err != nil {
				return err
			}
			_, mismatch = tx.Query[testmodel.Item]().Limit(1).All(velox.OnShard(tctx, "us"))
			_, lookup = tx.Query[testmodel.Item]().Limit(1).All(velox.WithTenant(bg, int64(4)))
			return tx.Tx(bg, func(tx *velox.DB) error { // a savepoint, on eu too
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

		// A transaction OnShard names begins on that shard.
		s = openShards(hooked)
		if err := s.db.Tx(velox.OnShard(bg, "us"), func(tx *velox.DB) error {
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
		"no tenant":       {bg, "", nil},
		"unknown shard":   {velox.WithTenant(bg, int64(3)), `"mars"`, nil},
		"a failed lookup": {velox.WithTenant(bg, int64(4)), "", errShardLookup},
	} {
		t.Run(name, func(t *testing.T) {
			s := openShards(false)
			ran := false
			err := s.db.Tx(c.ctx, func(*velox.DB) error { ran = true; return nil })
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

// TestDryRunIsRouted pins Ruling 9 and Review Focus 5: SQL(ctx) refuses a
// statement with no shard as a run would, calling WithShardFor once.
func TestDryRunIsRouted(t *testing.T) {
	s := openShards(true)
	q := s.db.Query[testmodel.Item]().Limit(1)
	for _, c := range []struct {
		ctx     context.Context
		lookups int32
	}{{bg, 0}, {velox.WithTenant(bg, int64(3)), 1}, {velox.WithTenant(bg, int64(4)), 1}} {
		s.lookups.Store(0)
		if _, _, err := q.SQL(c.ctx); !errors.Is(err, velox.ErrNoShard) {
			t.Errorf("Query.SQL: %v, want ErrNoShard", err)
		}
		if _, _, err := s.db.Update[testmodel.Item]().Set(It.Qty.Set(2)).Where(It.ID.EQ(1)).SQL(c.ctx); !errors.Is(err, velox.ErrNoShard) {
			t.Errorf("Update.SQL: %v, want ErrNoShard", err)
		}
		if _, _, err := s.db.Insert(&testmodel.Item{ID: 1}).SQL(c.ctx); !errors.Is(err, velox.ErrNoShard) {
			t.Errorf("Insert.SQL: %v, want ErrNoShard", err)
		}
		if n := s.lookups.Load(); n != 3*c.lookups {
			t.Errorf("WithShardFor called %d times for 3 dry runs, want %d", n, 3*c.lookups)
		}
	}

	tctx := velox.WithTenant(bg, int64(1))
	text, _, err := q.SQL(tctx)
	if err != nil || !strings.Contains(text, `FROM "items"`) {
		t.Fatalf("SQL with a tenant: %q, %v", text, err)
	}
	st := velox.Statement{Op: velox.OpSelect, Entity: velox.InfoOf[testmodel.Item](), Limit: 1}
	if _, _, err := velox.DryRun(s.db, tctx, &st); err != nil {
		t.Fatal(err)
	}
	s.euR.Rows = [][]any{itemRow(1, 1, "A", 1)}
	if _, err := q.All(tctx); err != nil {
		t.Fatal(err)
	}
	s.check(t, true, [3]int{0, 1, 0}, euR0)
	if st.Route != euR0 {
		t.Errorf("the dry run's Route %+v, want %+v as the run's", st.Route, euR0)
	}
}

// TestSQLiteFilePerShard: two SQLite files as two shards; each tenant's rows
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
	}, velox.WithShardFor(func(_ context.Context, tenant any) (string, error) {
		switch tenant {
		case int64(1):
			return "eu", nil
		case int64(2):
			return "us", nil
		}
		return "", errShardLookup
	}))
	for org, numbers := range map[int64][]string{1: {"E1", "E2"}, 2: {"U1"}} {
		tctx := velox.WithTenant(bg, org)
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
			t.Errorf("tenant %d read %d invoices, want %d", org, len(got), len(numbers))
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
