// Package gen holds the hooks example's entities. The *_velox.go files are
// written by go tool velox-gen; the other files are written by hand: this one
// carries the user of a request, task_hooks.go the hooks of Task, and
// created_by.go a hook that any entity can list.
package gen

import "context"

type userKey struct{}

// WithUser returns ctx acting as user, as authentication middleware would.
func WithUser(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, userKey{}, user)
}

func userFromContext(ctx context.Context) string { u, _ := ctx.Value(userKey{}).(string); return u }
