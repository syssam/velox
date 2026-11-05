//go:build ignore

package schema

import (
	"context"

	"github.com/syssam/velox"
)

// InOrg's policy names a column that Issue has and Project lacks.
func (InOrg) Policy() velox.Policy {
	return velox.Policy{Read: func(context.Context) (velox.Predicate, error) {
		return velox.FieldEQ("author_id", 1), nil
	}}
}

func (Issue) Policy() velox.Policy {
	return velox.Policy{
		Read: func(context.Context) (velox.Predicate, error) {
			return velox.Or(authoredBy(), velox.HasEdgeWith("project", velox.FieldEQ("title", "x"))), nil
		},
		Write: func(context.Context) (velox.Predicate, error) {
			return velox.FieldEQ("project_id", "7"), nil
		},
	}
}

func authoredBy() velox.Predicate { return velox.FieldEQ("authr_id", 1) }

func (Project) Hooks() []velox.Hook {
	return []velox.Hook{velox.Before(func(_ context.Context, m *velox.Mutation) error {
		if err := m.Fill("priority", 300); err != nil {
			return err
		}
		return m.Set("name", nil)
	}, velox.OpInsert)}
}

func (Label) Interceptors() []velox.Interceptor {
	return []velox.Interceptor{{Filter: func(context.Context) (velox.Predicate, error) {
		return velox.And(velox.FieldNotNull(column()), velox.HasEdge("owner"), velox.FieldIsNull("text")), nil
	}}}
}

func column() string { return "text" }
