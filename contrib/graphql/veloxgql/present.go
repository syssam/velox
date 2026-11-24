package veloxgql

import (
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
	"github.com/syssam/velox/scope"
)

// The codes Present gives, which an adapter's error presenter writes as
// extensions.code: Apollo's, NOT_FOUND and COST_LIMIT_EXCEEDED.
const (
	// CodeBadUserInput: the client's input is wrong, and says how: an ID,
	// a cursor, paging arguments, a filter or a scalar that veloxgql,
	// generated code or the engine's integer types refused, a value a validator
	// refused, or a violated constraint.
	CodeBadUserInput = gqlcore.CodeBadUserInput
	// CodeForbidden: a policy denied the statement (velox.ErrDenied), or it
	// wrote a row outside the caller's scope (scope.ErrMismatch).
	CodeForbidden = "FORBIDDEN"
	// CodeUnauthenticated: the request has no scope value (scope.ErrNoScope).
	CodeUnauthenticated = "UNAUTHENTICATED"
	// CodeNotFound: an edge setter's key names no row the caller may see or
	// change (a *velox.EdgeError of velox.ErrNotFound).
	CodeNotFound = gqlcore.CodeNotFound
	// CodeTooCostly: the request costs more than Limits.MaxCost allows
	// (ErrTooCostly). It is not a bad argument: the client can ask for less.
	CodeTooCostly = "COST_LIMIT_EXCEEDED"
	// CodeTimeout: the request ran over Limits.Timeout (ErrTimeout).
	CodeTimeout = "TIMEOUT"
	// CodeInternal: anything else, which the client cannot fix, such as a
	// driver's error, a deadline of the application's, a list edge over MaxList,
	// or ErrNoDB.
	CodeInternal = "INTERNAL_SERVER_ERROR"
)

// Present is the code and the message a client may see for err, which an
// adapter's error presenter shows it, by the first that applies:
//   - An error that holds a coded error (one with a method Extensions()
//     map[string]any whose map has a string code, as veloxgql's errors for a
//     client's input and gqlgraph.InputErrorf's have): that code, and the
//     coded error's own text, without "veloxgql: ".
//   - scope.ErrNoScope: CodeUnauthenticated, "unauthenticated".
//   - velox.ErrDenied, or a *velox.PolicyError, and scope.ErrMismatch:
//     CodeForbidden, "forbidden".
//   - A *velox.EdgeError of velox.ErrNotFound: CodeNotFound, "edge <edge>:
//     no node has this ID", or "edge <edge>: an ID names no node" for more
//     than one key. Of velox.ErrLinked: CodeBadUserInput, "edge <edge>:
//     already linked to another owner".
//   - A *velox.ValidationError: CodeBadUserInput, "invalid value: " and the
//     validator's text.
//   - A *velox.ConstraintError: CodeBadUserInput, "<kind> constraint
//     violated".
//   - Anything else: CodeInternal, "internal error", which the presenter
//     logs.
//
// It names no entity, constraint, scope or driver text. An engine's own
// errors (gqlgen's for an integer an argument cannot hold) are its adapter's
// to present.
func Present(err error) (code, msg string) {
	if c, code := gqlcore.Coded(err); c != nil {
		return code, strings.TrimPrefix(c.Error(), "veloxgql: ")
	}
	// Through shared's Is and As: a driver's typed nil in err's tree
	// cannot panic.
	switch {
	case gqlcore.Is(err, scope.ErrNoScope):
		return CodeUnauthenticated, "unauthenticated"
	case gqlcore.Is(err, velox.ErrDenied), gqlcore.Is(err, scope.ErrMismatch):
		return CodeForbidden, "forbidden"
	}
	if edge, ok := gqlcore.As[*velox.EdgeError](err); ok {
		switch {
		case gqlcore.Is(edge.Err, velox.ErrLinked):
			return CodeBadUserInput, "edge " + edge.Edge + ": already linked to another owner"
		case !gqlcore.Is(edge.Err, velox.ErrNotFound):
		case len(edge.Keys) == 1:
			return CodeNotFound, "edge " + edge.Edge + ": no node has this ID"
		default:
			return CodeNotFound, "edge " + edge.Edge + ": an ID names no node"
		}
	}
	if validation, ok := gqlcore.As[*velox.ValidationError](err); ok {
		if validation.Err == nil {
			return CodeBadUserInput, "invalid value"
		}
		return CodeBadUserInput, "invalid value: " + validation.Err.Error()
	}
	if constraint, ok := gqlcore.As[*velox.ConstraintError](err); ok {
		return CodeBadUserInput, constraint.Kind.String() + " constraint violated"
	}
	return CodeInternal, "internal error"
}
