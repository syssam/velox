package gqlcore

import (
	"errors"
	"fmt"
	"io"
	"testing"
)

// TestCodedErrors: InputErrorf and NotFoundf carry their code and wrap what
// %w wraps, one error or several; Within keeps a coded error's code and
// drops an uncoded one's; Coded finds the first code in a tree.
func TestCodedErrors(t *testing.T) {
	err := InputErrorf("x: %w", io.EOF)
	if err.Error() != "veloxgql: x: EOF" || !errors.Is(err, io.EOF) {
		t.Errorf("InputErrorf: %v", err)
	}
	if _, code := Coded(err); code != CodeBadUserInput {
		t.Errorf("InputErrorf's code: %q", code)
	}
	two := NotFoundf("%w and %w", io.EOF, io.ErrUnexpectedEOF)
	if !errors.Is(two, io.EOF) || !errors.Is(two, io.ErrUnexpectedEOF) {
		t.Errorf("NotFoundf of two: %v", two)
	}
	if _, code := Coded(two); code != CodeNotFound {
		t.Errorf("NotFoundf's code: %q", code)
	}
	if e := InputErrorf("plain").(*Error); e.Err != nil {
		t.Errorf("an error wrapping nothing wraps %v", e.Err)
	}

	in := Within("In.field", err)
	if in.Error() != "veloxgql: In.field: x: EOF" {
		t.Errorf("Within: %q", in)
	}
	if _, code := Coded(in); code != CodeBadUserInput {
		t.Errorf("Within of a coded error: code %q", code)
	}
	plain := Within("In", fmt.Errorf("server: %w", err))
	if plain.(*Error).Extensions() != nil {
		t.Errorf("Within of an uncoded error has the code %v", plain.(*Error).Extensions())
	}
	if c, code := Coded(plain); code != CodeBadUserInput || c != err {
		t.Errorf("Coded finds the wrapped coded error: %v %q", c, code)
	}
	if c, code := Coded(io.EOF); c != nil || code != "" {
		t.Errorf("Coded of an uncoded error: %v %q", c, code)
	}
	if c, code := Coded(&Error{Msg: "no code"}); c != nil || code != "" {
		t.Errorf("Coded of an empty code: %v %q", c, code)
	}
}

type nilError struct{}

func (*nilError) Error() string { panic("called on nil") }

type isError struct{ target error }

func (e isError) Error() string        { return "is" }
func (e isError) Is(target error) bool { return target == e.target }
func (e isError) As(target any) bool   { return false }

type asError struct{ nilAs bool }

func (e asError) Error() string { return "as" }
func (e asError) As(target any) bool {
	if p, ok := target.(**Error); ok {
		if !e.nilAs {
			*p = &Error{Msg: "from As"}
		}
		return true
	}
	return false
}

// TestWalk: Walk, Is and As pass over a nil pointer in the tree, go through
// joined errors, and use an error's Is and As methods.
func TestWalk(t *testing.T) {
	var np *nilError
	tree := errors.Join(np, fmt.Errorf("w: %w", io.EOF))
	if Walk(nil, func(error) bool { return true }) || Walk(np, func(error) bool { return true }) {
		t.Error("Walk of nil or a nil pointer")
	}
	if !Is(tree, io.EOF) || Is(tree, io.ErrClosedPipe) {
		t.Error("Is through a join with a nil pointer")
	}
	if !Is(nil, nil) || Is(io.EOF, nil) {
		t.Error("Is of a nil target")
	}
	if !Is(isError{target: io.ErrNoProgress}, io.ErrNoProgress) {
		t.Error("Is by an Is method")
	}
	coded := &Error{Code: "C", Msg: "m"}
	if e, ok := As[*Error](fmt.Errorf("w: %w", coded)); !ok || e != coded {
		t.Errorf("As: %v %v", e, ok)
	}
	if e, ok := As[*Error](asError{}); !ok || e.Msg != "from As" {
		t.Errorf("As by an As method: %v %v", e, ok)
	}
	if _, ok := As[*Error](asError{nilAs: true}); ok {
		t.Error("As of an As method that gives a nil pointer")
	}
	if _, ok := As[*Error](isError{}); ok {
		t.Error("As of an As method that refuses")
	}
	if !NilPointer(np) || NilPointer(io.EOF) {
		t.Error("NilPointer")
	}
}
