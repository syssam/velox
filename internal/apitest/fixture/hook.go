package fixture

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
	_ "github.com/syssam/velox/internal/testmodel/runtime" // binds the schema's policies and hooks
	"github.com/syssam/velox/scope"
)

// Trace records the order hooks and the core ran in, per test.
var Trace []string

func (*DBook) Hooks() []velox.Hook[DBook] {
	return []velox.Hook[DBook]{
		func(next velox.Mutator[DBook]) velox.Mutator[DBook] {
			return velox.MutateFunc[DBook](func(ctx context.Context, m *velox.Mutation[DBook]) error {
				Trace = append(Trace, "outer before "+m.Op().String())
				if m.Op() == velox.OpInsert {
					for _, b := range m.Rows() {
						b.Title = strings.TrimSpace(b.Title)
					}
				}
				err := next.Mutate(ctx, m)
				Trace = append(Trace, "outer after")
				return err
			})
		},
		velox.On(func(next velox.Mutator[DBook]) velox.Mutator[DBook] {
			return velox.MutateFunc[DBook](func(ctx context.Context, m *velox.Mutation[DBook]) error {
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

// OrgPolicy confines entity E to the context's testmodel.Org, by its column
// org_id, as a tenancy mixin's Policy does.
func OrgPolicy[E any]() velox.Policy[E] { return scope.By[E](testmodel.Org, "org_id").Policy() }

// OrgHooks fills entity E's org_id on insert, as a tenancy mixin's Hooks do.
func OrgHooks[E any]() []velox.Hook[E] {
	return []velox.Hook[E]{scope.By[E](testmodel.Org, "org_id").Fill()}
}

// Policy confines DTag to the context's org.
func (*DTag) Policy() velox.Policy[DTag] { return OrgPolicy[DTag]() }

// Hooks fills DTag's OrgID on insert.
func (*DTag) Hooks() []velox.Hook[DTag] { return OrgHooks[DTag]() }

func (*DAuthor) Hooks() []velox.Hook[DAuthor] {
	return []velox.Hook[DAuthor]{func(next velox.Mutator[DAuthor]) velox.Mutator[DAuthor] {
		return velox.MutateFunc[DAuthor](func(ctx context.Context, m *velox.Mutation[DAuthor]) error {
			if v, _ := ctx.Value(BlankName{}).(bool); v && m.Op() == velox.OpUpdate {
				m.Set(DAuthors.Name.Set("")) // validated after the hook
			}
			if v, _ := ctx.Value(TouchCreated{}).(bool); v && m.Op() == velox.OpUpdate {
				m.Set(DAuthors.CreatedAt.Set(SomeTime)) // immutable: refused after the hook
			}
			if v, _ := ctx.Value(RenameTo{}).(string); v != "" && m.Op() == velox.OpUpdate {
				m.Set(DAuthors.Name.Set(v)) // Save writes it back
			}
			return next.Mutate(ctx, m)
		})
	}}
}

type BlankName struct{}

type TouchCreated struct{}

type RenameTo struct{}

var SomeTime = time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)

func (*DAuthor) Policy() velox.Policy[DAuthor] {
	return velox.Policy[DAuthor]{Insert: func(ctx context.Context, a *DAuthor) error {
		if a.Name == "mallory" {
			return velox.Deny("not mallory")
		}
		return nil
	}}
}
