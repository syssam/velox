package gqlimpl_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

// TestWriteString: WriteString writes what encoding/json writes for a
// string, and that is valid JSON, for control characters, invalid UTF-8 and
// the characters encoding/json escapes for HTML.
func TestWriteString(t *testing.T) {
	for _, s := range []string{"", "draft", `a"b\c`, "\x00\x01\x1f\n\r\t", "<&>", "  ", "é日本", "\xff\xfe", "a\xc3", "\x7f"} {
		var b strings.Builder
		gqlimpl.WriteString(&b, s)
		want, _ := json.Marshal(s)
		if b.String() != string(want) {
			t.Errorf("WriteString(%q) = %s, want %s", s, b.String(), want)
		}
		if !json.Valid([]byte(b.String())) {
			t.Errorf("WriteString(%q) = %s: not JSON", s, b.String())
		}
	}
}
