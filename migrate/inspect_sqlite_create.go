package migrate

import (
	"slices"
	"strings"
)

// sqliteCreateTable is what the CREATE TABLE text of sqlite_schema names that the
// pragmas do not: constraint names, CHECK texts and AUTOINCREMENT, and what
// inspect notes: a column's collation, a DEFERRABLE key, and the table's
// options. A column a constraint names is the column's declared name,
// matched by ASCII case as SQLite matches it, and a text has no comments.
type sqliteCreateTable struct {
	autoincrement bool
	checks        []Check           // in order; Name "" for an unnamed one
	foreignKeys   []textKey         // in order
	defaults      map[string]string // a column's DEFAULT as written, parentheses kept, by column
	collations    map[string]string // a column's COLLATE, by column
	options       []string          // WITHOUT ROWID, STRICT
}

// textKey is a foreign key as the CREATE TABLE text gives it: its
// referenced table and columns as written, none when it names none.
type textKey struct {
	name       string // "" for an unnamed one
	columns    []string
	refTable   string
	refColumns []string
	deferrable bool
}

// sameName reports whether a and b are one name to SQLite, which folds
// ASCII letters only.
func sameName(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

// declared is name as one of cols, the declared names, spells it, or name
// when none is it.
func declared(name string, cols []string) string {
	for _, c := range cols {
		if sameName(c, name) {
			return c
		}
	}
	return name
}

// sameNames reports whether a and b are the same names, in order.
func sameNames(a, b []string) bool {
	return slices.EqualFunc(a, b, sameName)
}

// tokenText is the text of toks[from:to] of src without its comments, the
// space between tokens kept where it holds none, and each name of a column
// of cols spelled as cols declares it, in its own quotes.
func tokenText(src string, toks []sqlTok, from, to int, cols []string) string {
	var b strings.Builder
	for k := from; k < to; k++ {
		t := toks[k]
		if k > from {
			gap := src[toks[k-1].end:t.start]
			if strings.TrimSpace(gap) != "" {
				gap = " " // a comment
			}
			b.WriteString(gap)
		}
		text := t.text
		if n, ok := t.name(); ok && t.kind != 's' {
			if c := declared(n, cols); c != n {
				switch t.text[0] {
				case '"', '`':
					q := t.text[:1]
					text = q + strings.ReplaceAll(c, q, q+q) + q
				case '[':
					text = "[" + c + "]"
				default:
					text = c
				}
			}
		}
		b.WriteString(text)
	}
	return b.String()
}

// parseCreateTable reads sql, a CREATE TABLE as sqlite_schema holds it:
// velox's own shape and the hand-written forms, with CONSTRAINT <name>
// FOREIGN KEY or CHECK, unnamed CHECK (…), column-level REFERENCES and
// CHECK, the four quote forms and comments. false: not a form it reads.
func parseCreateTable(sql string) (sqliteCreateTable, bool) {
	toks, ok := lexSQL(sql)
	if !ok {
		return sqliteCreateTable{}, false
	}
	p := &ctParser{src: sql, toks: toks, ct: sqliteCreateTable{defaults: map[string]string{}, collations: map[string]string{}},
		defaults: map[string][2]int{}}
	if !p.parse() {
		return sqliteCreateTable{}, false
	}
	// The columns are all declared now: a check may name a later one.
	for _, c := range p.checks {
		p.ct.checks = append(p.ct.checks, Check{Name: c.name, Expr: tokenText(sql, toks, c.from, c.to, p.cols)})
	}
	for col, r := range p.defaults {
		p.ct.defaults[col] = tokenText(sql, toks, r[0], r[1], p.cols)
	}
	for i := range p.ct.foreignKeys {
		k := &p.ct.foreignKeys[i]
		for j, c := range k.columns {
			k.columns[j] = declared(c, p.cols)
		}
	}
	return p.ct, true
}

// indexWhere is the predicate of sql, a CREATE INDEX on a table of the
// columns cols: the text after its WHERE, as tokenText gives it. false: no
// WHERE, or not a text it reads.
func indexWhere(sql string, cols []string) (string, bool) {
	toks, ok := lexSQL(sql)
	if !ok {
		return "", false
	}
	depth := 0
	for k, t := range toks {
		switch {
		case t.is("("):
			depth++
		case t.is(")"):
			depth--
		case depth == 0 && t.word("WHERE"):
			where := tokenText(sql, toks, k+1, len(toks), cols)
			return where, where != ""
		}
	}
	return "", false
}

// A sqlTok is a token of SQLite's SQL: a word (a keyword, a bare name or a
// number), a quoted name (in double quotes, brackets or backquotes), a '…'
// literal, or one other byte.
type sqlTok struct {
	kind       byte // 'w', 'q', 's' or 'p'
	text       string
	start, end int
}

// is reports whether t is the punctuation s.
func (t sqlTok) is(s string) bool { return t.kind == 'p' && t.text == s }

// word reports whether t is the bare word w, in any case.
func (t sqlTok) word(w string) bool { return t.kind == 'w' && strings.EqualFold(t.text, w) }

// name is t as a name: a quoted one unquoted. ok is false for punctuation.
func (t sqlTok) name() (string, bool) {
	switch t.kind {
	case 'w':
		return t.text, true
	case 'q', 's':
		q := t.text[0]
		closing := q
		if q == '[' {
			return t.text[1 : len(t.text)-1], true
		}
		return strings.ReplaceAll(t.text[1:len(t.text)-1], string([]byte{closing, closing}), string(closing)), true
	}
	return "", false
}

// lexSQL is src's tokens, white space and comments left out. false: a
// quote or block comment is not closed.
func lexSQL(src string) ([]sqlTok, bool) {
	var toks []sqlTok
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			i++
		case c == '-' && i+1 < len(src) && src[i+1] == '-':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return nil, false
			}
			i += 2 + end + 2
		case c == '"' || c == '`' || c == '\'' || c == '[':
			closing := c
			if c == '[' {
				closing = ']'
			}
			j := i + 1
			for {
				k := strings.IndexByte(src[j:], closing)
				if k < 0 {
					return nil, false
				}
				j += k + 1
				if c != '[' && j < len(src) && src[j] == closing {
					j++ // a doubled quote
					continue
				}
				break
			}
			kind := byte('q')
			if c == '\'' {
				kind = 's'
			}
			toks = append(toks, sqlTok{kind: kind, text: src[i:j], start: i, end: j})
			i = j
		case c >= '0' && c <= '9' || c == '.' && i+1 < len(src) && src[i+1] >= '0' && src[i+1] <= '9':
			// A number: 1, 1.5, .5, 1e-5, 0x1F.
			j := i + 1
			for j < len(src) && (wordByte(src[j]) || src[j] == '.' ||
				(src[j] == '+' || src[j] == '-') && (src[j-1] == 'e' || src[j-1] == 'E') && !strings.HasPrefix(src[i:], "0x")) {
				j++
			}
			toks = append(toks, sqlTok{kind: 'w', text: src[i:j], start: i, end: j})
			i = j
		case wordByte(c) || c == '$':
			j := i
			for j < len(src) && (wordByte(src[j]) || src[j] == '$') {
				j++
			}
			toks = append(toks, sqlTok{kind: 'w', text: src[i:j], start: i, end: j})
			i = j
		default:
			toks = append(toks, sqlTok{kind: 'p', text: src[i : i+1], start: i, end: i + 1})
			i++
		}
	}
	return toks, true
}

