package ddl

import (
	"testing"

	"github.com/syssam/velox"
)

var dialects = [...]velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite}

func TestLiteral(t *testing.T) {
	for _, c := range []struct {
		d       velox.Dialect
		in, out string
	}{
		{velox.Postgres, `it's a \ test`, `'it''s a \ test'`},
		{velox.SQLite, `it's a \ test`, `'it''s a \ test'`},
		{velox.MySQL, `it's a \ test`, `'it''s a \\ test'`},
		{velox.Postgres, "", "''"},
		{velox.MySQL, "o'neil", "'o''neil'"},
	} {
		if got := Literal(c.d, c.in); got != c.out {
			t.Errorf("%v: Literal(%q) = %s, want %s", c.d, c.in, got, c.out)
		}
		if got, ok := Unquote(c.d, c.out); !ok || got != c.in {
			t.Errorf("%v: Unquote(%s) = %q %v, want %q", c.d, c.out, got, ok, c.in)
		}
	}
	for _, c := range []struct {
		d   velox.Dialect
		lit string
	}{
		{velox.Postgres, ""},
		{velox.Postgres, "'"},
		{velox.Postgres, "abc"},
		{velox.Postgres, "'a'b'"},
		{velox.Postgres, "'a'::text"},
		{velox.MySQL, `'a\b'`},
		{velox.MySQL, `'a\'`},
	} {
		if s, ok := Unquote(c.d, c.lit); ok {
			t.Errorf("%v: Unquote(%s) = %q, true; want false", c.d, c.lit, s)
		}
	}
}

// Every string goes through Literal and back.
func FuzzLiteral(f *testing.F) {
	for _, s := range []string{"", "a", "'", "''", `\`, `\'`, `it's a \ test`, "é\x00"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, d := range dialects {
			lit := Literal(d, s)
			if got, ok := Unquote(d, lit); !ok || got != s {
				t.Errorf("%v: Unquote(Literal(%q)) = %q %v", d, s, got, ok)
			}
		}
	})
}

func TestIdent(t *testing.T) {
	for _, c := range []struct {
		d       velox.Dialect
		in, out string
	}{
		{velox.Postgres, `role`, `"role"`},
		{velox.SQLite, `a"b`, `"a""b"`},
		{velox.MySQL, "a`b", "`a``b`"},
	} {
		if got := Ident(c.d, c.in); got != c.out {
			t.Errorf("%v: Ident(%q) = %s, want %s", c.d, c.in, got, c.out)
		}
	}
}
