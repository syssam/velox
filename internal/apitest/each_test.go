package apitest

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/scope"
)

// eUser has posts; ePost is confined to the context's org and has a Read policy that hides
// secret posts: the per-owner page of an O2M relation whose target is scoped.
// eUser has tags too, through eLink, whose links may repeat.
type eUser struct {
	ID    int64
	Name  string
	Posts velox.Many[ePost]
	Tags  velox.Many[fixture.DTag]
}

var eUsers = struct {
	ID    velox.NumberCol[eUser, int64]
	Name  velox.StringCol[eUser, string]
	Posts velox.Rel[eUser, ePost]
	Tags  velox.Rel[eUser, fixture.DTag]
}{velox.NumberCol[eUser, int64]{I: 0}, velox.StringCol[eUser, string]{I: 1}, velox.Rel[eUser, ePost]{I: 0}, velox.Rel[eUser, fixture.DTag]{I: 1}}

var eUserMeta velox.MetaOnce[eUser]

func (*eUser) VeloxMeta() *velox.Meta[eUser] { return eUserMeta.Load(eUserDescriptor) }

func eUserDescriptor() velox.Descriptor[eUser] {
	return velox.Descriptor[eUser]{
		Table:      "e_users",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"}},
		PrimaryKey: []int{0},
		Edges:      []velox.EdgeDescriptor{velox.EdgeTo[ePost]("Posts", 0, 1), velox.EdgeThrough[eLink, fixture.DTag]("Tags", 0, 1, 2, 0)},
		FieldPtr:   func(x *eUser, i int) any { return [...]any{&x.ID, &x.Name}[i] },
		EdgeFieldPtr: func(x *eUser, i int) any {
			switch i {
			case 0:
				return &x.Posts
			case 1:
				return &x.Tags
			}
			return nil
		},
	}
}

// eLink is the join entity of eUser.Tags. Its key is an id of its own, so a
// link between a user and a tag may repeat.
type eLink struct {
	ID     int64
	UserID int64
	TagID  int64
}

var eLinkMeta velox.MetaOnce[eLink]

func (*eLink) VeloxMeta() *velox.Meta[eLink] { return eLinkMeta.Load(eLinkDescriptor) }

func eLinkDescriptor() velox.Descriptor[eLink] {
	return velox.Descriptor[eLink]{
		Table:      "e_links",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "UserID", Name: "user_id"}, {Field: "TagID", Name: "tag_id"}},
		PrimaryKey: []int{0},
		FieldPtr:   func(x *eLink, i int) any { return [...]any{&x.ID, &x.UserID, &x.TagID}[i] },
	}
}

type ePost struct {
	ID     int64
	UserID int64
	OrgID  int64
	Title  string
	Secret bool
}

var ePosts = struct {
	ID     velox.NumberCol[ePost, int64]
	UserID velox.NumberCol[ePost, int64]
	OrgID  velox.NumberCol[ePost, int64]
	Title  velox.StringCol[ePost, string]
	Secret velox.Col[ePost, bool]
}{velox.NumberCol[ePost, int64]{I: 0}, velox.NumberCol[ePost, int64]{I: 1}, velox.NumberCol[ePost, int64]{I: 2},
	velox.StringCol[ePost, string]{I: 3}, velox.Col[ePost, bool]{I: 4}}

var ePostMeta velox.MetaOnce[ePost]

func (*ePost) VeloxMeta() *velox.Meta[ePost] { return ePostMeta.Load(ePostDescriptor) }

var ePostScope = scope.By(testmodel.Org, ePosts.OrgID.EQ, func(x *ePost) *int64 { return &x.OrgID })

func (*ePost) Policy() velox.Policy[ePost] {
	return ePostScope.Wrap(velox.Policy[ePost]{Read: func(context.Context) (velox.Pred[ePost], error) { return ePosts.Secret.EQ(false), nil }})
}

func ePostDescriptor() velox.Descriptor[ePost] {
	return velox.Descriptor[ePost]{
		Policy: func() velox.BoundPolicy { return velox.BindPolicy((*ePost)(nil).Policy()) },
		Table:  "e_posts",
		Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "UserID", Name: "user_id"}, {Field: "OrgID", Name: "org_id"},
			{Field: "Title", Name: "title"}, {Field: "Secret", Name: "secret"}},
		PrimaryKey: []int{0},
		FieldPtr:   func(x *ePost, i int) any { return [...]any{&x.ID, &x.UserID, &x.OrgID, &x.Title, &x.Secret}[i] },
	}
}

// eachLog records the SQL of every statement, by entity.
type eachLog struct {
	mu  sync.Mutex
	sql map[string][]string
}

func (l *eachLog) around(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
	l.mu.Lock()
	l.sql[st.Entity.Name] = append(l.sql[st.Entity.Name], st.SQL)
	l.mu.Unlock()
	return next(ctx)
}

func (l *eachLog) of(name string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sql[name]
}

func (l *eachLog) reset() {
	l.mu.Lock()
	l.sql = map[string][]string{}
	l.mu.Unlock()
}

