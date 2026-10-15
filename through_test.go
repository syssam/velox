package velox_test

import (
	"context"
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
	TenantJ  velox.Many[dTag]
	PolicyJ  velox.Many[dTag]
}

var dBadThroughs = struct {
	Mismatch, JoinCol velox.Rel[dBadThrough, dTag]
	Target            velox.Rel[dBadThrough, dAuthor]
	Single, BadJoin   velox.Rel[dBadThrough, dTag]
	TenantJ, PolicyJ  velox.Rel[dBadThrough, dTag]
}{velox.Rel[dBadThrough, dTag]{I: 0}, velox.Rel[dBadThrough, dTag]{I: 1}, velox.Rel[dBadThrough, dAuthor]{I: 2},
	velox.Rel[dBadThrough, dTag]{I: 3}, velox.Rel[dBadThrough, dTag]{I: 4}, velox.Rel[dBadThrough, dTag]{I: 5},
	velox.Rel[dBadThrough, dTag]{I: 6}}

var dBadThroughMeta velox.MetaOnce[dBadThrough]

func (*dBadThrough) VeloxMeta() *velox.Meta[dBadThrough] {
	return dBadThroughMeta.Load(func() velox.Desc[dBadThrough] {
		return velox.Desc[dBadThrough]{
			Table:      "bad",
			Columns:    []velox.ColumnDesc{{Field: "Key", Name: "key"}, {Field: "ID", Name: "id"}},
			PrimaryKey: []int{1},
			Tenant:     -1,
			Relations: []velox.Relation{
				velox.EdgeThrough[dBookTag, dTag]("Mismatch", 0, 0, 1, 0),   // a string owner key, an int64 join key
				velox.EdgeThrough[dBookTag, dTag]("JoinCol", 1, 2, 1, 0),    // the join has two columns
				velox.EdgeThrough[dBookTag, dAuthor]("Target", 1, 0, 1, 2),  // an int64 join key, a string target key
				velox.EdgeThrough[dBookTag, dTag]("Single", 1, 0, 1, 0),     // a One
				velox.EdgeThrough[badDesc, dTag]("BadJoin", 1, 0, 0, 0),     // the join entity is invalid
				velox.EdgeThrough[dAuthor, dTag]("TenantJ", 1, 0, 0, 0),     // the join entity has a tenant
				velox.EdgeThrough[dPolicyJoin, dTag]("PolicyJ", 1, 0, 1, 0), // the join entity has a Read policy
			},
			Addr: func(x *dBadThrough, i int) any { return [...]any{&x.Key, &x.ID}[i] },
			RelAddr: func(x *dBadThrough, i int) any {
				return [...]any{&x.Mismatch, &x.JoinCol, &x.Target, &x.Single, &x.BadJoin, &x.TenantJ, &x.PolicyJ}[i]
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
		{dBadThroughs.TenantJ.Has(), "velox: velox_test.dBadThrough.TenantJ: its join entity velox_test.dAuthor has a tenant column; join rows are not scoped, so a join entity can have neither a tenant column nor a Read policy"},
		{dBadThroughs.PolicyJ.Has(), "velox: velox_test.dBadThrough.PolicyJ: its join entity velox_test.dPolicyJoin has a Read policy; join rows are not scoped, so a join entity can have neither a tenant column nor a Read policy"},
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
	return dPolicyJoinMeta.Load(func() velox.Desc[dPolicyJoin] {
		return velox.Desc[dPolicyJoin]{
			Policy:     (*dPolicyJoin).Policy,
			Table:      "policy_joins",
			Columns:    []velox.ColumnDesc{{Field: "BookID", Name: "book_id"}, {Field: "TagID", Name: "tag_id"}},
			PrimaryKey: []int{0, 1},
			Tenant:     -1,
			Addr: func(x *dPolicyJoin, i int) any {
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

func (*dPolicyJoin) Policy() velox.Policy[dPolicyJoin] {
	return velox.Policy[dPolicyJoin]{Read: func(context.Context) (velox.Pred[dPolicyJoin], error) { return velox.Pred[dPolicyJoin]{}, nil }}
}

// dLinker has tags through dLink, a join entity whose Insert policy checks a
// link's endpoints, which is how an application guards a link it writes.
type dLinker struct {
	ID   int64
	Tags velox.Many[dTag]
}

var dLinkers = struct {
	ID   velox.NumberCol[dLinker, int64]
	Tags velox.Rel[dLinker, dTag]
}{velox.NumberCol[dLinker, int64]{I: 0}, velox.Rel[dLinker, dTag]{I: 0}}

var dLinkerMeta velox.MetaOnce[dLinker]

func (*dLinker) VeloxMeta() *velox.Meta[dLinker] {
	return dLinkerMeta.Load(func() velox.Desc[dLinker] {
		return velox.Desc[dLinker]{
			Table:      "linkers",
			Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}},
			PrimaryKey: []int{0},
			Tenant:     -1,
			Relations:  []velox.Relation{velox.EdgeThrough[dLink, dTag]("Tags", 0, 0, 1, 0)},
			Addr:       func(x *dLinker, i int) any { return &x.ID },
			RelAddr:    func(x *dLinker, i int) any { return &x.Tags },
		}
	})
}

type dLink struct{ LinkerID, TagID int64 }

var dLinkMeta velox.MetaOnce[dLink]

func (*dLink) VeloxMeta() *velox.Meta[dLink] {
	return dLinkMeta.Load(func() velox.Desc[dLink] {
		return velox.Desc[dLink]{
			Policy:     (*dLink).Policy,
			Table:      "links",
			Columns:    []velox.ColumnDesc{{Field: "LinkerID", Name: "linker_id"}, {Field: "TagID", Name: "tag_id"}},
			PrimaryKey: []int{0, 1},
			Tenant:     -1,
			Addr: func(x *dLink, i int) any {
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
func (*dLink) Policy() velox.Policy[dLink] {
	return velox.Policy[dLink]{
		Insert: func(_ context.Context, l *dLink) error {
			if l.TagID == 13 {
				return velox.Deny("not that tag")
			}
			return nil
		},
		Write: func(context.Context) (velox.Pred[dLink], error) { return velox.Pred[dLink]{}, nil },
	}
}

// TestThroughJoinEntityWithInsertPolicy: a join entity with Insert and Write
// policies is allowed. Its relation loads and answers Has, and the policy
// refuses a link written to an endpoint it rejects.
func TestThroughJoinEntityWithInsertPolicy(t *testing.T) {
	e := &velox.FakeExec{Results: [][][]any{
		{{int64(1)}},
		{{int64(1), int64(7)}},
		{{int64(7), int64(1), "go"}},
	}}
	db := velox.OpenFake(velox.Postgres, e)
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
	e2 := &velox.FakeExec{Affected: 1}
	db = velox.OpenFake(velox.Postgres, e2)
	if err := db.Insert(&dLink{LinkerID: 1, TagID: 2}).Exec(ctx); err != nil || e2.Calls != 1 {
		t.Fatalf("an allowed link: %v, %d calls", err, e2.Calls)
	}
	err = db.Insert(&dLink{LinkerID: 1, TagID: 13}).Exec(ctx)
	if !errors.Is(err, velox.ErrDenied) || e2.Calls != 1 {
		t.Fatalf("a refused link: %v, %d calls", err, e2.Calls)
	}
}

// TestThroughLoadChunksByOwner: when the targets need several statements,
// each holds whole owners, so that every owner's targets come from one query
// in the order asked; an owner with more targets than one statement holds is
// refused.
func TestThroughLoadChunksByOwner(t *testing.T) {
	defer velox.SetMaxParams(velox.Postgres, 66)() // 64 are kept for hooks: two keys a statement
	e := &velox.FakeExec{Results: [][][]any{
		{{int64(1), int64(1), "a"}, {int64(2), int64(1), "b"}, {int64(3), int64(1), "c"}},
		{{int64(1), int64(1)}, {int64(1), int64(2)}, {int64(2), int64(2)}, {int64(2), int64(3)}}, // books 1 and 2
		{{int64(3), int64(3)}},                                 // book 3
		{{int64(2), int64(7), "b"}, {int64(1), int64(7), "a"}}, // tags 1 and 2, for book 1
		{{int64(3), int64(7), "c"}, {int64(2), int64(7), "b"}}, // tags 2 and 3, for books 2 and 3
	}}
	db := velox.OpenFake(velox.Postgres, e)
	bs, err := db.Query[dBook]().With(dBooks.Tags.OrderBy(dTags.Name.Desc())).Limit(3).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"b", "a"}, {"c", "b"}, {"c"}}
	for i, b := range bs {
		if got := tagNames(t, b.Tags); !reflect.DeepEqual(got, want[i]) {
			t.Errorf("book %d: tags %v, want %v", b.ID, got, want[i])
		}
	}
	if len(e.Log) != 5 || !reflect.DeepEqual(e.Args, []any{int64(2), int64(3)}) {
		t.Errorf("queries %q, last args %v", e.Log, e.Args)
	}
	if want := `SELECT "id", "org_id", "name" FROM "tags" WHERE "id" IN ($1, $2) ORDER BY "name" DESC`; e.Log[3] != want {
		t.Errorf("target SQL %s", e.Log[3])
	}

	e = &velox.FakeExec{Results: [][][]any{
		{{int64(1), int64(1), "a"}},
		{{int64(1), int64(1)}, {int64(1), int64(2)}, {int64(1), int64(3)}},
	}}
	_, err = velox.OpenFake(velox.Postgres, e).Query[dBook]().With(dBooks.Tags).Limit(1).All(ctx)
	if want := "velox: velox_test.dBook: relation Tags: an owner has 3 related rows, more than one statement can load in order"; err == nil || err.Error() != want {
		t.Errorf("got %v", err)
	}
	if len(e.Log) != 2 {
		t.Errorf("queries %q", e.Log)
	}
}

// TestThroughJoinRewriteKeepsKeys: a Rewrite hook may not leave either key
// column out of the join query; the keys would read as zero values.
func TestThroughJoinRewriteKeepsKeys(t *testing.T) {
	for kept, missing := range []string{"tag_id", "book_id"} {
		narrow := velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
			if st.Entity.Name == "velox_test.dBookTag" {
				st.Columns = []int{kept}
			}
			return nil
		})
		e := &velox.FakeExec{Results: [][][]any{{{int64(1), int64(1), "a"}}, {{int64(1)}}}}
		_, err := velox.OpenFake(velox.Postgres, e, narrow).Query[dBook]().With(dBooks.Tags).Limit(1).All(ctx)
		// The plain relation's error reads the same (TestWithRewriteNarrowsColumns).
		want := "velox: velox_test.dBookTag: a Rewrite hook left out the relation's key column " + missing
		if err == nil || !strings.HasPrefix(err.Error(), "velox: velox_test.dBook: loading Tags: ") || !strings.Contains(err.Error(), want) {
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
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Results: results()}, velox.WithMaxRows(2))
	_, err := db.Query[dBook]().With(dBooks.Tags).Limit(1).All(ctx)
	const want = "velox: velox_test.dBook: loading Tags: velox_test.dBookTag: read more rows than the cap: more than 2; raise it with the query's MaxRows"
	if !errors.Is(err, velox.ErrTooManyRows) || err.Error() != want {
		t.Errorf("got  %v\nwant %s", err, want)
	}
	db = velox.OpenFake(velox.Postgres, &velox.FakeExec{Results: results()}, velox.WithMaxRows(2))
	bs, err := db.Query[dBook]().With(dBooks.Tags).Limit(1).MaxRows(3).All(ctx)
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
	return dNoteMeta.Load(func() velox.Desc[dNote] {
		return velox.Desc[dNote]{
			Table:      "notes",
			Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "BookID", Name: "book_id"}},
			PrimaryKey: []int{0},
			Tenant:     -1,
			Relations:  []velox.Relation{velox.EdgeThrough[dBookTag, dNote]("SameBook", 1, 0, 0, 1)},
			Addr: func(x *dNote, i int) any {
				switch i {
				case 0:
					return &x.ID
				case 1:
					return &x.BookID
				}
				return nil
			},
			RelAddr: func(x *dNote, i int) any {
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
	e := &velox.FakeExec{Results: [][][]any{
		{{int64(1), int64(1)}, {int64(2), nil}, {int64(3), int64(1)}},
		{{int64(1), int64(1)}, {int64(1), int64(2)}}, // book 1's join rows, twice the same book
		{{int64(1), int64(1)}, {int64(3), int64(1)}},
	}}
	ns, err := velox.OpenFake(velox.Postgres, e).Query[dNote]().With(dNoteSameBook).Limit(3).All(ctx)
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
	Tags    velox.Many[dTag]
}

var dPeers = struct {
	ID      velox.NumberCol[dPeer, int64]
	Friends velox.Rel[dPeer, dPeer]
	Tags    velox.Rel[dPeer, dTag]
}{velox.NumberCol[dPeer, int64]{I: 0}, velox.Rel[dPeer, dPeer]{I: 0}, velox.Rel[dPeer, dTag]{I: 1}}

type peerCycle struct{}

var dPeerMeta velox.MetaOnce[dPeer]

func (*dPeer) VeloxMeta() *velox.Meta[dPeer] {
	return dPeerMeta.Load(func() velox.Desc[dPeer] {
		return velox.Desc[dPeer]{
			Policy:     (*dPeer).Policy,
			Table:      "peers",
			Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}},
			PrimaryKey: []int{0},
			Tenant:     -1,
			Relations: []velox.Relation{
				velox.EdgeThrough[dFriend, dPeer]("Friends", 0, 0, 1, 0),
				velox.EdgeThrough[dBookTag, dTag]("Tags", 0, 0, 1, 0),
			},
			Addr: func(x *dPeer, i int) any {
				if i == 0 {
					return &x.ID
				}
				return nil
			},
			RelAddr: func(x *dPeer, i int) any {
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

func (*dPeer) Policy() velox.Policy[dPeer] {
	return velox.Policy[dPeer]{Read: func(ctx context.Context) (velox.Pred[dPeer], error) {
		if ctx.Value(peerCycle{}) != nil {
			return dPeers.Friends.HasWhere(dPeers.ID.GT(0)), nil
		}
		return dPeers.Tags.HasWhere(dTags.Name.EQ("public")), nil
	}}
}

// dFriend is the join entity of dPeer.Friends.
type dFriend struct{ PeerID, FriendID int64 }

var dFriendMeta velox.MetaOnce[dFriend]

func (*dFriend) VeloxMeta() *velox.Meta[dFriend] {
	return dFriendMeta.Load(func() velox.Desc[dFriend] {
		return velox.Desc[dFriend]{
			Table:      "friends",
			Columns:    []velox.ColumnDesc{{Field: "PeerID", Name: "peer_id"}, {Field: "FriendID", Name: "friend_id"}},
			PrimaryKey: []int{0, 1},
			Tenant:     -1,
			Addr: func(x *dFriend, i int) any {
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
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	got, _, err := db.Query[dPeer]().Where(dPeers.Friends.Has()).Limit(1).SQL(ctx)
	inner := `EXISTS (SELECT 1 FROM "friends" AS "vx1" WHERE "vx1"."peer_id" = "peers"."id" AND EXISTS (SELECT 1 FROM "peers" AS "vx2" WHERE "vx2"."id" = "vx1"."friend_id" AND ` +
		`EXISTS (SELECT 1 FROM "book_tags" AS "vx3" WHERE "vx3"."book_id" = "vx2"."id" AND EXISTS (SELECT 1 FROM "tags" AS "vx4" WHERE "vx4"."id" = "vx3"."tag_id" AND "name" = $`
	if err != nil || !strings.Contains(got, inner) {
		t.Errorf("got  %s, %v\nwant it to hold %s", got, err, inner)
	}
	cyc := context.WithValue(ctx, peerCycle{}, true)
	const want = "velox: velox_test.dPeer: the Read policy of velox_test.dPeer reaches its own Read through an EXISTS; such a policy cannot be applied"
	for name, q := range map[string]velox.Query[dPeer]{
		"direct":         db.Query[dPeer]().Limit(1),
		"from an EXISTS": db.Query[dPeer]().Where(dPeers.Friends.Has()).Limit(1),
	} {
		if _, _, err := q.SQL(cyc); err == nil || err.Error() != want || errors.Is(err, velox.ErrDenied) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
