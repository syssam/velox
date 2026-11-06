package gqlimpl

import (
	"errors"
	"fmt"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// codeOf is the code of err's *gqlcore.Error, or "".
func codeOf(err error) string {
	var e *gqlcore.Error
	if !errors.As(err, &e) {
		return ""
	}
	return e.Code
}

// TestWriteError: a validator's error on a field the input names becomes
// BAD_USER_INPUT naming it, wrapping the *velox.ValidationError, wrapped or
// not; any other error, and one joined with an internal error, is returned
// as it is.
func TestWriteError(t *testing.T) {
	name := func(goField string) string {
		if goField == "Title" {
			return "title"
		}
		return ""
	}
	ve := &velox.ValidationError{Entity: "gen.Post", Field: "Title", Err: errors.New("must not be empty")}
	err := writeError(ve, "CreatePostInput", "gen.Post", name)
	if codeOf(err) != gqlcore.CodeBadUserInput {
		t.Errorf("code %q, want BAD_USER_INPUT", codeOf(err))
	}
	if want := "veloxgql: CreatePostInput.title: invalid value: must not be empty"; err == nil || err.Error() != want {
		t.Errorf("writeError = %v, want %q", err, want)
	}
	var got *velox.ValidationError
	if !errors.As(err, &got) || got != ve {
		t.Error("errors.As does not find the *velox.ValidationError")
	}
	if err := writeError(fmt.Errorf("insert: %w", ve), "CreatePostInput", "gen.Post", name); codeOf(err) != gqlcore.CodeBadUserInput {
		t.Errorf("a wrapped validation error: %v, want BAD_USER_INPUT", err)
	}
	joined := errors.Join(errors.New("driver: connection reset"), ve)
	if got := writeError(joined, "CreatePostInput", "gen.Post", name); got != joined {
		t.Errorf("a validation error joined with an internal one: %v, want it unchanged", got)
	}
	noText := &velox.ValidationError{Entity: "gen.Post", Field: "Title"}
	if err := writeError(noText, "CreatePostInput", "gen.Post", name); err == nil || err.Error() != "veloxgql: CreatePostInput.title: invalid value" {
		t.Errorf("a validation error with no text: %v", err)
	}
	for label, e := range map[string]error{
		"another entity": &velox.ValidationError{Entity: "gen.User", Field: "Title", Err: errors.New("must not be empty")},
		"unnamed field":  &velox.ValidationError{Entity: "gen.Post", Field: "Secret", Err: errors.New("must not be empty")},
		"constraint":     &velox.ConstraintError{Entity: "gen.Post", Kind: velox.ConstraintUnique, Constraint: "posts_title_key"},
	} {
		if got := writeError(e, "CreatePostInput", "gen.Post", name); got != e {
			t.Errorf("%s: %v, want the error unchanged", label, got)
		}
	}
	if writeError(nil, "CreatePostInput", "gen.Post", name) != nil {
		t.Error("writeError(nil) is not nil")
	}
}
