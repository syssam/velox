package velox_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

// rawRecorder is an Around hook that keeps a copy of each statement it sees.
type rawRecorder struct {
	mu  sync.Mutex
	sts []velox.Statement
}

func (r *rawRecorder) around(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
	err := next(ctx)
	r.mu.Lock()
	r.sts = append(r.sts, *st)
	r.mu.Unlock()
	return err
}

// TestRawNeedsAReason: Raw and Exec with a reason that is empty or only white
// space fail before anything runs: no hook sees them and nothing is sent.
func TestRawNeedsAReason(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{int64(1)}}, Cols: []string{"id"}, Affected: 1}
	rec := &rawRecorder{}
	db := velox.OpenFake(velox.Postgres, e, velox.WithAround(rec.around))
	for _, reason := range []string{"", " \t\n"} {
		q := db.Raw[testmodel.Invoice](reason, "SELECT id FROM invoices")
		check := func(what string, err error) {
			t.Helper()
			if err == nil || !strings.Contains(err.Error(), "reason") || !strings.HasPrefix(err.Error(), "velox: ") {
				t.Errorf("%s with the reason %q: %v", what, reason, err)
			}
		}
		_, err := q.All(ctx)
		check("All", err)
		_, err = q.One(ctx)
		check("One", err)
		for _, err := range q.Iter(ctx) {
			check("Iter", err)
		}
		_, err = db.Exec(ctx, reason, "DELETE FROM invoices")
		check("Exec", err)
	}
	if e.Calls != 0 || len(rec.sts) != 0 {
		t.Errorf("%d statements sent, %d seen by the hook; want none", e.Calls, len(rec.sts))
	}
}

// TestRawScansByName: the rows are scanned into E's fields by column name,
// in any order, with codecs decoding; the columns not returned stay zero.
// The SQL and the arguments are sent as given.
func TestRawScansByName(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		e := &velox.FakeExec{
			Cols: []string{"number", "id", "paid_at", "total"},
			Rows: [][]any{{"INV-7", int64(7), t0, 99.5}, {"INV-8", int64(8), nil, 1.0}},
		}
		var opts []velox.Option
		if hooked {
			opts = append(opts, velox.WithAround((&rawRecorder{}).around))
		}
		db := velox.OpenFake(velox.Postgres, e, opts...)
		const q = "SELECT number, id, paid_at, total FROM invoices WHERE org_id = $1 FOR UPDATE"
		got, err := db.Raw[testmodel.Invoice]("locks the rows", q, int64(1)).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].ID != 7 || got[0].Number != "INV-7" || got[0].Total != 99.5 ||
			got[0].PaidAt == nil || !got[0].PaidAt.Equal(t0) || got[1].PaidAt != nil || got[1].ID != 8 {
			t.Fatalf("%+v", got)
		}
		if got[0].OrgID != 0 || !got[0].CreatedAt.IsZero() || got[0].Note != nil || got[0].Data != nil {
			t.Errorf("a column not returned is not zero: %+v", got[0])
		}
		if e.SQL != q || !reflect.DeepEqual(e.Args, []any{int64(1)}) {
			t.Errorf("sent %q %v, want the caller's SQL and arguments", e.SQL, e.Args)
		}

		one, err := db.Raw[testmodel.Invoice]("r", q, int64(1)).One(ctx)
		if !errors.Is(err, velox.ErrNotSingular) {
			t.Errorf("One of two rows: %+v %v", one, err)
		}
		e.Rows = e.Rows[:1]
		if one, err = db.Raw[testmodel.Invoice]("r", q, int64(1)).One(ctx); err != nil || one.ID != 7 {
			t.Errorf("One: %+v %v", one, err)
		}
		e.Rows = nil
		if _, err = db.Raw[testmodel.Invoice]("r", q, int64(1)).One(ctx); !errors.Is(err, velox.ErrNotFound) ||
			!strings.Contains(err.Error(), "testmodel.Invoice") {
			t.Errorf("One of no rows: %v", err)
		}

		e.Rows = [][]any{{"A", int64(1), nil, 1.0}, {"B", int64(2), nil, 2.0}, {"C", int64(3), nil, 3.0}}
		var ids []int64
		for x, err := range db.Raw[testmodel.Invoice]("r", q, int64(1)).Iter(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, x.ID)
			if x.ID == 2 {
				break
			}
		}
		if !slices.Equal(ids, []int64{1, 2}) {
			t.Errorf("Iter: %v", ids)
		}

		// An encoded column is decoded; NULL leaves a nullable one nil.
		e.Cols = []string{"id", "tags", "shape", "nums"}
		e.Rows = [][]any{{int64(1), []byte(`["a","b"]`), `{"w":3,"h":4}`, nil}}
		docs, err := db.Raw[jDoc]("r", "SELECT id, tags, shape, nums FROM j_docs").All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(docs) != 1 || !slices.Equal(docs[0].Tags, []string{"a", "b"}) || docs[0].Shape != (jShape{3, 4}) || docs[0].Nums != nil {
			t.Errorf("%+v", docs)
		}
	})
}

