package velox_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

// eUser has posts; ePost is tenant-scoped and has a Read policy that hides
// secret posts: the per-owner page of an O2M relation whose target is scoped.
type eUser struct {
	ID    int64
	Name  string
	Posts velox.Many[ePost]
}

var eUsers = struct {
	ID    velox.NumberCol[eUser, int64]
	Name  velox.StringCol[eUser, string]
	Posts velox.Rel[eUser, ePost]
}{velox.NumberCol[eUser, int64]{I: 0}, velox.StringCol[eUser, string]{I: 1}, velox.Rel[eUser, ePost]{I: 0}}

var eUserMeta velox.MetaOnce[eUser]

func (*eUser) VeloxMeta() *velox.Meta[eUser] { return eUserMeta.Load(eUserDesc) }

func eUserDesc() velox.Desc[eUser] {
	return velox.Desc[eUser]{
		Table:      "e_users",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Relations:  []velox.Relation{velox.EdgeTo[ePost]("Posts", 0, 1)},
		Addr:       func(x *eUser, i int) any { return [...]any{&x.ID, &x.Name}[i] },
		RelAddr: func(x *eUser, i int) any {
			if i == 0 {
				return &x.Posts
			}
			return nil
		},
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

func (*ePost) VeloxMeta() *velox.Meta[ePost] { return ePostMeta.Load(ePostDesc) }

func (*ePost) Policy() velox.Policy[ePost] {
	return velox.Policy[ePost]{Read: func(context.Context) (velox.Pred[ePost], error) { return ePosts.Secret.EQ(false), nil }}
}

func ePostDesc() velox.Desc[ePost] {
	return velox.Desc[ePost]{
		Policy: func() velox.BoundPolicy { return velox.BindPolicy((*ePost)(nil).Policy()) },
		Table:  "e_posts",
		Columns: []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "UserID", Name: "user_id"}, {Field: "OrgID", Name: "org_id"},
			{Field: "Title", Name: "title"}, {Field: "Secret", Name: "secret"}},
		PrimaryKey: []int{0},
		Tenant:     2,
		Addr:       func(x *ePost, i int) any { return [...]any{&x.ID, &x.UserID, &x.OrgID, &x.Title, &x.Secret}[i] },
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
//     secret, d/1 and e/1; user 2's a2/1 and b2/1; user 3 has none.
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
func bookTagNames(t *testing.T, bs []dBook) map[int64][]string {
	t.Helper()
	got := map[int64][]string{}
	for _, b := range bs {
		got[b.ID] = tagNames(t, b.Tags)
	}
	return got
}

func TestLimitEachThrough(t *testing.T) {
	db, log := eachDB(t)
	bs, err := db.Query[dBook]().OrderBy(dBooks.ID.Asc()).With(dBooks.Tags.LimitEach(1).OrderBy(dTags.Name.Asc())).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Tag 2, "db", is linked to books 1 and 2, and is the first of both.
	want := map[int64][]string{1: {"db"}, 2: {"db"}, 3: {}, 4: {"aa"}}
	if got := bookTagNames(t, bs); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if q := log.of("velox_test.dTag"); len(q) != 1 || !strings.Contains(q[0], `JOIN "book_tags" AS "vx_j"`) {
		t.Errorf("the tags' statements: %q", q)
	}
	if q := log.of("velox_test.dBookTag"); len(q) != 0 {
		t.Errorf("the join rows were read apart: %q", q)
	}
	// A limit over every book's count: every tag, in the relation's order.
	log.reset()
	bs, err = db.Query[dBook]().OrderBy(dBooks.ID.Asc()).With(dBooks.Tags.LimitEach(5).OrderBy(dTags.Name.Desc())).Limit(10).All(ctx)
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

// TestLimitEachScoped is Review Focus 1: a row the tenant scope or the Read
// policy hides takes no rank inside the window, so every owner still gets n
// rows when it has them.
func TestLimitEachScoped(t *testing.T) {
	db, _ := eachDB(t)
	acme := velox.WithTenant(context.Background(), int64(1))
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
	bs, err := db.Query[dBook]().OrderBy(dBooks.ID.Asc()).With(dBooks.Tags.LimitEach(1).OrderBy(dTags.Name.Asc())).Limit(10).All(acme)
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
