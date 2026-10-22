package itest

import (
	"context"
	"testing"

	"github.com/syssam/velox2"
)

// throughSetup creates books 1–4, tags 1 "go" and 2 "db" of org 1 and 3 "x"
// of org 2, and, with velox's Insert, the join rows: book 1 has tags 1 and 2,
// book 2 has tag 2, book 3 none and book 4 tag 3.
func throughSetup(t *testing.T, tg target) {
	t.Helper()
	tg.exec(t, "DROP TABLE IF EXISTS book_tags")
	tg.exec(t, "DROP TABLE IF EXISTS tags")
	tg.exec(t, "DROP TABLE IF EXISTS books")
	tg.exec(t, "CREATE TABLE books (id BIGINT PRIMARY KEY, title VARCHAR(64) NOT NULL)")
	tg.exec(t, "CREATE TABLE tags (id BIGINT PRIMARY KEY, org_id BIGINT NOT NULL, name VARCHAR(64) NOT NULL)")
	tg.exec(t, "CREATE TABLE book_tags (book_id BIGINT NOT NULL, tag_id BIGINT NOT NULL, PRIMARY KEY (book_id, tag_id))")
	for id := range int64(4) {
		tg.exec(t, "INSERT INTO books (id, title) VALUES (?, ?)", id+1, "book")
	}
	for _, r := range []struct {
		id, org int64
		name    string
	}{{1, 1, "go"}, {2, 1, "db"}, {3, 2, "x"}} {
		tg.exec(t, "INSERT INTO tags (id, org_id, name) VALUES (?, ?, ?)", r.id, r.org, r.name)
	}
	if err := tg.db.Insert(&bookTag{BookID: 1, TagID: 1}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tg.db.InsertAll([]bookTag{{1, 2}, {2, 2}, {4, 3}}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

// bookTags loads every book's tags through rel, by book id.
func bookTags(t *testing.T, tg target, qctx context.Context, rel velox.Rel[book, tag]) map[int64][]string {
	t.Helper()
	bs, err := tg.db.Query[book]().With(rel).OrderBy(Books.ID.Asc()).Limit(10).All(qctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64][]string{}
	for _, b := range bs {
		tags, ok := b.Tags.Get()
		if !ok || tags == nil {
			t.Fatalf("book %d: tags not loaded", b.ID)
		}
		names := []string{}
		for _, g := range tags {
			names = append(names, g.Name)
		}
		got[b.ID] = names
	}
	return got
}

func wantTags(t *testing.T, got, want map[int64][]string) {
	t.Helper()
	for id := int64(1); id <= 4; id++ {
		wantStrings(t, got[id], want[id])
	}
}

func TestThroughWith(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		throughSetup(t, tg)
		wantTags(t, bookTags(t, tg, ctx, Books.Tags), map[int64][]string{1: {"go", "db"}, 2: {"db"}, 3: {}, 4: {"x"}})
		wantTags(t, bookTags(t, tg, ctx, Books.Tags.OrderBy(Tags.Name.Asc())), map[int64][]string{1: {"db", "go"}, 2: {"db"}, 3: {}, 4: {"x"}})
		wantTags(t, bookTags(t, tg, ctx, Books.Tags.Where(Tags.Name.NEQ("go"))), map[int64][]string{1: {"db"}, 2: {"db"}, 3: {}, 4: {"x"}})
		// Org 1 sees none of book 4's tags: the target's tenant applies.
		wantTags(t, bookTags(t, tg, velox.WithTenant(ctx, int64(1)), Books.Tags), map[int64][]string{1: {"go", "db"}, 2: {"db"}, 3: {}, 4: {}})

		// Nested: each tag's books, through the join the other way.
		bs, err := tg.db.Query[book]().Where(Books.ID.EQ(1)).With(Books.Tags.With(Tags.Books.OrderBy(Books.ID.Desc()))).Limit(1).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		tags, _ := bs[0].Tags.Get()
		if len(tags) != 2 || tags[1].Name != "db" {
			t.Fatalf("tags %+v", tags)
		}
		dbBooks, ok := tags[1].Books.Get()
		if !ok {
			t.Fatal("the tag's books are not loaded")
		}
		var back []int64
		for _, b := range dbBooks {
			back = append(back, b.ID)
		}
		wantIDs(t, back, []int64{2, 1})

		// A join row is deleted like any entity's row.
		n, err := tg.db.Delete[bookTag]().Where(BookTags.BookID.EQ(1), BookTags.TagID.EQ(2)).Exec(ctx)
		if err != nil || n != 1 {
			t.Fatalf("delete %d, %v", n, err)
		}
		wantTags(t, bookTags(t, tg, ctx, Books.Tags), map[int64][]string{1: {"go"}, 2: {"db"}, 3: {}, 4: {"x"}})
	})
}

func TestThroughHas(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		throughSetup(t, tg)
		count := func(c velox.Pred[book], tenant ...int64) int64 {
			t.Helper()
			qctx := ctx
			if len(tenant) > 0 {
				qctx = velox.WithTenant(ctx, tenant[0])
			}
			n, err := tg.db.Query[book]().Where(c).Count(qctx)
			if err != nil {
				t.Fatal(err)
			}
			return n
		}
		for _, c := range []struct {
			name   string
			p      velox.Pred[book]
			tenant []int64
			want   int64
		}{
			{"has", Books.Tags.Has(), nil, 3},
			{"has, org 1", Books.Tags.Has(), []int64{1}, 2},
			{"has db", Books.Tags.HasWhere(Tags.Name.EQ("db")), nil, 2},
			{"has none", velox.Not(Books.Tags.Has()), nil, 1},
			{"has a tag of book 2's", Books.Tags.HasWhere(Tags.Books.HasWhere(Books.ID.EQ(2))), nil, 2},
		} {
			if got := count(c.p, c.tenant...); got != c.want {
				t.Errorf("%s: %d, want %d", c.name, got, c.want)
			}
		}
	})
}

