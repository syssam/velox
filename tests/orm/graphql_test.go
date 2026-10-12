package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/gqlinput"
	reffilter "github.com/syssam/velox/tests/integration/filter"
	client "github.com/syssam/velox/tests/orm/db"
	"github.com/syssam/velox/tests/orm/db/entity"
	"github.com/syssam/velox/tests/orm/db/filter"
	"github.com/syssam/velox/tests/orm/db/pet"
	"github.com/syssam/velox/tests/orm/db/post"
	"github.com/syssam/velox/tests/orm/db/predicate"
	"github.com/syssam/velox/tests/orm/db/token"
	"github.com/syssam/velox/tests/orm/db/user"
)

// The GraphQL layer of this tree is generated from the schema velox writes
// for testschema (tests/graphql), which a GraphQL engine binds to these
// types. The tests below use it as a resolver would, without an engine.

// seedGraph makes users with posts: ann (admin) with two, bob with one, cat
// with none.
func seedGraph(t *testing.T, db *velox.DB) (ann, bob, cat *entity.User) {
	t.Helper()
	ctx := context.Background()
	ann, bob, cat = newUser(t, db, "ann", 30), newUser(t, db, "bob", 40), newUser(t, db, "cat", 50)
	_, err := client.User.UpdateByID(db, ann.ID).Set(user.Role.Set(user.RoleAdmin)).SaveOne(ctx)
	require.NoError(t, err)
	newPost(t, db, ann, "a1")
	p := newPost(t, db, ann, "a2")
	_, err = client.Post.UpdateByID(db, p.ID).Set(post.Status.Set(post.StatusPublished)).SaveOne(ctx)
	require.NoError(t, err)
	newPost(t, db, bob, "b1")
	return ann, bob, cat
}

func ptr[T any](v T) *T { return &v }

func TestWhereInput(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	seedGraph(t, db)
	find := func(w *filter.UserWhereInput) []string {
		t.Helper()
		us, err := client.User.Query(db).Where(w.P()).OrderBy(user.Name.Asc()).All(ctx)
		require.NoError(t, err)
		return names(us)
	}
	all := []string{"ann", "bob", "cat"}

	// No input, and an input that says nothing, are no condition.
	assert.Equal(t, all, find(nil))
	assert.Equal(t, all, find(&filter.UserWhereInput{}))
	assert.Equal(t, predicate.User{}, (*filter.UserWhereInput)(nil).P())

	for name, c := range map[string]struct {
		in   filter.UserWhereInput
		want []string
	}{
		"equal":            {filter.UserWhereInput{Name: ptr("bob")}, []string{"bob"}},
		"not equal":        {filter.UserWhereInput{NameNEQ: ptr("bob")}, []string{"ann", "cat"}},
		"in":               {filter.UserWhereInput{NameIn: []string{"ann", "cat", "zed"}}, []string{"ann", "cat"}},
		"in nothing":       {filter.UserWhereInput{NameIn: []string{}}, nil},
		"not in":           {filter.UserWhereInput{NameNotIn: []string{"ann"}}, []string{"bob", "cat"}},
		"contains":         {filter.UserWhereInput{EmailContains: ptr("ob@")}, []string{"bob"}},
		"enum":             {filter.UserWhereInput{Role: ptr(user.RoleAdmin)}, []string{"ann"}},
		"enum in":          {filter.UserWhereInput{RoleIn: []user.RoleEnum{user.RoleUser}}, []string{"bob", "cat"}},
		"fields are anded": {filter.UserWhereInput{NameNEQ: ptr("bob"), Role: ptr(user.RoleUser)}, []string{"cat"}},
		"not":              {filter.UserWhereInput{Not: &filter.UserWhereInput{Name: ptr("ann")}}, []string{"bob", "cat"}},
		"or":               {filter.UserWhereInput{Or: []*filter.UserWhereInput{{Name: ptr("ann")}, {Name: ptr("cat")}}}, []string{"ann", "cat"}},
		"and":              {filter.UserWhereInput{And: []*filter.UserWhereInput{{NameNEQ: ptr("ann")}, {NameNEQ: ptr("cat")}}}, []string{"bob"}},
		"has an edge":      {filter.UserWhereInput{HasPosts: ptr(true)}, []string{"ann", "bob"}},
		"has no edge":      {filter.UserWhereInput{HasPosts: ptr(false)}, []string{"cat"}},
		"has with":         {filter.UserWhereInput{HasPostsWith: []*filter.PostWhereInput{{Status: ptr(post.StatusPublished)}}}, []string{"ann"}},
		"has with, twice":  {filter.UserWhereInput{HasPostsWith: []*filter.PostWhereInput{{Title: ptr("a1")}, {TitleContains: ptr("a")}}}, []string{"ann"}},
		"added in Go":      {filter.UserWhereInput{Name: ptr("ann"), Predicates: []predicate.User{user.Age.GT(35)}}, nil},
		"only added in Go": {filter.UserWhereInput{Predicates: []predicate.User{user.Age.GT(35)}}, []string{"bob", "cat"}},
	} {
		assert.ElementsMatch(t, c.want, find(&c.in), name)
	}

	// An input that cannot be a condition fails the query; it does not
	// quietly match everything.
	for name, in := range map[string]filter.UserWhereInput{
		"empty not":              {Not: &filter.UserWhereInput{}},
		"empty in or":            {Or: []*filter.UserWhereInput{{Name: ptr("ann")}, {}}},
		"nil in and":             {And: []*filter.UserWhereInput{nil}},
		"empty neighbor":         {HasPostsWith: []*filter.PostWhereInput{{}}},
		"empty under a neighbor": {HasPostsWith: []*filter.PostWhereInput{{Not: &filter.PostWhereInput{}}}},
	} {
		_, err := client.User.Query(db).Where(in.P()).All(ctx)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "empty", name)
		_, err = client.User.Query(db).Where(in.P()).Count(ctx)
		require.Error(t, err, name)
		// A mutation given it fails too, and writes nothing.
		_, err = client.User.Update(db).Set(user.Age.Set(1)).Where(in.P()).Exec(ctx)
		require.Error(t, err, name)
	}
	assert.Equal(t, 3, must(client.User.Query(db).Where(user.Age.GT(1)).Count(ctx)), "nothing was written")

	// Nesting has a limit a request cannot exceed.
	deep := &filter.UserWhereInput{Name: ptr("ann")}
	for range gqlinput.MaxWhereDepth {
		deep = &filter.UserWhereInput{Not: deep}
	}
	_, err := client.User.Query(db).Where(deep.P()).All(ctx)
	require.NoError(t, err, "at the limit")
	deep = &filter.UserWhereInput{Not: deep}
	_, err = client.User.Query(db).Where(deep.P()).All(ctx)
	require.ErrorIs(t, err, gqlinput.ErrWhereDepth)
}