// eachDB opens an in-memory SQLite database holding:
//   - invoices 1–5 of org 1, with items (invoice: skus) 1: A B C, 2: D, 3: none,
//     4: E F, 5: G H, item ids 1–8 in that order;
//   - books 1–4 and tags 1 "go", 2 "db" and 4 "zz" of org 1 and 3 "aa" of
//     org 2: book 1 has tags 1 and 2, book 2 tag 2, book 3 none and book 4
//     tags 3 and 4;
//   - users 1–3: user 1's posts (title, org, secret) are a/2, b/1, c/1
//     secret, d/1 and e/1; user 2's a2/1 and b2/1; user 3 has none. User 1
//     is linked to tags 1 "go" twice and 4 "zz", user 2 to tag 2 "db"
//     twice, and user 3 to none.
//
// The DB logs every statement's SQL.
func eachDB(t *testing.T, opts ...velox.Option) (*velox.DB, *eachLog) {
	t.Helper()
	raw, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	stmts := []string{
		`CREATE TABLE invoices (created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, id INTEGER PRIMARY KEY,
			org_id INTEGER NOT NULL, number TEXT NOT NULL, total REAL NOT NULL, paid BOOLEAN NOT NULL,
			paid_at DATETIME, note TEXT NOT NULL, data BLOB NOT NULL)`,
		`CREATE TABLE items (id INTEGER PRIMARY KEY, invoice_id INTEGER NOT NULL, sku TEXT NOT NULL, qty INTEGER NOT NULL)`,
		`CREATE TABLE books (id INTEGER PRIMARY KEY, author_id INTEGER NOT NULL, title TEXT NOT NULL)`,
		`CREATE TABLE tags (id INTEGER PRIMARY KEY, org_id INTEGER NOT NULL, name TEXT NOT NULL)`,
		`CREATE TABLE book_tags (book_id INTEGER NOT NULL, tag_id INTEGER NOT NULL, PRIMARY KEY (book_id, tag_id))`,
		`CREATE TABLE e_users (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE e_posts (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL, org_id INTEGER NOT NULL, title TEXT NOT NULL, secret BOOLEAN NOT NULL)`,
		`CREATE TABLE e_links (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL, tag_id INTEGER NOT NULL)`,
		`INSERT INTO e_links (id, user_id, tag_id) VALUES (1, 1, 1), (2, 1, 1), (3, 1, 4), (4, 2, 2), (5, 2, 2)`,
		`INSERT INTO books (id, author_id, title) VALUES (1, 1, 'b1'), (2, 1, 'b2'), (3, 1, 'b3'), (4, 1, 'b4')`,
		`INSERT INTO tags (id, org_id, name) VALUES (1, 1, 'go'), (2, 1, 'db'), (3, 2, 'aa'), (4, 1, 'zz')`,
		`INSERT INTO book_tags (book_id, tag_id) VALUES (1, 1), (1, 2), (2, 2), (4, 3), (4, 4)`,
		`INSERT INTO e_users (id, name) VALUES (1, 'u1'), (2, 'u2'), (3, 'u3')`,
		`INSERT INTO e_posts (id, user_id, org_id, title, secret) VALUES (1, 1, 2, 'a', false), (2, 1, 1, 'b', false),
			(3, 1, 1, 'c', true), (4, 1, 1, 'd', false), (5, 1, 1, 'e', false), (6, 2, 1, 'a2', false), (7, 2, 1, 'b2', false)`,
	}
	for _, s := range stmts {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	for id := int64(1); id <= 5; id++ {
		if _, err := raw.Exec(`INSERT INTO invoices (created_at, updated_at, id, org_id, number, total, paid, paid_at, note, data)
			VALUES (?, ?, ?, 1, ?, 0, false, NULL, '', ?)`, t0, t0, id, "N"+string(rune('0'+id)), []byte("d")); err != nil {
			t.Fatal(err)
		}
	}
	for i, it := range []struct {
		inv int64
		sku string
	}{{1, "A"}, {1, "B"}, {1, "C"}, {2, "D"}, {4, "E"}, {4, "F"}, {5, "G"}, {5, "H"}} {
		if _, err := raw.Exec(`INSERT INTO items (id, invoice_id, sku, qty) VALUES (?, ?, ?, 1)`, i+1, it.inv, it.sku); err != nil {
			t.Fatal(err)
		}
	}
	log := &eachLog{sql: map[string][]string{}}
	return velox.Open(velox.SQLite, raw, append([]velox.Option{velox.WithAround(log.around)}, opts...)...), log
}

// invoiceSKUs is each invoice's loaded items' SKUs, by invoice id.
func invoiceSKUs(t *testing.T, invs []testmodel.Invoice) map[int64][]string {
	t.Helper()
	got := map[int64][]string{}
	for _, inv := range invs {
		items, ok := inv.Items.Get()
		if !ok || items == nil {
			t.Fatalf("invoice %d: items not loaded", inv.ID)
		}
		skus := []string{}
		for _, it := range items {
			skus = append(skus, it.SKU)
		}
		got[inv.ID] = skus
	}
	return got
}

func TestLimitEachWith(t *testing.T) {
	db, log := eachDB(t)
	invs, err := db.Query[testmodel.Invoice]().OrderBy(I.ID.Asc()).
		With(I.Items.LimitEach(1).OrderBy(It.SKU.Desc()).With(It.Invoice)).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64][]string{1: {"C"}, 2: {"D"}, 3: {}, 4: {"F"}, 5: {"H"}}
	if got := invoiceSKUs(t, invs); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	for _, inv := range invs {
		items, _ := inv.Items.Get()
		for _, it := range items {
			if back, ok := it.Invoice.Get(); !ok || back == nil || back.ID != inv.ID {
				t.Errorf("item %d: its invoice %v %v", it.ID, back, ok)
			}
		}
	}
	if q := log.of("testmodel.Item"); len(q) != 1 || !strings.Contains(q[0], "ROW_NUMBER() OVER (PARTITION BY \"invoice_id\"") {
		t.Errorf("the items' statements: %q", q)
	}
}

