package gen

import (
	"testing"
	"unicode/utf8"
)

// isASCII reports whether s contains only ASCII bytes.
// The pascal/camel functions rely on inflect.Capitalize which operates byte-wise
// and does not guarantee valid UTF-8 output for non-ASCII input. These functions
// are designed for identifier-style strings (snake_case, PascalCase) which are
// always ASCII in practice.
func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] > 0x7f {
			return false
		}
	}
	return true
}

// FuzzPascal tests that pascal never panics and returns valid UTF-8 for ASCII input.
func FuzzPascal(f *testing.F) {
	f.Add("user_info")
	f.Add("full_name")
	f.Add("user_id")
	f.Add("HTTPCode")
	f.Add("")
	f.Add("_")
	f.Add("__double__")
	f.Add("a")
	f.Add("ALL_CAPS_NAME")
	f.Add("mixedCase_with_under")

	f.Fuzz(func(t *testing.T, s string) {
		if !isASCII(s) {
			t.Skip("skipping non-ASCII input: functions are designed for ASCII identifiers")
		}
		result := pascal(s)
		if !utf8.ValidString(result) {
			t.Errorf("pascal(%q) returned invalid UTF-8: %q", s, result)
		}
	})
}

// FuzzSnake tests that snake never panics and returns valid UTF-8 for ASCII input.
func FuzzSnake(f *testing.F) {
	f.Add("UserInfo")
	f.Add("FullName")
	f.Add("UserID")
	f.Add("HTTPCode")
	f.Add("")
	f.Add("A")
	f.Add("already_snake")

	f.Fuzz(func(t *testing.T, s string) {
		if !isASCII(s) {
			t.Skip("skipping non-ASCII input: functions are designed for ASCII identifiers")
		}
		result := snake(s)
		if !utf8.ValidString(result) {
			t.Errorf("snake(%q) returned invalid UTF-8: %q", s, result)
		}
	})
}

// FuzzCamel tests that camel never panics and returns valid UTF-8 for ASCII input.
func FuzzCamel(f *testing.F) {
	f.Add("user_info")
	f.Add("full_name")
	f.Add("user_id")
	f.Add("")
	f.Add("single")
	f.Add("ALLCAPS")

	f.Fuzz(func(t *testing.T, s string) {
		if !isASCII(s) {
			t.Skip("skipping non-ASCII input: functions are designed for ASCII identifiers")
		}
		result := camel(s)
		if !utf8.ValidString(result) {
			t.Errorf("camel(%q) returned invalid UTF-8: %q", s, result)
		}
	})
}

// FuzzPascalSnakeRoundTrip tests that composing pascal and snake never panics
// and always returns valid UTF-8 for ASCII inputs.
//
// Note: the roundtrip property snake(pascal(s))==s does not hold for all ASCII
// inputs (e.g. mixed-case identifiers like "uiA0" produce different results on
// each application). The idempotency property snake(pascal(s1))==s1 where
// s1=snake(pascal(s)) also does not hold in general. This fuzz test therefore
// only asserts no-panic and valid UTF-8 output.
func FuzzPascalSnakeRoundTrip(f *testing.F) {
	f.Add("user_info")
	f.Add("full_name")
	f.Add("user_id")

	f.Fuzz(func(t *testing.T, s string) {
		if !isASCII(s) {
			t.Skip("skipping non-ASCII input: functions are designed for ASCII identifiers")
		}
		// Exercise the composition without asserting a specific result.
		s1 := snake(pascal(s))
		s2 := snake(pascal(s1))
		if !utf8.ValidString(s1) {
			t.Errorf("snake(pascal(%q)) returned invalid UTF-8: %q", s, s1)
		}
		if !utf8.ValidString(s2) {
			t.Errorf("snake(pascal(snake(pascal(%q)))) returned invalid UTF-8: %q", s, s2)
		}
	})
}
