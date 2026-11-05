package apitest

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

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

// TestThroughLoadSharedAndEmpty loads a many-to-many relation: tag 2 is
// shared by books 1 and 2, and book 3 has none.
func TestThroughLoadSharedAndEmpty(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{
		{{int64(1), int64(1), "a"}, {int64(2), int64(1), "b"}, {int64(3), int64(1), "c"}},
		{{int64(1), int64(1)}, {int64(1), int64(2)}, {int64(2), int64(2)}},
		{{int64(1), int64(7), "go"}, {int64(2), int64(7), "db"}},
	}}
	db := openFake(velox.Postgres, e)
	bs, err := db.Query[fixture.DBook]().With(fixture.DBooks.Tags).Limit(3).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"go", "db"}, {"db"}, {}}
	for i, b := range bs {
		if got := tagNames(t, b.Tags); !reflect.DeepEqual(got, want[i]) {
			t.Errorf("book %d: tags %v, want %v", b.ID, got, want[i])
		}
	}
	if tags, ok := bs[2].Tags.Get(); !ok || tags == nil || len(tags) != 0 {
		t.Errorf("book 3: %v %v", tags, ok)
	}
	if want := `SELECT "book_id", "tag_id" FROM "book_tags" WHERE "book_id" IN ($1, $2, $3) ORDER BY "book_id", "tag_id"`; e.Log[1] != want {
		t.Errorf("join SQL %s", e.Log[1])
	}
	if want := `SELECT "id", "org_id", "name" FROM "tags" WHERE "id" IN ($1, $2) ORDER BY "id"`; e.Log[2] != want {
		t.Errorf("target SQL %s", e.Log[2])
	}
	if want := []any{int64(1), int64(2)}; !reflect.DeepEqual(e.Args, want) {
		t.Errorf("target args %v, want %v", e.Args, want)
	}
}

// TestThroughLoadOwnersDoNotShareRows: a target of two owners is in both
// owners' slices, but not in the same memory; an append to one leaves the
// next as it was.
func TestThroughLoadOwnersDoNotShareRows(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{
		{{int64(1), int64(1), "a"}, {int64(2), int64(1), "b"}, {int64(3), int64(1), "c"}},
		{{int64(1), int64(1)}, {int64(1), int64(2)}, {int64(2), int64(2)}, {int64(3), int64(1)}},
		{{int64(1), int64(7), "go"}, {int64(2), int64(7), "db"}},
	}}
	db := openFake(velox.Postgres, e)
	bs, err := db.Query[fixture.DBook]().With(fixture.DBooks.Tags).Limit(3).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := bs[0].Tags.Get()
	first[0].Name, first[1].Name = "changed", "changed"
	_ = append(first, fixture.DTag{Name: "appended"})
	if got := tagNames(t, bs[1].Tags); !reflect.DeepEqual(got, []string{"db"}) {
		t.Errorf("book 2: tags %v, want [db]", got)
	}
	if got := tagNames(t, bs[2].Tags); !reflect.DeepEqual(got, []string{"go"}) {
		t.Errorf("book 3: tags %v, want [go]", got)
	}
}

// TestThroughLoadAppliesTheOptions: the relation's Where and OrderBy apply to
// the targets, each owner's targets come in their fetched order, and a join
// row repeated does not repeat a target.
func TestThroughLoadAppliesTheOptions(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{
		{{int64(1), int64(1), "a"}},
		{{int64(1), int64(1)}, {int64(1), int64(2)}, {int64(1), int64(2)}},
		{{int64(2), int64(7), "go"}, {int64(1), int64(7), "db"}},
	}}
	db := openFake(velox.Postgres, e)
	bs, err := db.Query[fixture.DBook]().With(fixture.DBooks.Tags.Where(fixture.DTags.Name.NEQ("x")).OrderBy(fixture.DTags.Name.Desc())).Limit(1).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := tagNames(t, bs[0].Tags); !reflect.DeepEqual(got, []string{"go", "db"}) {
		t.Errorf("tags %v", got)
	}
	if want := `SELECT "id", "org_id", "name" FROM "tags" WHERE "id" IN ($1, $2) AND "name" <> $3 ORDER BY "name" DESC`; e.Log[2] != want {
		t.Errorf("target SQL %s", e.Log[2])
	}
	if want := []any{int64(1), int64(2), "x"}; !reflect.DeepEqual(e.Args, want) {
		t.Errorf("target args %v", e.Args)
	}
}

// TestThroughLoadWithoutJoinRows fetches no targets when no owner has a join
// row, and still loads every owner's relation, empty.
func TestThroughLoadWithoutJoinRows(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{{{int64(1), int64(1), "a"}}, {}}}
	db := openFake(velox.Postgres, e)
	bs, err := db.Query[fixture.DBook]().With(fixture.DBooks.Tags).Limit(1).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := tagNames(t, bs[0].Tags); len(got) != 0 {
		t.Errorf("tags %v", got)
	}
	if len(e.Log) != 2 {
		t.Errorf("queries %q", e.Log)
	}
}

