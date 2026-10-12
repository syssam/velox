package tests

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	db "github.com/syssam/velox/tests/orm/db"

	"github.com/syssam/velox"
	"github.com/syssam/velox/orm"
	"github.com/syssam/velox/tests/orm/db/entity"
	"github.com/syssam/velox/tests/orm/db/migrate"
	"github.com/syssam/velox/tests/orm/db/post"
	"github.com/syssam/velox/tests/orm/db/predicate"
	"github.com/syssam/velox/tests/orm/db/user"
)

// The examples below are the README's, run by go test: a snippet that stops
// compiling or prints something else fails the build.

var exampleSeq int

// exampleDB opens an empty database with the tables of the schema.
func exampleDB() (*velox.DB, context.Context) {
	ctx := context.Background()
	exampleSeq++
	conn, err := velox.Open("sqlite", fmt.Sprintf("file:example%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", exampleSeq))
	if err != nil {
		log.Fatal(err)
	}
	if err := migrate.Create(ctx, conn); err != nil {
		log.Fatal(err)
	}
	return conn, ctx
}

func Example() {
	conn, ctx := exampleDB()
	defer conn.Close()

	// Create, with setters.
	alice, err := db.User.Create(conn).
		Set(user.Name.Set("alice"), user.Email.Set("alice@example.com"), user.Age.Set(30)).
		Save(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, title := range []string{"first", "second"} {
		_, err := db.Post.Create(conn).
			Set(post.Title.Set(title), post.Content.Set("..."), post.Author.Set(alice.ID)).
			Save(ctx)
		if err != nil {
			log.Fatal(err)
		}
	}

	// Query: conditions, order, an eager-loaded edge with conditions of its own.
	users, err := db.User.Query(conn).
		Where(user.Name.EQ("alice"), user.Age.GT(18)).
		OrderBy(user.CreatedAt.Desc()).
		WithPosts(db.Post.Query(nil).Where(post.Title.NEQ("draft")).OrderBy(post.Title.Asc()).Limit(10)).
		Limit(20).
		All(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, u := range users {
		fmt.Println(u.Name, u.Age, len(u.Edges.Posts), u.Edges.Posts[0].Title)
	}

	// The neighbors of a row, as a query: conditions, order, a count.
	count, err := alice.QueryPosts(conn).Where(post.Title.EQ("first")).Count(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(count)

	// Update one row and get it back; update many and get the count.
	alice, err = db.User.UpdateByID(conn, alice.ID).Set(user.Age.Add(1)).SaveOne(ctx)
	if err != nil {
		log.Fatal(err)
	}
	n, err := db.Post.Update(conn).Set(post.ViewCount.Add(1)).Where(post.Author.HasWith(user.ID.EQ(alice.ID))).Exec(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(alice.Age, n)

	// By id; a missing row is velox.ErrNotFound.
	_, err = db.User.Get(ctx, conn, 404)
	fmt.Println(errors.Is(err, velox.ErrNotFound))
	// Output:
	// alice 30 2 first
	// 1
	// 31 2
	// true
}

func Example_transaction() {
	conn, ctx := exampleDB()
	defer conn.Close()

	// What a query or mutation runs on is given where it starts: the
	// transaction here. An error returned from the function rolls it back.
	err := conn.RunTx(ctx, func(tx *velox.Tx) error {
		u, err := db.User.Create(tx).
			Set(user.Name.Set("bob"), user.Email.Set("bob@example.com"), user.Age.Set(40)).
			Save(ctx)
		if err != nil {
			return err
		}
		locked, err := db.User.Query(tx).Where(user.ID.EQ(u.ID)).ForUpdate().Only(ctx)
		if err != nil {
			return err
		}
		_, err = db.User.UpdateByID(tx, locked.ID).Set(user.Age.Set(locked.Age + 1)).SaveOne(ctx)
		if err != nil {
			return err
		}
		return errors.New("changed my mind")
	})
	n, _ := db.User.Query(conn).Count(ctx)
	fmt.Println(err, n)
	// Output:
	// changed my mind 0
}

func Example_fill() {
	conn, ctx := exampleDB()
	defer conn.Close()

	// The body of a request, as an endpoint declares it. Each field says by
	// its type whether it was given.
	type createUser struct {
		Name     string
		Email    string
		Age      *int            // nil: not given
		Nickname orm.Opt[string] // absent, null, or a value
		Source   string          `orm:"-"` // not for the row
	}
	type updateUser struct {
		Name     orm.Opt[string] `json:"name"`
		Age      orm.Opt[int]    `json:"age"`
		Nickname orm.Opt[string] `json:"nickname"`
	}

	age := 30
	u, err := db.User.Create(conn).Fill(createUser{Name: "carol", Email: "carol@example.com", Age: &age, Nickname: orm.Some("caz"), Source: "signup"}).Save(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(u.Name, u.Age, *u.Nickname, u.Role)

	// A PATCH body: a missing key leaves the column alone, null clears it.
	var patch updateUser
	if err := json.Unmarshal([]byte(`{"age": 31, "nickname": null}`), &patch); err != nil {
		log.Fatal(err)
	}
	u, err = db.User.UpdateByID(conn, u.ID).Fill(patch).SaveOne(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(u.Name, u.Age, u.Nickname == nil)
	// Output:
	// carol 30 caz user
	// carol 31 true
}

func Example_iter() {
	conn, ctx := exampleDB()
	defer conn.Close()
	for i := range 5 {
		name := fmt.Sprintf("u%d", i)
		if _, err := db.User.Create(conn).Set(user.Name.Set(name), user.Email.Set(name+"@example.com"), user.Age.Set(20+i)).Save(ctx); err != nil {
			log.Fatal(err)
		}
	}

	// One row at a time, without holding the result.
	var names []string
	for u, err := range db.User.Query(conn).Where(user.Age.GTE(22)).OrderBy(user.Age.Asc()).Iter(ctx) {
		if err != nil {
			log.Fatal(err)
		}
		names = append(names, u.Name)
	}
	fmt.Println(names)

	// A page at a time, with a cursor.
	var page *entity.UserConnection
	size := 2
	page, err := db.User.Query(conn).OrderBy(user.Age.Desc()).Paginate(ctx, nil, &size, nil, nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(page.Edges[0].Node.Name, page.Edges[1].Node.Name, page.PageInfo.HasNextPage, page.TotalCount)
	// The next page starts after the last cursor of this one.
	page, err = db.User.Query(conn).OrderBy(user.Age.Desc()).Paginate(ctx, page.PageInfo.EndCursor, &size, nil, nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(len(page.Edges), page.PageInfo.HasNextPage)

	// Columns and aggregates instead of entities.
	total, err := db.User.Query(conn).Select().Aggregate(orm.Sum(user.ColumnAge)).Int(ctx)
	if err != nil {
		log.Fatal(err)
	}
	var rows []struct {
		Role string `json:"role"`
		N    int    `json:"n"`
	}
	if err := db.User.Query(conn).Select().GroupBy(user.ColumnRole).Aggregate(orm.As(orm.Count(), "n")).Scan(ctx, &rows); err != nil {
		log.Fatal(err)
	}
	fmt.Println(total, rows)

	// A condition that arrives at run time, as the filter of a list endpoint
	// does; a typed one as a function on the SQL builder.
	p, err := orm.FieldPredicate(&entity.UserMeta, "age", orm.OpGT, 22)
	if err != nil {
		log.Fatal(err)
	}
	older, err := db.User.Query(conn).Where(predicate.User(p)).Count(ctx)
	if err != nil {
		log.Fatal(err)
	}
	_ = orm.Compile(user.Age.GT(18))
	exists, err := db.User.Query(conn).Where(user.Name.EQ("u0")).Exists(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(older, exists)
	// Output:
	// [u2 u3 u4]
	// u4 u3 true 5
	// 2 true
	// 110 [{user 5}]
	// 2 true
}
