package gqlgraph_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
)

// codeOf is the code err carries for gqlgenx.ErrorPresenter, "" for none.
func codeOf(err error) string {
	c, ok := errors.AsType[interface {
		error
		Extensions() map[string]any
	}](err)
	if !ok {
		return ""
	}
	s, _ := c.Extensions()["code"].(string)
	return s
}

// keys returns the keys 1 to n.
func keys(n int) []int64 {
	ks := make([]int64, n)
	for i := range ks {
		ks[i] = int64(i + 1)
	}
	return ks
}

// TestIDsCap: a list of keys is capped by MaxIDs, with BAD_USER_INPUT
// naming the input's field.
func TestIDsCap(t *testing.T) {
	const at = "UpdatePostInput.addTagIDs"
	if k, err := gqlgraph.IDs(at, keys(gqlgraph.MaxIDs)); err != nil || len(k) != gqlgraph.MaxIDs {
		t.Errorf("MaxIDs keys: %d keys, %v; want %d", len(k), err, gqlgraph.MaxIDs)
	}
	_, err := gqlgraph.IDs(at, keys(gqlgraph.MaxIDs+1))
	if !errors.Is(err, veloxgql.ErrPageTooLarge) {
		t.Errorf("MaxIDs+1 keys: %v, want ErrPageTooLarge", err)
	}
	if code := codeOf(err); code != veloxgql.CodeBadUserInput {
		t.Errorf("MaxIDs+1 keys: code %q, want BAD_USER_INPUT", code)
	}
	if err != nil && !strings.HasPrefix(err.Error(), "veloxgql: "+at+": ") {
		t.Errorf("%q does not start with the input field", err)
	}
}

// TestIDsRepeats: a repeated key is kept once, in the order of its first
// place; the keys are what a relation handle's Add takes, in their own Go
// type.
func TestIDsRepeats(t *testing.T) {
	k, err := gqlgraph.IDs("x", []int64{1, 2, 1})
	if err != nil || !slices.Equal(k, []any{int64(1), int64(2)}) {
		t.Errorf("IDs = %v, %v; want [1 2]", k, err)
	}
	type userID int64
	v, err := gqlgraph.IDs("x", []userID{2, 1, 2, 2})
	if err != nil || !slices.Equal(v, []any{userID(2), userID(1)}) {
		t.Errorf("IDs of userID = %v, %v; want [2 1]", v, err)
	}
	if k, err := gqlgraph.IDs[string]("x", nil); err != nil || len(k) != 0 {
		t.Errorf("IDs of none = %v, %v; want none", k, err)
	}
}

func TestFromJSON(t *testing.T) {
	type meta struct{ Tags []string }
	got, err := gqlgraph.FromJSON[meta]("CreatePostInput.meta", veloxgql.JSON(`{"Tags":["a"]}`))
	if err != nil || !slices.Equal(got.Tags, []string{"a"}) {
		t.Errorf("FromJSON = %v, %v", got, err)
	}
	_, err = gqlgraph.FromJSON[meta]("CreatePostInput.meta", veloxgql.JSON(`{"Tags":1}`))
	if codeOf(err) != veloxgql.CodeBadUserInput || !strings.HasPrefix(err.Error(), "veloxgql: CreatePostInput.meta: ") {
		t.Errorf("a JSON of another shape: %v, want BAD_USER_INPUT naming the field", err)
	}
	if strings.Contains(err.Error(), "gqlgraph_test") || strings.Contains(err.Error(), "meta.Tags") {
		t.Errorf("%q names a Go type", err)
	}
}

// TestShow: what an input's String prints for a pointer field.
func TestShow(t *testing.T) {
	s, j := "x", veloxgql.JSON(`{"a":1}`)
	if gqlgraph.Show[string](nil) != nil || gqlgraph.Show(&s) != "x" {
		t.Errorf("Show = %v, %v", gqlgraph.Show[string](nil), gqlgraph.Show(&s))
	}
	if gqlgraph.ShowJSON(nil) != nil || gqlgraph.ShowJSON(&j) != `{"a":1}` {
		t.Errorf("ShowJSON = %v, %v", gqlgraph.ShowJSON(nil), gqlgraph.ShowJSON(&j))
	}
}