// TestThroughLoadNested loads the targets' own relation: each tag's books.
func TestThroughLoadNested(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{
		{{int64(1), int64(1), "a"}},
		{{int64(1), int64(2)}},
		{{int64(2), int64(7), "db"}},
		{{int64(1), int64(2)}, {int64(2), int64(2)}},
		{{int64(1), int64(1), "a"}, {int64(2), int64(1), "b"}},
	}}
	db := openFake(velox.Postgres, e)
	bs, err := db.Query[fixture.DBook]().With(fixture.DBooks.Tags.With(fixture.DTags.Books)).Limit(1).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tags, _ := bs[0].Tags.Get()
	books, ok := tags[0].Books.Get()
	if !ok || len(books) != 2 || books[1].Title != "b" {
		t.Fatalf("the tag's books %+v %v", books, ok)
	}
	if want := `SELECT "book_id", "tag_id" FROM "book_tags" WHERE "tag_id" IN ($1) ORDER BY "book_id", "tag_id"`; e.Log[3] != want {
		t.Errorf("nested join SQL %s", e.Log[3])
	}
}

func TestThroughHasRenders(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	for _, c := range []struct {
		p    velox.Pred[fixture.DBook]
		want string
	}{
		{fixture.DBooks.Tags.HasWhere(fixture.DTags.Name.EQ("go")),
			`SELECT "id", "author_id", "title" FROM "books" WHERE EXISTS (SELECT 1 FROM "book_tags" AS "vx1" WHERE "vx1"."book_id" = "books"."id" AND EXISTS (SELECT 1 FROM "tags" AS "vx2" WHERE "vx2"."id" = "vx1"."tag_id" AND "name" = $1)) LIMIT $2`},
		{fixture.DBooks.Tags.Has(),
			`SELECT "id", "author_id", "title" FROM "books" WHERE EXISTS (SELECT 1 FROM "book_tags" AS "vx1" WHERE "vx1"."book_id" = "books"."id" AND EXISTS (SELECT 1 FROM "tags" AS "vx2" WHERE "vx2"."id" = "vx1"."tag_id")) LIMIT $1`},
		// Nested: the tag's books correlate with the innermost alias, vx2.
		{fixture.DBooks.Tags.HasWhere(fixture.DTags.Books.HasWhere(fixture.DBooks.Title.EQ("dune"))),
			`SELECT "id", "author_id", "title" FROM "books" WHERE EXISTS (SELECT 1 FROM "book_tags" AS "vx1" WHERE "vx1"."book_id" = "books"."id" AND EXISTS (SELECT 1 FROM "tags" AS "vx2" WHERE "vx2"."id" = "vx1"."tag_id" AND EXISTS (SELECT 1 FROM "book_tags" AS "vx3" WHERE "vx3"."tag_id" = "vx2"."id" AND EXISTS (SELECT 1 FROM "books" AS "vx4" WHERE "vx4"."id" = "vx3"."book_id" AND "title" = $1)))) LIMIT $2`},
		// After a many-to-many EXISTS, the outer table's conditions are its own again.
		{velox.And(fixture.DBooks.Tags.Has(), fixture.DBooks.Title.EQ("dune")),
			`SELECT "id", "author_id", "title" FROM "books" WHERE (EXISTS (SELECT 1 FROM "book_tags" AS "vx1" WHERE "vx1"."book_id" = "books"."id" AND EXISTS (SELECT 1 FROM "tags" AS "vx2" WHERE "vx2"."id" = "vx1"."tag_id")) AND "title" = $1) LIMIT $2`},
	} {
		got, _, err := db.Query[fixture.DBook]().Where(c.p).Limit(1).SQL(ctx)
		if err != nil || got != c.want {
			t.Errorf("got  %s, %v\nwant %s", got, err, c.want)
		}
	}
	got, _, err := openFake(velox.MySQL, &fakeconn.Exec{}).Query[fixture.DTag]().Where(fixture.DTags.Books.Has()).Limit(1).SQL(ctx)
	want := "SELECT `id`, `org_id`, `name` FROM `tags` WHERE EXISTS (SELECT 1 FROM `book_tags` AS `vx1` WHERE `vx1`.`tag_id` = `tags`.`id` AND EXISTS (SELECT 1 FROM `books` AS `vx2` WHERE `vx2`.`id` = `vx1`.`book_id`)) LIMIT ?"
	if err != nil || got != want {
		t.Errorf("the other way, on MySQL:\n got %s, %v\nwant %s", got, err, want)
	}
}

// TestThroughHasScopesTarget is Review Focus 5: the target's org condition
// lands inside the inner EXISTS, on the target, not on the join or the owner.
func TestThroughHasScopesTarget(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	got, args, err := db.Query[fixture.DBook]().Where(fixture.DBooks.Tags.HasWhere(fixture.DTags.Name.EQ("go"))).Limit(1).SQL(testmodel.Org.With(context.Background(), int64(7)))
	want := `SELECT "id", "author_id", "title" FROM "books" WHERE EXISTS (SELECT 1 FROM "book_tags" AS "vx1" WHERE "vx1"."book_id" = "books"."id" AND EXISTS (SELECT 1 FROM "tags" AS "vx2" WHERE "vx2"."id" = "vx1"."tag_id" AND "name" = $1 AND "org_id" = $2)) LIMIT $3`
	if err != nil || got != want {
		t.Fatalf("got  %s, %v\nwant %s", got, err, want)
	}
	if !reflect.DeepEqual(args, []any{"go", int64(7), 1}) {
		t.Errorf("args %v", args)
	}
}

