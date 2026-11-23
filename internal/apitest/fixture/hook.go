package fixture

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/scope"
)

// Trace records the order hooks and the core ran in, per test.
var Trace []string

func (*DBook) Hooks() []velox.Hook {
	return []velox.Hook{
		func(next velox.Mutator) velox.Mutator {
			return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
				Trace = append(Trace, "outer before "+m.Op().String())
				if m.Op() == velox.OpInsert {
					for r := range m.Rows() {
						title, _ := r.Value("title").(string)
						if err := r.Set("title", strings.TrimSpace(title)); err != nil {
							return err
						}
					}
				}
				err := next.Mutate(ctx, m)
				Trace = append(Trace, "outer after")
				return err
			})
		},
		velox.On(func(next velox.Mutator) velox.Mutator {
			return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
				Trace = append(Trace, "delete only")
				if HookRefuses {
					return ErrRefused
				}
				return next.Mutate(ctx, m)
			})
		}, velox.OpDelete),
	}
}

var (
	ErrRefused  = errors.New("refused")
	HookRefuses bool
)

// OrgPolicy confines an entity to the context's testmodel.Org, by its column
// org_id, as a tenancy mixin's Policy does.
func OrgPolicy() velox.Policy { return KeyPolicy(testmodel.Org, "org_id") }

// OrgHooks fills an entity's org_id on insert, in each row that leaves it
// zero, as a tenancy mixin's Hooks do.
func OrgHooks() []velox.Hook { return KeyHooks(testmodel.Org, "org_id") }

// KeyPolicy confines an entity to the context's value of key, by column,
// as a tenancy mixin's Policy does: Read requires the value, and Insert
// refuses a row whose column is not it.
func KeyPolicy[V comparable](key *scope.Key[V], column string) velox.Policy {
	return velox.Policy{
		Read: func(ctx context.Context, _ *velox.DB) (velox.Predicate, error) {
			v, err := key.Require(ctx)
			if err != nil {
				return velox.Predicate{}, err
			}
			return velox.FieldEQ(column, v), nil
		},
		Insert: func(ctx context.Context, _ *velox.DB, r velox.Row) error {
			return key.Match(ctx, r.Value(column))
		},
	}
}

// KeyHooks fills column on insert with the context's value of key, in each
// row that leaves it zero, as a tenancy mixin's Hooks do.
func KeyHooks[V comparable](key *scope.Key[V], column string) []velox.Hook {
	return []velox.Hook{velox.Before(func(ctx context.Context, m *velox.Mutation) error {
		if v, ok := key.From(ctx); ok {
			return m.Fill(column, v)
		}
		return nil
	}, velox.OpInsert)}
}

// Policy confines DTag to the context's org.
func (*DTag) Policy() velox.Policy { return OrgPolicy() }

// Hooks fills DTag's OrgID on insert.
func (*DTag) Hooks() []velox.Hook { return OrgHooks() }

// Mixin lists Org, whose policy and hooks run before DAuthor's own.
func (*DAuthor) Mixin() []schema.Mixin { return []schema.Mixin{Org{}} }

func (*DAuthor) Hooks() []velox.Hook {
	return []velox.Hook{func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			if v, _ := ctx.Value(BlankName{}).(bool); v && m.Op() == velox.OpUpdate {
				_ = m.Set("name", "") // validated after the hook
			}
			if v, _ := ctx.Value(TouchCreated{}).(bool); v && m.Op() == velox.OpUpdate {
				_ = m.Set("created_at", SomeTime) // immutable: refused after the hook
			}
			if v, _ := ctx.Value(RenameTo{}).(string); v != "" && m.Op() == velox.OpUpdate {
				_ = m.Set("name", v) // Save writes it back
			}
			return next.Mutate(ctx, m)
		})
	}}
}

type BlankName struct{}

type TouchCreated struct{}

type RenameTo struct{}

var SomeTime = time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)

func (*DAuthor) Policy() velox.Policy {
	return velox.Policy{Insert: func(_ context.Context, _ *velox.DB, r velox.Row) error {
		if r.Value("name") == "mallory" {
			return velox.Deny("not mallory")
		}
		return nil
	}}
}
