package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// e2eEnumSchema is the schema of the enum e2e app: an entity renamed in
// GraphQL (Alarm is the type Alert) with a field of the application's own
// enum type.
const e2eEnumSchema = `package schema

import (
	"example.com/app/levels"
	"github.com/syssam/velox2/contrib/graphql"
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/field"
)

type Alarm struct{ schema.Schema }

func (Alarm) Fields() []schema.Field {
	return []schema.Field{field.Enum("level").GoType(levels.Level(""))}
}

func (Alarm) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.Type("Alert"), graphql.QueryField("alerts")}
}
`

// e2eLevelsGQL is the package of Alarm's own enum type, with the marshalers
// gqlgen needs of a type that @goModel binds: they write (as JSON) and read the
// GraphQL value, the stored one upper-cased.
const e2eLevelsGQL = `package levels

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

type Level string

func (Level) Values() []string { return []string{"low", "high"} }

// MarshalGQL writes l as the GraphQL enum value, a JSON string.
func (l Level) MarshalGQL(w io.Writer) {
	_, _ = io.WriteString(w, strconv.Quote(strings.ToUpper(string(l))))
}

// UnmarshalGQL reads the GraphQL enum value.
func (l *Level) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("level: %T is not a string", v)
	}
	if s != "LOW" && s != "HIGH" {
		return fmt.Errorf("level: %s is not a level", strconv.Quote(s))
	}
	*l = Level(strings.ToLower(s))
	return nil
}
`

// e2eEnumResolvers are the root query's resolvers, one line each; gqlgen
// keeps them when it generates.
const e2eEnumResolvers = `package graph

import (
	"context"

	"example.com/app/gen"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
)

// Node is the resolver for the node field.
func (r *queryResolver) Node(ctx context.Context, id string) (veloxgql.Noder, error) {
	return gen.Node(ctx, id)
}

// Nodes is the resolver for the nodes field.
func (r *queryResolver) Nodes(ctx context.Context, ids []string) ([]veloxgql.Noder, error) {
	return gen.Nodes(ctx, ids)
}

// Alerts is the resolver for the alerts field.
func (r *queryResolver) Alerts(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int) (*gen.AlertConnection, error) {
	return gen.AlertsConnection(ctx, after, first, before, last)
}

// Query returns QueryResolver implementation.
func (r *Resolver) Query() QueryResolver { return &queryResolver{r} }

type queryResolver struct{ *Resolver }
`

// e2eEnumRun is the application's test: an alarm of level high is served as
// the GraphQL value HIGH, through the enum that @goModel binds to the
// application's type.
const e2eEnumRun = `package app_test

import (
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	_ "modernc.org/sqlite"

	"example.com/app/graph"
	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
)

func TestAlertLevel(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	for _, s := range []string{
		"CREATE TABLE alarms (id INTEGER PRIMARY KEY, level TEXT NOT NULL)",
		"INSERT INTO alarms (id, level) VALUES (1, 'high')",
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	srv := handler.New(graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{}}))
	srv.Use(veloxgql.NewExtension(velox.Open(velox.SQLite, db)))
	srv.AddTransport(transport.POST{})
	req := httptest.NewRequest("POST", "/", strings.NewReader(` + "`" + `{"query":"{ alerts { edges { node { level } } } }"}` + "`" + `))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if got := w.Body.String(); !strings.Contains(got, ` + "`" + `"level":"HIGH"` + "`" + `) || strings.Contains(got, "errors") {
		t.Errorf("response %d: %s", w.Code, got)
	}
}
`

// TestAppEnumRenamed generates an application whose renamed entity has a
// field of its own enum type, then runs gqlgen over it with no models entry
// for the enum: velox.graphqls binds it with @goModel. The application is a
// module of its own; it needs no database but SQLite.
func TestAppEnumRenamed(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir, run := app(t, map[string]string{
		"schema/schema.go":          e2eEnumSchema,
		"levels/levels.go":          e2eLevelsGQL,
		"gqlgen.yml":                e2eCollectGqlgen,
		"graph/schema.resolvers.go": e2eEnumResolvers,
		"enum_test.go":              e2eEnumRun,
	}, true)
	if out, err := run("tool", "velox-gen", "-out", "gen", "-plugin", "velox-graphql", "./schema"); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	if out, err := run("tool", "gqlgen", "generate"); err != nil {
		t.Fatalf("gqlgen generate: %v\n%s", err, out)
	}
	// Bound by @goModel, the enum is the application's type. Unbound, gqlgen
	// would generate a type of its own for it, which the field's marshaler
	// happens to serve.
	sdlText, err := os.ReadFile(filepath.Join(dir, "gen", "velox.graphqls"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "enum AlarmLevel @goModel(model: \"example.com/app/levels.Level\") {\n"; !strings.Contains(string(sdlText), want) {
		t.Errorf("no %q in\n%s", want, sdlText)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "graph", "models_gen.go")); err == nil && strings.Contains(string(b), "AlarmLevel") {
		t.Errorf("gqlgen generated a type for the enum:\n%s", b)
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
	if !strings.Contains(out, "--- PASS: TestAlertLevel ") {
		t.Errorf("no PASS of TestAlertLevel in\n%s", out)
	}
}
