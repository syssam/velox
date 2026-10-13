package ormgen

import (
	"bytes"
	"strings"
)

// docWidth is the column the doc comments of generated code are wrapped at.
const docWidth = 80

// reflow wraps the doc comments of a generated source at docWidth.
//
// A comment is written by a template with the names of a schema in it: the
// line that fits "the author edge of Post" does not fit "the
// approved_by_employee edge of PurchaseRequisitionLine", and a template
// wrapped by hand for one is ragged or too long for the other. So the
// templates say what a comment says, and where its lines break is decided
// here, once the names are in.
//
// Only prose is touched: the lines of a comment at the start of a line that
// begin with "// " and a letter, digit or quote. A directive (//go:noinline),
// an indented line (code, a table), a list item and an empty comment line
// are kept as they are, and end the paragraph before them. A paragraph that
// already fits is left alone, so a comment whose lines were broken on
// purpose and are short enough stays as written.
func reflow(src []byte) []byte {
	lines := strings.SplitAfter(string(src), "\n")
	var out bytes.Buffer
	out.Grow(len(src))
	for i := 0; i < len(lines); {
		if !prose(lines[i]) {
			out.WriteString(lines[i])
			i++
			continue
		}
		j, long := i, false
		for j < len(lines) && prose(lines[j]) {
			long = long || len(strings.TrimRight(lines[j], "\n")) > docWidth
			j++
		}
		if !long {
			for ; i < j; i++ {
				out.WriteString(lines[i])
			}
			continue
		}
		var words []string
		for ; i < j; i++ {
			words = append(words, strings.Fields(strings.TrimPrefix(lines[i], "// "))...)
		}
		n := 0
		for _, w := range words {
			switch {
			case n == 0:
				out.WriteString("// ")
				n = 3
			case n+1+len(w) > docWidth:
				out.WriteString("\n// ")
				n = 3
			default:
				out.WriteByte(' ')
				n++
			}
			out.WriteString(w)
			n += len(w)
		}
		out.WriteByte('\n')
	}
	return out.Bytes()
}

// prose reports whether a line is a line of running text of a comment that
// starts at the beginning of the line.
func prose(line string) bool {
	rest, ok := strings.CutPrefix(line, "// ")
	if !ok || rest == "" {
		return false
	}
	// The header of a generated file is one line whatever follows it.
	if strings.HasPrefix(line, header[:len(header)-1]) || strings.HasPrefix(rest, "Code generated ") {
		return false
	}
	switch c := rest[0]; {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '"', c == '(', c == '`':
	default:
		return false // indented, a list item, a rule
	}
	// A numbered list item, and a line that is all one symbol.
	if i := strings.IndexByte(rest, ' '); i > 0 && (strings.HasSuffix(rest[:i], ".") || strings.HasSuffix(rest[:i], ")")) && rest[0] >= '0' && rest[0] <= '9' {
		return false
	}
	return true
}
