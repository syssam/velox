package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	gqlgenpkg "example.com/fullgql/gqlgen"
	"example.com/fullgql/velox"
	auditlogclient "example.com/fullgql/velox/client/auditlog"
	commentclient "example.com/fullgql/velox/client/comment"
	memberclient "example.com/fullgql/velox/client/member"
	tagclient "example.com/fullgql/velox/client/tag"
	todoclient "example.com/fullgql/velox/client/todo"
	userclient "example.com/fullgql/velox/client/user"
	workspaceclient "example.com/fullgql/velox/client/workspace"
	"example.com/fullgql/velox/entity"
	"example.com/fullgql/velox/member"
	"example.com/fullgql/velox/todo"

	gqlclient "github.com/99designs/gqlgen/client"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql"
)

// queryLog records every SELECT the driver runs.
type queryLog struct {
	mu      sync.Mutex
	queries []string
}

func (l *queryLog) record(_ context.Context, v ...any) {
	s := fmt.Sprint(v...)
	const prefix = "driver.Query: query="
	if !strings.HasPrefix(s, prefix) {
		return
	}
	q := strings.TrimPrefix(s, prefix)
	if i := strings.Index(q, " args="); i >= 0 {
		q = q[:i]
	}
	l.mu.Lock()
	l.queries = append(l.queries, q)
	l.mu.Unlock()
}

func (l *queryLog) reset() {
	l.mu.Lock()
	l.queries = nil
	l.mu.Unlock()
}

func (l *queryLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.queries)
}

// selects returns the recorded row queries against table (COUNT queries
// excluded).
func (l *queryLog) selects(table string) []string {
	var out []string
	for _, q := range l.snapshot() {
		if strings.Contains(q, "COUNT(") {
			continue
		}
		if strings.Contains(q, "FROM `"+table+"`") {
			out = append(out, q)
		}
	}
	return out
}

// selectedColumns returns the column list of a recorded SELECT, unquoted
// and without table qualifiers.
func selectedColumns(t *testing.T, q string) []string {
	t.Helper()
	q = strings.TrimPrefix(q, "SELECT ")
	i := strings.Index(q, " FROM ")
	require.Positive(t, i, q)
	var cols []string
	for c := range strings.SplitSeq(q[:i], ", ") {
		c = strings.ReplaceAll(c, "`", "")
		if j := strings.LastIndex(c, "."); j >= 0 {
			c = c[j+1:]
		}
		cols = append(cols, c)
	}
	return cols
}

// openCountingClient opens a fresh in-memory database whose driver logs
// every query, and a gqlgen client over the example's real executor.
func openCountingClient(t *testing.T) (*velox.Client, *gqlclient.Client, *queryLog) {
	t.Helper()
	drv, err := sql.Open(dialect.SQLite, "file:"+t.Name()+"?mode=memory&_pragma=foreign_keys(1)")
	require.NoError(t, err)
	// One connection: every new connection to a memory database is a new,
	// empty database, and gqlgen resolves sibling fields concurrently.
	drv.DB().SetMaxOpenConns(1)
	log := &queryLog{}
	client := velox.NewClient(velox.Driver(dialect.DebugWithContext(drv, log.record)))
	t.Cleanup(func() { client.Close() })
	require.NoError(t, client.Schema.Create(context.Background()))
	srv := handler.NewDefaultServer(gqlgenpkg.NewExecutableSchema(gqlgenpkg.Config{
		Resolvers: &gqlgenpkg.Resolver{Client: client},
	}))
	return client, gqlclient.New(srv), log
}