// ctParser is parseCreateTable's reader: toks from i.
type ctParser struct {
	src      string
	toks     []sqlTok
	i        int
	ct       sqliteCreateTable
	cols     []string          // the declared columns, in order
	autoinc  bool              // names read AUTOINCREMENT
	checks   []textCheck       // made texts once every column is declared
	defaults map[string][2]int // a column's DEFAULT, as tokens from and to
}

// textCheck is a check as its tokens from and to.
type textCheck struct {
	name     string
	from, to int
}

func (p *ctParser) done() bool { return p.i >= len(p.toks) }

func (p *ctParser) peek() sqlTok {
	if p.done() {
		return sqlTok{}
	}
	return p.toks[p.i]
}

// word takes the next token when it is one of the bare words ws.
func (p *ctParser) word(ws ...string) bool {
	for _, w := range ws {
		if p.peek().word(w) {
			p.i++
			return true
		}
	}
	return false
}

// punct takes the next token when it is the punctuation s.
func (p *ctParser) punct(s string) bool {
	if p.peek().is(s) {
		p.i++
		return true
	}
	return false
}

// name takes a name.
func (p *ctParser) name() (string, bool) {
	if p.done() {
		return "", false
	}
	n, ok := p.toks[p.i].name()
	if ok {
		p.i++
	}
	return n, ok
}

