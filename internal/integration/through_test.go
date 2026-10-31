package integration

import (
	"context"
	"errors"
	"reflect"
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
func bookTags(t *testing.T, qctx context.Context, tg target, rel velox.Rel[book, tag]) map[int64][]string {
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
		wantTags(t, bookTags(t, ctx, tg, Books.Tags), map[int64][]string{1: {"go", "db"}, 2: {"db"}, 3: {}, 4: {"x"}})
		wantTags(t, bookTags(t, ctx, tg, Books.Tags.OrderBy(Tags.Name.Asc())), map[int64][]string{1: {"db", "go"}, 2: {"db"}, 3: {}, 4: {"x"}})
		wantTags(t, bookTags(t, ctx, tg, Books.Tags.Where(Tags.Name.NEQ("go"))), map[int64][]string{1: {"db"}, 2: {"db"}, 3: {}, 4: {"x"}})
		// Org 1 sees none of book 4's tags: the target's tenant applies.
		wantTags(t, bookTags(t, velox.WithTenant(ctx, int64(1)), tg, Books.Tags), map[int64][]string{1: {"go", "db"}, 2: {"db"}, 3: {}, 4: {}})

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
		wantTags(t, bookTags(t, ctx, tg, Books.Tags), map[int64][]string{1: {"go"}, 2: {"db"}, 3: {}, 4: {"x"}})
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
			loaded := bookTags(t, qctx, tg, Books.Tags)
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

// TestLimitEachThroughOnRealDatabases pages a many-to-many relation per
// owner, in one statement that joins the join table. To throughSetup's tags
// it adds 4 "aa" of org 2 and 5 "go" of org 1, both on book 1: "aa" sorts
// first and is another tenant's, and the two "go" tie, and break by id.
func TestLimitEachThroughOnRealDatabases(t *testing.T) {
	log := &stmtLog{n: map[string]int{}}
	each(t, func(t *testing.T, tg target) {
		throughSetup(t, tg)
		tg.exec(t, "INSERT INTO tags (id, org_id, name) VALUES (?, ?, ?)", int64(4), int64(2), "aa")
		tg.exec(t, "INSERT INTO tags (id, org_id, name) VALUES (?, ?, ?)", int64(5), int64(1), "go")
		tg.exec(t, "INSERT INTO book_tags (book_id, tag_id) VALUES (?, ?)", int64(1), int64(4))
		tg.exec(t, "INSERT INTO book_tags (book_id, tag_id) VALUES (?, ?)", int64(1), int64(5))
		tagIDs := func(qctx context.Context, rel velox.Rel[book, tag], ps ...velox.Pred[book]) map[int64][]int64 {
			t.Helper()
			log.take()
			bs, err := tg.db.Query[book]().Where(ps...).OrderBy(Books.ID.Asc()).With(rel).Limit(10).All(qctx)
			if err != nil {
				t.Fatal(err)
			}
			if n := log.take(); n["integration.tag"] != 1 || n["integration.bookTag"] != 0 {
				t.Errorf("statements %v, want one for the tags and none for the join rows", n)
			}
			got := map[int64][]int64{}
			for _, b := range bs {
				tags, ok := b.Tags.Get()
				if !ok || tags == nil {
					t.Fatalf("book %d: tags not loaded", b.ID)
				}
				got[b.ID] = []int64{}
				for _, g := range tags {
					got[b.ID] = append(got[b.ID], g.ID)
				}
			}
			return got
		}
		check := func(got, want map[int64][]int64) {
			t.Helper()
			for id, w := range want {
				wantIDs(t, got[id], w)
			}
			if len(got) != len(want) {
				t.Errorf("owners %v, want %v", got, want)
			}
		}
		acme := velox.WithTenant(ctx, int64(1))
		byName := Books.Tags.OrderBy(Tags.Name.Asc())
		// Tag 2 is books 1's and 2's, and the first of both.
		check(tagIDs(acme, byName.LimitEach(1)), map[int64][]int64{1: {2}, 2: {2}, 3: {}, 4: {}})
		check(tagIDs(acme, byName.LimitEach(2)), map[int64][]int64{1: {2, 1}, 2: {2}, 3: {}, 4: {}})
		check(tagIDs(acme, byName.LimitEach(4)), map[int64][]int64{1: {2, 1, 5}, 2: {2}, 3: {}, 4: {}})
		check(tagIDs(acme, Books.Tags.OrderBy(Tags.Name.Desc()).LimitEach(2)), map[int64][]int64{1: {1, 5}, 2: {2}, 3: {}, 4: {}})
		check(tagIDs(acme, byName.LimitEach(2), Books.ID.EQ(1)), map[int64][]int64{1: {2, 1}})
		// Every tenant: "aa" ranks first on book 1, and "x" is book 4's.
		check(tagIDs(ctx, byName.LimitEach(1)), map[int64][]int64{1: {4}, 2: {2}, 3: {}, 4: {3}})
		// The relation's condition is inside the window too.
		check(tagIDs(ctx, byName.Where(Tags.Name.NEQ("aa")).LimitEach(1)), map[int64][]int64{1: {2}, 2: {2}, 3: {}, 4: {3}})
	}, velox.WithAround(log.around))
}

// TestEachThroughRepeatedLinksOnRealDatabases: a link that repeats in a join
// table whose key is an id of its own relates its target to the owner once,
// in a per-owner page and a count as in a plain With. Shelf 1 links tag 1
// "go" twice and tag 2 "db" once, and tag 3 "x" of org 2; shelf 2 links tag 2
// twice; shelf 3 none.
func TestEachThroughRepeatedLinksOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		throughSetup(t, tg)
		shelfSetup(t, tg)
		tg.exec(t, "DROP TABLE IF EXISTS shelf_tags")
		tg.exec(t, "CREATE TABLE shelf_tags (id BIGINT PRIMARY KEY, shelf_id BIGINT NOT NULL, tag_id BIGINT NOT NULL)")
		for i, r := range [][2]int64{{1, 1}, {1, 1}, {1, 2}, {1, 3}, {2, 2}, {2, 2}} {
			tg.exec(t, "INSERT INTO shelf_tags (id, shelf_id, tag_id) VALUES (?, ?, ?)", int64(i+1), r[0], r[1])
		}
		acme := velox.WithTenant(ctx, int64(1))
		owners := shelves(1, 2, 3)
		byName := Shelves.Tags.OrderBy(Tags.Name.Asc())
		for _, c := range []struct {
			qctx context.Context
			r    velox.Rel[shelf, tag]
			want [][]int64
		}{
			{acme, byName.LimitEach(2), [][]int64{{2, 1}, {2}, {}}},
			{acme, byName, [][]int64{{2, 1}, {2}, {}}},
			{ctx, byName.LimitEach(3), [][]int64{{2, 1, 3}, {2}, {}}},
		} {
			got, err := velox.LoadEach(c.qctx, tg.db.Query[tag](), c.r, owners)
			if err != nil {
				t.Fatal(err)
			}
			wantIDLists(t, tagIDLists(got), c.want)
			// A With load of the same relation agrees.
			ss, err := tg.db.Query[shelf]().Where(Shelves.ID.In(1, 2, 3)).OrderBy(Shelves.ID.Asc()).With(c.r).Limit(10).All(c.qctx)
			if err != nil {
				t.Fatal(err)
			}
			var with [][]tag
			for _, s := range ss {
				ts, _ := s.Tags.Get()
				with = append(with, ts)
			}
			wantIDLists(t, tagIDLists(with), c.want)
		}
		n, err := velox.CountEach(acme, tg.db.Query[tag](), Shelves.Tags, owners)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, n, []int64{2, 1, 0})
		if n, err = velox.CountEach(ctx, tg.db.Query[tag](), Shelves.Tags, owners); err != nil {
			t.Fatal(err)
		}
		wantIDs(t, n, []int64{3, 1, 0})
	})
}

