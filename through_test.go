package velox_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox2"
)

func tagNames(t *testing.T, m velox.Many[dTag]) []string {
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
	e := &velox.FakeExec{Results: [][][]any{
		{{int64(1), int64(1), "a"}, {int64(2), int64(1), "b"}, {int64(3), int64(1), "c"}},
		{{int64(1), int64(1)}, {int64(1), int64(2)}, {int64(2), int64(2)}},
		{{int64(1), int64(7), "go"}, {int64(2), int64(7), "db"}},
	}}
	db := velox.OpenFake(velox.Postgres, e)
	bs, err := db.Query[dBook]().With(dBooks.Tags).Limit(3).All(ctx)
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

// TestThroughLoadAppliesTheOptions: the relation's Where and OrderBy apply to
// the targets, each owner's targets come in their fetched order, and a join
// row repeated does not repeat a target.
func TestThroughLoadAppliesTheOptions(t *testing.T) {
	e := &velox.FakeExec{Results: [][][]any{
		{{int64(1), int64(1), "a"}},
		{{int64(1), int64(1)}, {int64(1), int64(2)}, {int64(1), int64(2)}},
		{{int64(2), int64(7), "go"}, {int64(1), int64(7), "db"}},
	}}
	db := velox.OpenFake(velox.Postgres, e)
	bs, err := db.Query[dBook]().With(dBooks.Tags.Where(dTags.Name.NEQ("x")).OrderBy(dTags.Name.Desc())).Limit(1).All(ctx)
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
	e := &velox.FakeExec{Results: [][][]any{{{int64(1), int64(1), "a"}}, {}}}
	db := velox.OpenFake(velox.Postgres, e)
	bs, err := db.Query[dBook]().With(dBooks.Tags).Limit(1).All(ctx)
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
	e := &velox.FakeExec{Results: [][][]any{
		{{int64(1), int64(1), "a"}},
		{{int64(1), int64(2)}},
		{{int64(2), int64(7), "db"}},
		{{int64(1), int64(2)}, {int64(2), int64(2)}},
		{{int64(1), int64(1), "a"}, {int64(2), int64(1), "b"}},
	}}
	db := velox.OpenFake(velox.Postgres, e)
	bs, err := db.Query[dBook]().With(dBooks.Tags.With(dTags.Books)).Limit(1).All(ctx)
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
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	for _, c := range []struct {
		p    velox.Pred[dBook]
		want string
	}{
		{dBooks.Tags.HasWhere(dTags.Name.EQ("go")),
			`SELECT "id", "author_id", "title" FROM "books" WHERE EXISTS (SELECT 1 FROM "book_tags" AS "vx1" WHERE "vx1"."book_id" = "books"."id" AND EXISTS (SELECT 1 FROM "tags" AS "vx2" WHERE "vx2"."id" = "vx1"."tag_id" AND "name" = $1)) LIMIT $2`},
		{dBooks.Tags.Has(),
			`SELECT "id", "author_id", "title" FROM "books" WHERE EXISTS (SELECT 1 FROM "book_tags" AS "vx1" WHERE "vx1"."book_id" = "books"."id" AND EXISTS (SELECT 1 FROM "tags" AS "vx2" WHERE "vx2"."id" = "vx1"."tag_id")) LIMIT $1`},
		// Nested: the tag's books correlate with the innermost alias, vx2.
		{dBooks.Tags.HasWhere(dTags.Books.HasWhere(dBooks.Title.EQ("dune"))),
			`SELECT "id", "author_id", "title" FROM "books" WHERE EXISTS (SELECT 1 FROM "book_tags" AS "vx1" WHERE "vx1"."book_id" = "books"."id" AND EXISTS (SELECT 1 FROM "tags" AS "vx2" WHERE "vx2"."id" = "vx1"."tag_id" AND EXISTS (SELECT 1 FROM "book_tags" AS "vx3" WHERE "vx3"."tag_id" = "vx2"."id" AND EXISTS (SELECT 1 FROM "books" AS "vx4" WHERE "vx4"."id" = "vx3"."book_id" AND "title" = $1)))) LIMIT $2`},
		// After a many-to-many EXISTS, the outer table's conditions are its own again.
		{velox.And(dBooks.Tags.Has(), dBooks.Title.EQ("dune")),
			`SELECT "id", "author_id", "title" FROM "books" WHERE (EXISTS (SELECT 1 FROM "book_tags" AS "vx1" WHERE "vx1"."book_id" = "books"."id" AND EXISTS (SELECT 1 FROM "tags" AS "vx2" WHERE "vx2"."id" = "vx1"."tag_id")) AND "title" = $1) LIMIT $2`},
	} {
		got, _, err := db.Query[dBook]().Where(c.p).Limit(1).SQL(ctx)
		if err != nil || got != c.want {
			t.Errorf("got  %s, %v\nwant %s", got, err, c.want)
		}
	}
	got, _, err := velox.OpenFake(velox.MySQL, &velox.FakeExec{}).Query[dTag]().Where(dTags.Books.Has()).Limit(1).SQL(ctx)
	want := "SELECT `id`, `org_id`, `name` FROM `tags` WHERE EXISTS (SELECT 1 FROM `book_tags` AS `vx1` WHERE `vx1`.`tag_id` = `tags`.`id` AND EXISTS (SELECT 1 FROM `books` AS `vx2` WHERE `vx2`.`id` = `vx1`.`book_id`)) LIMIT ?"
	if err != nil || got != want {
		t.Errorf("the other way, on MySQL:\n got %s, %v\nwant %s", got, err, want)
	}
}

// TestThroughHasScopesTarget is Review Focus 5: the target's tenant condition
// lands inside the inner EXISTS, on the target, not on the join or the owner.
func TestThroughHasScopesTarget(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	got, args, err := db.Query[dBook]().Where(dBooks.Tags.HasWhere(dTags.Name.EQ("go"))).Limit(1).SQL(velox.WithTenant(ctx, int64(7)))
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
	for _, order := range [][2]velox.Rel[dBook, dTag]{{dBooks.Tags, dBooks.Picks}, {dBooks.Picks, dBooks.Tags}} {
		db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
		a, _, errA := db.Query[dBook]().Where(order[0].Has()).Limit(1).SQL(ctx)
		b, _, errB := db.Query[dBook]().Where(order[1].Has()).Limit(1).SQL(ctx)
		if errA != nil || errB != nil || a == b {
			t.Fatalf("one SQL for two relations: %s, %v, %v", a, errA, errB)
		}
		if !strings.Contains(a+b, `FROM "picks" AS "vx1"`) || !strings.Contains(a+b, `FROM "book_tags" AS "vx1"`) {
			t.Errorf("%s\n%s", a, b)
		}
	}
}

// TestThroughJoinRowsWrite: join rows are an entity's rows, written with
// Insert and Delete (Ruling 10).
func TestThroughJoinRowsWrite(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	if err := db.Insert(&dBookTag{BookID: 1, TagID: 2}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `INSERT INTO "book_tags" ("book_id", "tag_id") VALUES ($1, $2)`; e.SQL != want || !reflect.DeepEqual(e.Args, []any{int64(1), int64(2)}) {
		t.Errorf("insert %s %v", e.SQL, e.Args)
	}
	n, err := db.Delete[dBookTag]().Where(dBookTags.BookID.EQ(1), dBookTags.TagID.EQ(2)).Exec(ctx)
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
	Mismatch velox.Many[dTag]
	JoinCol  velox.Many[dTag]
	Target   velox.Many[dAuthor]
	Single   velox.One[dTag]
	BadJoin  velox.Many[dTag]
}

var dBadThroughs = struct {
	Mismatch, JoinCol velox.Rel[dBadThrough, dTag]
	Target            velox.Rel[dBadThrough, dAuthor]
	Single, BadJoin   velox.Rel[dBadThrough, dTag]
}{velox.Rel[dBadThrough, dTag]{I: 0}, velox.Rel[dBadThrough, dTag]{I: 1}, velox.Rel[dBadThrough, dAuthor]{I: 2},
	velox.Rel[dBadThrough, dTag]{I: 3}, velox.Rel[dBadThrough, dTag]{I: 4}}

var dBadThroughMeta velox.MetaOnce[dBadThrough]

func (*dBadThrough) VeloxMeta() *velox.Meta[dBadThrough] {
	return dBadThroughMeta.Load(func() velox.Desc[dBadThrough] {
		return velox.Desc[dBadThrough]{
			Table:      "bad",
			Columns:    []velox.ColumnDesc{{Field: "Key", Name: "key"}, {Field: "ID", Name: "id"}},
			PrimaryKey: []int{1},
			Tenant:     -1,
			Relations: []velox.Relation{
				velox.EdgeThrough[dBookTag, dTag]("Mismatch", 0, 0, 1, 0),  // a string owner key, an int64 join key
				velox.EdgeThrough[dBookTag, dTag]("JoinCol", 1, 2, 1, 0),   // the join has two columns
				velox.EdgeThrough[dBookTag, dAuthor]("Target", 1, 0, 1, 2), // an int64 join key, a string target key
				velox.EdgeThrough[dBookTag, dTag]("Single", 1, 0, 1, 0),    // a One
				velox.EdgeThrough[badDesc, dTag]("BadJoin", 1, 0, 0, 0),    // the join entity is invalid
			},
			Addr: func(x *dBadThrough, i int) any { return [...]any{&x.Key, &x.ID}[i] },
			RelAddr: func(x *dBadThrough, i int) any {
				return [...]any{&x.Mismatch, &x.JoinCol, &x.Target, &x.Single, &x.BadJoin}[i]
			},
		}
	})
}

func TestThroughRefusals(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	for _, c := range []struct {
		p    velox.Pred[dBadThrough]
		want string
	}{
		{dBadThroughs.Mismatch.Has(), "velox: velox_test.dBadThrough.Mismatch: the key types differ: velox_test.dBadThrough.Key is string and velox_test.dBookTag.BookID is int64"},
		{dBadThroughs.JoinCol.Has(), "velox: velox_test.dBadThrough.JoinCol: its key names column 2 of velox_test.dBookTag, which has 2"},
		{dBadThroughs.Target.Has(), "velox: velox_test.dBadThrough.Target: the key types differ: velox_test.dBookTag.TagID is int64 and velox_test.dAuthor.Name is string"},
		{dBadThroughs.Single.Has(), "velox: velox_test.dBadThrough.Single: a many-to-many relation needs a Many field"},
		{dBadThroughs.BadJoin.Has(), "velox: velox_test.dBadThrough.BadJoin: its join entity is invalid: velox_test.badDesc: the tenant names column 3, of 1"},
	} {
		_, _, err := db.Query[dBadThrough]().Where(c.p).Limit(1).SQL(ctx)
		var se *velox.SchemaError
		if !errors.As(err, &se) || err.Error() != c.want {
			t.Errorf("got  %v\nwant %s", err, c.want)
		}
	}
}
