package graphqlgo

import (
	"context"
	"slices"
	"sync"
	"testing"

	graphql "github.com/syssam/graphql-go"
)

// velox passes an object type with the interfaces it implements, and a
// concrete selection answers each of those names with itself. Collected once
// per name, every field would be planned several times over: the plan comes
// out the same, the work does not, so this is asserted on the fields.
func TestFieldsListsEachResponseKeyOnce(t *testing.T) {
	type obj struct{}
	var (
		mu  sync.Mutex
		got [][]string
	)
	record := func(names []string) {
		mu.Lock()
		got = append(got, names)
		mu.Unlock()
	}
	s, err := graphql.NewSchema(graphql.SDL(`
		type Query { a: A n: N }
		interface N { id: ID! }
		type A implements N { id: ID! x: Int! }
		type B implements N { id: ID! y: Int! }`),
		graphql.Object[graphql.Root]("Query",
			graphql.Resolve("a", func(ctx context.Context, _ graphql.Root) (*obj, error) {
				record(fieldNames(ctx, "A", "N"))
				return nil, nil
			}),
			graphql.Resolve("n", func(ctx context.Context, _ graphql.Root) (any, error) {
				record(fieldNames(ctx, "N", "A"))
				return nil, nil
			}),
		),
		graphql.Interface[any]("N"),
		graphql.Object[obj]("A",
			graphql.Field("id", func(*obj) graphql.ID { return "" }),
			graphql.Field("x", func(*obj) int { return 0 }),
		),
		graphql.Object[struct{}]("B",
			graphql.Field("id", func(*struct{}) graphql.ID { return "" }),
			graphql.Field("y", func(*struct{}) int { return 0 }),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	resp := graphql.NewExecutor(s, Collect()).Execute(context.Background(), &graphql.Request{
		Query: `{ a { id x } n { id ... on A { x } ... on B { y } } }`,
	})
	if len(resp.Errors) > 0 {
		t.Fatal(resp.Errors)
	}
	want := [][]string{{"id", "x"}, {"id", "x"}}
	if len(got) != 2 || !slices.Equal(got[0], want[0]) || !slices.Equal(got[1], want[1]) {
		t.Errorf("fields = %v, want %v", got, want)
	}
}

func fieldNames(ctx context.Context, satisfies ...string) []string {
	f, ok := source(ctx)
	if !ok {
		return nil
	}
	var out []string
	for _, c := range f.Fields(satisfies) {
		out = append(out, c.FieldName())
	}
	slices.Sort(out)
	return out
}
