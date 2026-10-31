package codegen

import "testing"

func TestSuggest(t *testing.T) {
	cases := []struct {
		name  string
		names []string
		want  string
	}{
		{"nmae", []string{"name", "email"}, `; did you mean "name"?`},
		{"x", []string{"name"}, ""},
		{"post", []string{"posts", "post_tags"}, `; did you mean "posts"?`},
		{"abc", nil, ""},
		{"ab", []string{"abc"}, `; did you mean "abc"?`},
		{"ab", []string{"abcd"}, ""},                               // a distance of 2 is not less than len("ab")
		{"tag", []string{"tags", "tab"}, `; did you mean "tags"?`}, // a tie: the first
	}
	for _, c := range cases {
		if got := suggest(c.name, c.names); got != c.want {
			t.Errorf("suggest(%q, %q) = %q, want %q", c.name, c.names, got, c.want)
		}
	}
}
