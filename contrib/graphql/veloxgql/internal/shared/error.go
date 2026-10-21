package shared

import (
	"errors"
	"fmt"
	"strings"
)

// CodeBadUserInput is the code of an error in the client's input, which
// veloxgql exports as CodeBadUserInput.
const CodeBadUserInput = "BAD_USER_INPUT"

// Error is an error with a code for the client, which veloxgql's
// ErrorPresenter writes as extensions.code, and a message safe to show it.
type Error struct {
	Code string
	Msg  string // the message, without "veloxgql: "
	Err  error  // what the error wraps, for errors.Is and errors.As; nil for nothing
}

// Error is "veloxgql: " and the message.
func (e *Error) Error() string { return "veloxgql: " + e.Msg }

// Unwrap returns what the error wraps.
func (e *Error) Unwrap() error { return e.Err }

// Extensions is {"code": Code}, the shape of gqlgen's former ExtendedError,
// which veloxgql's ErrorPresenter reads; nil when Code is "".
func (e *Error) Extensions() map[string]any {
	if e.Code == "" {
		return nil
	}
	return map[string]any{"code": e.Code}
}

// InputErrorf is a BAD_USER_INPUT error whose text is "veloxgql: " and
// fmt.Sprintf of format and args, %w included. The client sees the text, so
// format and args must be safe to show it: what the server expected, and
// no error but a coded one.
func InputErrorf(format string, args ...any) error {
	err := fmt.Errorf(format, args...)
	e := &Error{Code: CodeBadUserInput, Msg: err.Error()}
	switch u := err.(type) {
	case interface{ Unwrap() error }:
		e.Err = u.Unwrap()
	case interface{ Unwrap() []error }:
		e.Err = errors.Join(u.Unwrap()...)
	}
	return e
}

// Within is err as an error of at: its text is "veloxgql: <at>: <err's
// text>", err's own "veloxgql: " left out, and its code is err's own. An
// err that is not itself coded gives an error without a code, even when it
// wraps a coded one, since its text is not known to be safe: a client is
// then shown the coded error's text alone.
func Within(at string, err error) error {
	var code string
	if c, ok := err.(Coding); ok {
		code, _ = c.Extensions()["code"].(string)
	}
	return &Error{Code: code, Msg: at + ": " + strings.TrimPrefix(err.Error(), "veloxgql: "), Err: err}
}

// Coding is an error that may carry a code for the client, in the shape of
// gqlgen's former ExtendedError.
type Coding interface {
	error
	Extensions() map[string]any
}

// Coded is the first error of err's tree, err itself first and then depth
// first as errors.As goes, whose Extensions has a non-empty string code,
// and that code; nil and "" when none has.
func Coded(err error) (Coding, string) {
	for err != nil {
		if c, ok := err.(Coding); ok {
			if code, _ := c.Extensions()["code"].(string); code != "" {
				return c, code
			}
		}
		switch u := err.(type) {
		case interface{ Unwrap() error }:
			err = u.Unwrap()
		case interface{ Unwrap() []error }:
			for _, e := range u.Unwrap() {
				if c, code := Coded(e); c != nil {
					return c, code
				}
			}
			return nil, ""
		default:
			return nil, ""
		}
	}
	return nil, ""
}
