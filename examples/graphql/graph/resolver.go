// Package graph is the GraphQL server gqlgen generates from gen/velox.graphqls
// and the application's app.graphqls (generated.go, models_gen.go), and its
// resolvers: Resolver here, and the root query's and the mutations' in
// schema.resolvers.go, most of them one call into the gen package. Every
// other field binds to gen's methods, so it needs no resolver.
package graph

import (
	"context"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// Resolver is the root resolver. It holds nothing: the generated code reads
// the *velox.DB from the request context, where veloxgql.NewExtension puts
// it.
type Resolver struct{}

// viewerKey is the context key of the request's viewer.
type viewerKey struct{}

// WithViewer is ctx with the viewer, the ID of the user the request is by.
func WithViewer(ctx context.Context, user int64) context.Context {
	return context.WithValue(ctx, viewerKey{}, user)
}

// Viewer is the viewer WithViewer put in ctx; without one, an
// UNAUTHENTICATED error, which veloxgql.ErrorPresenter shows the client as it
// is.
func Viewer(ctx context.Context) (int64, error) {
	if user, ok := ctx.Value(viewerKey{}).(int64); ok {
		return user, nil
	}
	return 0, &gqlerror.Error{
		Path:       graphql.GetPath(ctx),
		Message:    "a viewer is required: the X-User header",
		Extensions: map[string]any{"code": veloxgql.CodeUnauthenticated},
	}
}
