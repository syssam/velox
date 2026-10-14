package velox

import (
	"errors"
	"strings"
	"testing"
)

// TestSentinelsAreBarePhrases: sentinels are always returned wrapped in an
// error that says "velox: <Entity>: ", so their own text has no prefix and
// the prefix never doubles (policy design §6).
func TestSentinelsAreBarePhrases(t *testing.T) {
	for _, err := range []error{ErrNotFound, ErrNotSingular, ErrUnbounded, ErrNoWhere, ErrLimitTooLarge, ErrStale} {
		if s := err.Error(); strings.HasPrefix(s, "velox") || strings.Contains(s, ": ") {
			t.Errorf("%q is not a bare phrase", s)
		}
	}
}

func TestSchemaErrorNamesEntityAndField(t *testing.T) {
	cases := []struct {
		err  *SchemaError
		want string
	}{
		{&SchemaError{Entity: "billing.Invoice", Field: "Total", Reason: "bad"}, "velox: billing.Invoice.Total: bad"},
		{&SchemaError{Entity: "billing.Invoice", Reason: "no primary key"}, "velox: billing.Invoice: no primary key"},
	}
	for _, c := range cases {
		if got := c.err.Error(); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
	var se *SchemaError
	if !errors.As(error(cases[0].err), &se) {
		t.Error("errors.As does not find *SchemaError")
	}
}