// TestThroughCacheKey: two many-to-many relations of one entity render
// different SQL through one DB's render cache, either one first.
func TestThroughCacheKey(t *testing.T) {
	for _, order := range [][2]velox.Rel[fixture.DBook, fixture.DTag]{{fixture.DBooks.Tags, fixture.DBooks.Picks}, {fixture.DBooks.Picks, fixture.DBooks.Tags}} {
		db := openFake(velox.Postgres, &fakeconn.Exec{})
		a, _, errA := db.Query[fixture.DBook]().Where(order[0].Has()).Limit(1).SQL(ctx)
		b, _, errB := db.Query[fixture.DBook]().Where(order[1].Has()).Limit(1).SQL(ctx)
		if errA != nil || errB != nil || a == b {
			t.Fatalf("one SQL for two relations: %s, %v, %v", a, errA, errB)
		}
		if !strings.Contains(a+b, `FROM "picks" AS "vx1"`) || !strings.Contains(a+b, `FROM "book_tags" AS "vx1"`) {
			t.Errorf("%s\n%s", a, b)
		}
	}
}

// TestOfThroughRenders: Of on a many-to-many relation is an EXISTS on the join
// entity, correlated with the target, holding the owners' keys; nested in a
// HasWhere, the correlation names the outer alias.
func TestOfThroughRenders(t *testing.T) {
	b1, b2 := fixture.DBook{ID: 1}, fixture.DBook{ID: 2}
	for _, c := range []struct {
		d    velox.Dialect
		want string
	}{
		{velox.Postgres, `SELECT "id", "org_id", "name" FROM "tags" WHERE EXISTS (SELECT 1 FROM "book_tags" AS "vx1" WHERE "vx1"."tag_id" = "tags"."id" AND "book_id" IN ($1, $2)) LIMIT $3`},
		{velox.MySQL, "SELECT `id`, `org_id`, `name` FROM `tags` WHERE EXISTS (SELECT 1 FROM `book_tags` AS `vx1` WHERE `vx1`.`tag_id` = `tags`.`id` AND `book_id` IN (?, ?)) LIMIT ?"},
		{velox.SQLite, `SELECT "id", "org_id", "name" FROM "tags" WHERE EXISTS (SELECT 1 FROM "book_tags" AS "vx1" WHERE "vx1"."tag_id" = "tags"."id" AND "book_id" IN (?, ?)) LIMIT ?`},
	} {
		got, args, err := openFake(c.d, &fakeconn.Exec{}).Query[fixture.DTag]().Where(fixture.DBooks.Tags.Of(&b1, &b2)).Limit(5).SQL(ctx)
		if err != nil || got != c.want {
			t.Errorf("%v:\n got %s, %v\nwant %s", c.d, got, err, c.want)
		}
		if want := []any{int64(1), int64(2), 5}; !reflect.DeepEqual(args, want) {
			t.Errorf("%v: args %v, want %v", c.d, args, want)
		}
	}
	// The other way, with the target's org at the top, outside the EXISTS.
	g := fixture.DTag{ID: 7}
	got, args, err := openFake(velox.Postgres, &fakeconn.Exec{}).Query[fixture.DBook]().Where(fixture.DTags.Books.Of(&g)).Limit(1).SQL(testmodel.Org.With(context.Background(), int64(3)))
	want := `SELECT "id", "author_id", "title" FROM "books" WHERE EXISTS (SELECT 1 FROM "book_tags" AS "vx1" WHERE "vx1"."book_id" = "books"."id" AND "tag_id" IN ($1)) LIMIT $2`
	if err != nil || got != want || !reflect.DeepEqual(args, []any{int64(7), 1}) {
		t.Errorf("tag to books:\n got %s %v, %v\nwant %s", got, args, err, want)
	}
	got, args, err = openFake(velox.Postgres, &fakeconn.Exec{}).Query[fixture.DTag]().Where(fixture.DTags.Name.EQ("go"), fixture.DBooks.Tags.Of(&b1)).Limit(1).SQL(testmodel.Org.With(context.Background(), int64(3)))
	want = `SELECT "id", "org_id", "name" FROM "tags" WHERE "name" = $1 AND EXISTS (SELECT 1 FROM "book_tags" AS "vx1" WHERE "vx1"."tag_id" = "tags"."id" AND "book_id" IN ($2)) AND "org_id" = $3 LIMIT $4`
	if err != nil || got != want || !reflect.DeepEqual(args, []any{"go", int64(1), int64(3), 1}) {
		t.Errorf("org:\n got %s %v, %v\nwant %s", got, args, err, want)
	}
	got, _, err = openFake(velox.Postgres, &fakeconn.Exec{}).Query[fixture.DBook]().Where(fixture.DBooks.Tags.HasWhere(fixture.DBooks.Picks.Of(&b2))).Limit(1).SQL(ctx)
	want = `SELECT "id", "author_id", "title" FROM "books" WHERE EXISTS (SELECT 1 FROM "book_tags" AS "vx1" WHERE "vx1"."book_id" = "books"."id" AND EXISTS (SELECT 1 FROM "tags" AS "vx2" WHERE "vx2"."id" = "vx1"."tag_id" AND EXISTS (SELECT 1 FROM "picks" AS "vx3" WHERE "vx3"."tag_id" = "vx2"."id" AND "book_id" IN ($1)))) LIMIT $2`
	if err != nil || got != want {
		t.Errorf("nested:\n got %s, %v\nwant %s", got, err, want)
	}
}

