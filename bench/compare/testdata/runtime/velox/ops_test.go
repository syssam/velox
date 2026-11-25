package bench

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"example.com/tmp/gen"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/syssam/velox"
	"github.com/syssam/velox/veloxpgx"
)

const ormName = "velox"

var (
	//go:embed ddl_sqlite.sql
	ddlSQLite string
	//go:embed ddl_postgres.sql
	ddlPostgres string
)

// created is every row's created_at.
var created = time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)

type app struct {
	db    *velox.DB
	sqlDB *sql.DB // the tables' connection, for ANALYZE
	ids   []int64 // the E0s
	e1    int64   // the first E1
	e2    int64   // the seeded E2
	items []int64 // the E4s, by index: seedPages
	kids  []int64 // the E5s, by index: seedPages
	after *gen.E5 // PageKeyset's cursor, the last row of page 4
}

func newApp(tb testing.TB, sqlDB *sql.DB) *app {
	tb.Helper()
	d, ddl := velox.SQLite, ddlSQLite
	if dbDialect() == "postgres" {
		d, ddl = velox.Postgres, ddlPostgres
	}
	for _, st := range strings.Split(ddl, ";\n") {
		if strings.TrimSpace(st) == "" {
			continue
		}
		if _, err := sqlDB.Exec(st); err != nil {
			tb.Fatalf("%s: %v", st, err)
		}
	}
	if os.Getenv("COMPARE_VELOX_PGX") != "1" {
		return &app{db: velox.Open(d, sqlDB), sqlDB: sqlDB}
	}
	if d != velox.Postgres {
		tb.Fatal("COMPARE_VELOX_PGX needs postgres")
	}
	// The veloxpgx variant: the same app on a pgx pool of one connection, as
	// sqlDB has one. sqlDB stays open: setup and TestGuard use it for ANALYZE
	// and the schema checks.
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, pgDSN(tb)+"&pool_max_conns=1")
	if err != nil {
		// Not err: a parse error quotes the DSN.
		tb.Fatal("veloxpgx: pgxpool.New cannot parse $VELOX_COMPARE_POSTGRES with pool_max_conns=1")
	}
	tb.Cleanup(pool.Close)
	a := &app{db: veloxpgx.Open(pool), sqlDB: sqlDB}
	// The probe: an operation must acquire from the pool and leave sqlDB
	// untouched. With no idle connection kept, a query on sqlDB would open a
	// connection and close it on release, which MaxIdleClosed counts.
	sqlDB.SetMaxIdleConns(0)
	s0, n0 := sqlDB.Stats(), pool.Stat().AcquireCount()
	if _, err := a.count(ctx); err != nil {
		tb.Fatalf("veloxpgx: count: %v", err)
	}
	if n := pool.Stat().AcquireCount(); n <= n0 {
		tb.Fatalf("veloxpgx: count acquired no pool connection (acquires %d, then %d)", n0, n)
	}
	s1 := sqlDB.Stats()
	if s1.OpenConnections != s0.OpenConnections || s1.InUse != s0.InUse || s1.WaitCount != s0.WaitCount || s1.MaxIdleClosed != s0.MaxIdleClosed {
		tb.Fatalf("veloxpgx: count used database/sql: stats %+v, then %+v", s0, s1)
	}
	// Back to database/sql's default of 2, which SetMaxOpenConns(1) caps at 1:
	// the one idle connection the harness had before the probe.
	sqlDB.SetMaxIdleConns(2)
	return a
}