// A where input reads itself from what an engine hands over for the where
// argument, and from a filter kept as a document: the generated UnmarshalGQL.
// The same rows come back as for the input built in Go.
func TestWhereInput_FromADocument(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	seedGraph(t, db)
	find := func(doc map[string]any) []string {
		t.Helper()
		var w filter.UserWhereInput
		require.NoError(t, w.UnmarshalGQL(doc), "%v", doc)
		us, err := client.User.Query(db).Where(w.P()).OrderBy(user.Name.Asc()).All(ctx)
		require.NoError(t, err, "%v", doc)
		return names(us)
	}
	for name, c := range map[string]struct {
		doc  map[string]any
		want []string
	}{
		"nothing":  {map[string]any{}, []string{"ann", "bob", "cat"}},
		"a string": {map[string]any{"nameNEQ": "bob"}, []string{"ann", "cat"}},
		// An enum arrives by its name in the schema; the column holds "admin".
		"an enum by name":       {map[string]any{"role": "ADMIN"}, []string{"ann"}},
		"enums by name":         {map[string]any{"roleIn": []any{"USER"}}, []string{"bob", "cat"}},
		"an enum as stored":     {map[string]any{"roleNEQ": "admin"}, []string{"bob", "cat"}},
		"one value for a list":  {map[string]any{"nameIn": "cat"}, []string{"cat"}},
		"null is not given":     {map[string]any{"name": nil, "hasPosts": true}, []string{"ann", "bob"}},
		"a false that is given": {map[string]any{"hasPosts": false}, []string{"cat"}},
		"through an edge": {map[string]any{"hasPostsWith": []any{
			map[string]any{"status": "PUBLISHED"},
		}}, []string{"ann"}},
		"nested": {map[string]any{"or": []any{
			map[string]any{"not": map[string]any{"hasPosts": true}},
			map[string]any{"and": []any{map[string]any{"role": "USER"}, map[string]any{"nameContains": "o"}}},
		}}, []string{"bob", "cat"}},
	} {
		assert.Equal(t, c.want, find(c.doc), name)
	}

	// What it cannot read it refuses: a condition dropped is a query widened.
	for name, c := range map[string]struct {
		doc  any
		want string
	}{
		"a field the input does not have":   {map[string]any{"age": 30}, `UserWhereInput has no field "age"`},
		"a name of no value of the enum":    {map[string]any{"role": "OWNER"}, `"OWNER" is not a value of user.RoleEnum`},
		"a field of the neighbors' input":   {map[string]any{"hasPostsWith": map[string]any{"role": "ADMIN"}}, `hasPostsWith[0]: velox: PostWhereInput has no field "role"`},
		"an enum of the neighbors, by name": {map[string]any{"hasPostsWith": map[string]any{"statusIn": []any{"DRAFT", "GONE"}}}, `statusIn[1]: "GONE" is not a value of post.StatusEnum`},
		"not an object":                     {"name = 'ann'", "given as an object"},
	} {
		var w filter.UserWhereInput
		err := w.UnmarshalGQL(c.doc)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), c.want, name)
	}

	// An input written out and read back is the same input, through the
	// engine's methods and through encoding/json.
	in := filter.UserWhereInput{
		Role:         ptr(user.RoleAdmin),
		Not:          &filter.UserWhereInput{NameIn: []string{"bob"}},
		HasPostsWith: []*filter.PostWhereInput{{StatusIn: []post.StatusEnum{post.StatusPublished}}},
	}
	var b bytes.Buffer
	in.MarshalGQL(&b)
	assert.JSONEq(t, `{"not":{"nameIn":["bob"]},"role":"ADMIN","hasPostsWith":[{"statusIn":["PUBLISHED"]}]}`, b.String())
	var doc map[string]any
	require.NoError(t, json.Unmarshal(b.Bytes(), &doc))
	var back filter.UserWhereInput
	require.NoError(t, back.UnmarshalGQL(doc))
	assert.Equal(t, in, back)
	assert.Equal(t, in.P(), back.P())
	text, err := json.Marshal(&in)
	require.NoError(t, err)
	assert.JSONEq(t, b.String(), string(text))
	back = filter.UserWhereInput{}
	require.NoError(t, json.Unmarshal(text, &back))
	assert.Equal(t, in, back)

	// Among no values nothing matches. A writer that left the empty list out
	// would give back a filter that matches every row.
	none := filter.UserWhereInput{NameIn: []string{}}
	text, err = json.Marshal(none)
	require.NoError(t, err)
	assert.JSONEq(t, `{"nameIn":[]}`, string(text))
	back = filter.UserWhereInput{}
	require.NoError(t, json.Unmarshal(text, &back))
	us, err := client.User.Query(db).Where(back.P()).All(ctx)
	require.NoError(t, err)
	assert.Empty(t, us)

	// json.Unmarshal refuses what UnmarshalGQL does: the struct's tags alone
	// would let an unknown key pass, and the condition with it.
	err = json.Unmarshal([]byte(`{"name": "ann", "age": 30}`), &back)
	require.ErrorContains(t, err, `UserWhereInput has no field "age"`)

	// Conditions added in Go have no form in a document. json.Marshal says
	// so, and what MarshalGQL writes is refused when it is read.
	scoped := filter.UserWhereInput{Name: ptr("ann"), Predicates: []predicate.User{user.Age.GT(35)}}
	_, err = json.Marshal(scoped)
	require.ErrorContains(t, err, "Predicates")
	b.Reset()
	scoped.MarshalGQL(&b)
	require.NoError(t, json.Unmarshal(b.Bytes(), &doc))
	require.ErrorContains(t, back.UnmarshalGQL(doc), "could not be written")
	require.ErrorContains(t, json.Unmarshal(b.Bytes(), &back), "could not be written")
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// The same filter, built at random, selects the same rows as velox's where
// input for it, or fails where velox's fails.
func TestWhereInput_EqualsVelox(t *testing.T) {
	ctx := context.Background()
	db, ref := open(t)
	rng := rand.New(rand.NewPCG(7, 11))
	roles := []user.RoleEnum{user.RoleAdmin, user.RoleUser, user.RoleGuest}
	for i := range 24 {
		u := newUser(t, db, fmt.Sprintf("u%02d", i), 20+i)
		_, err := client.User.UpdateByID(db, u.ID).Set(user.Role.Set(roles[i%3])).SaveOne(ctx)
		require.NoError(t, err)
		for j := range i % 4 {
			p := newPost(t, db, u, fmt.Sprintf("t%d-%d", i%5, j))
			if j%2 == 1 {
				_, err := client.Post.UpdateByID(db, p.ID).Set(post.Status.Set(post.StatusPublished)).SaveOne(ctx)
				require.NoError(t, err)
			}
		}
	}
	pool := []string{"u01", "u07", "u13", "u2", "1", "t1-0", "t3-1", "t", "@example.com", "zz"}
	var fill func(v reflect.Value, depth int)
	fill = func(v reflect.Value, depth int) {
		for i := range v.NumField() {
			f, name := v.Field(i), v.Type().Field(i).Name
			if name == "Predicates" || rng.IntN(100) >= 12 {
				continue
			}
			switch f.Kind() {
			case reflect.Pointer:
				el := reflect.New(f.Type().Elem())
				switch el.Elem().Kind() {
				case reflect.Struct: // Not
					if depth >= 2 {
						continue
					}
					fill(el.Elem(), depth+1)
				case reflect.Bool:
					el.Elem().SetBool(rng.IntN(2) == 0)
				default:
					el.Elem().SetString(pick(rng, f.Type().Elem(), pool))
				}
				f.Set(el)
			case reflect.Slice:
				n := 1 + rng.IntN(3)
				s := reflect.MakeSlice(f.Type(), n, n)
				for j := range n {
					if el := s.Index(j); el.Kind() == reflect.Pointer { // Or, And, HasPostsWith
						if depth >= 2 {
							s = s.Slice(0, 0)
							break
						}
						sub := reflect.New(el.Type().Elem())
						fill(sub.Elem(), depth+1)
						el.Set(sub)
					} else {
						el.SetString(pick(rng, el.Type(), pool))
					}
				}
				if s.Len() > 0 {
					f.Set(s)
				}
			}
		}
	}
	matched, failed := 0, 0
	for range 400 {
		var in filter.UserWhereInput
		fill(reflect.ValueOf(&in).Elem(), 0)
		var refIn reffilter.UserWhereInput
		mirror(reflect.ValueOf(&refIn).Elem(), reflect.ValueOf(&in).Elem())

		got, err := client.User.Query(db).Where(in.P()).OrderBy(user.ID.Asc()).IDs(ctx)
		p, refErr := refIn.Filter()
		var want []int
		if refErr == nil {
			q := ref.User.Query()
			if p != nil {
				q.Where(p)
			}
			want, refErr = q.IDs(ctx)
		}
		if refErr != nil {
			failed++
			assert.Error(t, err, "velox refuses %+v: %v", refIn, refErr)
			continue
		}
		require.NoError(t, err, "%+v", in)
		assert.ElementsMatch(t, want, got, "%+v", in)
		if len(got) > 0 && len(got) < 24 {
			matched++
		}
	}
	assert.Greater(t, matched, 40, "few of the random filters selected some rows and not all: the test compares little")
	assert.Greater(t, failed, 3, "no random filter was refused")
}

