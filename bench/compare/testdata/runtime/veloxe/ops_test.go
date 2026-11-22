package bench

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/syssam/velox/dialect"
	vsql "github.com/syssam/velox/dialect/sql"

	"example.com/tmp/velox"
	e0client "example.com/tmp/velox/client/e0"
	e1client "example.com/tmp/velox/client/e1"
	e3client "example.com/tmp/velox/client/e3"
	e4client "example.com/tmp/velox/client/e4"
	e5client "example.com/tmp/velox/client/e5"
	"example.com/tmp/velox/e0"
	"example.com/tmp/velox/e1"
	"example.com/tmp/velox/e2"
	"example.com/tmp/velox/e3"
	"example.com/tmp/velox/e4"
	"example.com/tmp/velox/e5"
	"example.com/tmp/velox/entity"
)

const ormName = "veloxe"

// created is every row's created_at.
var created = time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)

type app struct {
	client *velox.Client
	sqlDB  *sql.DB // the tables' connection, for ANALYZE
	ids    []int   // the E0s
	e1     int     // the first E1
	e2     int     // the seeded E2
	items  []int64 // the E4s, by index: seedPages
	kids   []int64 // the E5s, by index: seedPages
	// PageKeyset's cursor, the last row of page 4: its count and id.
	afterCount, afterID int
}

func newApp(tb testing.TB, db *sql.DB) *app {
	tb.Helper()
	d := dialect.SQLite
	if dbDialect() == "postgres" {
		d = dialect.Postgres
	}
	client := velox.NewClient(velox.Driver(vsql.OpenDB(d, db)))
	if err := client.Schema.Create(context.Background()); err != nil {
		tb.Fatal(err)
	}
	return &app{client: client, sqlDB: db}
}

func (a *app) seed(ctx context.Context) error {
	bs := make([]*e0client.E0Create, 100)
	for i := range bs {
		bs[i] = a.client.E0.Create().SetName(fmt.Sprintf("p%d", i)).SetCount(0).SetActive(false).SetCreatedAt(created).SetStatus(e0.StatusDraft)
	}
	ps, err := a.client.E0.CreateBulk(bs...).Save(ctx)
	if err != nil {
		return err
	}
	e1s := make([]*e1client.E1Create, 0, 500)
	for i, p := range ps {
		a.ids = append(a.ids, p.ID)
		for j := range 5 {
			c := 5*i + j
			e1s = append(e1s, a.client.E1.Create().SetName(fmt.Sprintf("c%d", c)).SetCount(c).SetActive(c%2 == 0).SetCreatedAt(created).SetStatus(e1.StatusDraft).SetPrevID(p.ID))
		}
	}
	cs, err := a.client.E1.CreateBulk(e1s...).Save(ctx)
	if err != nil {
		return err
	}
	a.e1 = cs[0].ID
	x, err := a.client.E2.Create().SetName("s").SetCount(0).SetActive(false).SetCreatedAt(created).SetStatus(e2.StatusDraft).SetPrevID(a.e1).Save(ctx)
	if err != nil {
		return err
	}
	a.e2 = x.ID
	return nil
}

func (a *app) insertOne(ctx context.Context) error {
	_, err := a.client.E2.Create().SetName("one").SetCount(1).SetActive(true).SetCreatedAt(created).SetStatus(e2.StatusDraft).SetPrevID(a.e1).Save(ctx)
	return err
}

func (a *app) insertBulk(ctx context.Context) error {
	bs := make([]*e3client.E3Create, 100)
	for i := range bs {
		bs[i] = a.client.E3.Create().SetName("bulk").SetCount(i).SetActive(i%2 == 0).SetCreatedAt(created).SetStatus(e3.StatusDraft).SetPrevID(a.e2)
	}
	_, err := a.client.E3.CreateBulk(bs...).Save(ctx)
	return err
}

func (a *app) seededID(i int) int64 { return int64(a.ids[i%100]) }

func (a *app) get(ctx context.Context, i int) (int64, string, error) {
	x, err := a.client.E0.Get(ctx, a.ids[i%100])
	if err != nil {
		return 0, "", err
	}
	return int64(x.ID), x.Name, nil
}

func (a *app) query(ctx context.Context) ([]int, error) {
	xs, err := a.client.E1.Query().Where(e1.CountField.LT(450)).Order(e1.ByCount(vsql.OrderDesc())).Limit(100).All(ctx)
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
	ps, err := a.client.E0.Query().Order(e0.ByID()).WithNext().Limit(100).All(ctx)
	if err != nil {
		return nil, 0, err
	}
	parents = make([]int64, len(ps))
	for i, p := range ps {
		parents[i] = int64(p.ID)
		for _, c := range p.Edges.Next {
			if c.PrevID != p.ID {
				return nil, 0, fmt.Errorf("E1 %d under E0 %d has prev_id %d", c.ID, p.ID, c.PrevID)
			}
		}
		children += len(p.Edges.Next)
	}
	return parents, children, nil
}

