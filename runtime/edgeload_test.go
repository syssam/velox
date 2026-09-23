package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql"
)

// capsDriver is a driver of the named dialect whose server reports caps,
// or err when set.
type capsDriver struct {
	dialect.Driver
	name string
	caps dialect.Capabilities
	err  error
}

func (d capsDriver) Dialect() string { return d.name }

func (d capsDriver) ServerCapabilities(context.Context) (dialect.Capabilities, error) {
	return d.caps, d.err
}

func intp(n int) *int { return &n }

func TestPlanPerParentLimit_Inactive(t *testing.T) {
	for name, qc := range map[string]*QueryContext{"nil context": nil, "no limit": {}} {
		t.Run(name, func(t *testing.T) {
			p, err := PlanPerParentLimit[int](context.Background(), qc, nil, "posts")
			require.NoError(t, err)
			assert.False(t, p.Active)
			for range 5 {
				assert.True(t, p.Keep(1), "an inactive limit keeps every row")
			}
		})
	}
}

func TestPlanPerParentLimit_RejectsLimitAndOffset(t *testing.T) {
	drv := capsDriver{name: dialect.Postgres, caps: dialect.GetCapabilities(dialect.Postgres)}
	for name, qc := range map[string]*QueryContext{
		"limit":  {PartitionLimit: intp(2), Limit: intp(3)},
		"offset": {PartitionLimit: intp(2), Offset: intp(1)},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := PlanPerParentLimit[int](context.Background(), qc, drv, "posts")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "per-parent limit")
			assert.Contains(t, err.Error(), "posts edge query")
		})
	}
}

func TestPlanPerParentLimit_ProbeError(t *testing.T) {
	boom := errors.New("boom")
	_, err := PlanPerParentLimit[int](context.Background(), &QueryContext{PartitionLimit: intp(2)},
		capsDriver{name: dialect.MySQL, err: boom}, "posts")
	require.ErrorIs(t, err, boom)
}

// render returns the SQL of a selector ranked by p.
func render(p PerParentLimit[int]) string {
	s := sql.Dialect(dialect.Postgres).Select("*").From(sql.Table("posts"))
	p.Apply(s, s.C("user_id"), s.C("id"))
	q, _ := s.Query()
	return q
}

func TestPlanPerParentLimit_Window(t *testing.T) {
	p, err := PlanPerParentLimit[int](context.Background(), &QueryContext{PartitionLimit: intp(2)},
		capsDriver{name: dialect.Postgres, caps: dialect.GetCapabilities(dialect.Postgres)}, "posts")
	require.NoError(t, err)
	assert.True(t, p.Active)
	assert.True(t, p.Window)
	assert.Equal(t, 2, p.N)
	q := render(p)
	assert.Contains(t, q, "ROW_NUMBER() OVER")
	assert.Contains(t, q, `PARTITION BY "posts"."user_id" ORDER BY "posts"."id"`)
	for range 5 {
		assert.True(t, p.Keep(1), "the window path keeps every row it reads: the database already limited them")
	}
}

func TestPlanPerParentLimit_InMemory(t *testing.T) {
	p, err := PlanPerParentLimit[int](context.Background(), &QueryContext{PartitionLimit: intp(2)},
		capsDriver{name: dialect.MySQL, caps: dialect.VersionCapabilities(dialect.MySQL, "5.7.44")}, "posts")
	require.NoError(t, err)
	assert.True(t, p.Active)
	assert.False(t, p.Window)
	q := render(p)
	assert.NotContains(t, q, "ROW_NUMBER()")
	assert.True(t, strings.HasSuffix(q, `ORDER BY "posts"."id"`), "the rows are read in the order the window would rank them: %s", q)
	// Interleaved parents, as rows arrive from the database.
	var kept []int
	for _, parent := range []int{1, 2, 1, 1, 2, 3, 2, 1} {
		if p.Keep(parent) {
			kept = append(kept, parent)
		}
	}
	assert.Equal(t, []int{1, 2, 1, 2, 3}, kept)
}

func TestPerParentLimit_ApplyWithoutTiebreak(t *testing.T) {
	p := PerParentLimit[int]{Active: true, N: 1, Window: true}
	s := sql.Dialect(dialect.Postgres).Select("*").From(sql.Table("posts"))
	p.Apply(s, s.C("user_id"), "")
	q, _ := s.Query()
	assert.Contains(t, q, `PARTITION BY "posts"."user_id"`)
	assert.NotContains(t, q, `ORDER BY "posts"."id"`)
}
