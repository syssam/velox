package runtime

import (
	"context"
	stdsql "database/sql"
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect"
	velsql "github.com/syssam/velox/dialect/sql"
)

// m2mParent is the parent side of the M2MLoad tests: a post with tags.
type m2mParent struct {
	ID   int
	Tags []*testEntity
}

// noWindowCaps reports a server without window functions, forcing
// PlanPerParentLimit onto the in-memory path over a real SQLite driver.
type noWindowCaps struct{ dialect.Driver }

func (noWindowCaps) ServerCapabilities(context.Context) (dialect.Capabilities, error) {
	return dialect.GetCapabilities(dialect.MySQL), nil
}

// newM2MFixture opens an in-memory SQLite database with posts 1..3, tags
// 10..14 and a post_tags join table. Post 1 has tags 10-13 plus a duplicate
// join row for 11, post 2 has 11 and 14 (11 is shared), post 3 has none.
func newM2MFixture(t *testing.T) dialect.Driver {
	t.Helper()
	db, err := stdsql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{
		`CREATE TABLE tags (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER NOT NULL)`,
		`CREATE TABLE post_tags (post_id INTEGER NOT NULL, tag_id INTEGER NOT NULL)`,
		`INSERT INTO tags (id, name, age) VALUES (10,'a',4),(11,'b',3),(12,'c',2),(13,'d',1),(14,'e',0)`,
		`INSERT INTO post_tags (post_id, tag_id) VALUES (1,10),(1,11),(1,11),(1,12),(1,13),(2,11),(2,14)`,
	} {
		_, err := db.Exec(stmt)
		require.NoError(t, err)
	}
	return velsql.OpenDB(dialect.SQLite, db)
}

// m2mCalls records what M2MLoad invoked on the generated-code hooks.
type m2mCalls struct {
	prepared  int
	eager     [][]int
	configSet int
}

func newM2MLoad(calls *m2mCalls, order string) M2MLoad[*testQuery, m2mParent, testEntity, *testEntity, int, int] {
	return M2MLoad[*testQuery, m2mParent, testEntity, *testEntity, int, int]{
		Edge:         "tags",
		JoinTable:    "post_tags",
		ParentColumn: "post_id",
		TargetColumn: "tag_id",
		TargetID:     "id",
		ParentKey:    func(p *m2mParent) int { return p.ID },
		TargetKey:    func(e *testEntity) int { return e.ID },
		NewPivot:     func() any { return new(stdsql.NullInt64) },
		PivotKey:     func(v any) int { return int(v.(*stdsql.NullInt64).Int64) },
		Selector: func(q *testQuery, _ context.Context) (*velsql.Selector, error) {
			t := velsql.Table("tags")
			s := velsql.Dialect(q.Driver.Dialect()).Select(t.C("id"), t.C("name"), t.C("age")).From(t)
			if order != "" {
				s.OrderBy(velsql.Desc(t.C(order)))
			}
			return s, nil
		},
		Prepare: func(*testQuery, context.Context) error {
			calls.prepared++
			return nil
		},
		EagerLoad: func(_ *testQuery, _ context.Context, ts []*testEntity) error {
			ids := make([]int, len(ts))
			for i, e := range ts {
				ids[i] = e.ID
			}
			calls.eager = append(calls.eager, ids)
			return nil
		},
		SetConfig: func(*testEntity, Config) { calls.configSet++ },
	}
}

// loadTags runs l over posts 1..3 and returns each post's tag IDs.
func loadTags(t *testing.T, l M2MLoad[*testQuery, m2mParent, testEntity, *testEntity, int, int], q *testQuery) (map[int][]int, error) {
	t.Helper()
	parents := []*m2mParent{{ID: 1}, {ID: 2}, {ID: 3}}
	err := l.Load(context.Background(), q, parents,
		func(p *m2mParent) { p.Tags = []*testEntity{} },
		func(p *m2mParent, e *testEntity) { p.Tags = append(p.Tags, e) })
	got := map[int][]int{}
	for _, p := range parents {
		ids := []int{}
		for _, e := range p.Tags {
			ids = append(ids, e.ID)
		}
		got[p.ID] = ids
	}
	return got, err
}

func TestM2MLoad_AssignsEachParentItsTargetsOnce(t *testing.T) {
	drv := newM2MFixture(t)
	var calls m2mCalls
	got, err := loadTags(t, newM2MLoad(&calls, ""), newTestQuery(drv, "tags", nil, "id", nil, "Tag"))
	require.NoError(t, err)
	for id, want := range map[int][]int{1: {10, 11, 12, 13}, 2: {11, 14}, 3: {}} {
		assert.ElementsMatch(t, want, got[id], "post %d", id)
	}
	assert.Equal(t, 1, calls.prepared, "policy and traversers run once per load")
	require.Len(t, calls.eager, 1, "nested edges load once per load")
	assert.ElementsMatch(t, []int{10, 11, 12, 13, 14}, calls.eager[0], "nested loads see each shared target once")
	assert.Equal(t, 5, calls.configSet, "every distinct target gets the runtime config")
}