// seedTodos creates users owners each with perUser todos, the todos of
// each owner tagged with tagsPerTodo tags, created round-robin so the rows
// of different parents interleave.
func seedTodos(t *testing.T, client *velox.Client, owners, perUser, tagsPerTodo int) []*entity.User {
	t.Helper()
	ctx := context.Background()
	cfg := client.RuntimeConfig()
	ws, err := workspaceclient.NewWorkspaceClient(cfg).Create().
		SetInput(workspaceclient.CreateWorkspaceInput{Name: "WS"}).Save(ctx)
	require.NoError(t, err)
	tags := make([]int, tagsPerTodo)
	for i := range tags {
		tg, err := tagclient.NewTagClient(cfg).Create().SetName(fmt.Sprintf("tag-%d", i)).Save(ctx)
		require.NoError(t, err)
		tags[i] = tg.ID
	}
	users := make([]*entity.User, owners)
	for i := range users {
		u, err := userclient.NewUserClient(cfg).Create().
			SetInput(userclient.CreateUserInput{Name: fmt.Sprintf("user-%d", i), Email: fmt.Sprintf("u%d@collect.com", i)}).Save(ctx)
		require.NoError(t, err)
		users[i] = u
	}
	for j := range perUser {
		for i, u := range users {
			_, err := todoclient.NewTodoClient(cfg).Create().
				SetInput(todoclient.CreateTodoInput{
					Title: fmt.Sprintf("u%d-t%d", i, j), Status: ptr(todo.StatusTodo), Priority: ptr(todo.PriorityLow),
					OwnerID: u.ID, WorkspaceID: ptr(ws.ID), TagIDs: tags,
				}).Save(ctx)
			require.NoError(t, err)
		}
	}
	return users
}

type todoNode struct {
	Title string `json:"title"`
	Owner struct {
		Name string `json:"name"`
	} `json:"owner"`
	Tags struct {
		TotalCount int `json:"totalCount"`
		Edges      []struct {
			Node struct {
				Name string `json:"name"`
			} `json:"node"`
		} `json:"edges"`
	} `json:"tags"`
}

type usersResponse struct {
	Users struct {
		Edges []struct {
			Node struct {
				Name  string `json:"name"`
				Email string `json:"email"`
				Todos struct {
					TotalCount int `json:"totalCount"`
					PageInfo   struct {
						HasNextPage bool `json:"hasNextPage"`
					} `json:"pageInfo"`
					Edges []struct {
						Node todoNode `json:"node"`
					} `json:"edges"`
				} `json:"todos"`
			} `json:"node"`
		} `json:"edges"`
	} `json:"users"`
}

// TestCollect_ProjectsSelectedColumns pins that the generated Paginate
// runs field collection: a page selecting two scalars reads only those
// columns and the key. Before the collector was wired into Paginate it
// read every column.
func TestCollect_ProjectsSelectedColumns(t *testing.T) {
	client, gql, log := openCountingClient(t)
	users := seedTodos(t, client, 2, 1, 0)
	log.reset()

	var resp usersResponse
	gql.MustPost(`{ users(first: 10) { edges { node { name email } } } }`, &resp)
	require.Len(t, resp.Users.Edges, 2)
	assert.Equal(t, users[0].Name, resp.Users.Edges[0].Node.Name)
	assert.Equal(t, "u0@collect.com", resp.Users.Edges[0].Node.Email)

	sel := log.selects("users")
	require.Len(t, sel, 1, "one page query: %v", log.snapshot())
	assert.Equal(t, []string{"id", "name", "email"}, selectedColumns(t, sel[0]))
}

// TestCollect_NestedEdgesAreNotNPlusOne pins that nested edges are
// eager-loaded by the collector: the number of queries does not grow with
// the number of parent rows. Before, every user's todos connection ran its
// own COUNT and SELECT (2 + 2N queries, plus a query per todo per nested
// edge).
func TestCollect_NestedEdgesAreNotNPlusOne(t *testing.T) {
	const query = `{ users(first: 50) { edges { node { name todos { edges { node { title owner { name } } } } } } } }`
	counts := map[int]int{}
	for _, n := range []int{2, 6} {
		client, gql, log := openCountingClient(t)
		users := seedTodos(t, client, n, 3, 0)
		log.reset()

		var resp usersResponse
		gql.MustPost(query, &resp)
		require.Len(t, resp.Users.Edges, n)
		for i, e := range resp.Users.Edges {
			assert.Equal(t, users[i].Name, e.Node.Name)
			require.Len(t, e.Node.Todos.Edges, 3)
			for j, te := range e.Node.Todos.Edges {
				assert.Equal(t, fmt.Sprintf("u%d-t%d", i, j), te.Node.Title)
				assert.Equal(t, users[i].Name, te.Node.Owner.Name)
			}
		}
		counts[n] = len(log.snapshot())
		// users page, todos (all parents), owners (all todos); no COUNT, as
		// totalCount is not selected.
		assert.Equal(t, 3, counts[n], "queries at N=%d: %v", n, log.snapshot())
		todoSel := log.selects("todos")
		require.Len(t, todoSel, 1)
		assert.Equal(t, []string{"id", "title", "user_todos", "category_todos", "workspace_todos"}, selectedColumns(t, todoSel[0]),
			"the todos query reads the title, the key and the foreign keys (the nested owner needs user_todos)")
	}
	assert.Equal(t, counts[2], counts[6], "query count must not depend on the number of parents")
}

