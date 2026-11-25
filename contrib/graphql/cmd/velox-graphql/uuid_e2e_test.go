package main

import (
	"strings"
	"testing"
)

// e2eUUIDSchema is the schema of the UUID app: both keys are UUIDs, as a
// schema's keys are of one Go type, so ID binds to veloxgql.UUID. A Volume
// may stand on a Shelf, by a nullable UUID key on its own row.
const e2eUUIDSchema = `package schema

import (
	"github.com/google/uuid"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type Shelf struct{ schema.Schema }

func (Shelf) Fields() []schema.Field {
	return []schema.Field{field.UUID("id", uuid.UUID{}).Default(uuid.New), field.String("name")}
}

func (Shelf) Edges() []schema.Edge {
	return []schema.Edge{edge.To("volumes", Volume.Type)}
}

func (Shelf) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.Mutations(graphql.MutationCreate)}
}

type Volume struct{ schema.Schema }

func (Volume) Fields() []schema.Field {
	return []schema.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.String("title").NotEmpty().Annotations(graphql.WhereOps(graphql.OpEQ)),
		field.UUID("shelf_id", uuid.UUID{}).Optional().Nillable(),
	}
}

func (Volume) Edges() []schema.Edge {
	return []schema.Edge{edge.From("shelf", Shelf.Type).Ref("volumes").Unique().Field("shelf_id")}
}

func (Volume) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.QueryField("volumes"), graphql.RelayConnection(), graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate)}
}
`

// e2eUUIDMutations is the app's Mutation type, which velox-graphql leaves to
// the application (ADR 0033): an ID argument is the volume's key.
const e2eUUIDMutations = `type Mutation {
  createShelf(input: CreateShelfInput!): Shelf!
  createVolume(input: CreateVolumeInput!): Volume!
  updateVolume(id: ID!, input: UpdateVolumeInput!): Volume!
}
`

// e2eUUIDResolvers are the app's resolvers: an ID argument is a uuid.UUID,
// used as it is.
const e2eUUIDResolvers = `package graph

import (
	"context"

	"github.com/google/uuid"

	"example.com/app/gen"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// CreateShelf is the resolver for the createShelf field.
func (r *mutationResolver) CreateShelf(ctx context.Context, input gen.CreateShelfInput) (*gen.Shelf, error) {
	x := &gen.Shelf{}
	if err := r.DB.Insert(x).SetInput(input).Exec(ctx); err != nil {
		return nil, err
	}
	return x, nil
}

// CreateVolume is the resolver for the createVolume field.
func (r *mutationResolver) CreateVolume(ctx context.Context, input gen.CreateVolumeInput) (*gen.Volume, error) {
	x := &gen.Volume{}
	if err := r.DB.Insert(x).SetInput(input).Exec(ctx); err != nil {
		return nil, err
	}
	return x, nil
}

// UpdateVolume is the resolver for the updateVolume field.
func (r *mutationResolver) UpdateVolume(ctx context.Context, id uuid.UUID, input gen.UpdateVolumeInput) (*gen.Volume, error) {
	return r.DB.Update[gen.Volume]().Where(gen.Volumes.ID.EQ(id)).SetInput(input).One(ctx)
}

// Volumes is the resolver for the volumes field.
func (r *queryResolver) Volumes(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, where *gen.VolumeWhereInput) (*gen.VolumeConnection, error) {
	return r.DB.Query[gen.Volume]().Paginate(ctx, after, first, before, last, gen.WithVolumeFilter(where))
}

// Mutation returns MutationResolver implementation.
func (r *Resolver) Mutation() MutationResolver { return &mutationResolver{r} }

// Query returns QueryResolver implementation.
func (r *Resolver) Query() QueryResolver { return &queryResolver{r} }

type mutationResolver struct{ *Resolver }
type queryResolver struct{ *Resolver }
`

