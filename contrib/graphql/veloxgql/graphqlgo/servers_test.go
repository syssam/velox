package graphqlgo_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	gqlgen "github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/syssam/graphql-go"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
	"github.com/syssam/velox/contrib/graphql/veloxgql/graphqlgo"
)

// A server runs a query, with its variables, and returns the response's
// JSON.
type server func(t testing.TB, ctx context.Context, query string, vars map[string]any) string

// graphqlgoSchema is paritySDL bound to the resolvers by
// graphqlgoSchemaOptions.
func graphqlgoSchema(t testing.TB) *graphql.Schema {
	t.Helper()
	s, err := graphql.NewSchema(graphql.SDL(paritySDL), graphqlgoSchemaOptions())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// graphqlgoSchemaOptions bind paritySDL to the resolvers by graphql-go's
// bindings, as gqlc writes them: a field that takes a context is Resolve or
// ResolveArgs, the others Field.
func graphqlgoSchemaOptions() graphql.SchemaOption {
	type connArgs struct {
		First *int
		After *veloxgql.Cursor
		Where *authorWhere
	}
	type pageArgs struct {
		First *int
		After *veloxgql.Cursor
	}
	type nodeArgs struct{ ID int64 }
	type postArgs struct {
		Author int64
		Title  string
	}
	return graphql.Options(
		graphql.ScalarMarshaler[veloxgql.Cursor]("Cursor"),
		graphql.Input[authorWhere]("AuthorWhereInput"),
		graphql.Query(
			graphql.ResolveArgs("authors", func(ctx context.Context, _ graphql.Root, a connArgs) (*conn[author], error) {
				return authors(ctx, a.First, a.After, a.Where)
			}),
			graphql.ResolveArgs("node", func(ctx context.Context, _ graphql.Root, a nodeArgs) (any, error) { return node(ctx, a.ID) }),
		),
		graphql.Mutation(
			graphql.ResolveArgs("addPost", func(ctx context.Context, _ graphql.Root, a postArgs) (*post, error) {
				return addPost(ctx, a.Author, a.Title, false)
			}),
			graphql.ResolveArgs("failPost", func(ctx context.Context, _ graphql.Root, a postArgs) (*post, error) {
				return addPost(ctx, a.Author, a.Title, true)
			}),
		),
		graphql.Object[author]("Author",
			graphql.Field("id", func(a *author) int64 { return a.ID }),
			graphql.Field("name", func(a *author) string { return a.Name }),
			graphql.ResolveArgs("posts", func(ctx context.Context, a *author, args pageArgs) (*conn[post], error) {
				return authorPosts(ctx, a, args.First, args.After)
			}),
		),
		graphql.Object[post]("Post",
			graphql.Field("id", func(p *post) int64 { return p.ID }),
			graphql.Field("title", func(p *post) string { return p.Title }),
			graphql.Resolve("author", postAuthor),
			graphql.Resolve("tags", postTags),
		),
		graphql.Object[tag]("Tag",
			graphql.Field("id", func(x *tag) int64 { return x.ID }),
			graphql.Field("name", func(x *tag) string { return x.Name }),
		),
		connBindings[author]("Author"),
		connBindings[post]("Post"),
		graphql.Object[veloxgql.PageInfo]("PageInfo",
			graphql.Field("hasNextPage", func(p *veloxgql.PageInfo) bool { return p.HasNextPage }),
			graphql.Field("hasPreviousPage", func(p *veloxgql.PageInfo) bool { return p.HasPreviousPage }),
			graphql.Field("startCursor", func(p *veloxgql.PageInfo) *veloxgql.Cursor { return p.StartCursor }),
			graphql.Field("endCursor", func(p *veloxgql.PageInfo) *veloxgql.Cursor { return p.EndCursor }),
		),
	)
}

// connBindings binds the connection and the edge of typ's nodes.
func connBindings[E any](typ string) graphql.SchemaOption {
	return graphql.Options(
		graphql.Object[conn[E]](typ+"Connection",
			graphql.Resolve("edges", func(ctx context.Context, c *conn[E]) ([]*edge[E], error) { return c.edges(ctx) }),
			graphql.Resolve("pageInfo", func(ctx context.Context, c *conn[E]) (*veloxgql.PageInfo, error) { return c.pageInfo(ctx) }),
			graphql.Resolve("totalCount", func(ctx context.Context, c *conn[E]) (int, error) { return c.totalCount(ctx) }),
		),
		graphql.Object[edge[E]](typ+"Edge",
			graphql.Field("node", func(e *edge[E]) *E { return e.Node }),
			graphql.Field("cursor", func(e *edge[E]) veloxgql.Cursor { return e.Cursor }),
		),
	)
}

// graphqlgoServer serves paritySDL through graphql-go, with
// graphqlgo.With(db, opts...) and graphqlgo.ErrorPresenter.
func graphqlgoServer(t testing.TB, db *velox.DB, opts ...veloxgql.Option) server {
	return executorServer(graphql.NewExecutor(graphqlgoSchema(t),
		append(graphqlgo.With(db, opts...), graphql.WithErrorPresenter(graphqlgo.ErrorPresenter(nil)))...))
}

// executorServer runs queries on e.
func executorServer(e *graphql.Executor) server {
	return func(t testing.TB, ctx context.Context, query string, vars map[string]any) string {
		t.Helper()
		req := &graphql.Request{Query: query}
		if vars != nil {
			b, err := json.Marshal(vars)
			if err != nil {
				t.Fatal(err)
			}
			req.Variables = b
		}
		resp := e.Execute(ctx, req)
		defer resp.Release()
		b, err := resp.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
}

// resolver resolves a field of the gqlgen harness from its parent and its
// arguments.
type resolver func(ctx context.Context, parent any, args map[string]any) (any, error)

// gqlgenResolvers binds paritySDL's fields for the gqlgen harness, by
// "Type.field", calling the resolvers graphql-go's schema calls.
func gqlgenResolvers() map[string]resolver {
	return map[string]resolver{
		"Query.authors": func(ctx context.Context, _ any, a map[string]any) (any, error) {
			var w *authorWhere
			if v, ok := a["where"]; ok && v != nil {
				w = new(authorWhere)
				b, _ := json.Marshal(v)
				if err := json.Unmarshal(b, w); err != nil {
					return nil, err
				}
			}
			after, err := cursorArg(a["after"])
			if err != nil {
				return nil, err
			}
			return authors(ctx, intArg(a["first"]), after, w)
		},
		"Query.node": func(ctx context.Context, _ any, a map[string]any) (any, error) { return node(ctx, idArg(a["id"])) },
		"Mutation.addPost": func(ctx context.Context, _ any, a map[string]any) (any, error) {
			return addPost(ctx, idArg(a["author"]), a["title"].(string), false)
		},
		"Mutation.failPost": func(ctx context.Context, _ any, a map[string]any) (any, error) {
			return addPost(ctx, idArg(a["author"]), a["title"].(string), true)
		},
		"Author.id":   func(_ context.Context, p any, _ map[string]any) (any, error) { return p.(*author).ID, nil },
		"Author.name": func(_ context.Context, p any, _ map[string]any) (any, error) { return p.(*author).Name, nil },
		"Author.posts": func(ctx context.Context, p any, a map[string]any) (any, error) {
			after, err := cursorArg(a["after"])
			if err != nil {
				return nil, err
			}
			return authorPosts(ctx, p.(*author), intArg(a["first"]), after)
		},
		"Post.id":     func(_ context.Context, p any, _ map[string]any) (any, error) { return p.(*post).ID, nil },
		"Post.title":  func(_ context.Context, p any, _ map[string]any) (any, error) { return p.(*post).Title, nil },
		"Post.author": func(ctx context.Context, p any, _ map[string]any) (any, error) { return postAuthor(ctx, p.(*post)) },
		"Post.tags":   func(ctx context.Context, p any, _ map[string]any) (any, error) { return postTags(ctx, p.(*post)) },
		"Tag.id":      func(_ context.Context, p any, _ map[string]any) (any, error) { return p.(*tag).ID, nil },
		"Tag.name":    func(_ context.Context, p any, _ map[string]any) (any, error) { return p.(*tag).Name, nil },
		"AuthorConnection.edges": func(ctx context.Context, p any, _ map[string]any) (any, error) {
			return p.(*conn[author]).edges(ctx)
		},
		"AuthorConnection.pageInfo": func(ctx context.Context, p any, _ map[string]any) (any, error) {
			return p.(*conn[author]).pageInfo(ctx)
		},
		"AuthorConnection.totalCount": func(ctx context.Context, p any, _ map[string]any) (any, error) {
			return p.(*conn[author]).totalCount(ctx)
		},
		"AuthorEdge.node":   func(_ context.Context, p any, _ map[string]any) (any, error) { return p.(*edge[author]).Node, nil },
		"AuthorEdge.cursor": func(_ context.Context, p any, _ map[string]any) (any, error) { return p.(*edge[author]).Cursor, nil },
		"PostConnection.edges": func(ctx context.Context, p any, _ map[string]any) (any, error) {
			return p.(*conn[post]).edges(ctx)
		},
		"PostConnection.pageInfo": func(ctx context.Context, p any, _ map[string]any) (any, error) {
			return p.(*conn[post]).pageInfo(ctx)
		},
		"PostConnection.totalCount": func(ctx context.Context, p any, _ map[string]any) (any, error) {
			return p.(*conn[post]).totalCount(ctx)
		},
		"PostEdge.node":   func(_ context.Context, p any, _ map[string]any) (any, error) { return p.(*edge[post]).Node, nil },
		"PostEdge.cursor": func(_ context.Context, p any, _ map[string]any) (any, error) { return p.(*edge[post]).Cursor, nil },
		"PageInfo.hasNextPage": func(_ context.Context, p any, _ map[string]any) (any, error) {
			return p.(*veloxgql.PageInfo).HasNextPage, nil
		},
		"PageInfo.hasPreviousPage": func(_ context.Context, p any, _ map[string]any) (any, error) {
			return p.(*veloxgql.PageInfo).HasPreviousPage, nil
		},
		"PageInfo.startCursor": func(_ context.Context, p any, _ map[string]any) (any, error) {
			return p.(*veloxgql.PageInfo).StartCursor, nil
		},
		"PageInfo.endCursor": func(_ context.Context, p any, _ map[string]any) (any, error) {
			return p.(*veloxgql.PageInfo).EndCursor, nil
		},
	}
}

// intArg is an Int argument of the gqlgen harness, or nil.
func intArg(v any) *int {
	switch n := v.(type) {
	case int64:
		i := int(n)
		return &i
	case json.Number:
		i, _ := n.Int64()
		x := int(i)
		return &x
	}
	return nil
}

// idArg is an ID argument of the gqlgen harness, a string or a number.
func idArg(v any) int64 {
	switch n := v.(type) {
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	case int64:
		return n
	case json.Number:
		i, _ := n.Int64()
		return i
	}
	return 0
}

// cursorArg is a Cursor argument of the gqlgen harness, or nil.
func cursorArg(v any) (*veloxgql.Cursor, error) {
	if v == nil {
		return nil, nil
	}
	var c veloxgql.Cursor
	if err := c.UnmarshalGQL(v); err != nil {
		return nil, err
	}
	return &c, nil
}

// gqlgenServer serves paritySDL through gqlgen, with
// gqlgenx.NewExtension(db, opts...) and gqlgenx.ErrorPresenter, on an
// executable schema that resolves as gqlgen's generated code does: a
// mutation's root fields one after another, every other object's fields
// concurrently, each through the field middleware, its children after it.
func gqlgenServer(t testing.TB, db *velox.DB, opts ...veloxgql.Option) server {
	schema := gqlparser.MustLoadSchema(&ast.Source{Input: paritySDL})
	resolvers := gqlgenResolvers()
	es := &gqlgen.ExecutableSchemaMock{
		SchemaFunc: func() *ast.Schema { return schema },
		ComplexityFunc: func(context.Context, string, string, int, map[string]any) (int, bool) {
			return 0, false
		},
		ExecFunc: func(ctx context.Context) gqlgen.ResponseHandler {
			oc := gqlgen.GetOperationContext(ctx)
			done := false
			return func(ctx context.Context) *gqlgen.Response {
				if done {
					return nil
				}
				done = true
				root := "Query"
				if oc.Operation.Operation == ast.Mutation {
					root = "Mutation"
				}
				h := &harness{oc: oc, schema: schema, resolvers: resolvers}
				var buf bytes.Buffer
				h.set(ctx, oc.Operation.SelectionSet, root, nil, root == "Query").MarshalGQL(&buf)
				return &gqlgen.Response{Data: buf.Bytes()}
			}
		},
	}
	srv := handler.New(es)
	srv.AddTransport(transport.POST{})
	srv.Use(gqlgenx.NewExtension(db, opts...))
	srv.SetErrorPresenter(gqlgenx.ErrorPresenter(nil))
	return func(t testing.TB, ctx context.Context, query string, vars map[string]any) string {
		t.Helper()
		body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w.Body.String()
	}
}

// harness resolves an operation as gqlgen's generated code does.
type harness struct {
	oc        *gqlgen.OperationContext
	schema    *ast.Schema
	resolvers map[string]resolver
}

// set resolves the fields of sels on parent, an object of the type typ.
func (h *harness) set(ctx context.Context, sels ast.SelectionSet, typ string, parent any, concurrent bool) gqlgen.Marshaler {
	satisfies := []string{typ}
	if d := h.schema.Types[typ]; d != nil {
		for _, i := range h.schema.GetImplements(d) {
			satisfies = append(satisfies, i.Name)
		}
	}
	fields := gqlgen.CollectFields(h.oc, sels, satisfies)
	fs := gqlgen.NewFieldSet(fields)
	for i, f := range fields {
		if f.Name == "__typename" {
			fs.Values[i] = gqlgen.MarshalString(typ)
			continue
		}
		run := func(ctx context.Context) gqlgen.Marshaler { return h.field(ctx, f, typ, parent) }
		if concurrent {
			fs.Concurrently(i, run)
		} else {
			fs.Values[i] = run(ctx)
		}
	}
	fs.Dispatch(ctx)
	return fs
}

// field resolves the field f of parent, an object of the type typ: its
// resolver through the field middleware, then its children.
func (h *harness) field(ctx context.Context, f gqlgen.CollectedField, typ string, parent any) (ret gqlgen.Marshaler) {
	fc := &gqlgen.FieldContext{Object: typ, Field: f, Args: f.ArgumentMap(h.oc.Variables), IsMethod: true, IsResolver: true}
	ctx = gqlgen.WithFieldContext(ctx, fc)
	defer func() {
		if p := recover(); p != nil {
			gqlgen.AddError(ctx, h.oc.Recover(ctx, p))
			ret = gqlgen.Null
		}
	}()
	r := h.resolvers[typ+"."+f.Name]
	res, err := h.oc.ResolverMiddleware(ctx, func(ctx context.Context) (any, error) { return r(ctx, parent, fc.Args) })
	if err != nil {
		gqlgen.AddError(ctx, err)
		return gqlgen.Null
	}
	fc.Result = res
	return h.value(ctx, f, f.Definition.Type, res)
}

// value writes res, of the type t.
func (h *harness) value(ctx context.Context, f gqlgen.CollectedField, t *ast.Type, res any) gqlgen.Marshaler {
	if isNil(res) {
		return gqlgen.Null
	}
	if t.Elem != nil {
		v := listOf(res)
		out := make(gqlgen.Array, len(v))
		for i, x := range v {
			ctx := gqlgen.WithFieldContext(ctx, &gqlgen.FieldContext{Index: &i, Result: x})
			out[i] = h.value(ctx, f, t.Elem, x)
		}
		return out
	}
	switch t.NamedType {
	case "ID":
		return gqlgen.MarshalString(strconv.FormatInt(res.(int64), 10))
	case "Cursor":
		switch c := res.(type) {
		case veloxgql.Cursor:
			return gqlgen.WriterFunc(c.MarshalGQL)
		case *veloxgql.Cursor:
			return gqlgen.WriterFunc(c.MarshalGQL)
		}
	}
	if len(f.Selections) == 0 {
		b, _ := json.Marshal(res)
		return rawJSON(b)
	}
	return h.set(ctx, f.Selections, objectType(t.NamedType, res), res, true)
}

// objectType is the object type of res at a position of the type typ: the
// type of its Go value for the interface Node.
func objectType(typ string, res any) string {
	if typ != "Node" {
		return typ
	}
	switch res.(type) {
	case *author:
		return "Author"
	case *post:
		return "Post"
	case *tag:
		return "Tag"
	}
	return typ
}

// isNil reports whether res is nil or a nil pointer.
func isNil(res any) bool {
	switch x := res.(type) {
	case nil:
		return true
	case *author:
		return x == nil
	case *post:
		return x == nil
	case *veloxgql.Cursor:
		return x == nil
	}
	return false
}

// listOf is res, a slice of the harness's, as a []any.
func listOf(res any) []any {
	var out []any
	switch v := res.(type) {
	case []*edge[author]:
		for _, x := range v {
			out = append(out, x)
		}
	case []*edge[post]:
		for _, x := range v {
			out = append(out, x)
		}
	case []*tag:
		for _, x := range v {
			out = append(out, x)
		}
	}
	return out
}

// rawJSON is a resolved leaf value.
type rawJSON []byte

func (j rawJSON) MarshalGQL(w io.Writer) { _, _ = w.Write(j) }

// normal is the JSON of a response, decoded, with each error's locations
// left out: graphql-go gives them, the harness does not.
func normal(t testing.TB, s string) any {
	t.Helper()
	var v map[string]any
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		t.Fatalf("%v: %s", err, s)
	}
	if errs, ok := v["errors"].([]any); ok {
		for _, e := range errs {
			delete(e.(map[string]any), "locations")
		}
	}
	return v
}