// TestRawUnknownColumn: a result column that E has no field for is an error
// naming every such column; so is a column returned more than once, named
// once however often and wherever it repeats.
func TestRawUnknownColumn(t *testing.T) {
	e := &velox.FakeExec{Cols: []string{"id", "revenue", "number", "extra"}, Rows: [][]any{{int64(1), 5.0, "N", 1}}}
	db := velox.OpenFake(velox.Postgres, e)
	_, err := db.Raw[testmodel.Invoice]("r", "SELECT …").All(ctx)
	if err == nil || !strings.Contains(err.Error(), `"revenue"`) || !strings.Contains(err.Error(), `"extra"`) ||
		!strings.HasPrefix(err.Error(), "velox: testmodel.Invoice: ") {
		t.Errorf("unknown columns: %v", err)
	}
	e.Cols = []string{"id", "number", "id"}
	_, err = db.Raw[testmodel.Invoice]("r", "SELECT …").All(ctx)
	if want := `velox: testmodel.Invoice: the raw SQL returned "id" more than once`; err == nil || err.Error() != want {
		t.Errorf("a column twice: %v, want %s", err, want)
	}
	e.Cols = []string{"id", "number", "id", "number", "id"}
	_, err = db.Raw[testmodel.Invoice]("r", "SELECT …").All(ctx)
	if want := `velox: testmodel.Invoice: the raw SQL returned "id", "number" more than once`; err == nil || err.Error() != want {
		t.Errorf("columns repeated apart: %v, want %s", err, want)
	}
	for _, err := range db.Raw[testmodel.Invoice]("r", "SELECT …").Iter(ctx) {
		if err == nil || !strings.Contains(err.Error(), "more than once") {
			t.Errorf("Iter, a column twice: %v", err)
		}
	}
}

// TestRawRoutes: Exec is a write, so it goes to the primary and pins the
// session; a Raw read goes to a replica, unless ReadPrimary or a pinned
// session says otherwise; in a transaction both run on it.
func TestRawRoutes(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := openReplicas(hooked)
		for _, e := range []*velox.FakeExec{s.p, s.r0, s.r1} {
			e.Rows, e.Cols, e.Affected = [][]any{{int64(1)}}, []string{"id"}, 3
		}
		raw := s.db.Raw[testmodel.Invoice]("r", "SELECT id FROM invoices")
		if _, err := raw.All(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := raw.All(velox.ReadPrimary(ctx)); err != nil {
			t.Fatal(err)
		}
		sctx := velox.WithSession(ctx)
		n, err := s.db.Exec(sctx, "r", "UPDATE invoices SET paid = true")
		if err != nil || n != 3 {
			t.Fatalf("Exec: %d %v", n, err)
		}
		if velox.PinnedUntil(sctx).IsZero() {
			t.Error("Exec did not pin the session")
		}
		if _, err := raw.One(sctx); err != nil { // the pinned session reads the primary
			t.Fatal(err)
		}
		if _, err := raw.One(ctx); err != nil {
			t.Fatal(err)
		}
		s.check(t, hooked, [3]int{3, 1, 1}, onR0, onPrimary, onPrimary, onPrimary, onR1)

		before := s.calls()
		err = s.db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			if _, err := tx.Raw[testmodel.Invoice]("r", "SELECT id FROM invoices FOR UPDATE").All(ctx); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "r", "UPDATE invoices SET paid = true")
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := s.calls(); got != [3]int{before[0] + 2, before[1], before[2]} {
			t.Errorf("in a transaction: calls %v after %v", got, before)
		}
	})
}