// TestBidiOnRealDatabases: a link of the bidirectional edge Friends is two
// join rows, so each person's With load sees the other, and a delete of one
// direction removes both; a self-link is one row.
func TestBidiOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		tg.exec(t, "DROP TABLE IF EXISTS person_friends")
		tg.exec(t, "DROP TABLE IF EXISTS persons")
		tg.exec(t, "CREATE TABLE persons (id BIGINT PRIMARY KEY, name VARCHAR(64) NOT NULL, person_spouse BIGINT)")
		tg.exec(t, "CREATE TABLE person_friends (node_id BIGINT NOT NULL, friend_id BIGINT NOT NULL, PRIMARY KEY (node_id, friend_id))")
		for id := range int64(3) {
			tg.exec(t, "INSERT INTO persons (id, name) VALUES (?, ?)", id+1, "p")
		}
		if err := tg.db.Insert(&personFriend{1, 2}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		ps, err := tg.db.Query[person]().Where(Persons.ID.In(1, 2)).With(Persons.Friends).OrderBy(Persons.ID.Asc()).Limit(2).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for i, want := range []int64{2, 1} {
			fs, _ := ps[i].Friends.Get()
			var got []int64
			for _, f := range fs {
				got = append(got, f.ID)
			}
			wantIDs(t, got, []int64{want})
		}
		count := func() int64 {
			t.Helper()
			n, err := tg.db.Query[personFriend]().Count(ctx)
			if err != nil {
				t.Fatal(err)
			}
			return n
		}
		if n := count(); n != 2 {
			t.Fatalf("%d join rows, want 2", n)
		}
		if _, err := tg.db.Delete[personFriend]().Where(PersonFriends.NodeID.EQ(2), PersonFriends.FriendID.In(1)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if n := count(); n != 0 {
			t.Fatalf("%d join rows after the delete, want 0", n)
		}
		if err := tg.db.Insert(&personFriend{3, 3}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if n := count(); n != 1 {
			t.Fatalf("%d join rows for a self-link, want 1", n)
		}

		// The same link twice in one InsertAll conflicts with itself, and
		// writes nothing; with DoNothing it is one link.
		tg.exec(t, "DELETE FROM person_friends")
		F := PersonFriends
		if err := tg.db.InsertAll([]personFriend{{1, 2}, {2, 1}}).Exec(ctx); err == nil {
			t.Fatal("InsertAll of a link both ways without DoNothing succeeded")
		}
		if n := count(); n != 0 {
			t.Fatalf("%d join rows after the conflict, want 0", n)
		}
		if err := tg.db.InsertAll([]personFriend{{1, 2}, {2, 1}}).OnConflict(F.NodeID, F.FriendID).DoNothing().Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if n := count(); n != 2 {
			t.Fatalf("%d join rows after DoNothing, want 2", n)
		}

		// A half-link, one row, is repaired by an insert with DoNothing.
		tg.exec(t, "DELETE FROM person_friends")
		tg.exec(t, "INSERT INTO person_friends (node_id, friend_id) VALUES (?, ?)", 2, 1)
		if err := tg.db.Insert(&personFriend{1, 2}).Exec(ctx); err == nil {
			t.Fatal("an insert onto a half-link without DoNothing succeeded")
		}
		if err := tg.db.Insert(&personFriend{1, 2}).OnConflict(F.NodeID, F.FriendID).DoNothing().Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if n := count(); n != 2 {
			t.Fatalf("%d join rows after the repair, want 2", n)
		}
	})
}