// pick returns a value for a string-kind type: one of an enum's, or a word.
func pick(rng *rand.Rand, t reflect.Type, pool []string) string {
	switch t {
	case reflect.TypeFor[user.RoleEnum]():
		return []string{"admin", "user", "guest"}[rng.IntN(3)]
	case reflect.TypeFor[post.StatusEnum]():
		return []string{"draft", "published", "archived"}[rng.IntN(3)]
	}
	return pool[rng.IntN(len(pool))]
}

// mirror copies a where input of this tree into velox's for the same schema:
// the same fields under the same names, the enum types being each tree's own.
func mirror(dst, src reflect.Value) {
	for i := range src.NumField() {
		name := src.Type().Field(i).Name
		if name == "Predicates" {
			continue
		}
		s, d := src.Field(i), dst.FieldByName(name)
		if !d.IsValid() {
			panic("velox's input has no field " + name)
		}
		switch s.Kind() {
		case reflect.Pointer:
			if s.IsNil() {
				continue
			}
			el := reflect.New(d.Type().Elem())
			if s.Elem().Kind() == reflect.Struct {
				mirror(el.Elem(), s.Elem())
			} else {
				el.Elem().Set(s.Elem().Convert(d.Type().Elem()))
			}
			d.Set(el)
		case reflect.Slice:
			if s.IsNil() {
				continue
			}
			out := reflect.MakeSlice(d.Type(), s.Len(), s.Len())
			for j := range s.Len() {
				if s.Index(j).Kind() == reflect.Pointer {
					el := reflect.New(d.Type().Elem().Elem())
					mirror(el.Elem(), s.Index(j).Elem())
					out.Index(j).Set(el)
				} else {
					out.Index(j).Set(s.Index(j).Convert(d.Type().Elem()))
				}
			}
			d.Set(out)
		default:
			d.Set(s.Convert(d.Type()))
		}
	}
}