// TestRawShowsReason pins Review Focus 4: Raw and Exec skip the tenant scope
// and the policy, so a context with no tenant runs them, unchanged; the
// Around hooks see the reason as Statement.TenantBypass (and PolicyBypass),
// and Raw's statement as OpRawQuery, Exec's as OpRawExec, before next runs
// them. Rewrite hooks, which rewrite what the builder made, do not see them.
func TestRawShowsReason(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{int64(1), "t"}}, Cols: []string{"id", "title"}, Affected: 2}
	rec := &rawRecorder{}
	rewrites := 0
	var before []velox.Op // the Op each statement had before next ran
	db := velox.OpenFake(velox.Postgres, e, velox.WithAround(rec.around),
		velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
			before = append(before, st.Op)
			return next(ctx)
		}),
		velox.WithRewrite(func(context.Context, *velox.Statement) error { rewrites++; return nil }))
	bare := context.Background() // neither a tenant nor a user
	if _, err := db.Query[testmodel.Ticket]().Limit(1).All(bare); !errors.Is(err, velox.ErrNoTenant) {
		t.Fatalf("the builder's read without a tenant: %v", err)
	}
	rewrites, before = 0, nil
	const q = "SELECT id, title FROM tickets WHERE id = $1"
	got, err := db.Raw[testmodel.Ticket]("support looks up a ticket", q, int64(1)).All(bare)
	if err != nil || len(got) != 1 || got[0].Title != "t" {
		t.Fatalf("Raw: %+v %v", got, err)
	}
	if e.SQL != q {
		t.Errorf("sent %q, want it unchanged", e.SQL)
	}
	n, err := db.Exec(bare, "nightly cleanup", "DELETE FROM tickets WHERE done")
	if err != nil || n != 2 {
		t.Fatalf("Exec: %d %v", n, err)
	}
	if rewrites != 0 {
		t.Errorf("Rewrite hooks ran %d times for raw SQL", rewrites)
	}
	if len(rec.sts) != 2 {
		t.Fatalf("the Around hook saw %d statements", len(rec.sts))
	}
	r, x := rec.sts[0], rec.sts[1]
	if !slices.Equal(before, []velox.Op{velox.OpRawQuery, velox.OpRawExec}) {
		t.Errorf("Ops before next: %v", before)
	}
	if r.Op != velox.OpRawQuery || r.TenantBypass != "support looks up a ticket" || r.PolicyBypass != r.TenantBypass ||
		r.Entity == nil || r.Entity.Name != "testmodel.Ticket" || r.SQL != q || !reflect.DeepEqual(r.Args, []any{int64(1)}) ||
		r.Fingerprint == "" || r.Result.Rows != 1 || r.Result.Affected != -1 || r.Tenant != nil {
		t.Errorf("Raw's statement: %+v", r)
	}
	if x.Op != velox.OpRawExec || x.TenantBypass != "nightly cleanup" || x.PolicyBypass != x.TenantBypass ||
		x.Entity == nil || x.Entity.Name == "" || x.SQL != "DELETE FROM tickets WHERE done" || x.Result.Affected != 2 {
		t.Errorf("Exec's statement: %+v", x)
	}
	if velox.OpRawQuery.String() != "raw query" || velox.OpRawExec.String() != "raw exec" {
		t.Errorf("String() = %q, %q", velox.OpRawQuery.String(), velox.OpRawExec.String())
	}
}