// TestOfSelfManyToMany: Of through a relation from an entity to itself names
// the join's far column in the correlation and its near column in the owners'
// IN, on every dialect.
func TestOfSelfManyToMany(t *testing.T) {
	skip := velox.WithoutPolicy(ctx, "self many-to-many test")
	p1, p2 := dPeer{ID: 1}, dPeer{ID: 2}
	for _, c := range []struct {
		d    velox.Dialect
		want string
	}{
		{velox.Postgres, `SELECT "id" FROM "peers" WHERE EXISTS (SELECT 1 FROM "friends" AS "vx1" WHERE "vx1"."friend_id" = "peers"."id" AND "peer_id" IN ($1, $2)) LIMIT $3`},
		{velox.MySQL, "SELECT `id` FROM `peers` WHERE EXISTS (SELECT 1 FROM `friends` AS `vx1` WHERE `vx1`.`friend_id` = `peers`.`id` AND `peer_id` IN (?, ?)) LIMIT ?"},
		{velox.SQLite, `SELECT "id" FROM "peers" WHERE EXISTS (SELECT 1 FROM "friends" AS "vx1" WHERE "vx1"."friend_id" = "peers"."id" AND "peer_id" IN (?, ?)) LIMIT ?`},
	} {
		db := openFake(c.d, &fakeconn.Exec{})
		got, args, err := db.Query[dPeer]().Where(dPeers.Friends.Of(&p1, &p2)).Limit(5).SQL(skip)
		if err != nil || got != c.want || !reflect.DeepEqual(args, []any{int64(1), int64(2), 5}) {
			t.Errorf("%v:\n got %s %v, %v\nwant %s", c.d, got, args, err, c.want)
		}
		// One owner is another shape from two, through the same cache.
		one, _, err := db.Query[dPeer]().Where(dPeers.Friends.Of(&p1)).Limit(5).SQL(skip)
		if err != nil || one == got {
			t.Errorf("%v: one owner renders as two: %s, %v", c.d, one, err)
		}
	}
}

// TestOfThroughCacheKey: Of on two relations through different join entities
// to one target, with as many owners, render different SQL through one DB's
// render cache, either one first.
func TestOfThroughCacheKey(t *testing.T) {
	b := fixture.DBook{ID: 1}
	for _, order := range [][2]velox.Rel[fixture.DBook, fixture.DTag]{{fixture.DBooks.Tags, fixture.DBooks.Picks}, {fixture.DBooks.Picks, fixture.DBooks.Tags}} {
		db := openFake(velox.Postgres, &fakeconn.Exec{})
		a, _, errA := db.Query[fixture.DTag]().Where(order[0].Of(&b)).Limit(1).SQL(ctx)
		c, _, errC := db.Query[fixture.DTag]().Where(order[1].Of(&b)).Limit(1).SQL(ctx)
		if errA != nil || errC != nil || a == c {
			t.Fatalf("one SQL for two relations: %s, %v, %v", a, errA, errC)
		}
		if !strings.Contains(a+c, `FROM "picks" AS "vx1"`) || !strings.Contains(a+c, `FROM "book_tags" AS "vx1"`) {
			t.Errorf("%s\n%s", a, c)
		}
	}
}