func TestOrderInput(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	seedGraph(t, db)
	list := func(o *entity.UserOrder) []string {
		t.Helper()
		us, err := client.User.Query(db).OrderBy(o.Term()).All(ctx)
		require.NoError(t, err)
		return names(us)
	}
	assert.Equal(t, []string{"cat", "bob", "ann"}, list(&entity.UserOrder{Field: user.OrderFieldName, Direction: gqlinput.OrderDirectionDesc}))
	assert.Equal(t, []string{"ann", "bob", "cat"}, list(&entity.UserOrder{Field: user.OrderFieldName}), "no direction is ascending")
	assert.Equal(t, []string{"cat", "bob", "ann"}, list(&entity.UserOrder{Direction: gqlinput.OrderDirectionDesc}), "no field is the id")
	assert.Equal(t, predicate.UserOrderTerm{}, (*entity.UserOrder)(nil).Term())
	assert.Len(t, list(nil), 3, "no input orders by nothing")

	// A value the schema does not have cannot come from a request; one a
	// program makes up fails the query.
	_, err := client.User.Query(db).OrderBy((&entity.UserOrder{Field: "PASSWORD"}).Term()).All(ctx)
	require.ErrorContains(t, err, `"PASSWORD"`)
	_, err = client.User.Query(db).OrderBy((&entity.UserOrder{Field: "PASSWORD"}).Term()).Paginate(ctx, nil, nil, nil, nil)
	require.ErrorContains(t, err, `"PASSWORD"`)

	// The usual list resolver: filter, order, page.
	where := &filter.UserWhereInput{HasPosts: ptr(true)}
	page, err := client.User.Query(db).
		Where(where.P()).
		OrderBy((&entity.UserOrder{Field: user.OrderFieldName, Direction: gqlinput.OrderDirectionDesc}).Term()).
		Paginate(ctx, nil, intp(1), nil, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"bob"}, names(nodes(page)))
	assert.Equal(t, 2, page.TotalCount)
	assert.True(t, page.PageInfo.HasNextPage)
}

