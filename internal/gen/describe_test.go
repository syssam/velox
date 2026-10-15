package gen

import (
	"bytes"
	"strings"
	"testing"
)

func TestDescribeText(t *testing.T) {
	var b bytes.Buffer
	if err := Describe(&b, mustBuild(t, User{}, Post{}, Group{}), false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"User (users)", "org_id", "tenant", "O2M → Post", "M2M → Group", "user_groups(user_id, group_id)", "pointer"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q in\n%s", want, b.String())
		}
	}
}

func TestDescribeDeterministic(t *testing.T) {
	var a, b bytes.Buffer
	_ = Describe(&a, mustBuild(t, User{}, Post{}, Group{}, Node{}), true)
	_ = Describe(&b, mustBuild(t, Node{}, Group{}, Post{}, User{}), true)
	if a.String() != b.String() || !strings.HasPrefix(a.String(), "{") {
		t.Fatal("describe -json depends on the order of the schemas")
	}
}
