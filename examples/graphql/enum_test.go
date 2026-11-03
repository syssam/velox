package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/examples/graphql/gen"
)

// TestEnumUnknownValue: an enum's marshalers turn a known value into its
// GraphQL name and back. A value the enum lacks is written as valid JSON.
// Reading one is BAD_USER_INPUT that names it, a non-string too.
func TestEnumUnknownValue(t *testing.T) {
	var b strings.Builder
	gen.PostStatusPublished.MarshalGQL(&b)
	if b.String() != `"PUBLISHED"` {
		t.Errorf("PUBLISHED is written %s", b.String())
	}
	var s gen.PostStatus
	if err := s.UnmarshalGQL("DRAFT"); err != nil || s != gen.PostStatusDraft {
		t.Errorf("DRAFT reads as %q, %v", s, err)
	}
	b.Reset()
	gen.PostStatus("ar\"chived\x00").MarshalGQL(&b)
	if want, _ := json.Marshal("ar\"chived\x00"); b.String() != string(want) || !json.Valid([]byte(b.String())) {
		t.Errorf("an unknown status is written %s, want %s", b.String(), want)
	}
	b.Reset()
	gen.PostOrderField("NOPE\x01").MarshalGQL(&b)
	if b.String() != `"NOPE\u0001"` {
		t.Errorf("an unknown order field is written %s", b.String())
	}
	for _, c := range []struct {
		err  error
		want string
	}{
		{s.UnmarshalGQL("ARCHIVED"), "veloxgql: PostStatus must be DRAFT or PUBLISHED, not ARCHIVED"},
		{s.UnmarshalGQL(7), "veloxgql: PostStatus must be DRAFT or PUBLISHED, not 7"},
		{new(gen.PostOrderField).UnmarshalGQL("NOPE"), "veloxgql: PostOrderField must be TITLE or VIEWS, not NOPE"},
		{new(gen.PostOrderField).UnmarshalGQL(nil), "veloxgql: PostOrderField must be TITLE or VIEWS, not <nil>"},
	} {
		var coded interface{ Extensions() map[string]any }
		if c.err == nil || c.err.Error() != c.want || !errors.As(c.err, &coded) || coded.Extensions()["code"] != veloxgql.CodeBadUserInput {
			t.Errorf("got %v, want BAD_USER_INPUT %q", c.err, c.want)
		}
	}
	if s != gen.PostStatusDraft {
		t.Errorf("a failed read changed the status to %q", s)
	}
}
