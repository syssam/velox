package graphqlgo

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/syssam/graphql-go"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// ErrorPresenter is a graphql-go error presenter for
// graphql.WithErrorPresenter. It shows a client only what is safe, with a
// code; it logs everything else with log and shows "internal error". A nil
// log logs with slog.ErrorContext, at level error, the message "veloxgql:
// internal error" with the attributes path (graphql.PathFrom(ctx)) and err.
//
// What a client sees, by the first that applies:
//   - A *graphql.Error or a *gqlerror.Error, not wrapped: graphql-go's own
//     (a parse or validation error, a limit, OPERATION_TIMEOUT) or the
//     application's, as graphql.DefaultErrorPresenter shows it: its
//     message, its extensions and the extensions of what it wraps.
//   - Anything else, as veloxgql.Present gives it: a coded error's code
//     and text, UNAUTHENTICATED, FORBIDDEN, NOT_FOUND or BAD_USER_INPUT
//     for velox's scope, policy, edge, validation and constraint errors,
//     and INTERNAL_SERVER_ERROR, "internal error", after log(ctx, err),
//     for the rest. A panic graphql-go recovered is shown so too, but not
//     passed to log: graphql-go has logged it, with its stack.
//
// So no entity, constraint, scope or driver text reaches a client through
// velox's errors. An error that is only the cancellation of the request
// (ctx is canceled, and err is context.Canceled) is not passed to log; a nil
// log records it with slog.DebugContext, as "veloxgql: request canceled".
// graphql-go's default presenter shows every error's text.
//
// One exception: the error a subscription's source function returns
// (graphql.Subscribe's fn) reaches a client as its text. graphql-go makes it
// a *graphql.Error of the text alone, dropping the error itself, before any
// presenter or interceptor sees it, so neither this presenter nor With can
// tell it from graphql-go's own, and a *graphql.Error loses its code too.
// A source function returns only errors whose text a client may read.
func ErrorPresenter(log func(ctx context.Context, err error)) graphql.ErrorPresenter {
	if log == nil {
		return presenter(slogInternal, slogCanceled)
	}
	return presenter(log, func(context.Context, error) {})
}

// presenter is ErrorPresenter with log for internal errors and canceled for
// the cancellation of the request.
func presenter(log, canceled func(ctx context.Context, err error)) graphql.ErrorPresenter {
	return func(ctx context.Context, err error) *graphql.Error {
		if err == nil {
			return nil
		}
		switch err.(type) {
		case *graphql.Error, *gqlerror.Error:
			return graphql.DefaultErrorPresenter(ctx, err)
		}
		code, msg := veloxgql.Present(err)
		if code == veloxgql.CodeInternal && !recoveredPanic(err) {
			if errors.Is(ctx.Err(), context.Canceled) && errors.Is(err, context.Canceled) {
				canceled(ctx, err)
			} else {
				log(ctx, err)
			}
		}
		return &graphql.Error{Message: msg, Extensions: map[string]any{"code": code}, Err: err}
	}
}

// recoveredPanic reports whether err, or an error it wraps, is the error
// graphql-go presents for a panic it recovered, which it has logged with
// the stack. graphql-go does not export its type, so it is known by its
// name. It walks the chain as errors.Is does.
func recoveredPanic(err error) bool {
	if err == nil {
		return false
	}
	if fmt.Sprintf("%T", err) == "*graphql.panicError" {
		return true
	}
	switch u := err.(type) {
	case interface{ Unwrap() error }:
		return recoveredPanic(u.Unwrap())
	case interface{ Unwrap() []error }:
		return slices.ContainsFunc(u.Unwrap(), recoveredPanic)
	}
	return false
}

// slogInternal is ErrorPresenter's log when it is given none.
func slogInternal(ctx context.Context, err error) {
	slog.ErrorContext(ctx, "veloxgql: internal error", "path", graphql.PathFrom(ctx).String(), "err", err)
}

// slogCanceled is what ErrorPresenter records of a canceled request when it
// is given no log.
func slogCanceled(ctx context.Context, err error) {
	slog.DebugContext(ctx, "veloxgql: request canceled", "path", graphql.PathFrom(ctx).String(), "err", err)
}