// TestCollect_NestedConnectionFirstIsPerParent pins that a nested
// connection with first: n returns the first n rows of EVERY parent —
// loaded in one query limited per parent with a window function — and
// that the page matches what the uncollected per-row Paginate returns.
func TestCollect_NestedConnectionFirstIsPerParent(t *testing.T) {
	client, gql, log := openCountingClient(t)
	users := seedTodos(t, client, 4, 5, 3)
	log.reset()

	const sel = `edges { node { name todos(first: 2) { pageInfo { hasNextPage } edges { node { title tags(first: 2) { edges { node { name } } } } } } } }`
	var collected usersResponse
	gql.MustPost(`{ users(first: 10) { `+sel+` } }`, &collected)
	collectedQueries := log.snapshot()

	todoSel := log.selects("todos")
	require.Len(t, todoSel, 1, "one todos query for every parent: %v", collectedQueries)
	assert.Contains(t, todoSel[0], "ROW_NUMBER() OVER (PARTITION BY", "the limit is applied per parent")
	assert.Len(t, collectedQueries, 3, "users, todos, tags (no totalCount, no COUNT): %v", collectedQueries)

	require.Len(t, collected.Users.Edges, 4)
	for i, e := range collected.Users.Edges {
		assert.Equal(t, users[i].Name, e.Node.Name)
		require.Len(t, e.Node.Todos.Edges, 2, "every parent gets its own first 2")
		assert.True(t, e.Node.Todos.PageInfo.HasNextPage)
		for j, te := range e.Node.Todos.Edges {
			assert.Equal(t, fmt.Sprintf("u%d-t%d", i, j), te.Node.Title)
			require.Len(t, te.Node.Tags.Edges, 2)
			assert.Equal(t, "tag-0", te.Node.Tags.Edges[0].Node.Name)
			assert.Equal(t, "tag-1", te.Node.Tags.Edges[1].Node.Name)
		}
	}

	// The same selection through the per-row Paginate path: an explicit
	// orderBy makes every edge method page on its own. The data must match.
	log.reset()
	var perRow usersResponse
	gql.MustPost(`{ users(first: 10) { edges { node { name todos(first: 2, orderBy: {field: CREATED_AT, direction: ASC}) {
		pageInfo { hasNextPage } edges { node { title tags(first: 2, orderBy: {field: NAME, direction: ASC}) { edges { node { name } } } } } } } } } }`, &perRow)
	assert.Equal(t, collected, perRow, "collected and per-row results must agree")
	assert.Greater(t, len(log.snapshot()), len(collectedQueries),
		"the per-row path queries once per parent: %d vs %d", len(log.snapshot()), len(collectedQueries))
	t.Logf("queries: collected=%d per-row=%d", len(collectedQueries), len(log.snapshot()))
}

// TestCollect_NestedConnectionTotalCount pins that a nested totalCount is
// counted over the whole edge, not over the rows a per-parent limit kept.
func TestCollect_NestedConnectionTotalCount(t *testing.T) {
	client, gql, log := openCountingClient(t)
	seedTodos(t, client, 3, 4, 0)
	log.reset()

	var resp usersResponse
	gql.MustPost(`{ users { edges { node { name todos(first: 1) { totalCount edges { node { title } } } } } } }`, &resp)
	require.Len(t, resp.Users.Edges, 3)
	for _, e := range resp.Users.Edges {
		assert.Equal(t, 4, e.Node.Todos.TotalCount)
		assert.Len(t, e.Node.Todos.Edges, 1)
	}
	assert.Len(t, log.snapshot(), 2, "users, todos — the nested totalCount is the loaded edge's length and the top level selects none: %v", log.snapshot())
}