// group takes a parenthesised group, the next token its "(", and returns
// the tokens between its parentheses, from and to.
func (p *ctParser) group() (from, to int, ok bool) {
	if !p.peek().is("(") {
		return 0, 0, false
	}
	from = p.i + 1
	depth := 0
	for ; p.i < len(p.toks); p.i++ {
		switch t := p.toks[p.i]; {
		case t.is("("):
			depth++
		case t.is(")"):
			depth--
			if depth == 0 {
				to = p.i
				p.i++
				return from, to, true
			}
		}
	}
	return 0, 0, false
}

// names takes a parenthesised list of column names, each perhaps with
// COLLATE and ASC or DESC, and for a table's primary key AUTOINCREMENT.
func (p *ctParser) names() ([]string, bool) {
	if !p.punct("(") {
		return nil, false
	}
	var out []string
	for {
		n, ok := p.name()
		if !ok {
			return nil, false
		}
		out = append(out, n)
		if p.word("COLLATE") {
			if _, ok := p.name(); !ok {
				return nil, false
			}
		}
		p.word("ASC", "DESC")
		if p.word("AUTOINCREMENT") {
			p.autoinc = true
		}
		if p.punct(")") {
			return out, true
		}
		if !p.punct(",") {
			return nil, false
		}
	}
}

// parse reads the whole statement.
func (p *ctParser) parse() bool {
	if !p.word("CREATE") {
		return false
	}
	p.word("TEMP", "TEMPORARY")
	if !p.word("TABLE") {
		return false
	}
	if p.word("IF") && (!p.word("NOT") || !p.word("EXISTS")) {
		return false
	}
	if _, ok := p.name(); !ok {
		return false
	}
	if p.punct(".") {
		if _, ok := p.name(); !ok {
			return false
		}
	}
	if !p.punct("(") {
		return false
	}
	for {
		if !p.definition() {
			return false
		}
		if p.punct(")") {
			break
		}
		if !p.punct(",") {
			return false
		}
	}
	for !p.done() {
		switch {
		case p.word("WITHOUT"):
			if !p.word("ROWID") {
				return false
			}
			p.ct.options = append(p.ct.options, "WITHOUT ROWID")
		case p.word("STRICT"):
			p.ct.options = append(p.ct.options, "STRICT")
		default:
			return false
		}
		if !p.done() && !p.punct(",") {
			return false
		}
	}
	return true
}

// definition reads a column's definition or a table constraint.
func (p *ctParser) definition() bool {
	if t := p.peek(); t.word("CONSTRAINT") || t.word("PRIMARY") || t.word("UNIQUE") || t.word("CHECK") || t.word("FOREIGN") {
		return p.tableConstraint()
	}
	col, ok := p.name()
	if !ok {
		return false
	}
	p.cols = append(p.cols, col)
	// The type: names, then perhaps (n) or (n, m).
	for t := p.peek(); (t.kind == 'w' || t.kind == 'q' || t.kind == 's') && !columnKeyword(t); t = p.peek() {
		p.i++
	}
	if p.peek().is("(") {
		if _, _, ok := p.group(); !ok {
			return false
		}
	}
	name := ""
	for t := p.peek(); !t.is(",") && !t.is(")"); t = p.peek() {
		if p.done() {
			return false
		}
		named := name
		name = ""
		switch {
		case p.word("CONSTRAINT"):
			if name, ok = p.name(); !ok {
				return false
			}
		case p.word("PRIMARY"):
			if !p.word("KEY") {
				return false
			}
			p.word("ASC", "DESC")
			if !p.conflict() {
				return false
			}
			if p.word("AUTOINCREMENT") {
				p.ct.autoincrement = true
			}
		case p.word("NOT"):
			if !p.word("NULL") || !p.conflict() {
				return false
			}
		case p.word("NULL"), p.word("UNIQUE"):
			if !p.conflict() {
				return false
			}
		case p.word("CHECK"):
			from, to, ok := p.group()
			if !ok {
				return false
			}
			p.checks = append(p.checks, textCheck{named, from, to})
		case p.word("DEFAULT"):
			from := p.i
			if !p.defaultValue() {
				return false
			}
			p.defaults[col] = [2]int{from, p.i}
		case p.word("COLLATE"):
			coll, ok := p.name()
			if !ok {
				return false
			}
			p.ct.collations[col] = coll
		case p.word("REFERENCES"):
			if !p.references([]string{col}, named) {
				return false
			}
		case p.word("GENERATED"):
			if !p.word("ALWAYS") || !p.peek().word("AS") {
				return false
			}
		case p.word("AS"):
			if _, _, ok := p.group(); !ok {
				return false
			}
			p.word("STORED", "VIRTUAL")
		default:
			return false
		}
	}
	return true
}

