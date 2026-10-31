package engine

import (
	"strings"
	"testing"
)

// unquote reverses quote: it strips the outer quote characters and
// halves the doubled ones; ok is false when s is not one quoted identifier.
func unquote(d Dialect, s string) (string, bool) {
	q := byte('"')
	if d == MySQL {
		q = '`'
	}
	if len(s) < 2 || s[0] != q || s[len(s)-1] != q {
		return "", false
	}
	var b strings.Builder
	in := s[1 : len(s)-1]
	for i := 0; i < len(in); i++ {
		if in[i] == q {
			if i+1 >= len(in) || in[i+1] != q {
				return "", false // a lone quote would end the identifier
			}
			i++
		}
		b.WriteByte(in[i])
	}
	return b.String(), true
}

func FuzzQuote(f *testing.F) {
	for _, s := range []string{"id", `a"b`, "a`b", `""`, "``", "", "x y", `\`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, ident string) {
		for _, d := range []Dialect{Postgres, MySQL, SQLite} {
			var b strings.Builder
			QuoteIdent(d, &b, ident)
			got, ok := unquote(d, b.String())
			if !ok || got != ident {
				t.Fatalf("%v: %q quoted as %s does not read back (%q, %v)", d, ident, b.String(), got, ok)
			}
		}
	})
}