// TestCollect_ListResolverCollectFields pins the list-resolver path: the
// members resolver calls the generated CollectFields, so to-one edges
// (whose keys live on the members table) are loaded with one query each,
// whatever the number of members.
func TestCollect_ListResolverCollectFields(t *testing.T) {
	client, gql, log := openCountingClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()
	users := seedTodos(t, client, 5, 0, 0)
	ws, err := workspaceclient.NewWorkspaceClient(cfg).Create().
		SetInput(workspaceclient.CreateWorkspaceInput{Name: "Members"}).Save(ctx)
	require.NoError(t, err)
	for _, u := range users {
		_, err := memberclient.NewMemberClient(cfg).Create().
			SetInput(memberclient.CreateMemberInput{WorkspaceID: ws.ID, UserID: u.ID}).Save(ctx)
		require.NoError(t, err)
	}
	log.reset()

	var resp struct {
		Members []struct {
			Role string `json:"role"`
			User struct {
				Name string `json:"name"`
			} `json:"user"`
			Workspace struct {
				Name string `json:"name"`
			} `json:"workspace"`
		} `json:"members"`
	}
	gql.MustPost(`{ members { role user { name } workspace { name } } }`, &resp)
	require.Len(t, resp.Members, len(users))
	for i, m := range resp.Members {
		assert.Equal(t, users[i].Name, m.User.Name)
		assert.Equal(t, "Members", m.Workspace.Name)
	}
	assert.Len(t, log.snapshot(), 3, "members, users, workspaces: %v", log.snapshot())
	sel := log.selects("members")
	require.Len(t, sel, 1)
	assert.ElementsMatch(t, []string{"id", "role", "workspace_members", "user_memberships"}, selectedColumns(t, sel[0]))
}

// TestCollect_ListResolversCollectThroughTheQuerier pins that every list
// resolver (auditLogs, comments, members) collects: CollectFields is part
// of entity.XxxQuerier, so the resolvers call it on Query() directly. A
// resolver that forgets it reads every column and loads a to-one edge once
// per row.
func TestCollect_ListResolversCollectThroughTheQuerier(t *testing.T) {
	client, gql, log := openCountingClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()
	users := seedTodos(t, client, 3, 1, 0)
	todos, err := todoclient.NewTodoClient(cfg).Query().All(ctx)
	require.NoError(t, err)
	for i, td := range todos {
		_, err := commentclient.NewCommentClient(cfg).Create().
			SetInput(commentclient.CreateCommentInput{Content: fmt.Sprintf("c%d", i), TodoID: td.ID, AuthorID: users[i].ID}).Save(ctx)
		require.NoError(t, err)
	}
	_, err = auditlogclient.NewAuditLogClient(cfg).Create().
		SetAction("create").SetEntityType("todo").SetEntityID(todos[0].ID).Save(ctx)
	require.NoError(t, err)

	t.Run("comments", func(t *testing.T) {
		log.reset()
		var resp struct {
			Comments []struct {
				Content string `json:"content"`
				Todo    struct {
					Title string `json:"title"`
				} `json:"todo"`
			} `json:"comments"`
		}
		gql.MustPost(`{ comments { content todo { title } } }`, &resp)
		require.Len(t, resp.Comments, len(todos))
		for i, c := range resp.Comments {
			assert.Equal(t, fmt.Sprintf("c%d", i), c.Content)
			assert.Equal(t, todos[i].Title, c.Todo.Title)
		}
		assert.Len(t, log.snapshot(), 2, "comments, todos — not one todo query per comment: %v", log.snapshot())
		sel := log.selects("comments")
		require.Len(t, sel, 1)
		assert.NotContains(t, selectedColumns(t, sel[0]), "created_at", "only the selected columns and keys are read")
	})
	t.Run("auditLogs", func(t *testing.T) {
		log.reset()
		var resp struct {
			AuditLogs []struct {
				Action string `json:"action"`
			} `json:"auditLogs"`
		}
		gql.MustPost(`{ auditLogs { action } }`, &resp)
		require.Len(t, resp.AuditLogs, 1)
		assert.Equal(t, "create", resp.AuditLogs[0].Action)
		sel := log.selects("audit_logs")
		require.Len(t, sel, 1, "%v", log.snapshot())
		assert.Equal(t, []string{"id", "action"}, selectedColumns(t, sel[0]))
	})
}