// TestThroughJoinRowsWrite: join rows are an entity's rows, written with
// Insert and Delete.
func TestThroughJoinRowsWrite(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	if err := db.Insert(&fixture.DBookTag{BookID: 1, TagID: 2}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `INSERT INTO "book_tags" ("book_id", "tag_id") VALUES ($1, $2)`; e.SQL != want || !reflect.DeepEqual(e.Args, []any{int64(1), int64(2)}) {
		t.Errorf("insert %s %v", e.SQL, e.Args)
	}
	n, err := db.Delete[fixture.DBookTag]().Where(dBookTags.BookID.EQ(1), dBookTags.TagID.EQ(2)).Exec(ctx)
	if err != nil || n != 1 {
		t.Fatalf("delete %d, %v", n, err)
	}
	if want := `DELETE FROM "book_tags" WHERE "book_id" = $1 AND "tag_id" = $2`; e.SQL != want || !reflect.DeepEqual(e.Args, []any{int64(1), int64(2)}) {
		t.Errorf("delete %s %v", e.SQL, e.Args)
	}
}

// dBadThrough holds many-to-many relations that cannot resolve.
type dBadThrough struct {
	Key      string
	ID       int64
	Mismatch velox.Many[fixture.DTag]
	JoinCol  velox.Many[fixture.DTag]
	Target   velox.Many[fixture.DAuthor]
	Single   velox.One[fixture.DTag]
	BadJoin  velox.Many[fixture.DTag]
	PolicyJ  velox.Many[fixture.DTag]
}

var dBadThroughs = struct {
	Mismatch, JoinCol velox.Rel[dBadThrough, fixture.DTag]
	Target            velox.Rel[dBadThrough, fixture.DAuthor]
	Single, BadJoin   velox.Rel[dBadThrough, fixture.DTag]
	PolicyJ           velox.Rel[dBadThrough, fixture.DTag]
}{velox.Rel[dBadThrough, fixture.DTag]{I: 0}, velox.Rel[dBadThrough, fixture.DTag]{I: 1}, velox.Rel[dBadThrough, fixture.DAuthor]{I: 2},
	velox.Rel[dBadThrough, fixture.DTag]{I: 3}, velox.Rel[dBadThrough, fixture.DTag]{I: 4}, velox.Rel[dBadThrough, fixture.DTag]{I: 5}}

var dBadThroughMeta velox.MetaOnce[dBadThrough]

func (*dBadThrough) VeloxMeta() *velox.Meta[dBadThrough] {
	return dBadThroughMeta.Load(func() velox.Descriptor[dBadThrough] {
		return velox.Descriptor[dBadThrough]{
			Table:      "bad",
			Columns:    []velox.ColumnDescriptor{{Field: "Key", Name: "key"}, {Field: "ID", Name: "id"}},
			PrimaryKey: []int{1},
			Edges: []velox.EdgeDescriptor{
				velox.EdgeThrough[fixture.DBookTag, fixture.DTag]("mismatch", 0, 0, 1, 0),  // a string owner key, an int64 join key
				velox.EdgeThrough[fixture.DBookTag, fixture.DTag]("join_col", 1, 2, 1, 0),  // the join has two columns
				velox.EdgeThrough[fixture.DBookTag, fixture.DAuthor]("target", 1, 0, 1, 2), // an int64 join key, a string target key
				velox.EdgeThrough[fixture.DBookTag, fixture.DTag]("single", 1, 0, 1, 0),    // a One
				velox.EdgeThrough[badDescriptor, fixture.DTag]("bad_join", 1, 0, 0, 0),     // the join entity is invalid
				velox.EdgeThrough[dPolicyJoin, fixture.DTag]("policy_j", 1, 0, 1, 0),       // the join entity has a Read policy
			},
			FieldPtr: func(x *dBadThrough, i int) any { return [...]any{&x.Key, &x.ID}[i] },
			EdgeFieldPtr: func(x *dBadThrough, i int) any {
				return [...]any{&x.Mismatch, &x.JoinCol, &x.Target, &x.Single, &x.BadJoin, &x.PolicyJ}[i]
			},
		}
	})
}

func TestThroughRefusals(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	for _, c := range []struct {
		p    velox.Pred[dBadThrough]
		want string
	}{
		{dBadThroughs.Mismatch.Has(), "velox: apitest.dBadThrough.mismatch: the key types differ: apitest.dBadThrough.Key is string and fixture.DBookTag.BookID is int64"},
		{dBadThroughs.JoinCol.Has(), "velox: apitest.dBadThrough.join_col: its key names column 2 of fixture.DBookTag, which has 2"},
		{dBadThroughs.Target.Has(), "velox: apitest.dBadThrough.target: the key types differ: fixture.DBookTag.TagID is int64 and fixture.DAuthor.Name is string"},
		{dBadThroughs.Single.Has(), "velox: apitest.dBadThrough.single: a many-to-many relation needs a Many field"},
		{dBadThroughs.BadJoin.Has(), "velox: apitest.dBadThrough.bad_join: its join entity is invalid: apitest.badDescriptor: the primary key names column 3, of 1"},
		{dBadThroughs.PolicyJ.Has(), "velox: apitest.dBadThrough.policy_j: its join entity apitest.dPolicyJoin has a Read policy; a join entity is read through its two sides, whose Read policies apply, and cannot have one of its own"},
	} {
		_, _, err := db.Query[dBadThrough]().Where(c.p).Limit(1).SQL(ctx)
		var se *velox.SchemaError
		if !errors.As(err, &se) || err.Error() != c.want {
			t.Errorf("got  %v\nwant %s", err, c.want)
		}
	}
}

// dPolicyJoin is a join entity with a Read policy, which a join entity cannot
// have: loading would scope its rows, and Has would not.
type dPolicyJoin struct{ BookID, TagID int64 }

var dPolicyJoinMeta velox.MetaOnce[dPolicyJoin]

func (*dPolicyJoin) VeloxMeta() *velox.Meta[dPolicyJoin] {
	return dPolicyJoinMeta.Load(func() velox.Descriptor[dPolicyJoin] {
		return velox.Descriptor[dPolicyJoin]{
			Policy:     []func() velox.Policy{(*dPolicyJoin)(nil).Policy},
			Table:      "policy_joins",
			Columns:    []velox.ColumnDescriptor{{Field: "BookID", Name: "book_id"}, {Field: "TagID", Name: "tag_id"}},
			PrimaryKey: []int{0, 1},
			FieldPtr: func(x *dPolicyJoin, i int) any {
				switch i {
				case 0:
					return &x.BookID
				case 1:
					return &x.TagID
				}
				return nil
			},
		}
	})
}

func (*dPolicyJoin) Policy() velox.Policy {
	return velox.Policy{Read: func(context.Context) (velox.Cond, error) { return velox.Allow(), nil }}
}

// dLinker has tags through dLink, a join entity whose Insert policy checks a
// link's endpoints, which is how an application guards a link it writes.
type dLinker struct {
	ID   int64
	Tags velox.Many[fixture.DTag]
}

var dLinkers = struct {
	ID   velox.NumberCol[dLinker, int64]
	Tags velox.Rel[dLinker, fixture.DTag]
}{velox.NumberCol[dLinker, int64]{I: 0}, velox.Rel[dLinker, fixture.DTag]{I: 0}}

var dLinkerMeta velox.MetaOnce[dLinker]

func (*dLinker) VeloxMeta() *velox.Meta[dLinker] {
	return dLinkerMeta.Load(func() velox.Descriptor[dLinker] {
		return velox.Descriptor[dLinker]{
			Table:        "linkers",
			Columns:      []velox.ColumnDescriptor{{Field: "ID", Name: "id"}},
			PrimaryKey:   []int{0},
			Edges:        []velox.EdgeDescriptor{velox.EdgeThrough[dLink, fixture.DTag]("tags", 0, 0, 1, 0)},
			FieldPtr:     func(x *dLinker, i int) any { return &x.ID },
			EdgeFieldPtr: func(x *dLinker, i int) any { return &x.Tags },
		}
	})
}

type dLink struct{ LinkerID, TagID int64 }

var dLinkMeta velox.MetaOnce[dLink]

func (*dLink) VeloxMeta() *velox.Meta[dLink] {
	return dLinkMeta.Load(func() velox.Descriptor[dLink] {
		return velox.Descriptor[dLink]{
			Policy:     []func() velox.Policy{(*dLink)(nil).Policy},
			Table:      "links",
			Columns:    []velox.ColumnDescriptor{{Field: "LinkerID", Name: "linker_id"}, {Field: "TagID", Name: "tag_id"}},
			PrimaryKey: []int{0, 1},
			FieldPtr: func(x *dLink, i int) any {
				switch i {
				case 0:
					return &x.LinkerID
				case 1:
					return &x.TagID
				}
				return nil
			},
		}
	})
}

// Policy refuses a link to tag 13, standing for an endpoint the caller may
// not see.
func (*dLink) Policy() velox.Policy {
	return velox.Policy{
		Insert: func(_ context.Context, r velox.Row) error {
			l := r.Entity().(*dLink)
			if l.TagID == 13 {
				return velox.Deny("not that tag")
			}
			return nil
		},
		Write: func(context.Context) (velox.Cond, error) { return velox.Allow(), nil },
	}
}

// TestThroughJoinEntityWithInsertPolicy: a join entity with Insert and Write
// policies is allowed. Its relation loads and answers Has, and the policy
// refuses a link written to an endpoint it rejects.
func TestThroughJoinEntityWithInsertPolicy(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{
		{{int64(1)}},
		{{int64(1), int64(7)}},
		{{int64(7), int64(1), "go"}},
	}}
	db := openFake(velox.Postgres, e)
	ls, err := db.Query[dLinker]().With(dLinkers.Tags).Limit(1).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := tagNames(t, ls[0].Tags); !reflect.DeepEqual(got, []string{"go"}) {
		t.Errorf("tags %v", got)
	}
	got, _, err := db.Query[dLinker]().Where(dLinkers.Tags.Has()).Limit(1).SQL(ctx)
	if want := `SELECT "id" FROM "linkers" WHERE EXISTS (SELECT 1 FROM "links" AS "vx1" WHERE "vx1"."linker_id" = "linkers"."id" AND EXISTS (SELECT 1 FROM "tags" AS "vx2" WHERE "vx2"."id" = "vx1"."tag_id")) LIMIT $1`; err != nil || got != want {
		t.Errorf("got  %s, %v\nwant %s", got, err, want)
	}
	e2 := &fakeconn.Exec{Affected: 1}
	db = openFake(velox.Postgres, e2)
	if err := db.Insert(&dLink{LinkerID: 1, TagID: 2}).Exec(context.Background()); err != nil || e2.Calls != 1 {
		t.Fatalf("an allowed link: %v, %d calls", err, e2.Calls)
	}
	err = db.Insert(&dLink{LinkerID: 1, TagID: 13}).Exec(context.Background())
	if !errors.Is(err, velox.ErrDenied) || e2.Calls != 1 {
		t.Fatalf("a refused link: %v, %d calls", err, e2.Calls)
	}
}

