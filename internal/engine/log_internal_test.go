package engine

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestSameArg: bytes compare by content, and a value Go cannot compare is
// not the same, so its statement's arguments are all hidden.
func TestSameArg(t *testing.T) {
	for _, c := range []struct {
		a, b any
		want bool
	}{
		{[]byte("x"), []byte("x"), true},
		{[]byte("x"), "x", false},
		{int64(1), int64(1), true},
		{int64(1), 1, false},
		{[]int{1}, []int{1}, false},
		{map[string]int{}, map[string]int{}, false},
	} {
		if got := sameArg(c.a, c.b); got != c.want {
			t.Errorf("sameArg(%v, %v) = %v", c.a, c.b, got)
		}
	}
}

// TestShownArgsUnknown: raw SQL and a statement whose arguments differ from
// a new rendering are not attributed.
func TestShownArgsUnknown(t *testing.T) {
	if s := ShownArgs(Postgres, &Statement{Op: OpRawQuery, Args: []any{1}}); s != nil {
		t.Errorf("raw: %v", s)
	}
	in := &EntityInfo{Name: "T", Table: "t", Columns: []ColumnInfo{{Name: "id"}, {Name: "pw", Sensitive: true}}, PrimaryKey: []int{0}}
	st := &Statement{Op: OpSelect, Entity: in, Where: []Predicate{NewPredicate(Node{Op: OpEQ, Col: 1, V: "s"})}, Limit: 2, Offset: 2}
	_, args, err := RenderStmt(Postgres, st)
	if err != nil {
		t.Fatal(err)
	}
	st.Args = args
	if s := ShownArgs(Postgres, st); len(s) != 2 || s[0] != ArgSensitive || s[1] != ArgShown {
		t.Errorf("rendered: %v", s)
	}
	if got := MaskedArgs(Postgres, st); got[0] != SensitiveArg || got[1] != 2 {
		t.Errorf("masked: %v", got)
	}
	st.Args = []any{"s"}
	if s := ShownArgs(Postgres, st); s != nil {
		t.Errorf("fewer args: %v", s)
	}
}

// TestRedactedErrorNamesSafeErrors: for each of velox's errors that hold no
// value, the log names the entity and the error's own fixed text, through
// any wrapping, a multi-wrap included; never the wrapper's text, which
// carries the secret here. ValidationError names its field alone, EdgeError
// its edge alone, and ErrOutOfRange and ErrNoShard fall back to the types.
// ErrTimeout is named once, by its own text.
func TestRedactedErrorNamesSafeErrors(t *testing.T) {
	st := &Statement{Entity: &EntityInfo{Name: "gen.Invoice"}}
	wrap := func(e error) error { return fmt.Errorf("velox: value 'the-secret': %w", e) }
	for _, s := range safeSentinels {
		got := RedactedError(st, wrap(s))
		if want := "velox: gen.Invoice: " + s.Error(); !strings.HasPrefix(got, want) {
			t.Errorf("%v: got %q, want prefix %q", s, got, want)
		}
		if strings.Contains(got, "secret") {
			t.Errorf("%v: leaked: %q", s, got)
		}
	}
	for _, c := range []struct {
		name string
		err  error
		want string // prefix
	}{
		{"multi-wrap", fmt.Errorf("velox: the-secret: %w (%w)", ErrStale, ErrNotFound),
			"velox: gen.Invoice: " + ErrStale.Error() + "; " + ErrNotFound.Error()},
		{"policy", wrap(&PolicyError{Entity: "gen.Invoice", Op: OpUpdate, Err: errors.New("owner is the-secret")}),
			"velox: gen.Invoice: " + ErrDenied.Error() + ";"},
		{"validation", wrap(&ValidationError{Entity: "gen.Invoice", Field: "Number", Err: fmt.Errorf("bad the-secret: %w", ErrNotFound)}),
			"velox: gen.Invoice.Number: validation failed;"},
		{"edge", wrap(&EdgeError{Entity: "gen.Post", Edge: "tags", Keys: []any{"the-secret"}, Err: fmt.Errorf("velox: %w", ErrNotFound)}),
			"velox: gen.Post: edge tags; " + ErrNotFound.Error() + ";"},
		{"out of range", wrap(ErrOutOfRange), "*fmt.wrapError > *errors.errorString;"},
		{"no shard", wrap(ErrNoShard), "*fmt.wrapError > *errors.errorString;"},
		{"timeout", wrap(ErrTimeout), "velox: gen.Invoice: " + ErrTimeout.Error() + "; the message"},
	} {
		got := RedactedError(st, c.err)
		if strings.Contains(got, "velox's deadline passed") {
			t.Errorf("%s: names the timeout twice: %q", c.name, got)
		}
		if !strings.HasPrefix(got, c.want) {
			t.Errorf("%s: got %q, want prefix %q", c.name, got, c.want)
		}
		if strings.Contains(got, "secret") {
			t.Errorf("%s: leaked: %q", c.name, got)
		}
	}
}