// columnKeyword reports whether t starts a column constraint, so ends the
// column's type.
func columnKeyword(t sqlTok) bool {
	for _, w := range []string{"CONSTRAINT", "PRIMARY", "NOT", "NULL", "UNIQUE", "CHECK", "DEFAULT", "COLLATE", "REFERENCES", "GENERATED", "AS"} {
		if t.word(w) {
			return true
		}
	}
	return false
}

// conflict takes an ON CONFLICT clause, when there is one.
func (p *ctParser) conflict() bool {
	if !p.word("ON") {
		return true
	}
	return p.word("CONFLICT") && p.word("ROLLBACK", "ABORT", "FAIL", "IGNORE", "REPLACE")
}

// defaultValue takes a default: a group, a signed number, a literal or a
// word.
func (p *ctParser) defaultValue() bool {
	if p.peek().is("(") {
		_, _, ok := p.group()
		return ok
	}
	if p.punct("-") || p.punct("+") {
		return p.peek().kind == 'w' && p.word(p.peek().text)
	}
	if t := p.peek(); t.kind == 'w' || t.kind == 's' || t.kind == 'q' {
		p.i++
		if (t.word("x") || t.word("X")) && p.peek().kind == 's' && p.peek().start == t.end {
			p.i++ // a blob literal, x'00'
		}
		return true
	}
	return false
}

// tableConstraint reads [CONSTRAINT name] PRIMARY KEY, UNIQUE, CHECK or
// FOREIGN KEY.
func (p *ctParser) tableConstraint() bool {
	name := ""
	if p.word("CONSTRAINT") {
		var ok bool
		if name, ok = p.name(); !ok {
			return false
		}
	}
	switch {
	case p.word("PRIMARY"):
		if !p.word("KEY") {
			return false
		}
		p.autoinc = false
		cols, ok := p.names()
		if !ok {
			return false
		}
		if p.autoinc {
			// SQLite refuses AUTOINCREMENT but on one INTEGER column.
			p.ct.autoincrement = len(cols) == 1
		}
		return p.conflict()
	case p.word("UNIQUE"):
		p.autoinc = false
		if _, ok := p.names(); !ok || p.autoinc {
			return false
		}
		return p.conflict()
	case p.word("CHECK"):
		from, to, ok := p.group()
		if !ok {
			return false
		}
		p.checks = append(p.checks, textCheck{name, from, to})
		return true
	case p.word("FOREIGN"):
		if !p.word("KEY") {
			return false
		}
		p.autoinc = false
		cols, ok := p.names()
		return ok && !p.autoinc && p.word("REFERENCES") && p.references(cols, name)
	}
	return false
}

// references reads a foreign key clause after REFERENCES, for the key of
// cols named name.
func (p *ctParser) references(cols []string, name string) bool {
	k := textKey{name: name, columns: cols}
	var ok bool
	if k.refTable, ok = p.name(); !ok {
		return false
	}
	if p.peek().is("(") {
		p.autoinc = false
		if k.refColumns, ok = p.names(); !ok || p.autoinc {
			return false
		}
	}
	for {
		switch {
		case p.word("ON"):
			if !p.word("DELETE", "UPDATE") {
				return false
			}
			switch {
			case p.word("SET"):
				if !p.word("NULL", "DEFAULT") {
					return false
				}
			case p.word("NO"):
				if !p.word("ACTION") {
					return false
				}
			case p.word("CASCADE", "RESTRICT"):
			default:
				return false
			}
		case p.word("MATCH"):
			if _, ok := p.name(); !ok {
				return false
			}
		case p.peek().word("NOT") && p.i+1 < len(p.toks) && p.toks[p.i+1].word("DEFERRABLE"):
			p.i += 2
			p.initially()
		case p.word("DEFERRABLE"):
			k.deferrable = true
			p.initially()
		default:
			p.ct.foreignKeys = append(p.ct.foreignKeys, k)
			return true
		}
	}
}

// initially takes INITIALLY DEFERRED or IMMEDIATE, when there is one.
func (p *ctParser) initially() {
	if p.word("INITIALLY") {
		p.word("DEFERRED", "IMMEDIATE")
	}
}
