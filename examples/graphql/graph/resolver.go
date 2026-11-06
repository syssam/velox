// Package graph is the GraphQL server gqlgen generates from gen/velox.graphql
// and the application's app.graphql (generated.go, models_gen.go), and its
// resolvers: Resolver here, and the root query's and the mutations' in
// schema.resolvers.go, most of them one call into the gen package. Every
// other field binds to gen's methods, so it needs no resolver.
package graph

import (
	"context"
	"errors"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/examples/graphql/gen"
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
	return 0, codedError(ctx, "a viewer is required: the X-User header", veloxgql.CodeUnauthenticated)
}

// Author is the viewer, as an author: Viewer's, once it names a user the
// request may read. A viewer that names none is UNAUTHENTICATED too.
func Author(ctx context.Context) (int64, error) {
	viewer, err := Viewer(ctx)
	if err != nil {
		return 0, err
	}
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return 0, err
	}
	ok, err := db.Query[gen.User]().Where(gen.Users.ID.EQ(viewer)).Exists(ctx)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, codedError(ctx, "the X-User header names no user", veloxgql.CodeUnauthenticated)
	}
	return viewer, nil
}

// OwnPost checks, on db, that the viewer wrote the post whose ID is id, and
// returns the post's key. Without a viewer it is UNAUTHENTICATED (Viewer);
// an ID that names no post the request may read is NOT_FOUND, as one of
// another organization is; a post by another user is FORBIDDEN.
func OwnPost(ctx context.Context, db *velox.DB, id string) (int64, error) {
	viewer, err := Viewer(ctx)
	if err != nil {
		return 0, err
	}
	key, err := veloxgql.DecodeID[int64](id, "Post")
	if err != nil {
		return 0, err
	}
	p, err := db.Query[gen.Post]().Where(gen.Posts.ID.EQ(key)).Columns(gen.Posts.AuthorID).One(ctx)
	if velox.IsNotFound(err) {
		return 0, NotFound(ctx, "Post")
	}
	if err != nil {
		return 0, err
	}
	if p.AuthorID != viewer {
		return 0, codedError(ctx, "only the post's author may change it", veloxgql.CodeForbidden)
	}
	return key, nil
}

// NotFound is the NOT_FOUND error of an ID that names no node of the
// GraphQL type typ the request may read or change.
func NotFound(ctx context.Context, typ string) error {
	return codedError(ctx, "no "+typ+" has this ID", veloxgql.CodeNotFound)
}

// update writes set, an update input's setters and the resolver's own, to
// the row of E whose key column id holds key, and returns the row as
// written. It runs on the DB in ctx, in the mutation's transaction, so an
// edge's error rolls the row back. The UPDATE is under E's policies: a row
// it does not change is NOT_FOUND, named typ. The row is read back without
// the Read policy, so an update that moves it out of the request's view
// still returns it. An input that sets nothing changes nothing: the row is
// read under the Read policy.
func update[E any, PE velox.Entity[E]](ctx context.Context, id velox.NumberCol[E, int64], key int64, typ string, set []velox.Setter[E]) (*E, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	read := ctx
	if len(set) > 0 {
		n, err := db.Update[E, PE]().Where(id.EQ(key)).Set(set...).Exec(ctx)
		if err != nil {
			return nil, err
		}
		switch {
		case n == 0:
			return nil, NotFound(ctx, typ)
		case n < 0:
			// A row the Write policy hides would read as written.
			return nil, errors.New("update: the driver did not report the rows it updated")
		}
		read = velox.WithoutPolicy(ctx, "graphql: the row this update wrote")
	}
	x, err := db.Query[E, PE]().Where(id.EQ(key)).One(read)
	if velox.IsNotFound(err) {
		return nil, NotFound(ctx, typ)
	}
	return x, err
}

// codedError is the error message with the code code, which
// veloxgql.ErrorPresenter shows the client as it is.
func codedError(ctx context.Context, message, code string) error {
	return &gqlerror.Error{Path: graphql.GetPath(ctx), Message: message, Extensions: map[string]any{"code": code}}
}
