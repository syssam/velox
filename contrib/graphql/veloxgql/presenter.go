package veloxgql

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql/internal/shared"
)

// The codes ErrorPresenter writes as extensions.code: Apollo's, and
// NOT_FOUND.
const (
	// CodeBadUserInput: the client's input is wrong, and says how: an ID,
	// a cursor, paging arguments, a filter or a scalar that veloxgql,
	// generated code or gqlgen's integer types refused, a value a validator
	// refused, or a violated constraint.
	CodeBadUserInput = shared.CodeBadUserInput
	// CodeForbidden: a policy denied the statement (velox.ErrDenied).
	CodeForbidden = "FORBIDDEN"
	// CodeUnauthenticated: the request has no tenant (velox.ErrNoTenant).
	CodeUnauthenticated = "UNAUTHENTICATED"
	// CodeNotFound: the generated mutations return it for an ID that names
	// no row the caller may see or change.
	CodeNotFound = shared.CodeNotFound
	// CodeInternal: anything else, which the client cannot fix, such as a
	// driver's error, a timeout, a list edge over MaxList or ErrNoDB.
	CodeInternal = "INTERNAL_SERVER_ERROR"
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
//     for a client's input and gqlimpl.InputErrorf's have): that code, and
//     the coded error's own text, without "veloxgql: ". Whatever wraps it
//     is not shown, nor what it is joined with.
//   - A *gqlerror.Error that wraps no error, gqlgen's own (a parse or
//     validation error) or the application's: unchanged. One that wraps an
//     error (its Err is set) is presented as that error is: its own
//     Message and Extensions are not shown, so an application's gqlerror
//     with Err set to an error of its own becomes "internal error". Leave
//     Err nil to show the client a message of the application's.
//   - velox.ErrNoTenant: UNAUTHENTICATED, "unauthenticated".
//   - velox.ErrDenied, or a *velox.PolicyError: FORBIDDEN, "forbidden".
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
// whole. What veloxgql's own coded errors and velox's policy, validation and
// constraint errors wrap is not searched: it is never shown.
//
// So no entity, constraint, tenant or driver text reaches a client. A
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
// the same attributes. Anything else that fails at the same moment, a
// driver's error or a panic, is logged as an internal error. The client, if
// still there, sees what it would otherwise.
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
func ErrorPresenter(log func(ctx context.Context, err error)) graphql.ErrorPresenterFunc {
	canceled := func(context.Context, error) {}
	if log == nil {
		log, canceled = slogInternal, slogCanceled
	}
	return func(ctx context.Context, err error) *gqlerror.Error {
		if err == nil {
			return nil
		}
		leaves := internalLeaves(err, nil)
		log := log
		if errors.Is(ctx.Err(), context.Canceled) && canceledOnly(leaves) {
			log = canceled
		}
		out := &gqlerror.Error{Path: graphql.GetPath(ctx)}
		var gqlErr *gqlerror.Error
		if errors.As(err, &gqlErr) {
			if gqlErr.Err == nil {
				if c, _ := shared.Coded(err); c == nil {
					if len(leaves) > 0 {
						log(ctx, err)
					}
					return gqlErr
				}
			}
			out.Path, out.Locations, out.Rule = gqlErr.Path, gqlErr.Locations, gqlErr.Rule
		}
		code, msg := present(err)
		if code == CodeInternal || len(leaves) > 0 {
			log(ctx, err)
		}
		out.Message, out.Extensions = msg, map[string]any{"code": code}
		return out
	}
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
	var gqlErr *gqlerror.Error
	if errors.As(err, &gqlErr) && gqlErr.Path != nil {
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
// the join of ErrBadCursor and ErrWrongIDType, is there for errors.Is), and
// velox's policy, validation and constraint errors. An application's coded
// error is searched, so one that wraps a driver's error is logged.
func internalLeaves(err error, out []error) []error {
	switch e := err.(type) {
	case nil:
		return out
	case *shared.Error:
		if e.Code != "" {
			return out
		}
	case *velox.PolicyError, *velox.ValidationError, *velox.ConstraintError:
		return out
	}
	switch u := err.(type) {
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
		if !errors.Is(l, context.Canceled) {
			return false
		}
	}
	return len(leaves) > 0
}

// internal reports whether err, presented alone, is an internal error.
func internal(err error) bool {
	if c, _ := shared.Coded(err); c != nil {
		return false
	}
	var gqlErr *gqlerror.Error
	if errors.As(err, &gqlErr) && gqlErr.Err == nil {
		return false
	}
	code, _ := present(err)
	return code == CodeInternal
}

// present is the code and the message the client sees for err.
func present(err error) (code, msg string) {
	if c, code := shared.Coded(err); c != nil {
		return code, strings.TrimPrefix(c.Error(), "veloxgql: ")
	}
	var (
		validation *velox.ValidationError
		constraint *velox.ConstraintError
		integer    *graphql.IntegerError
	)
	switch {
	case errors.Is(err, velox.ErrNoTenant):
		return CodeUnauthenticated, "unauthenticated"
	case errors.Is(err, velox.ErrDenied):
		return CodeForbidden, "forbidden"
	case errors.As(err, &validation):
		if validation.Err == nil {
			return CodeBadUserInput, "invalid value"
		}
		return CodeBadUserInput, "invalid value: " + validation.Err.Error()
	case errors.As(err, &constraint):
		return CodeBadUserInput, constraint.Kind.String() + " constraint violated"
	case errors.As(err, &integer):
		return CodeBadUserInput, integer.Message
	}
	return CodeInternal, "internal error"
}