// bookTagNames is each book's loaded tags' names, by book id.
func bookTagNames(t *testing.T, bs []fixture.DBook) map[int64][]string {
	t.Helper()
	got := map[int64][]string{}
	for _, b := range bs {
		got[b.ID] = tagNames(t, b.Tags)
	}
	return got
}

func TestLimitEachThrough(t *testing.T) {
	db, log := eachDB(t)
	bs, err := db.Query[fixture.DBook]().OrderBy(fixture.DBooks.ID.Asc()).With(fixture.DBooks.Tags.LimitEach(1).OrderBy(fixture.DTags.Name.Asc())).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Tag 2, "db", is linked to books 1 and 2, and is the first of both.
	want := map[int64][]string{1: {"db"}, 2: {"db"}, 3: {}, 4: {"aa"}}
	if got := bookTagNames(t, bs); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if q := log.of("fixture.DTag"); len(q) != 1 || !strings.Contains(q[0], `JOIN "book_tags" AS "vx_j"`) {
		t.Errorf("the tags' statements: %q", q)
	}
	if q := log.of("fixture.DBookTag"); len(q) != 0 {
		t.Errorf("the join rows were read apart: %q", q)
	}
	// A limit over every book's count: every tag, in the relation's order.
	log.reset()
	bs, err = db.Query[fixture.DBook]().OrderBy(fixture.DBooks.ID.Asc()).With(fixture.DBooks.Tags.LimitEach(5).OrderBy(fixture.DTags.Name.Desc())).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want = map[int64][]string{1: {"go", "db"}, 2: {"db"}, 3: {}, 4: {"zz", "aa"}}
	if got := bookTagNames(t, bs); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestLimitEachCap(t *testing.T) {
	db, _ := eachDB(t, velox.WithMaxRows(2))
	q := db.Query[testmodel.Invoice]().OrderBy(I.ID.Asc()).Limit(10).MaxRows(5)
	// Every item is 8 rows, over the owner query's 5.
	if _, err := q.With(I.Items).All(ctx); !errors.Is(err, velox.ErrTooManyRows) {
		t.Fatalf("a load of every item: %v, want ErrTooManyRows", err)
	}
	// Two items each is 7 rows, bounded by the per-owner limit.
	invs, err := q.With(I.Items.LimitEach(2).OrderBy(It.SKU.Asc())).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64][]string{1: {"A", "B"}, 2: {"D"}, 3: {}, 4: {"E", "F"}, 5: {"G", "H"}}
	if got := invoiceSKUs(t, invs); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// The relation's own cap wins over the bound.
	_, err = q.With(I.Items.LimitEach(2).MaxRows(3)).All(ctx)
	if !errors.Is(err, velox.ErrTooManyRows) || !strings.Contains(err.Error(), "loading Items") {
		t.Errorf("MaxRows(3): %v, want ErrTooManyRows naming Items", err)
	}
	// And raises the owner's cap for a load without a per-owner limit.
	if _, err := q.With(I.Items.MaxRows(8)).All(ctx); err != nil {
		t.Errorf("MaxRows(8): %v", err)
	}
	// MaxRows(0) restores the owner query's.
	if _, err := q.With(I.Items.MaxRows(8).MaxRows(0)).All(ctx); !errors.Is(err, velox.ErrTooManyRows) {
		t.Errorf("MaxRows(0): %v, want ErrTooManyRows", err)
	}
}

