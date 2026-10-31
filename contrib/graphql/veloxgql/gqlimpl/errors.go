package gqlimpl

import (
	"errors"

	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/shared"
)

// errPanicked is a connection's error, for the callers of Page or
// TotalCount but the one whose call panicked.
var errPanicked = errors.New("veloxgql: reading this connection panicked")

// InputErrorf is an error in the client's input, which
// veloxgql.ErrorPresenter shows the client with the code
// veloxgql.CodeBadUserInput: its text is "veloxgql: " and fmt.Sprintf of
// format and args, %w included, and errors.Is finds what %w wraps. The
// client sees the text without "veloxgql: ", so it must be safe to show:
// what the server expected, and no error but a coded one.
func InputErrorf(format string, args ...any) error {
	return shared.InputErrorf(format, args...)
}
