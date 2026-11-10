// Package principal carries who a request acts as, which the schema's
// policies read. It does not import veloxgen, so that anything may import it.
package principal

import "context"

// Principal is who a request acts as. Authentication middleware sets it.
type Principal struct {
	UserID int64
	Admin  bool
}

type principalKey struct{}

// With returns ctx acting as p.
func With(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// FromContext returns who ctx acts as, and whether anyone.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}