// TestOfThroughOnRealDatabases: the tags of books, and the books of tags,
// through Of are what With loads for the same owners, the target's tenant
// included.
func TestOfThroughOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		throughSetup(t, tg)
		tagNames := func(qctx context.Context, owners ...*book) []string {
			t.Helper()
			got, err := tg.db.Query[tag]().Where(Books.Tags.Of(owners...)).OrderBy(Tags.ID.Asc()).Limit(10).All(qctx)
			if err != nil {
				t.Fatal(err)
			}
			names := []string{}
			for _, g := range got {
				names = append(names, g.Name)
			}
			return names
		}
		for _, qctx := range []context.Context{ctx, velox.WithTenant(ctx, int64(1))} {
			loaded := bookTags(t, tg, qctx, Books.Tags)
			for id := int64(1); id <= 4; id++ {
				wantStrings(t, tagNames(qctx, &book{ID: id}), loaded[id])
			}
		}
		// Tag 2 is book 1's and book 2's, and comes once.
		wantStrings(t, tagNames(ctx, &book{ID: 1}, &book{ID: 2}), []string{"go", "db"})
		wantStrings(t, tagNames(ctx), []string{})

		// The books of each tag, against With the other way.
		tags, err := tg.db.Query[tag]().With(Tags.Books).OrderBy(Tags.ID.Asc()).Limit(10).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for i := range tags {
			loaded, _ := tags[i].Books.Get()
			var want []int64
			for _, b := range loaded {
				want = append(want, b.ID)
			}
			got, err := tg.db.Query[book]().Where(Tags.Books.Of(&tags[i])).OrderBy(Books.ID.Asc()).Limit(10).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var gotIDs []int64
			for _, b := range got {
				gotIDs = append(gotIDs, b.ID)
			}
			wantIDs(t, gotIDs, want)
		}

		// A write on the target through Of: its EXISTS reads the join table,
		// not the table being changed, so MySQL takes it too.
		n, err := tg.db.Update[tag]().Set(Tags.Name.Set("db2")).Where(Books.Tags.Of(&book{ID: 2})).Exec(ctx)
		if err != nil || n != 1 {
			t.Fatalf("update through Of: %d, %v", n, err)
		}
		wantStrings(t, tagNames(ctx, &book{ID: 1}), []string{"go", "db2"})
	})
}
