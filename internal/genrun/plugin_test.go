package genrun

import "testing"

func TestOneLine(t *testing.T) {
	for in, want := range map[string]string{
		"plain text, ünïcode":      "plain text, ünïcode",
		"a\nb\r\tc":                `a\nb\r\tc`,
		"\x1b[2J":                  `\x1b[2J`,
		"x\u2028y\u2029z":          `x\u2028y\u2029z`,
		"user\u202etxt.go":         `user\u202etxt.go`,
		"\u2066\u2069\u200e\u061c": `\u2066\u2069\u200e\u061c`,
	} {
		if got := oneLine(in); got != want {
			t.Errorf("oneLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// oneLines keeps tabs, and escapes the rest as oneLine does, line by line.
func TestOneLines(t *testing.T) {
	for in, want := range map[string]string{
		"main.main()\n\t/x/main.go:3 +0x1d\r\n": "main.main()\n\t/x/main.go:3 +0x1d\n",
		"a\rb\x1b[2J\u202e\u2028":               "a\\rb\\x1b[2J\\u202e\\u2028",
	} {
		if got := oneLines(in); got != want {
			t.Errorf("oneLines(%q) = %q, want %q", in, got, want)
		}
	}
}