func (a *app) update(ctx context.Context, i int, name string) error {
	return a.client.E0.Update().Where(e0.IDField.EQ(a.ids[i%100])).SetName(name).Exec(ctx)
}

func (a *app) count(ctx context.Context) (int, error) {
	return a.client.E1.Query().Where(e1.ActiveField.EQ(true)).Count(ctx)
}

func (a *app) rows(ctx context.Context) (n1, n2, n3 int, err error) {
	if n1, err = a.client.E1.Query().Count(ctx); err != nil {
		return 0, 0, 0, err
	}
	if n2, err = a.client.E2.Query().Count(ctx); err != nil {
		return 0, 0, 0, err
	}
	n3, err = a.client.E3.Query().Count(ctx)
	return n1, n2, n3, err
}

// seedPages seeds one E3 under the seeded E2, the items under it, and the
// kids under the items, and reads PageKeyset's cursor.
func (a *app) seedPages(ctx context.Context) error {
	x3, err := a.client.E3.Create().SetName("s3").SetCount(0).SetActive(false).SetCreatedAt(created).SetStatus(e3.StatusDraft).SetPrevID(a.e2).Save(ctx)
	if err != nil {
		return err
	}
	ibs := make([]*e4client.E4Create, nItems)
	for i := range ibs {
		st := e4.StatusDraft
		if itemLive(i) {
			st = e4.StatusLive
		}
		ibs[i] = a.client.E4.Create().SetName(itemName(i)).SetCount(i).SetActive(i%2 == 0).SetCreatedAt(created).SetStatus(st).SetPrevID(x3.ID)
	}
	xs, err := a.client.E4.CreateBulk(ibs...).Save(ctx)
	if err != nil {
		return err
	}
	a.items = make([]int64, nItems)
	for i, x := range xs {
		a.items[i] = int64(x.ID)
	}
	kbs := make([]*e5client.E5Create, nKids)
	for j := range kbs {
		kbs[j] = a.client.E5.Create().SetName(kidName(j)).SetCount(kidCount(j)).SetActive(kidActive(j)).SetCreatedAt(created).SetStatus(e5.StatusDraft).SetPrevID(xs[kidParent(j)].ID)
	}
	ks, err := a.client.E5.CreateBulk(kbs...).Save(ctx)
	if err != nil {
		return err
	}
	a.kids = make([]int64, nKids)
	for j, k := range ks {
		a.kids[j] = int64(k.ID)
	}
	last, err := a.client.E5.Query().Order(e5.ByCount(), e5.ByID()).Offset((pageNum-1)*pageSize - 1).First(ctx)
	if err != nil {
		return err
	}
	a.afterCount, a.afterID = last.Count, last.ID
	return nil
}

func (a *app) cursor() int64 { return int64(a.afterID) }

func (a *app) whereAnd(ctx context.Context) ([]int64, error) {
	xs, err := a.client.E4.Query().Where(
		e4.StatusField.EQ(e4.StatusLive),
		e4.CountField.GTE(whereFrom), e4.CountField.LT(whereTo),
		e4.NameField.HasPrefix(wherePrefix),
	).Order(e4.ByCount()).Limit(pageSize).All(ctx)
	return e4IDs(xs), err
}

func (a *app) whereHasEdge(ctx context.Context) ([]int64, error) {
	xs, err := a.client.E4.Query().Where(e4.HasNextWith(e5.ActiveField.EQ(true))).
		Order(e4.ByID()).Limit(pageSize).All(ctx)
	return e4IDs(xs), err
}

func (a *app) pageOffset(ctx context.Context) ([]int64, error) {
	ks, err := a.client.E5.Query().Order(e5.ByCount(), e5.ByID()).
		Offset((pageNum - 1) * pageSize).Limit(pageSize).All(ctx)
	return e5IDs(ks), err
}

// pageKeyset writes the keyset condition (count, id) > (c, i) as velox's
// After renders it: count > c OR (count = c AND id > i). ent has no keyset
// pagination in its core.
func (a *app) pageKeyset(ctx context.Context) ([]int64, error) {
	ks, err := a.client.E5.Query().Where(e5.Or(e5.CountField.GT(a.afterCount), e5.And(e5.CountField.EQ(a.afterCount), e5.IDField.GT(a.afterID)))).
		Order(e5.ByCount(), e5.ByID()).Limit(pageSize).All(ctx)
	return e5IDs(ks), err
}

func e4IDs(xs []*entity.E4) []int64 {
	ids := make([]int64, len(xs))
	for i, x := range xs {
		ids[i] = int64(x.ID)
	}
	return ids
}

func e5IDs(xs []*entity.E5) []int64 {
	ids := make([]int64, len(xs))
	for i, x := range xs {
		ids[i] = int64(x.ID)
	}
	return ids
}