// The methods a GraphQL engine resolves an entity's edges by.
func TestEdgeResolvers(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	ann, _, cat := seedGraph(t, db)
	req := velox.WithExecutor(ctx, db) // what a server puts in the context of a request

	// To one: queried on the handle of the context.
	p := must(client.Post.Query(db).Where(post.Title.EQ("a1")).Only(ctx))
	author, err := p.Author(req)
	require.NoError(t, err)
	assert.Equal(t, "ann", author.Name)
	// Without a handle in the context there is nothing to run on.
	_, err = p.Author(ctx)
	require.ErrorIs(t, err, velox.ErrNoExecutor)
	// A neighbor the query loaded is returned as it is: no handle, no query.
	loaded := must(client.Post.Query(db).Where(post.Title.EQ("a1")).WithAuthor().Only(ctx))
	author, err = loaded.Author(ctx)
	require.NoError(t, err)
	assert.Equal(t, "ann", author.Name)
	// No neighbor is nil, not an error.
	stray := must(client.Pet.Create(db).Set(pet.Name.Set("rex")).Save(ctx))
	owner, err := stray.Owner(req)
	require.NoError(t, err)
	assert.Nil(t, owner)

	// To many: a page, with the arguments of the field.
	conn, err := ann.Posts(req, nil, intp(1), nil, nil, &entity.PostOrder{Field: post.OrderFieldTitle, Direction: gqlinput.OrderDirectionDesc}, nil)
	require.NoError(t, err)
	require.Len(t, conn.Edges, 1)
	assert.Equal(t, "a2", conn.Edges[0].Node.Title)
	assert.Equal(t, 2, conn.TotalCount)
	assert.True(t, conn.PageInfo.HasNextPage)
	conn, err = ann.Posts(req, conn.PageInfo.EndCursor, intp(5), nil, nil, &entity.PostOrder{Field: post.OrderFieldTitle, Direction: gqlinput.OrderDirectionDesc}, nil)
	require.NoError(t, err)
	require.Len(t, conn.Edges, 1)
	assert.Equal(t, "a1", conn.Edges[0].Node.Title)
	conn, err = ann.Posts(req, nil, nil, nil, nil, nil, &filter.PostWhereInput{Status: ptr(post.StatusPublished)})
	require.NoError(t, err)
	require.Len(t, conn.Edges, 1)
	assert.Equal(t, "a2", conn.Edges[0].Node.Title)
	conn, err = cat.Posts(req, nil, nil, nil, nil, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, conn.Edges)
	assert.Zero(t, conn.TotalCount)
	// A list of orders, for an entity whose schema takes one.
	comments, err := p.Comments(req, nil, nil, nil, nil, []*entity.CommentOrder{{Direction: gqlinput.OrderDirectionDesc}})
	require.NoError(t, err)
	assert.Empty(t, comments.Edges)
	_, err = ann.Posts(ctx, nil, nil, nil, nil, nil, nil)
	require.ErrorIs(t, err, velox.ErrNoExecutor)

	// On a transaction: the resolver sees what the transaction wrote.
	require.NoError(t, db.RunTx(ctx, func(tx *velox.Tx) error {
		newPost(t, tx, cat, "c1")
		conn, err := cat.Posts(velox.WithExecutor(ctx, tx), nil, nil, nil, nil, nil, nil)
		require.NoError(t, err)
		assert.Len(t, conn.Edges, 1)
		return nil
	}))

	// Every entity of the schema is a node.
	var _ client.Noder = ann
	var _ client.Noder = p
}

