// Package model holds the hooks example's entity. task_velox.go beside this
// file is written by go tool velox-gen.
package model

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/syssam/velox2"
)

type userKey struct{}

// AsUser returns ctx acting as user, as authentication middleware would.
func AsUser(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, userKey{}, user)
}

func userOf(ctx context.Context) string { u, _ := ctx.Value(userKey{}).(string); return u }

// Audit is a mixin. Its BeforeInsert hook is typed on Audit, gets the
// embedded value, and runs before the entity's own hooks.
type Audit struct {
	CreatedBy string
}

// Mixin fills CreatedBy from the context's user.
func (a *Audit) Mixin(s *velox.Schema) {
	s.BeforeInsert(func(ctx context.Context, x *Audit) error {
		if x.CreatedBy == "" {
			x.CreatedBy = userOf(ctx)
		}
		return nil
	})
}

// Task is a to-do item. Slug is derived from Title by the hooks.
type Task struct {
	Audit
	ID    int64
	Title string
	Slug  string
	Done  bool
}

// Schema declares a hook of each kind. Hooks are given no *DB: a lookup
// belongs before the write. Any error a hook returns refuses the statement.
func (x *Task) Schema(s *velox.Schema) {
	s.Table("tasks")

	// BeforeInsert runs once per row, on the struct, for Insert, InsertAll
	// and upserts.
	s.BeforeInsert(func(_ context.Context, x *Task) error {
		if x.Title == "" {
			return errors.New("a task needs a title")
		}
		x.Slug = slug(x.Title)
		return nil
	})

	// BeforeUpdate sees the assignments of an Update or a Save through a
	// Mutation: Value reads one, Set adds or replaces one.
	s.BeforeUpdate(func(ctx context.Context, m *velox.Mutation[Task]) error {
		v, ok := m.Value(Tasks.Title)
		if !ok {
			return nil
		}
		title, _ := v.(string)
		if title == "" {
			return errors.New("a task needs a title")
		}
		m.Set(Tasks.Slug.Set(slug(title)))
		// SQL(ctx) runs hooks too, without sending anything: a hook with an
		// effect outside the statement checks IsDryRun.
		if log := logOf(ctx); log != nil && !velox.IsDryRun(ctx) {
			*log = append(*log, fmt.Sprintf("%s renamed a task to %s", userOf(ctx), title))
		}
		return nil
	})

	// BeforeDelete may narrow the rows a delete removes, or refuse it.
	s.BeforeDelete(func(_ context.Context, m *velox.Mutation[Task]) error {
		m.Where(Tasks.Done.EQ(true)) // only finished tasks are ever deleted
		return nil
	})
}

func slug(title string) string { return strings.ReplaceAll(strings.ToLower(title), " ", "-") }

type logKey struct{}

// WithLog returns ctx recording audit entries into log.
func WithLog(ctx context.Context, log *[]string) context.Context {
	return context.WithValue(ctx, logKey{}, log)
}

func logOf(ctx context.Context) *[]string { l, _ := ctx.Value(logKey{}).(*[]string); return l }