// TestLimitEachScoped is Review Focus 1: a row the scope or the Read
// policy hides takes no rank inside the window, so every owner still gets n
// rows when it has them.
func TestLimitEachScoped(t *testing.T) {
	db, _ := eachDB(t)
	acme := testmodel.Org.With(context.Background(), int64(1))
	posts := func(n int) map[int64][]string {
		t.Helper()
		us, err := db.Query[eUser]().OrderBy(eUsers.ID.Asc()).With(eUsers.Posts.LimitEach(n).OrderBy(ePosts.Title.Asc())).Limit(10).All(acme)
		if err != nil {
			t.Fatal(err)
		}
		got := map[int64][]string{}
		for _, u := range us {
			ps, ok := u.Posts.Get()
			if !ok {
				t.Fatalf("user %d: posts not loaded", u.ID)
			}
			titles := []string{}
			for _, p := range ps {
				titles = append(titles, p.Title)
			}
			got[u.ID] = titles
		}
		return got
	}
	// User 1's "a" is globex's and sorts first, and "c" is secret.
	for n, want := range map[int]map[int64][]string{
		1: {1: {"b"}, 2: {"a2"}, 3: {}},
		2: {1: {"b", "d"}, 2: {"a2", "b2"}, 3: {}},
		3: {1: {"b", "d", "e"}, 2: {"a2", "b2"}, 3: {}},
	} {
		if got := posts(n); !reflect.DeepEqual(got, want) {
			t.Errorf("LimitEach(%d): got %v, want %v", n, got, want)
		}
	}
	// Through a join: book 4's "aa" is org 2's and sorts first.
	bs, err := db.Query[fixture.DBook]().OrderBy(fixture.DBooks.ID.Asc()).With(fixture.DBooks.Tags.LimitEach(1).OrderBy(fixture.DTags.Name.Asc())).Limit(10).All(acme)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64][]string{1: {"db"}, 2: {"db"}, 3: {}, 4: {"zz"}}
	if got := bookTagNames(t, bs); !reflect.DeepEqual(got, want) {
		t.Errorf("through: got %v, want %v", got, want)
	}
}

func TestRewriteMayNotChangeEach(t *testing.T) {
	for name, hook := range map[string]func(st *velox.Statement){
		"cleared":      func(st *velox.Statement) { st.Each = nil },
		"its Limit":    func(st *velox.Statement) { st.Each.Limit = 5 },
		"its Keys":     func(st *velox.Statement) { st.Each.Keys[0] = int64(99) },
		"another copy": func(st *velox.Statement) { c := *st.Each; st.Each = &c },
	} {
		t.Run(name, func(t *testing.T) {
			saw := false
			db, _ := eachDB(t, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
				if st.Each != nil {
					saw = true
					hook(st)
				}
				return nil
			}))
			_, err := db.Query[testmodel.Invoice]().With(I.Items.LimitEach(1)).Limit(10).All(ctx)
			if !saw {
				t.Fatal("the hook saw no Each")
			}
			if want := "velox: testmodel.Invoice: loading Items: testmodel.Item: a Rewrite hook changed the statement's Each"; err == nil || err.Error() != want {
				t.Errorf("%v, want %q", err, want)
			}
		})
	}
	t.Run("set", func(t *testing.T) {
		db, _ := eachDB(t, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
			st.Each = &velox.EachClause{Keys: []any{int64(1)}, Column: 0, Limit: 1}
			return nil
		}))
		_, err := db.Query[testmodel.Invoice]().Limit(10).All(ctx)
		if want := "velox: testmodel.Invoice: a Rewrite hook changed the statement's Each"; err == nil || err.Error() != want {
			t.Errorf("%v, want %q", err, want)
		}
	})
}

func TestLimitEachRemoved(t *testing.T) {
	db, log := eachDB(t)
	run := func(r velox.Rel[testmodel.Invoice, testmodel.Item]) []string {
		t.Helper()
		log.reset()
		if _, err := db.Query[testmodel.Invoice]().With(r).Limit(10).All(ctx); err != nil {
			t.Fatal(err)
		}
		return log.of("testmodel.Item")
	}
	plain := run(I.Items)
	if got := run(I.Items.LimitEach(2).LimitEach(0)); !reflect.DeepEqual(got, plain) {
		t.Errorf("LimitEach(0) sent %q, want the plain load's %q", got, plain)
	}
	if got := run(I.Items.LimitEach(2).LimitEach(-1)); !reflect.DeepEqual(got, plain) {
		t.Errorf("LimitEach(-1) sent %q, want the plain load's %q", got, plain)
	}
}

// skuLists is each list of items' SKUs.
func skuLists(lists [][]testmodel.Item) [][]string {
	out := [][]string{}
	for _, items := range lists {
		skus := []string{}
		for _, it := range items {
			skus = append(skus, it.SKU)
		}
		out = append(out, skus)
	}
	return out
}

// nameLists is each list of tags' names.
func nameLists(lists [][]fixture.DTag) [][]string {
	out := [][]string{}
	for _, tags := range lists {
		names := []string{}
		for _, g := range tags {
			names = append(names, g.Name)
		}
		out = append(out, names)
	}
	return out
}

// eachInvoices returns pointers to the invoices with ids, in that order.
func eachInvoices(t *testing.T, db *velox.DB, ids ...int64) []*testmodel.Invoice {
	t.Helper()
	invs, err := db.Query[testmodel.Invoice]().Where(I.ID.In(ids...)).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]*testmodel.Invoice{}
	for i := range invs {
		byID[invs[i].ID] = &invs[i]
	}
	out := make([]*testmodel.Invoice, len(ids))
	for i, id := range ids {
		if out[i] = byID[id]; out[i] == nil {
			t.Fatalf("no invoice %d", id)
		}
	}
	return out
}

