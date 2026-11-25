package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// e2eOwnSDLSchema is the schema of an application that writes its own SDL:
// Task, whose status is velox-gen's enum type TaskStatus.
const e2eOwnSDLSchema = `package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

type Task struct{ schema.Schema }

func (Task) Fields() []schema.Field {
	return []schema.Field{field.String("id"), field.Enum("status").Values("open", "in_progress")}
}
`

// e2eOwnSDL is the application's SDL. It declares the enum as the stored
// values upper-cased, as velox-graphql writes it, and names it after
// velox-gen's type, so that autobind binds it.
const e2eOwnSDL = `enum TaskStatus {
  OPEN
  IN_PROGRESS
}

type Task {
  id: ID!
  status: TaskStatus!
}

type Query {
  tasks(status: TaskStatus): [Task!]!
}
`

// e2eOwnSDLResolvers is the resolver of tasks: the argument arrives as the
// stored value, which the query compares as it is.
const e2eOwnSDLResolvers = `package graph

import (
	"context"

	"example.com/app/gen"
)

// Tasks is the resolver for the tasks field.
func (r *queryResolver) Tasks(ctx context.Context, status *gen.TaskStatus) ([]*gen.Task, error) {
	q := r.DB.Query[gen.Task]().AllowUnbounded()
	if status != nil {
		q = q.Where(gen.Tasks.Status.EQ(*status))
	}
	rows, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*gen.Task, len(rows))
	for i := range rows {
		out[i] = &rows[i]
	}
	return out, nil
}

// Query returns QueryResolver implementation.
func (r *Resolver) Query() QueryResolver { return &queryResolver{r} }

type queryResolver struct{ *Resolver }
`

// e2eOwnSDLRun is the application's test: a stored open goes out as OPEN,
// and the argument IN_PROGRESS matches the stored in_progress.
const e2eOwnSDLRun = `package app_test

import (
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

func TestTaskStatus(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	for _, s := range []string{
		"CREATE TABLE tasks (id TEXT PRIMARY KEY, status TEXT NOT NULL)",
		"INSERT INTO tasks (id, status) VALUES ('t1', 'open'), ('t2', 'in_progress')",
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	srv := handler.New(graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{DB: velox.Open(velox.SQLite, db)}}))
	srv.AddTransport(transport.POST{})
	query := func(q string) string {
		req := httptest.NewRequest("POST", "/", strings.NewReader(q))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w.Body.String()
	}
	for q, want := range map[string]string{
		` + "`" + `{"query":"{ tasks { id status } }"}` + "`" + `:                  ` + "`" + `{"data":{"tasks":[{"id":"t1","status":"OPEN"},{"id":"t2","status":"IN_PROGRESS"}]}}` + "`" + `,
		` + "`" + `{"query":"{ tasks(status: IN_PROGRESS) { id status } }"}` + "`" + `: ` + "`" + `{"data":{"tasks":[{"id":"t2","status":"IN_PROGRESS"}]}}` + "`" + `,
	} {
		if got := query(q); got != want {
			t.Errorf("%s:\n got %s\nwant %s", q, got, want)
		}
	}
}
`

// TestAppOwnSDLEnums generates, with only=enums, the enum marshalers of an
// application that writes its own SDL, then runs gqlgen over that SDL with
// autobind and no models entry for the enum: gqlgen binds velox-gen's
// TaskStatus, whose marshalers turn open into OPEN and back.
func TestAppOwnSDLEnums(t *testing.T) {
	t.Parallel() // its own module, in its own directory
	if testing.Short() {
		t.Skip("builds a module")
	}
	gqlgen := strings.Replace(strings.Replace(e2eCollectGqlgen, "  - gen/*.graphql\n", "", 1),
		"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx.Int64ID", "github.com/99designs/gqlgen/graphql.ID", 1)
	dir, run := app(t, map[string]string{
		"schema/schema.go":          e2eOwnSDLSchema,
		"graph/schema.graphql":      e2eOwnSDL,
		"gqlgen.yml":                gqlgen,
		"graph/schema.resolvers.go": e2eOwnSDLResolvers,
		"graph/resolver.go":         e2eResolver,
		"enum_test.go":              e2eOwnSDLRun,
	}, true)
	if out, err := run("tool", "velox-gen", "-out", "gen", "-plugin", "velox-graphql:only=enums", "./schema"); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "gen", "velox.graphql")); !os.IsNotExist(err) {
		t.Errorf("only=enums wrote velox.graphql: %v", err)
	}
	if out, err := run("tool", "gqlgen", "generate"); err != nil {
		t.Fatalf("gqlgen generate: %v\n%s", err, out)
	}
	models, err := os.ReadFile(filepath.Join(dir, "graph", "models_gen.go"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if strings.Contains(string(models), "TaskStatus") {
		t.Errorf("gqlgen generated a type for the enum:\n%s", models)
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
	if !strings.Contains(out, "--- PASS: TestTaskStatus ") {
		t.Errorf("no PASS of TestTaskStatus in\n%s", out)
	}
}