// e2eUUIDRun is the app's test, through gqlgen on SQLite: a UUID ID goes out
// as its string, and comes back in as a key, an edge's key and a where
// input's ID; a string that is no UUID is BAD_USER_INPUT, and so is a title
// the validator refuses, the error of Setters that SetInput keeps.
const e2eUUIDRun = `package app_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"example.com/app/graph"
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
)

type reply struct {
	Data   map[string]any
	Errors []struct {
		Message    string
		Extensions map[string]any
	}
}

func TestUUIDIDs(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	for _, s := range []string{
		"CREATE TABLE shelves (id TEXT PRIMARY KEY, name TEXT NOT NULL)",
		"CREATE TABLE volumes (id TEXT PRIMARY KEY, title TEXT NOT NULL, shelf_id TEXT REFERENCES shelves (id))",
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	vdb := velox.Open(velox.SQLite, db).WithoutPolicy("test")
	srv := handler.New(graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{DB: vdb}}))
	srv.Use(gqlgenx.Transactioner{DB: vdb})
	srv.SetErrorPresenter(gqlgenx.ErrorPresenter(func(_ context.Context, err error) { t.Logf("internal: %v", err) }))
	srv.AddTransport(transport.POST{})
	do := func(query string) reply {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"query": query})
		req := httptest.NewRequest("POST", "/", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		var r reply
		if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
			t.Fatalf("%s: %v", w.Body.String(), err)
		}
		return r
	}
	ok := func(query string) map[string]any {
		t.Helper()
		r := do(query)
		if len(r.Errors) > 0 {
			t.Fatalf("%s: %+v", query, r.Errors)
		}
		return r.Data
	}

	shelf := ok(` + "`" + `mutation { createShelf(input: {name: "a"}) { id } }` + "`" + `)["createShelf"].(map[string]any)["id"].(string)
	if _, err := uuid.Parse(shelf); err != nil {
		t.Fatalf("the shelf's ID %q is not its UUID: %v", shelf, err)
	}
	vol := ok(` + "`" + `mutation { createVolume(input: {title: "t", shelfID: "` + "`" + ` + shelf + ` + "`" + `"}) { id shelf { id name } } }` + "`" + `)["createVolume"].(map[string]any)
	id := vol["id"].(string)
	if got := vol["shelf"].(map[string]any); got["id"] != shelf || got["name"] != "a" {
		t.Errorf("the volume's shelf: %v, want %s", got, shelf)
	}
	upd := ok(` + "`" + `mutation { updateVolume(id: "` + "`" + ` + id + ` + "`" + `", input: {title: "u"}) { id title } }` + "`" + `)["updateVolume"].(map[string]any)
	if upd["id"] != id || upd["title"] != "u" {
		t.Errorf("updateVolume: %v", upd)
	}
	edges := ok(` + "`" + `{ volumes(where: {id: "` + "`" + ` + id + ` + "`" + `"}) { edges { node { title } } } }` + "`" + `)["volumes"].(map[string]any)["edges"].([]any)
	if len(edges) != 1 || edges[0].(map[string]any)["node"].(map[string]any)["title"] != "u" {
		t.Errorf("volumes by ID: %v", edges)
	}

	r := do(` + "`" + `mutation { updateVolume(id: "not-a-uuid", input: {title: "x"}) { id } }` + "`" + `)
	if len(r.Errors) != 1 || r.Errors[0].Extensions["code"] != "BAD_USER_INPUT" {
		t.Errorf("an ID that is no UUID: %+v", r.Errors)
	}
	r = do(` + "`" + `mutation { updateVolume(id: "` + "`" + ` + id + ` + "`" + `", input: {title: ""}) { id } }` + "`" + `)
	if len(r.Errors) != 1 || r.Errors[0].Extensions["code"] != "BAD_USER_INPUT" || !strings.Contains(r.Errors[0].Message, "title") {
		t.Errorf("an empty title, refused by Setters through SetInput: %+v", r.Errors)
	}
}
`

// e2eUUIDGqlgen is e2eCollectGqlgen with ID bound to veloxgql.UUID, the
// model of a schema whose keys are UUIDs.
var e2eUUIDGqlgen = strings.Replace(e2eCollectGqlgen, "github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx.Int64ID", "github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx.UUID", 1)

// TestAppUUIDKeys generates an app whose keys are UUIDs, runs gqlgen over
// it with ID bound to veloxgql.UUID, and serves it: a resolver takes the
// uuid.UUID, an input's edge ID and a where input's ID are UUIDs, and a
// malformed one is BAD_USER_INPUT (ADR 0061).
func TestAppUUIDKeys(t *testing.T) {
	t.Parallel() // its own module, in its own directory
	if testing.Short() {
		t.Skip("builds a module")
	}
	_, run := app(t, map[string]string{
		"schema/schema.go":          e2eUUIDSchema,
		"gqlgen.yml":                e2eUUIDGqlgen,
		"graph/mutation.graphql":    e2eUUIDMutations,
		"graph/schema.resolvers.go": e2eUUIDResolvers,
		"graph/resolver.go":         e2eResolver,
		"uuid_test.go":              e2eUUIDRun,
	}, true)
	for _, args := range [][]string{
		{"tool", "velox-gen", "-out", "gen", "-plugin", "velox-graphql", "./schema"},
		{"tool", "gqlgen", "generate"},
		{"mod", "tidy", "-e"},
		{"vet", "./..."},
		{"test", "-count=1", "-v", "."},
	} {
		if out, err := run(args...); err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}