// eachBooks returns the books with ids, in that order.
func eachBooks(ids ...int64) []*fixture.DBook {
	out := make([]*fixture.DBook, len(ids))
	for i, id := range ids {
		out[i] = &fixture.DBook{ID: id}
	}
	return out
}

func TestLoadEach(t *testing.T) {
	db, log := eachDB(t)
	// Invoice 1 twice, out of order: the lists align with the owners.
	owners := eachInvoices(t, db, 2, 1, 3, 1)
	log.reset()
	got, err := velox.LoadEach(ctx, db.Query[testmodel.Item]().OrderBy(It.SKU.Asc()), I.Items.LimitEach(2), owners)
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"D"}, {"A", "B"}, {}, {"A", "B"}}; !reflect.DeepEqual(skuLists(got), want) {
		t.Errorf("got %v, want %v", skuLists(got), want)
	}
	if got[2] == nil {
		t.Error("an invoice with no items got nil, want an empty slice")
	}
	if q := log.of("testmodel.Item"); len(q) != 1 || !strings.Contains(q[0], "ROW_NUMBER() OVER (PARTITION BY \"invoice_id\"") {
		t.Errorf("the items' statements: %q", q)
	}
	// Each owner's slice is its own.
	got[1][0].SKU = "Z"
	if got[3][0].SKU != "A" {
		t.Error("owners with one key share a slice")
	}
	// q's conditions, then r's, and r's order after q's.
	got, err = velox.LoadEach(ctx, db.Query[testmodel.Item]().Where(It.SKU.NEQ("A")), I.Items.OrderBy(It.SKU.Desc()), owners[:2])
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"D"}, {"C", "B"}}; !reflect.DeepEqual(skuLists(got), want) {
		t.Errorf("without a limit: got %v, want %v", skuLists(got), want)
	}
	got, err = velox.LoadEach(ctx, db.Query[testmodel.Item](), I.Items.Where(It.SKU.NEQ("B")).LimitEach(5), owners[1:2])
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"A", "C"}}; !reflect.DeepEqual(skuLists(got), want) {
		t.Errorf("r's Where: got %v, want %v", skuLists(got), want)
	}
	// r's With loads, and q's, apply to the rows.
	for name, c := range map[string]struct {
		q velox.Query[testmodel.Item]
		r velox.Rel[testmodel.Invoice, testmodel.Item]
	}{
		"r's": {db.Query[testmodel.Item](), I.Items.LimitEach(1).With(It.Invoice)},
		"q's": {db.Query[testmodel.Item]().With(It.Invoice), I.Items.LimitEach(1)},
	} {
		got, err := velox.LoadEach(ctx, c.q, c.r, owners)
		if err != nil {
			t.Fatalf("%s With: %v", name, err)
		}
		for i, items := range got {
			for _, it := range items {
				if back, ok := it.Invoice.Get(); !ok || back == nil || back.ID != owners[i].ID {
					t.Errorf("%s With: item %d: its invoice %v %v", name, it.ID, back, ok)
				}
			}
		}
	}
	// A relation condition in r's Where, and q's, resolves with the
	// statement; so does one in a With load's.
	got, err = velox.LoadEach(ctx, db.Query[testmodel.Item]().Where(It.Invoice.Has()), I.Items.Where(It.Invoice.Has()).LimitEach(1), owners[1:2])
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"A"}}; !reflect.DeepEqual(skuLists(got), want) {
		t.Errorf("a relation condition: got %v, want %v", skuLists(got), want)
	}
	if _, err := db.Query[testmodel.Invoice]().With(I.Items.Where(It.Invoice.Has())).Limit(10).All(ctx); err != nil {
		t.Errorf("With, a relation condition: %v", err)
	}
	// No owners: no statement.
	log.reset()
	if got, err := velox.LoadEach(ctx, db.Query[testmodel.Item](), I.Items, nil); err != nil || len(got) != 0 {
		t.Errorf("no owners: %v, %v", got, err)
	}
	if q := log.of("testmodel.Item"); len(q) != 0 {
		t.Errorf("no owners sent %q", q)
	}
	// Through a join, in one statement that reads no join row apart.
	log.reset()
	tags, err := velox.LoadEach(ctx, db.Query[fixture.DTag]().OrderBy(fixture.DTags.Name.Asc()), fixture.DBooks.Tags.LimitEach(1), eachBooks(1, 2, 3, 4))
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"db"}, {"db"}, {}, {"aa"}}; !reflect.DeepEqual(nameLists(tags), want) {
		t.Errorf("through: got %v, want %v", nameLists(tags), want)
	}
	if q, j := log.of("fixture.DTag"), log.of("fixture.DBookTag"); len(q) != 1 || len(j) != 0 {
		t.Errorf("through: statements %q and %q", q, j)
	}
}