func (a *app) seed(ctx context.Context) error {
	// InsertAll reads no keys back: the keys are read by a query.
	e0s := make([]gen.E0, 100)
	for i := range e0s {
		e0s[i] = gen.E0{Name: fmt.Sprintf("p%d", i), CreatedAt: created, Status: gen.E0StatusDraft}
	}
	if err := a.db.InsertAll(e0s).Exec(ctx); err != nil {
		return err
	}
	ps, err := a.db.Query[gen.E0]().Limit(100).All(ctx)
	if err != nil {
		return err
	}
	a.ids = make([]int64, len(ps))
	for _, p := range ps {
		var i int
		if _, err := fmt.Sscanf(p.Name, "p%d", &i); err != nil || i < 0 || i >= len(ps) {
			return fmt.Errorf("E0 %d is named %q", p.ID, p.Name)
		}
		a.ids[i] = p.ID
	}
	e1s := make([]gen.E1, 0, 500)
	for i, id := range a.ids {
		for j := range 5 {
			c := 5*i + j
			e1s = append(e1s, gen.E1{Name: fmt.Sprintf("c%d", c), Count: c, Active: c%2 == 0, CreatedAt: created, Status: gen.E1StatusDraft, PrevID: id})
		}
	}
	if err := a.db.InsertAll(e1s).Exec(ctx); err != nil {
		return err
	}
	first, err := a.db.Query[gen.E1]().Where(gen.E1s.Count.EQ(0)).One(ctx)
	if err != nil {
		return err
	}
	a.e1 = first.ID
	e2 := gen.E2{Name: "s", CreatedAt: created, Status: gen.E2StatusDraft, PrevID: a.e1}
	if err := a.db.Insert(&e2).Exec(ctx); err != nil {
		return err
	}
	a.e2 = e2.ID
	return nil
}

func (a *app) insertOne(ctx context.Context) error {
	x := gen.E2{Name: "one", Count: 1, Active: true, CreatedAt: created, Status: gen.E2StatusDraft, PrevID: a.e1}
	return a.db.Insert(&x).Exec(ctx)
}

func (a *app) insertBulk(ctx context.Context) error {
	rows := make([]gen.E3, 100)
	for i := range rows {
		rows[i] = gen.E3{Name: "bulk", Count: i, Active: i%2 == 0, CreatedAt: created, Status: gen.E3StatusDraft, PrevID: a.e2}
	}
	return a.db.InsertAll(rows).Exec(ctx)
}

func (a *app) seededID(i int) int64 { return a.ids[i%100] }

func (a *app) get(ctx context.Context, i int) (int64, string, error) {
	x, err := a.db.Query[gen.E0]().Where(gen.E0s.ID.EQ(a.ids[i%100])).One(ctx)
	if err != nil {
		return 0, "", err
	}
	return x.ID, x.Name, nil
}

func (a *app) query(ctx context.Context) ([]int, error) {
	xs, err := a.db.Query[gen.E1]().Where(gen.E1s.Count.LT(450)).OrderBy(gen.E1s.Count.Desc()).Limit(100).All(ctx)
	if err != nil {
		return nil, err
	}
	counts := make([]int, len(xs))
	for i, x := range xs {
		counts[i] = x.Count
	}
	return counts, nil
}

func (a *app) eager(ctx context.Context) (parents []int64, children int, err error) {
	ps, err := a.db.Query[gen.E0]().OrderBy(gen.E0s.ID.Asc()).With(gen.E0s.Next).Limit(100).All(ctx)
	if err != nil {
		return nil, 0, err
	}
	parents = make([]int64, len(ps))
	for i, p := range ps {
		parents[i] = p.ID
		next, ok := p.Edges.Next.Get()
		if !ok {
			return nil, 0, fmt.Errorf("E0 %d: next is not loaded", p.ID)
		}
		for _, c := range next {
			if c.PrevID != p.ID {
				return nil, 0, fmt.Errorf("E1 %d under E0 %d has prev_id %d", c.ID, p.ID, c.PrevID)
			}
		}
		children += len(next)
	}
	return parents, children, nil
}

func (a *app) update(ctx context.Context, i int, name string) error {
	_, err := a.db.Update[gen.E0]().Set(gen.E0s.Name.Set(name)).Where(gen.E0s.ID.EQ(a.ids[i%100])).Exec(ctx)
	return err
}

func (a *app) count(ctx context.Context) (int, error) {
	n, err := a.db.Query[gen.E1]().Where(gen.E1s.Active.EQ(true)).Count(ctx)
	return n, err
}

