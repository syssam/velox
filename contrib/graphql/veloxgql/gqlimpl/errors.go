package gqlimpl

import "github.com/syssam/velox2/contrib/graphql/veloxgql/internal/shared"

// InputErrorf is an error in the client's input, which
// veloxgql.ErrorPresenter shows the client with the code
// veloxgql.CodeBadUserInput: its text is "veloxgql: " and fmt.Sprintf of
// format and args, %w included, and errors.Is finds what %w wraps. The
// client sees the text without "veloxgql: ", so it must be safe to show:
// what the server expected, and no error but a coded one.
func InputErrorf(format string, args ...any) error {
	return shared.InputErrorf(format, args...)
}