func TestLoadEachAfter(t *testing.T) {
	db, _ := eachDB(t)
	owners := eachInvoices(t, db, 1, 2, 4)
	after := func(id int64) *testmodel.Item {
		t.Helper()
		x, err := db.Query[testmodel.Item]().Where(It.ID.EQ(id)).One(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	got, err := velox.LoadEach(ctx, db.Query[testmodel.Item]().OrderBy(It.ID.Asc()).After(after(2)), I.Items.LimitEach(1), owners)
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"C"}, {"D"}, {"E"}}; !reflect.DeepEqual(skuLists(got), want) {
		t.Errorf("ascending: got %v, want %v", skuLists(got), want)
	}
	got, err = velox.LoadEach(ctx, db.Query[testmodel.Item]().OrderBy(It.ID.Desc()).After(after(6)), I.Items.LimitEach(2), owners)
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"C", "B"}, {"D"}, {"E"}}; !reflect.DeepEqual(skuLists(got), want) {
		t.Errorf("descending: got %v, want %v", skuLists(got), want)
	}
	// After's own rules hold.
	if _, err := velox.LoadEach(ctx, db.Query[testmodel.Item]().After(after(2)), I.Items, owners); err == nil || !strings.Contains(err.Error(), "After needs an OrderBy") {
		t.Errorf("After without an order: %v", err)
	}
}

func TestLoadEachRefuses(t *testing.T) {
	db, log := eachDB(t)
	owners := eachInvoices(t, db, 1, 2)
	log.reset()
	q := db.Query[testmodel.Item]()
	for method, q := range map[string]velox.Query[testmodel.Item]{
		"Limit":      q.Limit(1),
		"Offset":     q.Offset(1),
		"ForUpdate":  q.ForUpdate(),
		"ForShare":   q.ForShare(),
		"SkipLocked": q.SkipLocked(),
	} {
		_, err := velox.LoadEach(ctx, q, I.Items, owners)
		if err == nil || !strings.Contains(err.Error(), "testmodel.Item: LoadEach") || !strings.Contains(err.Error(), method) {
			t.Errorf("LoadEach with %s: %v, want an error naming the entity and the method", method, err)
		}
		_, err = velox.CountEach(ctx, q, I.Items, owners)
		if err == nil || !strings.Contains(err.Error(), "testmodel.Item: CountEach") || !strings.Contains(err.Error(), method) {
			t.Errorf("CountEach with %s: %v, want an error naming the entity and the method", method, err)
		}
	}
	nils := []*testmodel.Invoice{owners[0], nil}
	if _, err := velox.LoadEach(ctx, q, I.Items, nils); err == nil || !strings.Contains(err.Error(), "testmodel.Invoice: LoadEach was given a nil owner") {
		t.Errorf("LoadEach, a nil owner: %v", err)
	}
	if _, err := velox.CountEach(ctx, q, I.Items, nils); err == nil || !strings.Contains(err.Error(), "testmodel.Invoice: CountEach was given a nil owner") {
		t.Errorf("CountEach, a nil owner: %v", err)
	}
	if n := len(log.of("testmodel.Item")); n != 0 {
		t.Errorf("%d statements sent, want none", n)
	}
}

func TestLoadEachAddsKeyColumns(t *testing.T) {
	db, log := eachDB(t)
	owners := eachInvoices(t, db, 1, 2, 3)
	log.reset()
	got, err := velox.LoadEach(ctx, db.Query[testmodel.Item]().Columns(It.SKU).OrderBy(It.SKU.Asc()), I.Items.LimitEach(2), owners)
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"A", "B"}, {"D"}, {}}; !reflect.DeepEqual(skuLists(got), want) {
		t.Errorf("got %v, want %v", skuLists(got), want)
	}
	for _, it := range got[0] {
		if it.ID != 0 || it.InvoiceID != 1 {
			t.Errorf("item %+v: want only its SKU and invoice read", it)
		}
	}
	if q := log.of("testmodel.Item"); len(q) != 1 || !strings.HasPrefix(q[0], `SELECT "sku", "invoice_id" FROM`) {
		t.Errorf("the items' statements: %q", q)
	}
	// Through a join, the owner's key is read from the join table.
	tags, err := velox.LoadEach(ctx, db.Query[fixture.DTag]().Columns(fixture.DTags.Name).OrderBy(fixture.DTags.Name.Asc()), fixture.DBooks.Tags.LimitEach(2), eachBooks(1, 3))
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"db", "go"}, {}}; !reflect.DeepEqual(nameLists(tags), want) {
		t.Errorf("through: got %v, want %v", nameLists(tags), want)
	}
}

