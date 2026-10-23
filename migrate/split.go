package migrate

import (
	"strconv"
	"strings"

	"github.com/syssam/velox2"
)

// A piece is a statement or a directive line, in the order of the file.
type piece struct {
	SQL       string // a statement, from its first token to its last, without ";"; "" for a directive
	Directive string // a directive line's text after "-- velox:"; "" for a statement
	Line      int    // its first line, from 1
}

// directivePrefix starts a directive line.
const directivePrefix = "-- velox:"

// split reads src as d reads SQL: ";" ends a statement outside quotes,
// comments and dollar quotes. Comments before a statement's first token and
// after its last are not part of it. A "-- velox:" line between statements
// is a directive; one inside a statement is an error.
func split(d velox.Dialect, src string) ([]piece, error) {
	s := splitter{d: d, src: src}
	if err := s.run(); err != nil {
		return nil, err
	}
	return s.out, nil
}

// splitter holds the state of one split.
type splitter struct {
	d         velox.Dialect
	src       string
	out       []piece
	backslash bool // MySQL: a string literal holds a backslash
}

// run splits s.src into s.out. Its errors are *Error values with Code "file"
// and the line in Pos.
func (s *splitter) run() error {
	src := s.src
	line := 1
	start, end, startLine := -1, -1, 0 // the statement's first token, the end of its last, and its line
	flush := func() {
		if start >= 0 {
			s.out = append(s.out, piece{SQL: src[start:end], Line: startLine})
		}
		start, end = -1, -1
	}
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '\n':
			line++
			i++
			continue
		case space(c):
			i++
			continue
		case c == ';':
			flush()
			i++
			continue
		case s.lineComment(i):
			j := strings.IndexByte(src[i:], '\n')
			if j < 0 {
				j = len(src)
			} else {
				j += i
			}
			if text, ok := strings.CutPrefix(src[i:j], directivePrefix); ok {
				if start >= 0 {
					return fileErr(line, "a directive inside a statement")
				}
				s.out = append(s.out, piece{Directive: strings.TrimRight(text, " \t\r\f\v"), Line: line})
			}
			i = j
			continue
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			j := s.commentEnd(i)
			if j < 0 {
				return fileErr(line, "unterminated comment")
			}
			line += strings.Count(src[i:j], "\n")
			i = j
			continue
		}
		j, reason := s.token(i)
		if reason != "" {
			return fileErr(line, reason)
		}
		if start < 0 {
			start, startLine = i, line
		}
		line += strings.Count(src[i:j], "\n")
		i, end = j, j
	}
	flush()
	return nil
}

// fileErr is the splitter's refusal at line.
func fileErr(line int, reason string) *Error {
	return &Error{Code: "file", Pos: strconv.Itoa(line), Reason: reason}
}

// space reports whether c is white space between tokens.
func space(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

// identByte reports whether c continues an identifier: PostgreSQL's take "$".
func identByte(c byte) bool { return wordByte(c) || c == '$' }

// lineComment reports whether a comment to the end of the line starts at i:
// "--", which on MySQL needs white space or the end after it, or MySQL's "#".
func (s *splitter) lineComment(i int) bool {
	src := s.src
	switch {
	case src[i] == '#':
		return s.d == velox.MySQL
	case src[i] != '-' || i+1 >= len(src) || src[i+1] != '-':
		return false
	case s.d == velox.MySQL:
		return i+2 == len(src) || space(src[i+2])
	}
	return true
}

// commentEnd is the index after the block comment at i, or -1 when it is
// not closed. PostgreSQL's nest.
func (s *splitter) commentEnd(i int) int {
	src := s.src
	depth := 0
	for j := i; j+1 < len(src); {
		switch {
		case src[j] == '/' && src[j+1] == '*':
			depth++
			j += 2
			if s.d != velox.Postgres && depth > 1 {
				depth = 1
			}
		case src[j] == '*' && src[j+1] == '/':
			depth--
			j += 2
			if depth == 0 {
				return j
			}
		default:
			j++
		}
	}
	return -1
}

// token is the index after the token at i: a quoted literal or name, a
// dollar-quoted string, or a single byte. A reason is given when a quote is
// not closed.
func (s *splitter) token(i int) (int, string) {
	src := s.src
	c := src[i]
	prev := byte(' ')
	if i > 0 {
		prev = src[i-1]
	}
	switch {
	case c == '\'':
		return s.quoted(i, s.d == velox.MySQL, "unterminated string literal")
	case c == '"' && s.d == velox.MySQL:
		return s.quoted(i, true, "unterminated string literal")
	case c == '"':
		return s.quoted(i, false, "unterminated quoted name")
	case c == '`' && s.d != velox.Postgres:
		return s.quoted(i, false, "unterminated quoted name")
	case c == '[' && s.d == velox.SQLite:
		if j := strings.IndexByte(src[i+1:], ']'); j >= 0 {
			return i + 1 + j + 1, ""
		}
		return 0, "unterminated quoted name"
	case s.d == velox.Postgres && (c == 'E' || c == 'e') && i+1 < len(src) && src[i+1] == '\'' && !identByte(prev):
		j, reason := s.quoted(i+1, true, "unterminated string literal")
		return j, reason
	case s.d == velox.Postgres && c == '$' && !identByte(prev):
		if tag := dollarTag(src, i); tag != "" {
			if k := strings.Index(src[i+len(tag):], tag); k >= 0 {
				return i + len(tag) + k + len(tag), ""
			}
			return 0, "unterminated dollar-quoted string"
		}
	}
	return i + 1, ""
}

// quoted is the index after the quoted text that starts with the quote at
// i, in which the quote doubled, and with backslash a backslash, escapes the
// next byte. On MySQL it notes a string literal that holds a backslash.
func (s *splitter) quoted(i int, backslash bool, unterminated string) (int, string) {
	src, q := s.src, s.src[i]
	for j := i + 1; j < len(src); j++ {
		switch src[j] {
		case '\\':
			if backslash {
				if s.d == velox.MySQL {
					s.backslash = true
				}
				j++
			}
		case q:
			if j+1 < len(src) && src[j+1] == q {
				j++
				continue
			}
			return j + 1, ""
		}
	}
	return 0, unterminated
}

// dollarTag is the $tag$ that opens a PostgreSQL dollar-quoted string at
// src[i], or "" when there is none.
func dollarTag(src string, i int) string {
	for j := i + 1; j < len(src); j++ {
		switch c := src[j]; {
		case c == '$':
			return src[i : j+1]
		case !wordByte(c), j == i+1 && c >= '0' && c <= '9':
			return ""
		}
	}
	return ""
}