func (a *app) rows(ctx context.Context) (e1, e2, e3 int, err error) {
	n1, err := a.db.Query[gen.E1]().Count(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	n2, err := a.db.Query[gen.E2]().Count(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	n3, err := a.db.Query[gen.E3]().Count(ctx)
	return n1, n2, n3, err
}

// seedPages seeds one E3 under the seeded E2, the items under it, and the
// kids under the items, and reads PageKeyset's cursor.
func (a *app) seedPages(ctx context.Context) error {
	e3 := gen.E3{Name: "s3", CreatedAt: created, Status: gen.E3StatusDraft, PrevID: a.e2}
	if err := a.db.Insert(&e3).Exec(ctx); err != nil {
		return err
	}
	items := make([]gen.E4, nItems)
	for i := range items {
		st := gen.E4StatusDraft
		if itemLive(i) {
			st = gen.E4StatusLive
		}
		items[i] = gen.E4{Name: itemName(i), Count: i, Active: i%2 == 0, CreatedAt: created, Status: st, PrevID: e3.ID}
	}
	if err := a.db.InsertAll(items).Exec(ctx); err != nil {
		return err
	}
	// InsertAll reads no keys back: the keys are read by a query.
	xs, err := a.db.Query[gen.E4]().Limit(nItems).All(ctx)
	if err != nil {
		return err
	}
	a.items = make([]int64, nItems)
	for _, x := range xs {
		a.items[x.Count] = x.ID
	}
	kids := make([]gen.E5, nKids)
	for j := range kids {
		kids[j] = gen.E5{Name: kidName(j), Count: kidCount(j), Active: kidActive(j), CreatedAt: created, Status: gen.E5StatusDraft, PrevID: a.items[kidParent(j)]}
	}
	if err := a.db.InsertAll(kids).Exec(ctx); err != nil {
		return err
	}
	ks, err := a.db.Query[gen.E5]().Limit(nKids).All(ctx)
	if err != nil {
		return err
	}
	a.kids = make([]int64, nKids)
	for _, k := range ks {
		var j int
		if _, err := fmt.Sscanf(k.Name, "k%d", &j); err != nil || j < 0 || j >= nKids {
			return fmt.Errorf("E5 %d is named %q", k.ID, k.Name)
		}
		a.kids[j] = k.ID
	}
	last, err := a.db.Query[gen.E5]().OrderBy(gen.E5s.Count.Asc(), gen.E5s.ID.Asc()).Offset((pageNum-1)*pageSize - 1).First(ctx)
	if err != nil {
		return err
	}
	a.after = last
	return nil
}

func (a *app) cursor() int64 { return a.after.ID }

func (a *app) whereAnd(ctx context.Context) ([]int64, error) {
	xs, err := a.db.Query[gen.E4]().Where(
		gen.E4s.Status.EQ(gen.E4StatusLive),
		gen.E4s.Count.GTE(whereFrom), gen.E4s.Count.LT(whereTo),
		gen.E4s.Name.HasPrefix(wherePrefix),
	).OrderBy(gen.E4s.Count.Asc()).Limit(pageSize).All(ctx)
	return e4IDs(xs), err
}

func (a *app) whereHasEdge(ctx context.Context) ([]int64, error) {
	xs, err := a.db.Query[gen.E4]().Where(gen.E4s.Next.HasWith(gen.E5s.Active.EQ(true))).
		OrderBy(gen.E4s.ID.Asc()).Limit(pageSize).All(ctx)
	return e4IDs(xs), err
}

func (a *app) pageOffset(ctx context.Context) ([]int64, error) {
	ks, err := a.db.Query[gen.E5]().OrderBy(gen.E5s.Count.Asc(), gen.E5s.ID.Asc()).
		Offset((pageNum - 1) * pageSize).Limit(pageSize).All(ctx)
	return e5IDs(ks), err
}

func (a *app) pageKeyset(ctx context.Context) ([]int64, error) {
	ks, err := a.db.Query[gen.E5]().OrderBy(gen.E5s.Count.Asc(), gen.E5s.ID.Asc()).
		After(a.after).Limit(pageSize).All(ctx)
	return e5IDs(ks), err
}

func e4IDs(xs []gen.E4) []int64 {
	ids := make([]int64, len(xs))
	for i, x := range xs {
		ids[i] = x.ID
	}
	return ids
}

func e5IDs(xs []gen.E5) []int64 {
	ids := make([]int64, len(xs))
	for i, x := range xs {
		ids[i] = x.ID
	}
	return ids
}
