package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// e2eResolversSchema is the schema of the resolvers e2e app: an Asset that
// holds the key organization_id of a type the application declares itself,
// PublicOrganization, and a list it computes per row. Both are graphql.Map
// fields, which gqlgen asks resolvers for.
const e2eResolversSchema = `package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

type Asset struct{ schema.Schema }

func (Asset) Fields() []schema.Field {
	return []schema.Field{
		field.String("name"),
		field.Int64("organization_id"),
	}
}

func (Asset) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField("assets"), graphql.RelayConnection(),
		graphql.Resolvers(
			graphql.Map("organization", "PublicOrganization").WithComment("Owning organization."),
			graphql.Map("tags(first: Int)", "[String!]!"),
		),
	}
}
`

// e2eResolversOwn is the application's own SDL, which declares the type the
// mapped field returns: the plugin does not look for it.
const e2eResolversOwn = `type PublicOrganization {
  name: String!
  key: Int!
}
`

// e2eResolversResolvers are the resolvers: the root query's, and the two
// fields' of Asset, which read the row's own organization_id.
const e2eResolversResolvers = `package graph

import (
	"context"

	"example.com/app/gen"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// Assets is the resolver for the assets field.
func (r *queryResolver) Assets(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int) (*gen.AssetConnection, error) {
	return r.DB.Query[gen.Asset]().Paginate(ctx, after, first, before, last)
}

// Organization is the resolver for the organization field. The selection did
// not ask for organizationID: the row has it because a mapped field's
// selection reads every column.
func (r *assetResolver) Organization(ctx context.Context, obj *gen.Asset) (*PublicOrganization, error) {
	return &PublicOrganization{Name: "org-of-" + obj.Name, Key: int(obj.OrganizationID)}, nil
}

// Tags is the resolver for the tags field.
func (r *assetResolver) Tags(ctx context.Context, obj *gen.Asset, first *int) ([]string, error) {
	tags := []string{obj.Name + "-a", obj.Name + "-b", obj.Name + "-c"}
	if first != nil && *first < len(tags) {
		tags = tags[:*first]
	}
	return tags, nil
}

// Asset returns AssetResolver implementation.
func (r *Resolver) Asset() AssetResolver { return &assetResolver{r} }

// Query returns QueryResolver implementation.
func (r *Resolver) Query() QueryResolver { return &queryResolver{r} }

type assetResolver struct{ *Resolver }
type queryResolver struct{ *Resolver }
`

// e2eResolversRun is the application's test: an asset's organization and
// tags are served by the resolvers, through one query that selects neither
// the key nor the name the resolvers read.
const e2eResolversRun = `package app_test

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	_ "modernc.org/sqlite"

	"example.com/app/graph"
	"github.com/syssam/velox"
)

func serve(t *testing.T, query string) string {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	for _, s := range []string{
		"CREATE TABLE assets (id INTEGER PRIMARY KEY, name TEXT NOT NULL, organization_id INTEGER NOT NULL)",
		"INSERT INTO assets (id, name, organization_id) VALUES (1, 'pump', 7)",
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	srv := handler.New(graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{DB: velox.Open(velox.SQLite, db)}}))
	srv.AddTransport(transport.POST{})
	req := httptest.NewRequest("POST", "/", strings.NewReader(` + "`" + `{"query":"` + "`" + ` + query + ` + "`" + `"}` + "`" + `))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.Background())
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w.Body.String()
}

// TestMappedFields: the resolvers answer, and they read columns the query did
// not select (name, organization_id).
func TestMappedFields(t *testing.T) {
	got := serve(t, "{ assets { edges { node { id organization { name key } tags(first: 2) } } } }")
	want := ` + "`" + `"organization":{"name":"org-of-pump","key":7},"tags":["pump-a","pump-b"]` + "`" + `
	if !strings.Contains(got, want) || strings.Contains(got, "errors") {
		t.Errorf("response %s, want it to hold %s", got, want)
	}
}

// TestGeneratedFieldsStillNarrow: a query that selects no mapped field reads
// only the columns it names.
func TestGeneratedFieldsStillNarrow(t *testing.T) {
	got := serve(t, "{ assets { edges { node { id name } } } }")
	if !strings.Contains(got, ` + "`" + `"name":"pump"` + "`" + `) || strings.Contains(got, "errors") {
		t.Errorf("response %s", got)
	}
}
`

// TestAppResolvers generates an application with graphql.Resolvers, runs
// gqlgen over it with the application's own SDL for the mapped type, and
// serves the mapped fields through its resolvers.
func TestAppResolvers(t *testing.T) {
	t.Parallel() // its own module, in its own directory
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir, run := app(t, map[string]string{
		"schema/schema.go":          e2eResolversSchema,
		"gqlgen.yml":                e2eCollectGqlgen,
		"graph/own.graphql":         e2eResolversOwn,
		"graph/schema.resolvers.go": e2eResolversResolvers,
		"graph/resolver.go":         e2eResolver,
		"resolvers_test.go":         e2eResolversRun,
	}, true)
	if out, err := run("tool", "velox-gen", "-out", "gen", "-plugin", "velox-graphql", "./schema"); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	sdlText, err := os.ReadFile(filepath.Join(dir, "gen", "velox.graphql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"  \"\"\"Owning organization.\"\"\"\n  organization: PublicOrganization @goField(forceResolver: true)\n",
		"  tags(first: Int): [String!]! @goField(forceResolver: true)\n",
	} {
		if !strings.Contains(string(sdlText), want) {
			t.Errorf("no %q in\n%s", want, sdlText)
		}
	}
	if out, err := run("tool", "gqlgen", "generate"); err != nil {
		t.Fatalf("gqlgen generate: %v\n%s", err, out)
	}
	if out, err := run("mod", "tidy", "-e"); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}
	if out, err := run("vet", "./..."); err != nil {
		t.Fatalf("go vet: %v\n%s", err, out)
	}
	out, err := run("test", "-count=1", "-v", ".")
	if err != nil {
		t.Fatalf("go test: %v\n%s", err, out)
	}
	for _, name := range []string{"TestMappedFields", "TestGeneratedFieldsStillNarrow"} {
		if !strings.Contains(out, "--- PASS: "+name+" ") {
			t.Errorf("no PASS of %s in\n%s", name, out)
		}
	}
}
