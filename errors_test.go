package velox

import (
	"errors"
	"strings"
	"testing"
)

func TestSentinelErrorsStartWithVelox(t *testing.T) {
	for _, err := range []error{ErrNotFound, ErrNotSingular, ErrUnbounded, ErrNoWhere, ErrStale} {
		if !strings.HasPrefix(err.Error(), "velox: ") {
			t.Errorf("%q does not start with %q", err, "velox: ")
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
