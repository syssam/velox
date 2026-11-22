package migrate

import (
	"slices"
	"strconv"
	"strings"

	"github.com/syssam/velox"
)

// A piece is a statement or a directive line, in the order of the file.
type piece struct {
	SQL       string // a statement, from its first token to its last, without ";"; "" for a directive
	Directive string // a directive line's text after "-- velox:"; "" for a statement
	Line      int    // its first line, from 1
	Block     bool   // a statement between -- velox:statement begin and end lines, which is not split
}

// directivePrefix starts a directive line.
const directivePrefix = "-- velox:"

// split reads src as d reads SQL: ";" ends a statement outside quotes,
// comments and dollar quotes. Comments before a statement's first token and
// after its last are not part of it; MySQL's executable comments, "/*!",
// are statement text. A "-- velox:" line between statements is a directive;
// one inside a statement, or not at the start of its line, is an error. The
// text between "-- velox:statement begin" and "-- velox:statement end" lines
// is one statement, which is not split.
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
	dquote    bool // MySQL: a "…" literal
}

// run splits s.src into s.out. Its errors are *Error values with Code "file"
// and the line in Pos.
func (s *splitter) run() error {
	src := s.src
	line := 1
	start, end, startLine := -1, -1, 0 // the statement's first token, the end of its last, and its line
	flush := func() error {
		if start >= 0 {
			sql := src[start:end]
			if reason := s.refusal(sql); reason != "" {
				return fileErr(startLine, reason)
			}
			s.out = append(s.out, piece{SQL: sql, Line: startLine})
		}
		start, end = -1, -1
		return nil
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
			if err := flush(); err != nil {
				return err
			}
			i++
			continue
		case s.lineComment(i) && start < 0 && s.directiveAt(i) == blockBegin:
			j, lines, err := s.block(i, line)
			if err != nil {
				return err
			}
			line += lines
			i = j
			continue
		case s.lineComment(i):
			j, err := s.skipLineComment(i, line, start >= 0)
			if err != nil {
				return err
			}
			i = j
			continue
		case strings.HasPrefix(src[i:], "/*") && !s.executable(i):
			j := s.commentEnd(i)
			if j < 0 {
				return fileErr(line, "unterminated comment")
			}
			line += strings.Count(src[i:j], "\n")
			i = j
			continue
		}
		if c == '\\' && s.d == velox.Postgres && (start < 0 || lineStart(src, i)) {
			// A literal, a quoted name or a comment was skipped above or in
			// token, so this backslash is a psql meta-command's: one that
			// starts its line or, after a ";" on the same line, a statement.
			return s.metaCommand(i, line)
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
	return flush()
}

// skipLineComment returns the index after the line comment at s.src[i],
// on line line, and adds it to s.out when it is a directive; inStmt
// reports that a statement has begun, where a directive is an error.
func (s *splitter) skipLineComment(i, line int, inStmt bool) (int, error) {
	src := s.src
	j := strings.IndexByte(src[i:], '\n')
	if j < 0 {
		j = len(src)
	} else {
		j += i
	}
	text, ok := strings.CutPrefix(src[i:j], directivePrefix)
	if !ok {
		return j, nil
	}
	if !lineStart(src, i) {
		return 0, fileErr(line, "a directive must start its line")
	}
	if inStmt {
		return 0, fileErr(line, "a directive inside a statement")
	}
	if strings.TrimRight(text, " \t\r\f\v") == blockEnd {
		return 0, fileErr(line, "-- velox:statement end with no -- velox:statement begin before it")
	}
	s.out = append(s.out, piece{Directive: strings.TrimRight(text, " \t\r\f\v"), Line: line})
	return j, nil
}

// The directives that mark a statement the split does not read: the text
// between their lines is one statement, run as it is.
const (
	blockBegin = "statement begin"
	blockEnd   = "statement end"
)

// directiveAt is the directive of the line comment at s.src[i] that starts
// its line, its text after "-- velox:" without trailing white space; "" for
// any other comment.
func (s *splitter) directiveAt(i int) string {
	if !lineStart(s.src, i) {
		return ""
	}
	j := strings.IndexByte(s.src[i:], '\n')
	if j < 0 {
		j = len(s.src) - i
	}
	text, ok := strings.CutPrefix(s.src[i:i+j], directivePrefix)
	if !ok {
		return ""
	}
	return strings.TrimRight(text, " \t\r\f\v")
}

// block reads the statement marked by the -- velox:statement begin line at
// s.src[i], on line line, to the -- velox:statement end line, and adds it
// to s.out: the text between them from its first token, without a last
// ";" and the white space around it. It is not split or refused, so a
// trigger's or routine's body keeps its ";". It returns the index after the
// end line and the lines it read. Another directive before the end line is
// an error, as is no end line or nothing between.
func (s *splitter) block(i, line int) (int, int, error) {
	src := s.src
	from := strings.IndexByte(src[i:], '\n')
	if from < 0 {
		return 0, 0, fileErr(line, "-- velox:statement begin with no -- velox:statement end after it")
	}
	from += i + 1
	n := 1 // lines read, the begin line's
	for at := from; at < len(src); {
		eol := strings.IndexByte(src[at:], '\n')
		next := len(src)
		if eol >= 0 {
			next = at + eol + 1
		}
		k := at
		for k < next && space(src[k]) && src[k] != '\n' {
			k++
		}
		if d := s.directiveAt(k); strings.HasPrefix(src[k:], directivePrefix) {
			if d != blockEnd {
				return 0, 0, fileErr(line+n, "a directive inside a -- velox:statement block; end the block first")
			}
			body, first := blockBody(src[from:at])
			if body == "" {
				return 0, 0, fileErr(line, "the -- velox:statement block holds no statement")
			}
			s.scanLiterals(body)
			s.out = append(s.out, piece{SQL: body, Line: line + 1 + first, Block: true})
			return next, n + strings.Count(src[at:next], "\n"), nil
		}
		n += strings.Count(src[at:next], "\n")
		at = next
	}
	return 0, 0, fileErr(line, "-- velox:statement begin with no -- velox:statement end after it")
}

// blockBody is the statement of a block's text: from its first token, which
// skips white space and line comments, to its last, without a last ";". It
// returns the lines before the first token too.
func blockBody(text string) (string, int) {
	lines := 0
	for {
		t := strings.TrimLeft(text, " \t\r\f\v")
		switch {
		case strings.HasPrefix(t, "\n"):
			text, lines = t[1:], lines+1
			continue
		case strings.HasPrefix(t, "--"):
			j := strings.IndexByte(t, '\n')
			if j < 0 {
				return "", lines
			}
			text, lines = t[j+1:], lines+1
			continue
		}
		text = t
		break
	}
	text = strings.TrimRightFunc(text, func(r rune) bool { return r < 128 && space(byte(r)) })
	text = strings.TrimSuffix(text, ";")
	return strings.TrimRightFunc(text, func(r rune) bool { return r < 128 && space(byte(r)) }), lines
}

// scanLiterals reads body's literals as the split reads a statement's, for
// what MySQL's modes need to know of them: a backslash, a "…" literal.
func (s *splitter) scanLiterals(body string) {
	sub := splitter{d: s.d, src: body}
	for i := 0; i < len(body); {
		switch {
		case sub.lineComment(i):
			j := strings.IndexByte(body[i:], '\n')
			if j < 0 {
				i = len(body)
			} else {
				i += j
			}
		case strings.HasPrefix(body[i:], "/*") && !sub.executable(i):
			if j := sub.commentEnd(i); j > 0 {
				i = j
			} else {
				i = len(body)
			}
		default:
			j, reason := sub.token(i)
			if reason != "" {
				i = len(body)
			} else {
				i = j
			}
		}
	}
	s.backslash = s.backslash || sub.backslash
	s.dquote = s.dquote || sub.dquote
}

// metaCommand is the error for the psql meta-command whose backslash is
// s.src[i], on line line.
func (s *splitter) metaCommand(i, line int) error {
	j := i + 1
	for j < len(s.src) && wordByte(s.src[j]) {
		j++
	}
	return fileErr(line, "a psql meta-command ("+s.src[i:j]+") is not SQL; remove it")
}

// refusal is the reason a statement is refused, or "". On SQLite and MySQL it
// is a statement that starts a trigger, a MySQL routine or event, or a
// BEGIN … END block: their bodies hold ";", which the split cannot read as
// the database does, so they are refused rather than run in pieces. BEGIN
// alone, or with TRANSACTION or WORK (and SQLite's DEFERRED, IMMEDIATE or
// EXCLUSIVE), starts a transaction. On PostgreSQL it is a BEGIN ATOMIC body,
// whose ";" the split cannot tell from the end of the statement; the other
// bodies are dollar-quoted and split whole.
func (s *splitter) refusal(sql string) string {
	const body = "the split cannot read a trigger's or procedure's body; put the statement between -- velox:statement begin and -- velox:statement end lines"
	ws := lex(s.d, sql)
	if s.d == velox.Postgres {
		for i := 1; i < len(ws); i++ {
			if !ws[i-1].quoted && !ws[i].quoted && strings.EqualFold(ws[i-1].text, "BEGIN") && strings.EqualFold(ws[i].text, "ATOMIC") {
				return "the split cannot read a BEGIN ATOMIC body; write the body as a string, AS $$ … $$, or put the statement between -- velox:statement begin and -- velox:statement end lines"
			}
		}
		return ""
	}
	at := func(i int) string { // the i-th word upper-cased, "" when quoted
		if i < len(ws) && !ws[i].quoted {
			return strings.ToUpper(ws[i].text)
		}
		return ""
	}
	switch at(0) {
	case "BEGIN":
		i := 1
		if s.d == velox.SQLite && (at(1) == "DEFERRED" || at(1) == "IMMEDIATE" || at(1) == "EXCLUSIVE") {
			i = 2
		}
		if at(i) == "TRANSACTION" || at(i) == "WORK" {
			i++
		}
		if i < len(ws) {
			return body
		}
	case "CREATE":
		i := 1
		if s.d == velox.MySQL && at(1) == "DEFINER" {
			// DEFINER = user, a name, @ and a host or CURRENT_USER[()]: the
			// object's keyword is the first of these after it.
			for i = 2; i < len(ws) && !slices.Contains([]string{"TRIGGER", "PROCEDURE", "FUNCTION", "EVENT", "VIEW", "SQL"}, at(i)); i++ {
			}
		}
		if s.d == velox.SQLite && (at(i) == "TEMP" || at(i) == "TEMPORARY") {
			i++
		}
		switch at(i) {
		case "TRIGGER":
			return body
		case "PROCEDURE", "FUNCTION", "EVENT":
			if s.d == velox.MySQL {
				return body
			}
		}
	}
	return ""
}

// fileErr is the splitter's refusal at line.
func fileErr(line int, reason string) *Error {
	return &Error{Code: "file", Pos: strconv.Itoa(line), Reason: reason}
}

// lineStart reports whether only white space comes before src[i] on its line.
func lineStart(src string, i int) bool {
	for i > 0 && src[i-1] != '\n' {
		i--
		if !space(src[i]) {
			return false
		}
	}
	return true
}

// executable reports whether a MySQL executable comment, "/*!" or
// "/*!NNNNN", starts at i: MySQL runs its content as statement text.
func (s *splitter) executable(i int) bool {
	return s.d == velox.MySQL && strings.HasPrefix(s.src[i:], "/*!")
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
// dollar-quoted string, a MySQL executable comment, or a single byte. A
// reason is given when a quote is not closed.
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
		s.dquote = true
		return s.quoted(i, true, "unterminated string literal")
	case s.executable(i):
		if j := strings.Index(src[i+3:], "*/"); j >= 0 {
			return i + 3 + j + 2, ""
		}
		return 0, "unterminated comment"
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

// A word is a token of a statement outside literals and comments: a word as
// written, a quoted name unquoted, or a byte of punctuation.
type word struct {
	text       string
	quoted     bool // a quoted name
	start, end int  // its bytes in the statement
}

// lex is sql's words as d reads them. An unterminated quote or comment runs
// to the end.
func lex(d velox.Dialect, sql string) []word {
	s := splitter{d: d, src: sql}
	var out []word
	exec := false // inside a MySQL executable comment, whose text is the statement's
	end := func(j int, reason string) int {
		if reason != "" {
			return len(sql)
		}
		return j
	}
	for i := 0; i < len(sql); {
		c := sql[i]
		switch {
		case space(c):
			i++
		case s.lineComment(i):
			j := strings.IndexByte(sql[i:], '\n')
			if j < 0 {
				return out
			}
			i += j
		case exec && strings.HasPrefix(sql[i:], "*/"):
			exec = false
			i += 2
		case s.executable(i):
			exec = true
			for i += 3; i < len(sql) && sql[i] >= '0' && sql[i] <= '9'; i++ {
			}
		case strings.HasPrefix(sql[i:], "/*"):
			if i = s.commentEnd(i); i < 0 {
				return out
			}
		case c == '\'' || c == '"' && d == velox.MySQL:
			i = end(s.quoted(i, d == velox.MySQL, "unterminated"))
		case d == velox.Postgres && (c == 'E' || c == 'e') && i+1 < len(sql) && sql[i+1] == '\'' && (i == 0 || !identByte(sql[i-1])):
			i = end(s.quoted(i+1, true, "unterminated"))
		case d == velox.Postgres && c == '$' && (i == 0 || !identByte(sql[i-1])) && dollarTag(sql, i) != "":
			tag := dollarTag(sql, i)
			if k := strings.Index(sql[i+len(tag):], tag); k >= 0 {
				i += len(tag) + k + len(tag)
			} else {
				return out
			}
		case c == '"' || c == '`' && d != velox.Postgres || c == '[' && d == velox.SQLite:
			q := c
			if q == '[' {
				q = ']'
			}
			j := i + 1
			var b strings.Builder
			for ; j < len(sql); j++ {
				if sql[j] == q {
					if q != ']' && j+1 < len(sql) && sql[j+1] == q {
						b.WriteByte(q)
						j++
						continue
					}
					break
				}
				b.WriteByte(sql[j])
			}
			i, j = min(j+1, len(sql)), i
			out = append(out, word{text: b.String(), quoted: true, start: j, end: i})
		case wordByte(c):
			j := i + 1
			for j < len(sql) && (wordByte(sql[j]) || d == velox.Postgres && sql[j] == '$') {
				j++
			}
			out = append(out, word{text: sql[i:j], start: i, end: j})
			i = j
		default:
			out = append(out, word{text: sql[i : i+1], start: i, end: i + 1})
			i++
		}
	}
	return out
}

// words is sql's tokens outside literals and comments, keywords upper-cased,
// quoted names unquoted. Every word not quoted counts as a keyword, so a name
// written without quotes comes out upper-cased too.
func words(d velox.Dialect, sql string) []string {
	ws := lex(d, sql)
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.text
		if !w.quoted {
			out[i] = strings.ToUpper(w.text)
		}
	}
	return out
}
