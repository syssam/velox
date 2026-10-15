package velox

import (
	"strings"
	"testing"
)

func TestEncode(t *testing.T) {
	for in, want := range map[string]string{
		"AZaz09-._~": "AZaz09-._~",
		"a b/c:d'e*": "a%20b%2Fc%3Ad%27e%2A",
		"":           "",
		"é\x00\xff":  "%C3%A9%00%FF",
	} {
		if got := encode(in); got != want {
			t.Errorf("encode(%q) = %q, want %q", in, got, want)
		}
	}
}

// commentFor renders c's comment for a statement with caller and tags.
func commentFor(c *commenter, caller Caller, tags ...Tag) string {
	return c.text(&Statement{Caller: caller, Tags: tags})
}

// TestCommentKeysAreUnambiguous is Review Focus 2: what naive concatenation
// would confuse gets different comments, from the cache as on first render.
func TestCommentKeysAreUnambiguous(t *testing.T) {
	c := newCommenter("app", []string{"a", "b"})
	for _, p := range []struct {
		name             string
		caller1, caller2 Caller
		tags1, tags2     []Tag
		want1, want2     string
	}{
		{
			name:    "call sites",
			caller1: Caller{File: "a.go", Line: 12}, caller2: Caller{File: "a.go1", Line: 2},
			want1: ` /*app='app',caller='a.go%3A12'*/`, want2: ` /*app='app',caller='a.go1%3A2'*/`,
		},
		{
			name:  "tag values",
			tags1: []Tag{{"a", "a"}, {"b", "b\x00c"}}, tags2: []Tag{{"a", "a\x00b"}, {"b", "c"}},
			want1: ` /*app='app',a='a',b='b%00c'*/`, want2: ` /*app='app',a='a%00b',b='c'*/`,
		},
		{
			name:  "unset and empty",
			tags1: nil, tags2: []Tag{{"a", ""}},
			want1: ` /*app='app'*/`, want2: ` /*app='app',a=''*/`,
		},
	} {
		for range 2 { // rendered, then cached
			got1, got2 := commentFor(c, p.caller1, p.tags1...), commentFor(c, p.caller2, p.tags2...)
			if got1 != p.want1 || got2 != p.want2 {
				t.Errorf("%s: %q and %q, want %q and %q", p.name, got1, got2, p.want1, p.want2)
			}
		}
	}
}

func TestCommentCacheEvictsTheOldest(t *testing.T) {
	defer func(n int) { maxComments = n }(maxComments)
	maxComments = 3
	c := newCommenter("app", []string{"route"})
	for i := range 10 {
		v := string(rune('a' + i))
		if got, want := commentFor(c, Caller{}, Tag{"route", v}), ` /*app='app',route='`+v+`'*/`; got != want {
			t.Errorf("%s: %q, want %q", v, got, want)
		}
	}
	if len(c.m) != 3 || len(c.order) != 3 {
		t.Fatalf("the cache holds %d entries in m and %d in order, want 3", len(c.m), len(c.order))
	}
	for _, v := range []string{"h", "i", "j"} {
		if !strings.Contains(c.m[commentKey(c, v)], v) {
			t.Errorf("the cache lacks route=%s", v)
		}
	}
	if _, ok := c.m[commentKey(c, "a")]; ok {
		t.Error("the cache kept the oldest entry")
	}
}

// commentKey returns c's cache key for a statement with no caller and the
// one tag route=v.
func commentKey(c *commenter, v string) string {
	var buf [256]byte
	return string(c.key(buf[:0], &Statement{Tags: []Tag{{"route", v}}}))
}

func TestCommentCacheBoundedInBytes(t *testing.T) {
	defer func(n int) { maxCommentBytes = n }(maxCommentBytes)
	maxCommentBytes = 300
	c := newCommenter("app", []string{"route"})
	for i := range 50 {
		v := strings.Repeat(string(rune('a'+i%26)), 18) + string(rune('a'+i/26)) + "z"
		if got, want := commentFor(c, Caller{}, Tag{"route", v}), ` /*app='app',route='`+v+`'*/`; got != want {
			t.Errorf("%q, want %q", got, want)
		}
		if c.bytes > maxCommentBytes {
			t.Fatalf("the cache holds %d bytes, over its bound %d", c.bytes, maxCommentBytes)
		}
	}
	held := len(c.m)
	big := strings.Repeat("x", 400)
	if got, want := commentFor(c, Caller{}, Tag{"route", big}), ` /*app='app',route='`+big+`'*/`; got != want {
		t.Errorf("the 400-byte value rendered %q", got)
	}
	if _, ok := c.m[commentKey(c, big)]; ok || len(c.m) != held {
		t.Errorf("the 400-byte value was cached: %d entries, %d before", len(c.m), held)
	}
}