// TestRawPipeline: raw statements keep the rest of the pipeline: the dry-run
// rule (Exec is a write, Raw a read), ErrOutsideTx, the row cap and its
// MaxRows, deadlines and the SQL comment.
func TestRawPipeline(t *testing.T) {
	t.Run("dry run", func(t *testing.T) {
		e := &velox.FakeExec{Rows: [][]any{{int64(1), "ann"}}, Cols: []string{"id", "owner"}, Affected: 1}
		dry, db := dryContext(t, e)
		before := e.Calls
		if _, err := db.Exec(dry, "r", "DELETE FROM dnotes"); !errors.Is(err, velox.ErrDryRun) || !strings.HasPrefix(err.Error(), "velox: ") {
			t.Errorf("Exec under a dry run: %v", err)
		}
		if e.Calls != before {
			t.Errorf("Exec under a dry run sent %q", e.Log[len(e.Log)-1])
		}
		got, err := db.Raw[dnote]("r", "SELECT id, owner FROM dnotes").All(dry)
		if err != nil || len(got) != 1 || got[0].Owner != "ann" || e.Calls != before+1 {
			t.Errorf("Raw read under a dry run: %+v %v", got, err)
		}
	})
	t.Run("outside the transaction", func(t *testing.T) {
		e := &velox.FakeExec{Rows: [][]any{{int64(1)}}, Cols: []string{"id"}}
		db := velox.OpenFake(velox.Postgres, e)
		err := db.Tx(ctx, func(ctx context.Context, _ *velox.DB) error {
			if _, err := db.Exec(ctx, "r", "DELETE FROM invoices"); !errors.Is(err, velox.ErrOutsideTx) {
				t.Errorf("Exec on db in a transaction: %v", err)
			}
			if _, err := db.Raw[testmodel.Invoice]("r", "SELECT id FROM invoices").All(ctx); !errors.Is(err, velox.ErrOutsideTx) {
				t.Errorf("Raw on db in a transaction: %v", err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	t.Run("row cap", func(t *testing.T) {
		e := &velox.FakeExec{Rows: [][]any{{int64(1)}, {int64(2)}, {int64(3)}}, Cols: []string{"id"}}
		db := velox.OpenFake(velox.Postgres, e, velox.WithMaxRows(2))
		raw := db.Raw[testmodel.Invoice]("r", "SELECT id FROM invoices")
		if _, err := raw.All(ctx); !errors.Is(err, velox.ErrTooManyRows) {
			t.Errorf("All over the cap: %v", err)
		}
		var err error
		for _, err = range raw.Iter(ctx) {
		}
		if !errors.Is(err, velox.ErrTooManyRows) {
			t.Errorf("Iter over the cap: %v", err)
		}
		if got, err := raw.MaxRows(3).All(ctx); err != nil || len(got) != 3 {
			t.Errorf("MaxRows(3): %d rows, %v", len(got), err)
		}
		if e.SQL != "SELECT id FROM invoices" {
			t.Errorf("the cap changed the SQL: %q", e.SQL)
		}
	})
	t.Run("deadline", func(t *testing.T) {
		e := &velox.FakeExec{Rows: [][]any{{int64(1)}}, Cols: []string{"id"}}
		db := velox.OpenFake(velox.Postgres, e, velox.WithDeadlines(velox.Deadlines{Statement: time.Minute}))
		if _, err := db.Raw[testmodel.Invoice]("r", "SELECT id FROM invoices").All(ctx); err != nil {
			t.Fatal(err)
		}
		if e.Deadline.IsZero() {
			t.Error("Raw ran without the statement deadline")
		}
		e.Deadline = time.Time{}
		if _, err := db.Exec(ctx, "r", "DELETE FROM invoices"); err != nil {
			t.Fatal(err)
		}
		if e.Deadline.IsZero() {
			t.Error("Exec ran without the statement deadline")
		}
		blocked := &velox.FakeExec{Block: true}
		db = velox.OpenFake(velox.Postgres, blocked, velox.WithDeadlines(velox.Deadlines{Statement: 10 * time.Millisecond}))
		if _, err := db.Exec(ctx, "r", "DELETE FROM invoices"); !errors.Is(err, velox.ErrTimeout) || !strings.HasPrefix(err.Error(), "velox: ") {
			t.Errorf("Exec past its deadline: %v", err)
		}
	})
	t.Run("comment", func(t *testing.T) {
		e := &velox.FakeExec{Affected: 1}
		db := velox.OpenFake(velox.Postgres, e, velox.WithSQLComment("app"))
		// Trailing semicolons and white space go before the comment, which
		// would otherwise follow the statement's terminator.
		for _, sql := range []string{"DELETE FROM invoices", "DELETE FROM invoices;", "DELETE FROM invoices ;; \n\t"} {
			if _, err := db.Exec(ctx, "r", sql); err != nil {
				t.Fatal(err)
			}
			if e.SQL != "DELETE FROM invoices /*app='app'*/" {
				t.Errorf("%q sent as %q", sql, e.SQL)
			}
		}
		// Without a comment the SQL is sent as written.
		plain := velox.OpenFake(velox.Postgres, e)
		if _, err := plain.Exec(ctx, "r", "DELETE FROM invoices;"); err != nil || e.SQL != "DELETE FROM invoices;" {
			t.Errorf("sent %q, %v", e.SQL, err)
		}
	})
}

// TestRawOnShards: on a sharded DB, Raw and Exec run on the shard the context
// names, by OnShard or by its tenant through WithShardFor, though the tenant
// scopes nothing; with neither they fail with ErrNoShard and send nothing.
func TestRawOnShards(t *testing.T) {
	bothPaths(t, func(t *testing.T, hooked bool) {
		s := openShards(hooked)
		for _, e := range []*velox.FakeExec{s.euP, s.euR, s.usP} {
			e.Rows, e.Cols, e.Affected = [][]any{{int64(1)}}, []string{"id"}, 1
		}
		raw := s.db.Raw[testmodel.Invoice]("r", "SELECT id FROM invoices")
		eu := velox.WithTenant(bg, int64(1))
		if _, err := raw.All(eu); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(eu, "r", "DELETE FROM invoices WHERE paid"); err != nil {
			t.Fatal(err)
		}
		us := velox.OnShard(bg, "us")
		if _, err := raw.All(us); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(us, "r", "DELETE FROM invoices WHERE paid"); err != nil {
			t.Fatal(err)
		}
		if e := s.euP.Log[0]; e != "DELETE FROM invoices WHERE paid" {
			t.Errorf("eu's primary ran %q, want it unscoped", e)
		}
		for _, c := range []context.Context{bg, velox.AllTenants(bg, "every tenant")} {
			if _, err := raw.All(c); !errors.Is(err, velox.ErrNoShard) {
				t.Errorf("Raw with no shard: %v", err)
			}
			if _, err := s.db.Exec(c, "r", "DELETE FROM invoices"); !errors.Is(err, velox.ErrNoShard) {
				t.Errorf("Exec with no shard: %v", err)
			}
		}
		s.check(t, hooked, [3]int{1, 1, 2}, euR0, euPrimary, usPrimary, usPrimary)
	})
}