// TestThroughJoinRewriteKeepsKeys: a Rewrite hook may not leave either key
// column out of the join query; the keys would read as zero values.
func TestThroughJoinRewriteKeepsKeys(t *testing.T) {
	for kept, missing := range []string{"tag_id", "book_id"} {
		narrow := velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
			if st.Entity.Name == "fixture.DBookTag" {
				st.Columns = []int{kept}
			}
			return nil
		})
		e := &fakeconn.Exec{Results: [][][]any{{{int64(1), int64(1), "a"}}, {{int64(1)}}}}
		_, err := openFake(velox.Postgres, e, narrow).Query[fixture.DBook]().With(fixture.DBooks.Tags).Limit(1).All(ctx)
		// The plain relation's error reads the same (TestWithRewriteNarrowsColumns).
		want := "velox: fixture.DBookTag: a Rewrite hook left out the relation's key column " + missing
		if err == nil || !strings.HasPrefix(err.Error(), "velox: fixture.DBook: loading tags: ") || !strings.Contains(err.Error(), want) {
			t.Errorf("got  %v\nwant %s", err, want)
		}
	}
}

// TestThroughLoadMaxRows: the owner query's row cap covers the join query,
// and its MaxRows raises it there too.
func TestThroughLoadMaxRows(t *testing.T) {
	results := func() [][][]any {
		return [][][]any{
			{{int64(1), int64(1), "a"}},
			{{int64(1), int64(1)}, {int64(1), int64(2)}, {int64(1), int64(3)}},
			{{int64(1), int64(7), "a"}, {int64(2), int64(7), "b"}, {int64(3), int64(7), "c"}},
		}
	}
	db := openFake(velox.Postgres, &fakeconn.Exec{Results: results()}, velox.WithMaxRows(2))
	_, err := db.Query[fixture.DBook]().With(fixture.DBooks.Tags).Limit(1).All(ctx)
	const want = "velox: fixture.DBook: loading tags: fixture.DBookTag: read more rows than the cap: more than 2; raise it with the query's MaxRows"
	if !errors.Is(err, velox.ErrTooManyRows) || err.Error() != want {
		t.Errorf("got  %v\nwant %s", err, want)
	}
	db = openFake(velox.Postgres, &fakeconn.Exec{Results: results()}, velox.WithMaxRows(2))
	bs, err := db.Query[fixture.DBook]().With(fixture.DBooks.Tags).Limit(1).MaxRows(3).All(ctx)
	if err != nil || len(tagNames(t, bs[0].Tags)) != 3 {
		t.Errorf("MaxRows(3): %v", err)
	}
}