// TestBidiOneOnRealDatabases: the bidirectional one-to-one edge Spouse keeps
// both partners' keys pointing at each other. A write that would take a
// partner that already has one fails, and leaves every row as it was: the
// key's unique index refuses it, or, when one row's key points one way, the
// link step does, and the transaction undoes the clearing of the old
// partner. A delete leaves the partner to ON DELETE SET NULL.
func TestBidiOneOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		if tg.d == velox.SQLite {
			// The target's one connection enforces foreign keys only when told.
			tg.exec(t, "PRAGMA foreign_keys = ON")
			t.Cleanup(func() { tg.exec(t, "PRAGMA foreign_keys = OFF") })
		}
		tg.exec(t, "DROP TABLE IF EXISTS person_friends")
		tg.exec(t, "DROP TABLE IF EXISTS persons")
		tg.exec(t, "CREATE TABLE persons (id BIGINT PRIMARY KEY, name VARCHAR(64) NOT NULL, person_spouse BIGINT, "+
			"UNIQUE (person_spouse), FOREIGN KEY (person_spouse) REFERENCES persons (id) ON DELETE SET NULL)")
		for id := range int64(4) {
			tg.exec(t, "INSERT INTO persons (id, name) VALUES (?, ?)", id+1, "p")
		}
		P := Persons
		// spouses reads every person's key: 0 for none.
		spouses := func(db *velox.DB) map[int64]int64 {
			t.Helper()
			ps, err := db.Query[person]().OrderBy(P.ID.Asc()).Limit(20).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			m := make(map[int64]int64, len(ps))
			for _, p := range ps {
				m[p.ID] = 0
				if p.SpouseID != nil {
					m[p.ID] = *p.SpouseID
				}
			}
			return m
		}
		want := func(step string, w map[int64]int64) {
			t.Helper()
			if got := spouses(tg.db); !reflect.DeepEqual(got, w) {
				t.Fatalf("%s: spouses %v, want %v", step, got, w)
			}
		}
		save := func(db *velox.DB, id int64, spouse *int64) error {
			return db.Save(&person{ID: id, SpouseID: spouse}, P.SpouseID).Exec(ctx)
		}
		taken := func(step string, err error) {
			t.Helper()
			var ce *velox.ConstraintError
			if !errors.As(err, &ce) || ce.Kind != velox.ConstraintUnique {
				t.Fatalf("%s: err = %v, want a unique ConstraintError", step, err)
			}
		}

		if err := tg.db.Insert(&person{ID: 5, Name: "p", SpouseID: new(int64(1))}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		want("insert", map[int64]int64{1: 5, 2: 0, 3: 0, 4: 0, 5: 1})
		if err := save(tg.db, 5, new(int64(2))); err != nil {
			t.Fatal(err)
		}
		want("re-point", map[int64]int64{1: 0, 2: 5, 3: 0, 4: 0, 5: 2})
		if err := save(tg.db, 3, new(int64(4))); err != nil {
			t.Fatal(err)
		}
		// 4 has a partner, 3, whose key the unique index keeps 4's.
		taken("a taken partner", save(tg.db, 5, new(int64(4))))
		want("a taken partner", map[int64]int64{1: 0, 2: 5, 3: 4, 4: 3, 5: 2})

		// 6's key points at 7 one way: no row's key is 6, so the caller's
		// UPDATE passes and the old partner 2 is cleared before the link
		// finds 6 taken; the rollback restores 2.
		for _, id := range []int64{6, 7} {
			if err := tg.db.Insert(&person{ID: id, Name: "p"}).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		tg.exec(t, "UPDATE persons SET person_spouse = ? WHERE id = ?", 7, 6)
		oneWay := map[int64]int64{1: 0, 2: 5, 3: 4, 4: 3, 5: 2, 6: 7, 7: 0}
		taken("a one-way key", save(tg.db, 5, new(int64(6))))
		want("a one-way key", oneWay)
		err := tg.db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			taken("a one-way key in a Tx", save(tx, 5, new(int64(6))))
			p, err := tx.Query[person]().Where(P.ID.EQ(2)).First(ctx)
			if err != nil {
				return err
			}
			if p.SpouseID == nil || *p.SpouseID != 5 {
				t.Errorf("in the Tx, after the savepoint's rollback, 2's spouse is %v, want 5", p.SpouseID)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		want("a one-way key in a Tx", oneWay)

		if err := save(tg.db, 5, nil); err != nil {
			t.Fatal(err)
		}
		want("clear", map[int64]int64{1: 0, 2: 0, 3: 4, 4: 3, 5: 0, 6: 7, 7: 0})
		if _, err := tg.db.Delete[person]().Where(P.ID.EQ(3)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		want("delete", map[int64]int64{1: 0, 2: 0, 4: 0, 5: 0, 6: 7, 7: 0})
	})
}
