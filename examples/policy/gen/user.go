// Package gen holds the policy example's entities. The *_velox.go files are
// written by go tool velox-gen; the other files are written by hand: this one
// carries the user of a request, and doc_policy.go and comment_policy.go the
// policies, which velox-gen binds into the entities' metadata.
package gen

import "context"

type userKey struct{}

// WithUser returns ctx acting as user. In a service, authentication middleware
// sets it. The user "admin" may read and change everything.
func WithUser(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, userKey{}, user)
}

func userFromContext(ctx context.Context) string { u, _ := ctx.Value(userKey{}).(string); return u }