// dNote has a nullable key: SameBook is the notes on its book, through the
// book's join rows, so its owner key and its target key are both *int64,
// against the join's int64.
type dNote struct {
	ID       int64
	BookID   *int64
	SameBook velox.Many[dNote]
}

var dNoteSameBook = velox.Rel[dNote, dNote]{I: 0}

var dNoteMeta velox.MetaOnce[dNote]

func (*dNote) VeloxMeta() *velox.Meta[dNote] {
	return dNoteMeta.Load(func() velox.Descriptor[dNote] {
		return velox.Descriptor[dNote]{
			Table:      "notes",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "BookID", Name: "book_id"}},
			PrimaryKey: []int{0},
			Edges:      []velox.EdgeDescriptor{velox.EdgeThrough[fixture.DBookTag, dNote]("same_book", 1, 0, 0, 1)},
			FieldPtr: func(x *dNote, i int) any {
				switch i {
				case 0:
					return &x.ID
				case 1:
					return &x.BookID
				}
				return nil
			},
			EdgeFieldPtr: func(x *dNote, i int) any {
				if i == 0 {
					return &x.SameBook
				}
				return nil
			},
		}
	})
}

// TestThroughNullableKeys: an owner whose key is a nil pointer loads empty
// without being asked for, and pointer keys match the join's plain ones.
func TestThroughNullableKeys(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{
		{{int64(1), int64(1)}, {int64(2), nil}, {int64(3), int64(1)}},
		{{int64(1), int64(1)}, {int64(1), int64(2)}}, // book 1's join rows, twice the same book
		{{int64(1), int64(1)}, {int64(3), int64(1)}},
	}}
	ns, err := openFake(velox.Postgres, e).Query[dNote]().With(dNoteSameBook).Limit(3).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]int64{{1, 3}, {}, {1, 3}}
	for i, n := range ns {
		same, ok := n.SameBook.Get()
		got := []int64{}
		for _, s := range same {
			got = append(got, s.ID)
		}
		if !ok || same == nil || !reflect.DeepEqual(got, want[i]) {
			t.Errorf("note %d: %v %v, want %v", n.ID, got, ok, want[i])
		}
	}
	if !reflect.DeepEqual(e.Args, []any{int64(1)}) || e.Log[1] != `SELECT "book_id", "tag_id" FROM "book_tags" WHERE "book_id" IN ($1) ORDER BY "book_id", "tag_id"` {
		t.Errorf("%q %v", e.Log, e.Args)
	}
}

