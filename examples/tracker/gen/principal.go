// Package gen holds the tracker's entities. The *_velox.go files are written by go
// tool velox-gen; the other files are written by hand: this one carries who a
// request acts as, and policy.go the policies, which velox-gen binds into the
// entities' metadata.
package gen

import "context"

// Principal is who a request acts as. Authentication middleware sets it.
type Principal struct {
	UserID int64
	Admin  bool
}

type principalKey struct{}

// WithPrincipal returns ctx acting as p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFromContext returns who ctx acts as, and whether anyone.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}