// TestLimitEachThroughRepeatedLinks: a link that repeats in the join table
// relates its target to the owner once, as a plain With loads it, so it
// takes one place in a page and is counted once.
func TestLimitEachThroughRepeatedLinks(t *testing.T) {
	db, _ := eachDB(t)
	byName := eUsers.Tags.OrderBy(fixture.DTags.Name.Asc())
	load := func(r velox.Rel[eUser, fixture.DTag]) [][]string {
		t.Helper()
		us, err := db.Query[eUser]().OrderBy(eUsers.ID.Asc()).With(r).Limit(10).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := [][]string{}
		for _, u := range us {
			out = append(out, tagNames(t, u.Tags))
		}
		return out
	}
	want := [][]string{{"go", "zz"}, {"db"}, {}}
	if got := load(byName); !reflect.DeepEqual(got, want) {
		t.Errorf("With: got %v, want %v", got, want)
	}
	if got := load(byName.LimitEach(2)); !reflect.DeepEqual(got, want) {
		t.Errorf("With LimitEach(2): got %v, want %v", got, want)
	}
	if got := load(byName.LimitEach(1)); !reflect.DeepEqual(got, [][]string{{"go"}, {"db"}, {}}) {
		t.Errorf("With LimitEach(1): got %v", got)
	}
	users := []*eUser{{ID: 1}, {ID: 2}, {ID: 3}}
	for name, r := range map[string]velox.Rel[eUser, fixture.DTag]{"no limit": byName, "LimitEach(2)": byName.LimitEach(2)} {
		got, err := velox.LoadEach(ctx, db.Query[fixture.DTag](), r, users)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(nameLists(got), want) {
			t.Errorf("LoadEach, %s: got %v, want %v", name, nameLists(got), want)
		}
	}
	n, err := velox.CountEach(ctx, db.Query[fixture.DTag](), eUsers.Tags, users)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{2, 1, 0}; !reflect.DeepEqual(n, want) {
		t.Errorf("CountEach: %v, want %v", n, want)
	}
}

func TestCountEach(t *testing.T) {
	db, log := eachDB(t)
	owners := eachInvoices(t, db, 5, 3, 1, 1, 2, 4)
	log.reset()
	n, err := velox.CountEach(ctx, db.Query[testmodel.Item](), I.Items, owners)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{2, 0, 3, 3, 1, 2}; !reflect.DeepEqual(n, want) {
		t.Errorf("got %v, want %v", n, want)
	}
	if q := log.of("testmodel.Item"); len(q) != 1 || !strings.Contains(q[0], `COUNT(*)`) || !strings.HasSuffix(q[0], `GROUP BY "invoice_id"`) {
		t.Errorf("the items' statements: %q", q)
	}
	// q's conditions and r's; r's LimitEach and order do not count.
	n, err = velox.CountEach(ctx, db.Query[testmodel.Item]().Where(It.SKU.NEQ("A")), I.Items.Where(It.SKU.NEQ("H")).LimitEach(1).OrderBy(It.SKU.Asc()), owners)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{1, 0, 2, 2, 1, 2}; !reflect.DeepEqual(n, want) {
		t.Errorf("with conditions: got %v, want %v", n, want)
	}
	// Through a join: each link once, and a tag of two books for each.
	log.reset()
	n, err = velox.CountEach(ctx, db.Query[fixture.DTag](), fixture.DBooks.Tags, eachBooks(1, 2, 3, 4))
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{2, 1, 0, 2}; !reflect.DeepEqual(n, want) {
		t.Errorf("through: got %v, want %v", n, want)
	}
	if q, j := log.of("fixture.DTag"), log.of("fixture.DBookTag"); len(q) != 1 || len(j) != 0 {
		t.Errorf("through: statements %q and %q", q, j)
	}
	// No owners: no statement.
	if n, err := velox.CountEach(ctx, db.Query[testmodel.Item](), I.Items, nil); err != nil || len(n) != 0 {
		t.Errorf("no owners: %v, %v", n, err)
	}
}

// TestCountEachScoped is Review Focus 1 for counts: a row the scope or
// the Read policy hides is not counted.
func TestCountEachScoped(t *testing.T) {
	db, _ := eachDB(t)
	acme := testmodel.Org.With(context.Background(), int64(1))
	users := []*eUser{{ID: 1}, {ID: 2}, {ID: 3}}
	for name, c := range map[string]struct {
		ctx  context.Context
		want []int64
	}{
		// User 1's "a" is globex's, and "c" is secret.
		"acme":           {acme, []int64{3, 2, 0}},
		"without policy": {ctx, []int64{5, 2, 0}},
	} {
		n, err := velox.CountEach(c.ctx, db.Query[ePost](), eUsers.Posts, users)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(n, c.want) {
			t.Errorf("%s: got %v, want %v", name, n, c.want)
		}
	}
	// Through a join: book 4's "aa" is org 2's.
	n, err := velox.CountEach(acme, db.Query[fixture.DTag](), fixture.DBooks.Tags, eachBooks(1, 2, 3, 4))
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{2, 1, 0, 1}; !reflect.DeepEqual(n, want) {
		t.Errorf("through: got %v, want %v", n, want)
	}
}

func TestRewriteMayNotChangeCountEach(t *testing.T) {
	for name, c := range map[string]struct {
		hook func(st *velox.Statement)
		want string
	}{
		"its Keys":  {func(st *velox.Statement) { st.Each.Keys[0] = int64(99) }, "changed the statement's Each"},
		"its Limit": {func(st *velox.Statement) { st.Each.Limit = 1 }, "changed the statement's Each"},
		"Count":     {func(st *velox.Statement) { st.Count = false }, "changed the statement's Count"},
	} {
		t.Run(name, func(t *testing.T) {
			db, _ := eachDB(t, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
				if st.Each != nil && st.Count {
					c.hook(st)
				}
				return nil
			}))
			_, err := velox.CountEach(ctx, db.Query[testmodel.Item](), I.Items, []*testmodel.Invoice{{ID: 1}})
			if want := "velox: testmodel.Item: a Rewrite hook " + c.want; err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%v, want %q", err, want)
			}
		})
	}
}

