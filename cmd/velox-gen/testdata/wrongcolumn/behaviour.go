//go:build ignore

package schema

import (
	"context"

	"github.com/syssam/velox"
)

// InOrg's policy names a column that Issue has and Project lacks.
func (InOrg) Policy() velox.Policy {
	return velox.Policy{Read: func(context.Context) (velox.Cond, error) {
		return velox.C("author_id").EQ(1), nil
	}}
}

func (Issue) Policy() velox.Policy {
	return velox.Policy{
		Read: func(context.Context) (velox.Cond, error) {
			return velox.Or(authoredBy(), velox.Edge("project").HasWhere(velox.C("title").EQ("x"))), nil
		},
		Write: func(context.Context) (velox.Cond, error) {
			return velox.C("project_id").EQ("7"), nil
		},
	}
}

func authoredBy() velox.Cond { return velox.C("authr_id").EQ(1) }

func (Project) Hooks() []velox.Hook {
	return []velox.Hook{velox.Before(func(_ context.Context, m *velox.Mutation) error {
		if err := m.Fill("priority", 300); err != nil {
			return err
		}
		return m.Set("name", nil)
	}, velox.OpInsert)}
}

func (Label) Interceptors() []velox.Interceptor {
	return []velox.Interceptor{{Filter: func(context.Context) (velox.Cond, error) {
		return velox.And(velox.C(column()).NotNull(), velox.Edge("owner").Has(), velox.C("text").IsNull()), nil
	}}}
}

func column() string { return "text" }