// dPeer's Read policy uses a many-to-many HasWhere: through dBookTag to tags
// (the peer's id standing in for a book's), or, under peerCycle, through
// dFriend back to dPeer, which is a cycle.
type dPeer struct {
	ID      int64
	Friends velox.Many[dPeer]
	Tags    velox.Many[fixture.DTag]
}

var dPeers = struct {
	ID      velox.NumberCol[dPeer, int64]
	Friends velox.Rel[dPeer, dPeer]
	Tags    velox.Rel[dPeer, fixture.DTag]
}{velox.NumberCol[dPeer, int64]{I: 0}, velox.Rel[dPeer, dPeer]{I: 0}, velox.Rel[dPeer, fixture.DTag]{I: 1}}

type peerCycle struct{}

var dPeerMeta velox.MetaOnce[dPeer]

func (*dPeer) VeloxMeta() *velox.Meta[dPeer] {
	return dPeerMeta.Load(func() velox.Descriptor[dPeer] {
		return velox.Descriptor[dPeer]{
			Policy:     []func() velox.Policy{(*dPeer)(nil).Policy},
			Table:      "peers",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}},
			PrimaryKey: []int{0},
			Edges: []velox.EdgeDescriptor{
				velox.Linked[dPeer](velox.EdgeThrough[dFriend, dPeer]("friends", 0, 0, 1, 0)),
				velox.Linked[fixture.DTag](velox.EdgeThrough[fixture.DBookTag, fixture.DTag]("tags", 0, 0, 1, 0)),
			},
			FieldPtr: func(x *dPeer, i int) any {
				if i == 0 {
					return &x.ID
				}
				return nil
			},
			EdgeFieldPtr: func(x *dPeer, i int) any {
				switch i {
				case 0:
					return &x.Friends
				case 1:
					return &x.Tags
				}
				return nil
			},
		}
	})
}

func (*dPeer) Policy() velox.Policy {
	return velox.Policy{Read: func(ctx context.Context) (velox.Cond, error) {
		if ctx.Value(peerCycle{}) != nil {
			return velox.Edge("friends").HasWhere(velox.C("id").GT(0)), nil
		}
		return velox.Edge("tags").HasWhere(velox.C("name").EQ("public")), nil
	}}
}

// dFriend is the join entity of dPeer.Friends.
type dFriend struct{ PeerID, FriendID int64 }

var dFriendMeta velox.MetaOnce[dFriend]

func (*dFriend) VeloxMeta() *velox.Meta[dFriend] {
	return dFriendMeta.Load(func() velox.Descriptor[dFriend] {
		return velox.Descriptor[dFriend]{
			Table:      "friends",
			Columns:    []velox.ColumnDescriptor{{Field: "PeerID", Name: "peer_id"}, {Field: "FriendID", Name: "friend_id"}},
			PrimaryKey: []int{0, 1},
			FieldPtr: func(x *dFriend, i int) any {
				switch i {
				case 0:
					return &x.PeerID
				case 1:
					return &x.FriendID
				}
				return nil
			},
		}
	})
}

// TestThroughTargetPolicy: the target's Read policy, itself a many-to-many
// HasWhere, lands inside the inner EXISTS; a Read policy that reaches its own
// Read back through a join is refused as a cycle.
func TestThroughTargetPolicy(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	live := testmodel.Org.With(context.Background(), int64(7)) // ctx lifts every policy, the scope with it
	got, _, err := db.Query[dPeer]().Where(dPeers.Friends.Has()).Limit(1).SQL(live)
	inner := `EXISTS (SELECT 1 FROM "friends" AS "vx1" WHERE "vx1"."peer_id" = "peers"."id" AND EXISTS (SELECT 1 FROM "peers" AS "vx2" WHERE "vx2"."id" = "vx1"."friend_id" AND ` +
		`EXISTS (SELECT 1 FROM "book_tags" AS "vx3" WHERE "vx3"."book_id" = "vx2"."id" AND EXISTS (SELECT 1 FROM "tags" AS "vx4" WHERE "vx4"."id" = "vx3"."tag_id" AND "name" = $`
	if err != nil || !strings.Contains(got, inner) {
		t.Errorf("got  %s, %v\nwant it to hold %s", got, err, inner)
	}
	cyc := context.WithValue(live, peerCycle{}, true)
	const want = "velox: apitest.dPeer: the Read policy of apitest.dPeer reaches its own Read through an EXISTS; such a policy cannot be applied"
	for name, q := range map[string]velox.Query[dPeer]{
		"direct":         db.Query[dPeer]().Limit(1),
		"from an EXISTS": db.Query[dPeer]().Where(dPeers.Friends.Has()).Limit(1),
	} {
		if _, _, err := q.SQL(cyc); err == nil || err.Error() != want || errors.Is(err, velox.ErrDenied) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