func TestM2MLoad_PerParentLimitWindowAndMemoryAgree(t *testing.T) {
	drv := newM2MFixture(t)
	// Order by age DESC: post 1 ranks 10(4), 11(3), 12(2), 13(1); post 2
	// ranks 11(3), 14(0). A limit of 2 keeps each post's first two.
	want := map[int][]int{1: {10, 11}, 2: {11, 14}, 3: {}}
	for name, d := range map[string]dialect.Driver{"window": drv, "in memory": noWindowCaps{drv}} {
		t.Run(name, func(t *testing.T) {
			q := newTestQuery(d, "tags", nil, "id", nil, "Tag")
			n := 2
			q.Ctx.PartitionLimit = &n
			var calls m2mCalls
			got, err := loadTags(t, newM2MLoad(&calls, "age"), q)
			require.NoError(t, err)
			assert.Equal(t, want, got, "each parent keeps its own first n, in its own order")
		})
	}
}

func TestM2MLoad_FollowsInterceptorOrder(t *testing.T) {
	drv := newM2MFixture(t)
	var calls m2mCalls
	l := newM2MLoad(&calls, "")
	l.Inters = []velox.Interceptor{velox.InterceptFunc(func(next velox.Querier) velox.Querier {
		return velox.QuerierFunc(func(ctx context.Context, q velox.Query) (velox.Value, error) {
			v, err := next.Query(ctx, q)
			if err != nil {
				return nil, err
			}
			ts := slices.Clone(v.([]*testEntity))
			slices.SortFunc(ts, func(a, b *testEntity) int { return b.ID - a.ID })
			return ts, nil
		})
	})}
	got, err := loadTags(t, l, newTestQuery(drv, "tags", nil, "id", nil, "Tag"))
	require.NoError(t, err)
	assert.Equal(t, []int{13, 12, 11, 10}, got[1])
	assert.Equal(t, []int{14, 11}, got[2])
}

func TestM2MLoad_Errors(t *testing.T) {
	drv := newM2MFixture(t)
	boom := errors.New("boom")
	cases := map[string]struct {
		setup func(*M2MLoad[*testQuery, m2mParent, testEntity, *testEntity, int, int], *testQuery)
		want  string
	}{
		"policy denies": {
			setup: func(l *M2MLoad[*testQuery, m2mParent, testEntity, *testEntity, int, int], _ *testQuery) {
				l.Prepare = func(*testQuery, context.Context) error { return boom }
			},
			want: "boom",
		},
		"selector fails": {
			setup: func(l *M2MLoad[*testQuery, m2mParent, testEntity, *testEntity, int, int], _ *testQuery) {
				l.Selector = func(*testQuery, context.Context) (*velsql.Selector, error) { return nil, boom }
			},
			want: "boom",
		},
		"nested load fails": {
			setup: func(l *M2MLoad[*testQuery, m2mParent, testEntity, *testEntity, int, int], _ *testQuery) {
				l.EagerLoad = func(*testQuery, context.Context, []*testEntity) error { return boom }
			},
			want: "boom",
		},
		"limit with a per-parent limit": {
			setup: func(_ *M2MLoad[*testQuery, m2mParent, testEntity, *testEntity, int, int], q *testQuery) {
				n, lim := 2, 5
				q.Ctx.PartitionLimit, q.Ctx.Limit = &n, &lim
			},
			want: "per-parent limit cannot be combined",
		},
		"interceptor swaps the query type": {
			setup: func(l *M2MLoad[*testQuery, m2mParent, testEntity, *testEntity, int, int], _ *testQuery) {
				l.Inters = []velox.Interceptor{velox.InterceptFunc(func(next velox.Querier) velox.Querier {
					return velox.QuerierFunc(func(ctx context.Context, _ velox.Query) (velox.Value, error) {
						return next.Query(ctx, "not a query")
					})
				})}
			},
			want: "unexpected query type",
		},
		"interceptor fails": {
			setup: func(l *M2MLoad[*testQuery, m2mParent, testEntity, *testEntity, int, int], _ *testQuery) {
				l.Inters = []velox.Interceptor{velox.InterceptFunc(func(velox.Querier) velox.Querier {
					return velox.QuerierFunc(func(context.Context, velox.Query) (velox.Value, error) { return nil, boom })
				})}
			},
			want: "boom",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var calls m2mCalls
			l := newM2MLoad(&calls, "")
			q := newTestQuery(drv, "tags", nil, "id", nil, "Tag")
			tc.setup(&l, q)
			_, err := loadTags(t, l, q)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}
