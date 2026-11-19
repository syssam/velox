package gqlgraph_test

import (
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
)

type postOrderField string

// TestOrderField: an order field's value is one of the Type's Order names,
// and any other value fails as an input error that lists them.
func TestOrderField(t *testing.T) {
	c := &gqlgraph.Type{Name: "Post", Order: []gqlgraph.Column{{Name: "TITLE"}, {Name: "VIEWS"}, {Name: "AUTHOR_NAME", Edge: 1}}}
	if !c.IsOrderField("AUTHOR_NAME") || c.IsOrderField("title") || c.IsOrderField("") {
		t.Error("IsOrderField")
	}
	var o postOrderField
	if err := gqlgraph.UnmarshalOrderField(&o, "VIEWS", c); err != nil || o != "VIEWS" {
		t.Fatalf("VIEWS: %q, %v", o, err)
	}
	for _, v := range []any{"views", 3, nil} {
		err := gqlgraph.UnmarshalOrderField(&o, v, c)
		want := "veloxgql: PostOrderField must be TITLE, VIEWS or AUTHOR_NAME, not "
		if err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%v: %v", v, err)
		}
		if o != "VIEWS" {
			t.Errorf("%v changed o to %q", v, o)
		}
	}
	one := &gqlgraph.Type{Name: "Tag", Order: []gqlgraph.Column{{Name: "NAME"}}}
	if err := gqlgraph.UnmarshalOrderField(&o, "X", one); err == nil || err.Error() != "veloxgql: TagOrderField must be NAME, not X" {
		t.Errorf("one value: %v", err)
	}
}
