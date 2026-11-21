package gqlgenx_test

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
)

// TestInt64IDMarshal: an int64 key is written as a JSON string of its
// decimal digits, as the GraphQL specification serializes an ID.
func TestInt64IDMarshal(t *testing.T) {
	for _, c := range []struct {
		key  int64
		want string
	}{
		{1, `"1"`}, {0, `"0"`}, {-42, `"-42"`},
		{math.MaxInt64, `"9223372036854775807"`}, {math.MinInt64, `"-9223372036854775808"`},
	} {
		var b strings.Builder
		gqlgenx.MarshalInt64ID(c.key).MarshalGQL(&b)
		if b.String() != c.want {
			t.Errorf("MarshalInt64ID(%d) = %s, want %s", c.key, b.String(), c.want)
		}
	}
}

// TestInt64IDUnmarshal: a string of decimal digits or an integer is a key;
// anything else is BAD_USER_INPUT, whose text does not echo the input.
func TestInt64IDUnmarshal(t *testing.T) {
	for _, c := range []struct {
		in   any
		want int64
	}{
		{"7", 7}, {"-7", -7}, {"9223372036854775807", math.MaxInt64},
		{json.Number("8"), 8}, {int64(9), 9}, {10, 10}, {int32(11), 11},
	} {
		got, err := gqlgenx.UnmarshalInt64ID(c.in)
		if err != nil || got != c.want {
			t.Errorf("UnmarshalInt64ID(%#v) = %d, %v, want %d", c.in, got, err, c.want)
		}
	}
	for _, in := range []any{
		"", "x7", "1.5", "9223372036854775808", "VXNlcjo0Mg", json.Number("1.5"),
		1.5, true, nil, []any{"1"}, map[string]any{"id": "1"},
	} {
		_, err := gqlgenx.UnmarshalInt64ID(in)
		if err == nil {
			t.Errorf("UnmarshalInt64ID(%#v): no error", in)
			continue
		}
		if code := codeOf(err); code != veloxgql.CodeBadUserInput {
			t.Errorf("UnmarshalInt64ID(%#v): code %q, want BAD_USER_INPUT", in, code)
		}
		if s, ok := in.(string); ok && s != "" && strings.Contains(err.Error(), s) {
			t.Errorf("UnmarshalInt64ID(%q): the error %q echoes the input", s, err)
		}
	}
}