func TestSelects(t *testing.T) {
	db, _ := eachDB(t)
	q := db.Query[testmodel.Item]()
	for name, c := range map[string]struct {
		got, want bool
	}{
		"every column":           {q.Selects(It.SKU, It.ID), true},
		"no columns asked":       {q.Columns(It.ID).Selects(), true},
		"a column Columns names": {q.Columns(It.ID, It.SKU).Selects(It.SKU), true},
		"a column Columns omits": {q.Columns(It.ID).Selects(It.ID, It.SKU), false},
		"a column Omit names":    {q.Omit(It.SKU).Selects(It.SKU), false},
		"a column Omit keeps":    {q.Omit(It.SKU).Selects(It.ID), true},
		"Columns() again":        {q.Columns(It.ID).Columns().Selects(It.SKU), true},
	} {
		if c.got != c.want {
			t.Errorf("%s: %v, want %v", name, c.got, c.want)
		}
	}
}

// A per-owner read can join a to-one relation: the rows' relation is filled from the
// same statement, in the ranking window or without one, and CountEach counts the
// joined rows. The owner's invoice is the joined row here, so what the join changes
// is the condition: an Is that leaves out an owner's rows leaves out its list.
func TestLoadEachJoin(t *testing.T) {
	db, log := eachDB(t)
	owners := eachInvoices(t, db, 2, 1, 3, 1)
	log.reset()
	numbers := func(lists [][]testmodel.Item) [][]string {
		out := [][]string{}
		for _, items := range lists {
			ns := []string{}
			for _, it := range items {
				inv, ok := it.Invoice.Get()
				if !ok || inv == nil {
					t.Fatalf("item %s: invoice not joined", it.SKU)
				}
				ns = append(ns, it.SKU+":"+inv.Number)
			}
			out = append(out, ns)
		}
		return out
	}
	q := db.Query[testmodel.Item]().Join(It.Invoice).OrderBy(It.Invoice.By(I.Number.Desc()), It.SKU.Desc())
	got, err := velox.LoadEach(ctx, q, I.Items.LimitEach(2), owners)
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"D:N2"}, {"C:N1", "B:N1"}, {}, {"C:N1", "B:N1"}}; !reflect.DeepEqual(numbers(got), want) {
		t.Errorf("with a window: got %v, want %v", numbers(got), want)
	}
	st := log.of("testmodel.Item")
	if len(st) != 1 || !strings.Contains(st[0], "ROW_NUMBER() OVER (PARTITION BY \"items\".\"invoice_id\" ORDER BY \"vxj1\".\"number\" DESC") ||
		!strings.Contains(st[0], `"vxj1"."number" AS "vxj1_4"`) || !strings.Contains(st[0], `"vxj1_4"`) {
		t.Errorf("the statement: %q", st)
	}
	// With no limit there is no window.
	log.reset()
	got, err = velox.LoadEach(ctx, q, I.Items, owners)
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"D:N2"}, {"C:N1", "B:N1", "A:N1"}, {}, {"C:N1", "B:N1", "A:N1"}}; !reflect.DeepEqual(numbers(got), want) {
		t.Errorf("without a limit: got %v, want %v", numbers(got), want)
	}
	if st := log.of("testmodel.Item"); len(st) != 1 || strings.Contains(st[0], "ROW_NUMBER") {
		t.Errorf("the statement: %q", st)
	}
	// A condition on the joined row leaves out the owner's rows.
	only := db.Query[testmodel.Item]().Join(It.Invoice).Where(It.Invoice.Is(I.Number.NEQ("N2"))).OrderBy(It.SKU.Asc())
	if got, err = velox.LoadEach(ctx, only, I.Items.LimitEach(5), owners); err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{}, {"A:N1", "B:N1", "C:N1"}, {}, {"A:N1", "B:N1", "C:N1"}}; !reflect.DeepEqual(numbers(got), want) {
		t.Errorf("with Is: got %v, want %v", numbers(got), want)
	}
	// CountEach counts the joined rows.
	counts, err := velox.CountEach(ctx, only, I.Items, owners)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{0, 3, 0, 3}; !reflect.DeepEqual(counts, want) {
		t.Errorf("CountEach: %v, want %v", counts, want)
	}
	// A relation of the joined entity loads as With loads it.
	with := db.Query[testmodel.Item]().Join(It.Invoice.With(I.Items)).OrderBy(It.SKU.Asc())
	if got, err = velox.LoadEach(ctx, with, I.Items.LimitEach(1), owners[:1]); err != nil {
		t.Fatal(err)
	}
	inv, _ := got[0][0].Invoice.Get()
	if items, ok := inv.Items.Get(); !ok || len(items) != 1 || items[0].SKU != "D" {
		t.Errorf("the joined invoice's items: %v %v", items, ok)
	}
}
