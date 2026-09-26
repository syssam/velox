package graphqlgo_test

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"testing"

	graphql "github.com/syssam/graphql-go"
	"github.com/syssam/graphql-go/fed"

	"github.com/syssam/velox/contrib/graphql/gqlrelay"
	"github.com/syssam/velox/contrib/graphqlgo"
)

type fedUser struct {
	ID          int
	Name, Email string
}

// A router resolves references in one _entities call: here four, one of
// them twice and one to a row that does not exist. They must cost one load
// with each key once, come back in request order with null for the missing
// one, and the load must be able to plan its columns from the fragment the
// router selected, which sits under a union.
func TestEntitiesResolvesARoutersBatchInOneLoad(t *testing.T) {
	rows := map[int]*fedUser{1: {1, "Ada", "a@x"}, 2: {2, "Bob", "b@x"}}
	var (
		loads [][]int
		plan  string
	)
	src, bindings, err := fed.Subgraph(`
		type User @key(fields: "id") { id: ID! name: String! email: String! }
		type Query { me: User }`,
		graphqlgo.Entities("User", graphqlgo.IntKey("id"),
			func(ctx context.Context, ids []int) ([]*fedUser, error) {
				loads = append(loads, ids)
				q := newRecQuery(userMeta())
				if err := gqlrelay.CollectFields(ctx, q, q.meta, "User"); err != nil {
					return nil, err
				}
				plan = q.plan()
				var out []*fedUser
				for _, id := range ids {
					if u := rows[id]; u != nil {
						out = append(out, u)
					}
				}
				return out, nil
			},
			func(u *fedUser) int { return u.ID }),
	)
	if err != nil {
		t.Fatal(err)
	}
	s, err := graphql.NewSchema(src, bindings,
		graphql.Object[graphql.Root]("Query", graphql.Resolve("me", func(context.Context, graphql.Root) (*fedUser, error) { return nil, nil })),
		graphql.Object[fedUser]("User",
			graphql.Field("id", func(u *fedUser) graphql.ID { return graphql.ID(strconv.Itoa(u.ID)) }),
			graphql.Field("name", func(u *fedUser) string { return u.Name }),
			graphql.Field("email", func(u *fedUser) string { return u.Email }),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	vars, _ := json.Marshal(map[string]any{"reps": []map[string]any{
		{"__typename": "User", "id": "2"},
		{"__typename": "User", "id": "9"},
		{"__typename": "User", "id": "2"},
		{"__typename": "User", "id": 1},
		{"__typename": "User", "id": "not-a-number"},
	}})
	resp := graphql.NewExecutor(s, graphqlgo.Collect()).Execute(context.Background(), &graphql.Request{
		Query:     `query($reps: [_Any!]!) { _entities(representations: $reps) { ... on User { name } } }`,
		Variables: vars,
	})
	if len(resp.Errors) > 0 {
		t.Fatal(resp.Errors)
	}
	if want := `{"_entities":[{"name":"Bob"},null,{"name":"Bob"},{"name":"Ada"},null]}`; string(resp.Data) != want {
		t.Errorf("data = %s, want %s", resp.Data, want)
	}
	if len(loads) != 1 || !slices.Equal(loads[0], []int{2, 9, 1}) {
		t.Errorf("loads = %v, want one load of [2 9 1]", loads)
	}
	if want := "columns [id name]\n"; plan != want {
		t.Errorf("the load planned %q from the router's selection, want %q", plan, want)
	}
}
