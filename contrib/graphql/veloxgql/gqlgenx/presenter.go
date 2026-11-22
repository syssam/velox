package gqlgenx

import (
	"context"
	"errors"
	"log/slog"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// ErrorPresenter is a gqlgen error presenter for srv.SetErrorPresenter. It
// shows a client only what is safe, with a code; it logs everything else
// with log and shows "internal error". A nil log logs with slog.ErrorContext,
// at level error, the message "veloxgql: internal error" with the attributes
// path (the field's path, as the client sees it), operation (the
// operation's name, when ctx has gqlgen's operation context and the
// operation is named) and err.
//
// log gets the request's context and the whole error. A log of the
// application's finds what the default records the same way: the path in
// the *gqlerror.Error that err holds, if any, else graphql.GetPath(ctx); the
// operation in graphql.GetOperationContext(ctx), when
// graphql.HasOperationContext(ctx).
//
// What a client sees, by the first that applies:
//   - An error that holds a coded error (one with a method Extensions()
//     map[string]any whose map has a string code, as veloxgql's errors
//     for a client's input and gqlgraph.InputErrorf's have): that code, and
//     the coded error's own text, without "veloxgql: ". Whatever wraps it
//     is not shown, nor what it is joined with.
//   - A *gqlerror.Error that wraps no error, gqlgen's own (a parse or
//     validation error) or the application's: unchanged. One that wraps an
//     error (its Err is set) is presented as that error is: its own
//     Message and Extensions are not shown, so an application's gqlerror
//     with Err set to an error of its own becomes "internal error". Leave
//     Err nil to show the client a message of the application's.
//   - scope.ErrNoScope: UNAUTHENTICATED, "unauthenticated".
//   - velox.ErrDenied, or a *velox.PolicyError: FORBIDDEN, "forbidden".
//   - scope.ErrMismatch, a write of a row in another scope: FORBIDDEN,
//     "forbidden". It names no scope or column.
//   - A *velox.EdgeError of velox.ErrNotFound, an edge setter's key that
//     names no row the caller may read or change: NOT_FOUND, "edge <edge>:
//     no node has this ID", or "edge <edge>: an ID names no node" for more
//     than one key. Of velox.ErrLinked, a child another owner holds:
//     BAD_USER_INPUT, "edge <edge>: already linked to another owner". The
//     edge is the schema's name, as "tags"; no key is named.
//   - A *velox.ValidationError: BAD_USER_INPUT, "invalid value: " and the
//     validator's text.
//   - A *velox.ConstraintError: BAD_USER_INPUT, "<kind> constraint
//     violated", such as "unique constraint violated".
//   - gqlgen's error for an integer the argument's Go type cannot hold (a
//     *graphql.IntegerError, which *graphql.NumberOverflowError and
//     *graphql.UintSignError wrap), as Int8…Uint32 return for 300 or -1:
//     BAD_USER_INPUT, gqlgen's text, such as "300 overflows signed 8-bit
//     integer", which holds only the client's own number.
//   - Anything else: INTERNAL_SERVER_ERROR, "internal error", after
//     log(ctx, err).
//
// An error that joins one the client may see (a coded error, an
// application's gqlerror) with one it may not, as errors.Join(coded,
// driverErr) does, shows the client the first, and is logged as well, for
// the second. So is an application's coded error that wraps one the client
// may not see: the client sees the coded error's text, and log gets the
// whole. What veloxgql's own coded errors and velox's policy, validation,
// constraint and edge errors wrap is not searched: it is never shown.
//
// So no entity, constraint, scope or driver text reaches a client. A
// *gqlerror.Error that wraps an error is presented as its error is, with its
// path and locations kept; every other error gets the field's path,
// graphql.GetPath(ctx), as gqlgen's default presenter gives it. The error
// the Extension adds when a mutation's transaction does not commit is
// presented like any other.
//
// An error that is only the cancellation of the request, as when the client
// went away, is not passed to log: the request's context is canceled, and
// every error in the error's tree that the client is not shown is
// context.Canceled (errors.Is). A nil log records it with
// slog.DebugContext, at level debug, as "veloxgql: request canceled" with
// the same attributes; a non-nil log drops it. OnCanceled(f) sends it to f
// instead, or drops it when f is nil, whether or not log is nil. Anything
// else that fails at the same moment, a driver's error or a panic, is logged
// as an internal error. The client, if still there, sees what it would
// otherwise.
//
// A panic in a resolver does not reach the presenter as a panic: gqlgen
// turns it into an error with the server's recover function first. gqlgen's
// default one prints the panic and the stack to stderr and returns a
// *gqlerror.Error "internal system error" with no Err, which the client then
// sees as it is, with no code, and which is not logged. A recover function
// that returns a plain error makes the panic an INTERNAL_SERVER_ERROR, which
// log records:
//
//	srv.SetRecoverFunc(func(ctx context.Context, p any) error {
//		return fmt.Errorf("panic: %v\n%s", p, debug.Stack())
//	})
//
// gqlgen's default presenter shows every error's text, and no codes.
//
// It is what holds a response to veloxgql.Limits.MaxErrors as its errors are made: under the
// Extension it returns nil, which gqlgen drops, once a response has MaxErrors or has
// stopped. A presenter of the application's that does not call it keeps the limit on
// what a resolver returns, as the request stops there, and its errors are made and
// dropped at the end.
func ErrorPresenter(log func(ctx context.Context, err error), opts ...PresenterOption) graphql.ErrorPresenterFunc {
	p := presenter{log: log, canceled: func(context.Context, error) {}}
	if log == nil {
		p.log, p.canceled = slogInternal, slogCanceled
	}
	for _, o := range opts {
		if o.apply == nil {
			panic("gqlgenx.ErrorPresenter: zero PresenterOption")
		}
		o.apply(&p)
	}
	return func(ctx context.Context, err error) *gqlerror.Error {
		if err == nil {
			return nil
		}
		if r, _ := ctx.Value(responseKey{}).(*response); r != nil && !r.admit() {
			return nil
		}
		leaves := internalLeaves(err, nil)
		log := p.log
		if errors.Is(ctx.Err(), context.Canceled) && canceledOnly(leaves) {
			log = p.canceled
		}
		out := &gqlerror.Error{Path: graphql.GetPath(ctx)}
		if gqlErr, ok := gqlcore.As[*gqlerror.Error](err); ok {
			if gqlErr.Err == nil {
				if c, _ := gqlcore.Coded(err); c == nil {
					if len(leaves) > 0 {
						log(ctx, err)
					}
					return gqlErr
				}
			}
			out.Path, out.Locations, out.Rule = gqlErr.Path, gqlErr.Locations, gqlErr.Rule
		}
		code, msg := present(err)
		if code == veloxgql.CodeInternal || len(leaves) > 0 {
			log(ctx, err)
		}
		out.Message, out.Extensions = msg, map[string]any{"code": code}
		return out
	}
}

// presenter is what ErrorPresenter's options set: where an internal error
// goes, and where an error that is only the cancellation of the request goes.
type presenter struct {
	log, canceled func(ctx context.Context, err error)
}

// PresenterOption configures ErrorPresenter. OnCanceled makes one. Its
// options are named for what they set, not With…: the With functions make an
// Extension's Option. The zero PresenterOption is not valid: ErrorPresenter
// panics on it.
type PresenterOption struct{ apply func(*presenter) }

// OnCanceled sets the function an error that is only the cancellation of the
// request goes to, in place of the default: slog at level debug when log is
// nil, and nothing when log is not. A nil f drops such errors. Given more
// than once, the last OnCanceled wins.
func OnCanceled(f func(ctx context.Context, err error)) PresenterOption {
	if f == nil {
		f = func(context.Context, error) {}
	}
	return PresenterOption{func(p *presenter) { p.canceled = f }}
}

// slogInternal is ErrorPresenter's log when it is given none.
func slogInternal(ctx context.Context, err error) {
	slog.ErrorContext(ctx, "veloxgql: internal error", logAttrs(ctx, err)...)
}

// slogCanceled is ErrorPresenter's log, when it is given none, for an error
// that is only the cancellation of the request.
func slogCanceled(ctx context.Context, err error) {
	slog.DebugContext(ctx, "veloxgql: request canceled", logAttrs(ctx, err)...)
}

// logAttrs are the attributes the default log records: the path, the
// operation's name if any, and err.
func logAttrs(ctx context.Context, err error) []any {
	path := graphql.GetPath(ctx)
	if gqlErr, ok := gqlcore.As[*gqlerror.Error](err); ok && gqlErr.Path != nil {
		path = gqlErr.Path
	}
	attrs := []any{"path", path.String()}
	if graphql.HasOperationContext(ctx) {
		if op := graphql.GetOperationContext(ctx).OperationName; op != "" {
			attrs = append(attrs, "operation", op)
		}
	}
	return append(attrs, "err", err)
}

// internalLeaves appends to out the internal leaves of err's tree: the
// errors that end a branch, through wraps and joins, and that presented
// alone would be internal errors. A client is not shown them, so they are
// logged. The tree is not searched below an error whose wrapped errors are
// known and never shown: one of veloxgql's coded errors (what it wraps, as
// veloxgql.ErrBadCursor, is there for errors.Is), and
// velox's policy, validation, constraint and edge errors. An application's coded
// error is searched, so one that wraps a driver's error is logged.
func internalLeaves(err error, out []error) []error {
	if err == nil || gqlcore.NilPointer(err) {
		// A nil pointer, a driver's typed nil, is no error, and its methods
		// could panic on it: it is passed over, as gqlcore.Walk does.
		return out
	}
	switch e := err.(type) { //nolint:errorlint // each node of the walk is judged by its own type
	case *gqlcore.Error:
		if e.Code != "" {
			return out
		}
	case *velox.PolicyError, *velox.ValidationError, *velox.ConstraintError, *velox.EdgeError:
		return out
	}
	switch u := err.(type) { //nolint:errorlint // the walk descends through each wrapper itself
	case interface{ Unwrap() []error }:
		for _, e := range u.Unwrap() {
			out = internalLeaves(e, out)
		}
		return out
	case interface{ Unwrap() error }:
		if next := u.Unwrap(); next != nil {
			return internalLeaves(next, out)
		}
	}
	if internal(err) {
		out = append(out, err)
	}
	return out
}

// canceledOnly reports whether leaves, an error's internal leaves, are all
// the cancellation of a context: the error is only the cancellation.
func canceledOnly(leaves []error) bool {
	for _, l := range leaves {
		if !gqlcore.Is(l, context.Canceled) {
			return false
		}
	}
	return len(leaves) > 0
}

// internal reports whether err, presented alone, is an internal error.
func internal(err error) bool {
	if c, _ := gqlcore.Coded(err); c != nil {
		return false
	}
	if gqlErr, ok := gqlcore.As[*gqlerror.Error](err); ok && gqlErr.Err == nil {
		return false
	}
	code, _ := present(err)
	return code == veloxgql.CodeInternal
}

// present is the code and the message the client sees for err: what
// veloxgql.Present gives, and for gqlgen's error of an integer the argument's
// Go type cannot hold (a *graphql.IntegerError, which
// *graphql.NumberOverflowError and *graphql.UintSignError wrap)
// BAD_USER_INPUT and gqlgen's text, which holds only the client's number.
func present(err error) (code, msg string) {
	code, msg = veloxgql.Present(err)
	if code != veloxgql.CodeInternal {
		return code, msg
	}
	if integer, ok := gqlcore.As[*graphql.IntegerError](err); ok {
		return veloxgql.CodeBadUserInput, integer.Message
	}
	return code, msg
}