// TestCollect_CollectedFor pins graphql.CollectedFor end to end: User.summary
// is a custom resolver (extensions.graphql) reading name and bio, and
// schema/user.go declares both columns for it. Selecting it with email
// must read exactly those columns — not fall back to SELECT *, which is
// what an unannotated custom resolver field does.
func TestCollect_CollectedFor(t *testing.T) {
	client, gql, log := openCountingClient(t)
	ctx := context.Background()
	u, err := userclient.NewUserClient(client.RuntimeConfig()).Create().
		SetInput(userclient.CreateUserInput{Name: "Ada", Email: "ada@collect.com", Bio: ptr("mathematician")}).Save(ctx)
	require.NoError(t, err)
	log.reset()

	var resp struct {
		Users struct {
			Edges []struct {
				Node struct {
					Email   string `json:"email"`
					Summary string `json:"summary"`
				} `json:"node"`
			} `json:"edges"`
		} `json:"users"`
	}
	gql.MustPost(`{ users { edges { node { email summary } } } }`, &resp)
	require.Len(t, resp.Users.Edges, 1)
	assert.Equal(t, u.Name+" — mathematician", resp.Users.Edges[0].Node.Summary)
	assert.Equal(t, "ada@collect.com", resp.Users.Edges[0].Node.Email)

	sel := log.selects("users")
	require.Len(t, sel, 1)
	assert.Equal(t, []string{"id", "email", "name", "bio"}, selectedColumns(t, sel[0]))
}

// TestCollect_InterfaceFieldSharesEdgeWithDirectSelection pins that an
// interface field (graphql.InterfaceField) and a direct selection of the same
// edge read correct data. Both paths load the edge into one child query; the
// direct selection used to narrow that query's projection to its own fields,
// and the interface resolver then answered from rows missing the rest —
// description came back null and role came back "".
func TestCollect_InterfaceFieldSharesEdgeWithDirectSelection(t *testing.T) {
	client, gql, log := openCountingClient(t)
	ctx := context.Background()
	cfg := client.RuntimeConfig()
	alice, err := userclient.NewUserClient(cfg).Create().
		SetInput(userclient.CreateUserInput{Name: "Alice", Email: "alice@iface.com"}).Save(ctx)
	require.NoError(t, err)
	desc := "the platform team"
	ws, err := workspaceclient.NewWorkspaceClient(cfg).Create().
		SetInput(workspaceclient.CreateWorkspaceInput{Name: "Platform", Description: &desc}).Save(ctx)
	require.NoError(t, err)
	_, err = memberclient.NewMemberClient(cfg).Create().
		SetInput(memberclient.CreateMemberInput{Role: ptr(member.RoleAdmin), WorkspaceID: ws.ID, UserID: alice.ID}).Save(ctx)
	require.NoError(t, err)

	t.Run("to-one", func(t *testing.T) {
		var out struct {
			Members []struct {
				Workspace struct{ Name string }
				Principal struct {
					Typename    string `json:"__typename"`
					Name        string
					Description *string
				}
			}
		}
		log.reset()
		gql.MustPost(`{ members { workspace { name } principal { __typename ... on Workspace { name description } } } }`, &out)
		require.Len(t, out.Members, 1)
		m := out.Members[0]
		assert.Equal(t, "Platform", m.Workspace.Name)
		assert.Equal(t, "Workspace", m.Principal.Typename)
		assert.Equal(t, "Platform", m.Principal.Name)
		require.NotNil(t, m.Principal.Description, "the interface resolver read a row projected for the direct selection")
		assert.Equal(t, desc, *m.Principal.Description)
		assert.Len(t, log.selects("workspaces"), 1, "one load serves both paths")
	})

	t.Run("to-many", func(t *testing.T) {
		var out struct {
			Users struct {
				Edges []struct {
					Node struct {
						Memberships []struct{ Accepted bool }
						Relations   []struct{ Role string }
					}
				}
			}
		}
		gql.MustPost(`{ users { edges { node { memberships { accepted } relations { role } } } } }`, &out)
		require.Len(t, out.Users.Edges, 1)
		n := out.Users.Edges[0].Node
		require.Len(t, n.Memberships, 1)
		require.Len(t, n.Relations, 1)
		assert.Equal(t, "ADMIN", n.Relations[0].Role)
	})
}
