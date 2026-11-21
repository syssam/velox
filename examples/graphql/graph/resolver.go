// Package graph is the GraphQL server gqlgen generates from veloxgen/velox.graphql
// and the application's app.graphql (generated.go, models_gen.go), and its
// resolvers: Resolver here, and the root query's and the mutations' in
// schema.resolvers.go, most of them one call into the veloxgen package. Every
// other field binds to veloxgen's methods, so it needs no resolver.
package graph

import (
	"context"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/examples/graphql/veloxgen"
)

// Resolver is the root resolver. It holds nothing: the generated code reads
// the *velox.DB from the request context, where gqlgenx.NewExtension puts
// it.
type Resolver struct{}

// viewerKey is the context key of the request's viewer.
type viewerKey struct{}

// WithViewer is ctx with the viewer, the ID of the user the request is by.
func WithViewer(ctx context.Context, user int64) context.Context {
	return context.WithValue(ctx, viewerKey{}, user)
}

// Viewer is the viewer WithViewer put in ctx; without one, an
// UNAUTHENTICATED error, which gqlgenx.ErrorPresenter shows the client as it
// is.
func Viewer(ctx context.Context) (int64, error) {
	if user, ok := ctx.Value(viewerKey{}).(int64); ok {
		return user, nil
	}
	return 0, codedError(ctx, "a viewer is required: the X-User header", veloxgql.CodeUnauthenticated)
}

// Author is the viewer, as an author: Viewer's, once it names a user the
// request may read. A viewer that names none is UNAUTHENTICATED too.
func Author(ctx context.Context) (int64, error) {
	viewer, err := Viewer(ctx)
	if err != nil {
		return 0, err
	}
	ok, err := veloxgql.DB(ctx).Query[veloxgen.User]().Where(veloxgen.Users.ID.EQ(viewer)).Exists(ctx)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, codedError(ctx, "the X-User header names no user", veloxgql.CodeUnauthenticated)
	}
	return viewer, nil
}

// OwnPost checks, on db, that the viewer wrote the post whose key is id, the
// ID gqlgen decoded. Without a viewer it is UNAUTHENTICATED (Viewer); an ID
// that names no post the request may read is NOT_FOUND, as one of another
// organization is; a post by another user is FORBIDDEN.
func OwnPost(ctx context.Context, db *velox.DB, id int64) error {
	viewer, err := Viewer(ctx)
	if err != nil {
		return err
	}
	p, err := db.Query[veloxgen.Post]().Where(veloxgen.Posts.ID.EQ(id)).Columns(veloxgen.Posts.AuthorID).One(ctx)
	if velox.IsNotFound(err) {
		return NotFound(ctx, "Post")
	}
	if err != nil {
		return err
	}
	if p.AuthorID != viewer {
		return codedError(ctx, "only the post's author may change it", veloxgql.CodeForbidden)
	}
	return nil
}

// NotFound is the NOT_FOUND error of an ID that names no row of the
// GraphQL type typ the request may read or change.
func NotFound(ctx context.Context, typ string) error {
	return codedError(ctx, "no "+typ+" has this ID", veloxgql.CodeNotFound)
}

// codedError is the error message with the code code, which
// gqlgenx.ErrorPresenter shows the client as it is.
func codedError(ctx context.Context, message, code string) error {
	return &gqlerror.Error{Path: graphql.GetPath(ctx), Message: message, Extensions: map[string]any{"code": code}}
}
