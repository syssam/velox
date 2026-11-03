package gqlimpl

import (
	"io"
	"unicode/utf8"
)

const hex = "0123456789abcdef"

// WriteString writes s to w as a JSON string. For generated code. A
// generated enum's MarshalGQL writes a value of the enum as a literal, and
// any other value with WriteString. As encoding/json does, it escapes '"',
// '\' and the control characters, and writes each byte of invalid UTF-8 as
// U+FFFD. It escapes '<', '>', '&', U+2028 and U+2029 too. A write error is
// dropped, as gqlgen's marshalers drop it.
func WriteString(w io.Writer, s string) {
	b := make([]byte, 0, len(s)+2)
	b = append(b, '"')
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			switch {
			case c == '"' || c == '\\':
				b = append(b, '\\', c)
			case c == '\n':
				b = append(b, '\\', 'n')
			case c == '\r':
				b = append(b, '\\', 'r')
			case c == '\t':
				b = append(b, '\\', 't')
			case c < 0x20 || c == '<' || c == '>' || c == '&':
				b = append(b, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xf])
			default:
				b = append(b, c)
			}
			i++
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			b = append(b, `�`...)
		case r == ' ' || r == ' ':
			b = append(b, '\\', 'u', '2', '0', '2', hex[r&0xf])
		default:
			b = append(b, s[i:i+n]...)
		}
		i += n
	}
	b = append(b, '"')
	_, _ = w.Write(b)
}