// An edge method declares its filter as an interface (so that package entity
// does not import package filter), and the interface still tells the where
// input of one entity from another's.
func TestEdgeResolvers_WhereParameter(t *testing.T) {
	m, ok := reflect.TypeFor[*entity.User]().MethodByName("Posts")
	require.True(t, ok)
	where := m.Type.In(m.Type.NumIn() - 1)
	require.Equal(t, reflect.Interface, where.Kind())
	assert.True(t, reflect.TypeFor[*filter.PostWhereInput]().Implements(where))
	assert.False(t, reflect.TypeFor[*filter.UserWhereInput]().Implements(where), "the filter of another entity")
}

// The node types of the schema, by which a node(id:) field finds a node
// whose type the id does not say. The test schema mixes id types (a UUID on
// Token, an int elsewhere), which is the normal case for every type but one.
func TestNodeTypes(t *testing.T) {
	ctx := context.Background()
	db, _ := open(t)
	ann, _, _ := seedGraph(t, db)
	tok, err := client.Token.Create(db).Set(token.Name.Set("t")).Save(ctx)
	require.NoError(t, err)

	types := client.NodeTypes()
	byName := map[string]client.NodeType{}
	var names []string
	for _, nt := range types {
		byName[nt.Name] = nt
		names = append(names, nt.Name)
	}
	assert.Equal(t, []string{"Comment", "Like", "Pet", "Post", "Tag", "Token", "User"}, names, "ordered by name")
	assert.Equal(t, "users", byName["User"].Table)

	// The id as the entity has it, and as an engine that reads every ID as
	// an int64 hands it over.
	for _, id := range []any{ann.ID, int64(ann.ID), uint32(ann.ID)} {
		n, err := byName["User"].Get(ctx, db, id)
		require.NoError(t, err, "%T", id)
		assert.Equal(t, ann.ID, n.(*entity.User).ID)
	}
	n, err := byName["Token"].Get(ctx, db, tok.ID)
	require.NoError(t, err)
	assert.Equal(t, tok.ID, n.(*entity.Token).ID)

	// An id of another type is the id of no node of the type, and so is an
	// id no row has; the node is then nil, not a nil *entity.User.
	for _, c := range []struct {
		typ string
		id  any
	}{
		{"User", tok.ID}, {"User", "1"}, {"User", int64(1) << 40}, {"User", nil}, {"User", -1},
		{"Token", ann.ID}, {"Token", tok.ID.String()},
	} {
		n, err := byName[c.typ].Get(ctx, db, c.id)
		assert.True(t, velox.IsNotFound(err), "%s %v (%T): %v", c.typ, c.id, c.id, err)
		assert.Nil(t, n, "%s %v", c.typ, c.id)
	}

	// The scan the doc describes finds each node by its id alone.
	find := func(id any) client.Noder {
		for _, nt := range types {
			n, err := nt.Get(ctx, db, id)
			if velox.IsNotFound(err) {
				continue
			}
			require.NoError(t, err)
			return n
		}
		return nil
	}
	assert.IsType(t, &entity.Token{}, find(tok.ID))
	assert.Nil(t, find(int64(1)<<40))
}
