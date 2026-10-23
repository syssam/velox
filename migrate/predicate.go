package migrate

import (
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/ddl"
)

// normalizePredicate is expr in one canonical spelling, for comparing what a
// database prints with what was written. It reads the predicates velox
// users write: and, or, not, comparisons, IS [NOT] NULL, [NOT] IN, [NOT]
// LIKE and ILIKE (PostgreSQL's ~~ forms), BETWEEN, = ANY and <> ALL of an
// ARRAY, arithmetic, function calls, literals and casts. It drops the casts
// that only spell a value again (castOf), unquotes the names that need no
// quotes, lower-cases keywords, function names and unquoted names, and
// prints one space around each binary operator and only the parentheses
// precedence needs, so two predicates of one meaning in different spellings
// print alike and two of different meanings never do. A chain of
// comparisons without parentheses, a comment and what else it does not read
// fail the parse, and the text comes back with its white space collapsed
// outside quotes.
//
// samePredicate compares two predicates by their trees, where a cast the
// database adds itself matches no cast. A date or time literal is compared
// by its value when it reads in an ISO layout, and a timestamptz literal
// without a zone is read as UTC, so the database's session must be in UTC.
// A literal that does not read, an interval's always, keeps its cast, so a
// cast literal of it never matches an uncast one.
func normalizePredicate(d velox.Dialect, expr string) string {
	s, _ := normalized(d, expr)
	return s
}

// normalized is normalizePredicate, and whether expr parsed.
func normalized(d velox.Dialect, expr string) (string, bool) {
	if p, n, ok := parsePredicate(d, expr); ok {
		return p.print(n, 0), true
	}
	return collapseSpace(d, expr), false
}

// samePredicate reports whether a and b are one predicate: when both parse,
// their trees match (match); else they are alike with their white space
// collapsed. comparable is whether both parsed.
func samePredicate(d velox.Dialect, a, b string) (same, comparable bool) {
	p, na, oka := parsePredicate(d, a)
	_, nb, okb := parsePredicate(d, b)
	if oka && okb {
		return p.match(na, nb), true
	}
	return collapseSpace(d, a) == collapseSpace(d, b), false
}

// parsePredicate is expr's tree, and whether it parsed.
func parsePredicate(d velox.Dialect, expr string) (*predParser, *predNode, bool) {
	toks, ok := lexPredicate(d, expr)
	if !ok || len(toks) == 0 {
		return nil, nil, false
	}
	p := &predParser{d: d, toks: toks, ok: true}
	n := p.or()
	return p, n, p.ok && p.i == len(p.toks)
}

// match reports whether a and b are one predicate: alike, but that a cast the
// database adds itself (elides) matches no cast on the other side. Two casts
// match only when they are to one type.
func (p *predParser) match(a, b *predNode) bool {
	if p.text(a) == p.text(b) { // one meaning, as and and or associate
		return true
	}
	ac, bc := a.kind == nodeCast, b.kind == nodeCast
	switch {
	case ac && bc:
		if a.text != b.text {
			return false
		}
		if t, ok := sameTime(a.cast.base, a.kids[0], b.kids[0]); ok {
			return t
		}
		return p.match(a.kids[0], b.kids[0])
	case ac:
		return p.elides(a, b)
	case bc:
		return p.elides(b, a)
	}
	if a.kind != b.kind || a.op != b.op || a.text != b.text || a.str != b.str || len(a.kids) != len(b.kids) ||
		p.name(a.parts) != p.name(b.parts) {
		return false
	}
	for i := range a.kids {
		if !p.match(a.kids[i], b.kids[i]) {
			return false
		}
	}
	return true
}

// elides reports whether the cast c matches e, which is not a cast, where
// the cast is exact: to numeric on any expression, the cast PostgreSQL
// inserts; to bigint on an integer literal; to real or double precision on
// a number literal; a string literal cast to a date or time type that is the
// same value as the literal e; a string literal cast to regclass, as a
// serial's default prints, that is e.
func (p *predParser) elides(c, e *predNode) bool {
	if c.cast.mod || c.cast.array {
		return false
	}
	k := c.kids[0]
	number := k.kind == nodeLit && !k.str && plainNumber.MatchString(k.text)
	switch c.cast.base {
	case "numeric", "decimal":
		return p.match(k, e)
	case "bigint", "int8":
		return number && integer.MatchString(k.text) && p.match(k, e)
	case "real", "float4", "double precision", "float8":
		return number && p.match(k, e)
	case "regclass":
		return k.str && e.str && k.val == e.val
	}
	same, ok := sameTime(c.cast.base, k, e)
	return ok && same
}

// timeLayouts are the layouts a string literal of each date and time type
// is read in: the ISO forms PostgreSQL prints and the shorter ones written.
var timeLayouts = func() map[string][]string {
	date := []string{"2006-01-02"}
	var stamp []string
	for _, sep := range []string{" ", "T"} {
		for _, clock := range []string{"15:04", "15:04:05", "15:04:05.999999999"} {
			stamp = append(stamp, "2006-01-02"+sep+clock)
		}
	}
	var zoned []string
	for _, l := range stamp {
		for _, z := range []string{"Z07", "Z07:00", "Z0700", " Z07", " Z07:00", " MST"} {
			zoned = append(zoned, l+z)
		}
	}
	clock := []string{"15:04", "15:04:05", "15:04:05.999999999"}
	stamp = append(date, stamp...)
	return map[string][]string{
		"date":                        date,
		"timestamp":                   stamp,
		"timestamp without time zone": stamp,
		"timestamptz":                 append(zoned, stamp...),
		"timestamp with time zone":    append(zoned, stamp...),
		"time":                        clock,
		"time without time zone":      clock,
	}
}()

// sameTime reports, when a and b are string literals that are both values
// of the date or time type typ, whether they are one value. A literal
// without a zone is read as UTC. ok is false when typ is no such type or a
// literal is not one of its values, as an interval never is.
func sameTime(typ string, a, b *predNode) (same, ok bool) {
	layouts := timeLayouts[typ]
	if layouts == nil || !a.str || !b.str {
		return false, false
	}
	ta, oka := parseTime(layouts, a.val)
	tb, okb := parseTime(layouts, b.val)
	if !oka || !okb {
		return false, false
	}
	return ta.Equal(tb), true
}

func parseTime(layouts []string, s string) (time.Time, bool) {
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// collapseSpace is s with each run of white space outside quotes made one
// space, and none at either end. Quotes are literals, quoted names, and on
// PostgreSQL E'…' and dollar-quoted strings; a MySQL literal's \ escapes.
func collapseSpace(d velox.Dialect, s string) string {
	var b strings.Builder
	space := false
	for i := 0; i < len(s); {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v' {
			space = true
			i++
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		j := i + 1
		switch {
		case c == '\'' || c == '"' || c == '`':
			j = stringEnd(s, i, c != '`' && d == velox.MySQL)
		case d == velox.Postgres && (c == 'E' || c == 'e') && i+1 < len(s) && s[i+1] == '\'' && (i == 0 || !wordByte(s[i-1])):
			j = stringEnd(s, i+1, true)
		case d == velox.Postgres && c == '$' && dollarEnd(s, i) > 0:
			j = dollarEnd(s, i)
		}
		b.WriteString(s[i:j])
		i = j
	}
	return b.String()
}

// The kinds of a predicate's token.
const (
	tokWord   = iota // a keyword or an unquoted name, as written
	tokQuoted        // a quoted name, unquoted
	tokString        // a string literal, its value
	tokNumber
	tokOp
)

type predToken struct {
	kind int
	text string
}

// lexPredicate splits expr into tokens; false when it holds what the
// normaliser does not read.
func lexPredicate(d velox.Dialect, expr string) ([]predToken, bool) {
	var toks []predToken
	for i := 0; i < len(expr); {
		c := expr[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			i++
		case c == '\'' || c == '"' && d == velox.MySQL:
			v, j, ok := unquoteAt(expr, i, d == velox.MySQL)
			if !ok {
				return nil, false
			}
			toks = append(toks, predToken{tokString, v})
			i = j
		case c == '"' || c == '`' && d != velox.Postgres:
			v, j, ok := unquoteAt(expr, i, false)
			if !ok {
				return nil, false
			}
			toks = append(toks, predToken{tokQuoted, v})
			i = j
		case c == '[' && d == velox.SQLite:
			j := strings.IndexByte(expr[i:], ']')
			if j < 0 {
				return nil, false
			}
			toks = append(toks, predToken{tokQuoted, expr[i+1 : i+j]})
			i += j + 1
		case c >= '0' && c <= '9' || c == '.' && i+1 < len(expr) && expr[i+1] >= '0' && expr[i+1] <= '9':
			m := numberRE.FindString(expr[i:])
			if m == "" || i+len(m) < len(expr) && wordByte(expr[i+len(m)]) {
				return nil, false
			}
			toks = append(toks, predToken{tokNumber, strings.ToLower(m)})
			i += len(m)
		case wordByte(c):
			j := i
			for j < len(expr) && (wordByte(expr[j]) || expr[j] == '$') {
				j++
			}
			word := expr[i:j]
			if j < len(expr) && expr[j] == '\'' {
				// MySQL's UTF-8 character set before a literal, _utf8mb4'a',
				// is dropped; another, which changes the comparison, and
				// PostgreSQL's E'' and the like are not read.
				switch strings.ToLower(word) {
				case "_utf8mb4", "_utf8mb3", "_utf8":
				default:
					return nil, false
				}
				if d != velox.MySQL {
					return nil, false
				}
				i = j
				continue
			}
			toks = append(toks, predToken{tokWord, word})
			i = j
		case strings.HasPrefix(expr[i:], "--") || strings.HasPrefix(expr[i:], "/*"):
			return nil, false // a comment
		default:
			op := ""
			for _, o := range predOps {
				if strings.HasPrefix(expr[i:], o) {
					op = o
					break
				}
			}
			if op == "" || op == "==" && d != velox.SQLite {
				return nil, false
			}
			toks = append(toks, predToken{tokOp, op})
			i += len(op)
		}
	}
	return toks, true
}

var numberRE = regexp.MustCompile(`^([0-9]+(\.[0-9]*)?|\.[0-9]+)([eE][-+]?[0-9]+)?`)

// predOps are the operators the normaliser reads, a longer one before its
// prefix.
var predOps = []string{"!~~*", "!~~", "~~*", "~~", "::", "<=", ">=", "<>", "!=", "==", "=", "<", ">", "+", "-", "*", "/", "(", ")", ",", "[", "]", "."}

// unquoteAt is the value of the quoted text at expr[i], a quote doubled
// inside it and, when backslash, a character after \, and the index after
// it; false when it is not closed.
func unquoteAt(expr string, i int, backslash bool) (string, int, bool) {
	q := expr[i]
	var b strings.Builder
	for j := i + 1; j < len(expr); j++ {
		c := expr[j]
		switch {
		case backslash && c == '\\':
			if j+1 == len(expr) {
				return "", 0, false
			}
			j++
			// MySQL's escapes: \% and \_ keep their backslash, for LIKE;
			// any other character but these stands for itself.
			switch e := expr[j]; e {
			case '0':
				b.WriteByte(0)
			case 'b':
				b.WriteByte('\b')
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'Z':
				b.WriteByte(0x1a)
			case '%', '_':
				b.WriteByte('\\')
				b.WriteByte(e)
			default:
				b.WriteByte(e)
			}
		case c == q && j+1 < len(expr) && expr[j+1] == q:
			b.WriteByte(q)
			j++
		case c == q:
			return b.String(), j + 1, true
		default:
			b.WriteByte(c)
		}
	}
	return "", 0, false
}

// The kinds of a predicate's node.
const (
	nodeLit    = iota // text: a literal or a number as printed
	nodeName          // parts: a name, maybe qualified
	nodeCall          // parts: the function's name; kids: the arguments
	nodeArray         // kids: the elements
	nodeUnary         // op: "not" or "-"; kids[0]
	nodeBinary        // op; kids[0] and kids[1]
	nodeIs            // op: "is null", "is not true", …; kids[0]
	nodeIn            // op: "in" or "not in"; kids[0] in kids[1:]
	nodeCast          // text: the type; kids[0]
)

type predNode struct {
	kind  int
	op    string
	text  string
	str   bool   // a string literal, whose value is val
	val   string // a string literal's value
	parts []predName
	cast  castTo // a cast's type
	kids  []*predNode
}

type predName struct {
	name   string
	quoted bool
}

// The precedence of each level, loosest first.
const (
	precOr = 1 + iota
	precAnd
	precNot
	precCompare // comparisons, IS, IN and LIKE
	precAdd
	precMul
	precNeg
	precPrimary
)

// predParser reads tokens by recursive descent; ok turns false at the first
// thing it does not read.
type predParser struct {
	d    velox.Dialect
	toks []predToken
	i    int
	ok   bool
}

func (p *predParser) peek(k int) predToken {
	if p.i+k < len(p.toks) {
		return p.toks[p.i+k]
	}
	return predToken{kind: -1}
}

// is reports whether the token k ahead is the keyword or operator s.
func (p *predParser) is(k int, s string) bool {
	t := p.peek(k)
	return (t.kind == tokWord && strings.EqualFold(t.text, s)) || (t.kind == tokOp && t.text == s)
}

func (p *predParser) accept(s string) bool {
	if p.is(0, s) {
		p.i++
		return true
	}
	return false
}

func (p *predParser) expect(s string) {
	if !p.accept(s) {
		p.fail()
	}
}

func (p *predParser) fail() {
	p.ok = false
	p.i = len(p.toks)
}

func (p *predParser) or() *predNode {
	n := p.and()
	for p.ok && p.accept("or") {
		n = binaryNode("or", n, p.and())
	}
	return n
}

func (p *predParser) and() *predNode {
	n := p.not()
	for p.ok && p.accept("and") {
		n = binaryNode("and", n, p.not())
	}
	return n
}

func (p *predParser) not() *predNode {
	if p.accept("not") {
		return &predNode{kind: nodeUnary, op: "not", kids: []*predNode{p.not()}}
	}
	return p.compare()
}

var compareOps = map[string]string{"=": "=", "==": "=", "<>": "<>", "!=": "<>", "<": "<", "<=": "<=", ">": ">", ">=": ">=",
	"~~": "like", "!~~": "not like", "~~*": "ilike", "!~~*": "not ilike"}

// compare reads one comparison, IS, IN, LIKE or BETWEEN. The databases
// rank these apart from each other in different ways, so a chain of them
// without parentheses is not read.
func (p *predParser) compare() *predNode {
	n := p.add()
	if !p.ok || !p.comparing() {
		return n
	}
	n = p.comparison(n)
	if p.ok && p.comparing() {
		p.fail()
	}
	return n
}

// comparing reports whether the next tokens start a comparison's operator.
func (p *predParser) comparing() bool {
	t := p.peek(0)
	return t.kind == tokOp && compareOps[t.text] != "" || p.is(0, "is") || p.is(0, "in") || p.is(0, "like") ||
		p.is(0, "ilike") || p.is(0, "between") ||
		p.is(0, "not") && (p.is(1, "in") || p.is(1, "like") || p.is(1, "ilike") || p.is(1, "between"))
}

// comparison reads the operator after n, and its right side.
func (p *predParser) comparison(n *predNode) *predNode {
	t := p.peek(0)
	switch {
	case t.kind == tokOp && compareOps[t.text] != "":
		p.i++
		op := compareOps[t.text]
		if p.is(0, "any") || p.is(0, "all") || p.is(0, "some") {
			return p.quantified(n, op)
		}
		n = binaryNode(op, n, p.add())
	case p.accept("is"):
		op := "is "
		if p.accept("not") {
			op += "not "
		}
		switch {
		case p.accept("null"):
			op += "null"
		case p.accept("true"):
			op += "true"
		case p.accept("false"):
			op += "false"
		default:
			p.fail()
		}
		n = &predNode{kind: nodeIs, op: op, kids: []*predNode{n}}
	case p.is(0, "in") || p.is(0, "not") && p.is(1, "in"):
		neg := p.accept("not")
		p.expect("in")
		p.expect("(")
		list := []*predNode{n}
		for p.ok {
			list = append(list, p.or())
			if !p.accept(",") {
				break
			}
		}
		p.expect(")")
		n = in(neg, list)
	case p.is(0, "like") || p.is(0, "ilike") || p.is(0, "not") && (p.is(1, "like") || p.is(1, "ilike")):
		op := ""
		if p.accept("not") {
			op = "not "
		}
		op += strings.ToLower(p.peek(0).text)
		p.i++
		n = binaryNode(op, n, p.add())
	case p.is(0, "between") || p.is(0, "not") && p.is(1, "between"):
		neg := p.accept("not")
		p.expect("between")
		lo := p.add()
		p.expect("and")
		hi := p.add()
		if neg {
			n = binaryNode("or", binaryNode("<", n, lo), binaryNode(">", n, hi))
		} else {
			n = binaryNode("and", binaryNode(">=", n, lo), binaryNode("<=", n, hi))
		}
	}
	return n
}

// quantified reads ANY (ARRAY[…]) or ALL (ARRAY[…]) after x op: = ANY is
// IN and <> ALL is NOT IN; the others are not read.
func (p *predParser) quantified(x *predNode, op string) *predNode {
	all := p.is(0, "all")
	p.i++
	p.expect("(")
	arr := p.or()
	p.expect(")")
	if !p.ok || arr.kind != nodeArray || len(arr.kids) == 0 {
		p.fail()
		return x
	}
	switch {
	case op == "=" && !all:
		return in(false, append([]*predNode{x}, arr.kids...))
	case op == "<>" && all:
		return in(true, append([]*predNode{x}, arr.kids...))
	}
	p.fail()
	return x
}

// in is x [NOT] IN the rest of list; of one value, = or <>, as PostgreSQL
// reads it.
func in(neg bool, list []*predNode) *predNode {
	if len(list) == 2 {
		if neg {
			return binaryNode("<>", list[0], list[1])
		}
		return binaryNode("=", list[0], list[1])
	}
	op := "in"
	if neg {
		op = "not in"
	}
	return &predNode{kind: nodeIn, op: op, kids: list}
}

func binaryNode(op string, a, b *predNode) *predNode {
	return &predNode{kind: nodeBinary, op: op, kids: []*predNode{a, b}}
}

func (p *predParser) add() *predNode {
	n := p.mul()
	for p.ok && (p.is(0, "+") || p.is(0, "-")) {
		op := p.peek(0).text
		p.i++
		n = binaryNode(op, n, p.mul())
	}
	return n
}

func (p *predParser) mul() *predNode {
	n := p.unary()
	for p.ok && (p.is(0, "*") || p.is(0, "/")) {
		op := p.peek(0).text
		p.i++
		n = binaryNode(op, n, p.unary())
	}
	return n
}

func (p *predParser) unary() *predNode {
	if p.accept("-") {
		n := p.unary()
		if p.ok && n.kind == nodeLit && !n.str && n.text != "" && n.text[0] >= '0' && n.text[0] <= '9' {
			return &predNode{kind: nodeLit, text: "-" + n.text} // a negative number
		}
		return &predNode{kind: nodeUnary, op: "-", kids: []*predNode{n}}
	}
	n := p.primary()
	for p.ok && p.accept("::") {
		n = castOf(n, p.castType())
	}
	return n
}

// A castTo is the type after ::.
type castTo struct {
	base  string // an unquoted type's name, lower case: "character varying"; "" for another
	mod   bool   // it has a modifier, as varchar(1)
	array bool
	text  string // as printed
}

// castOf is n cast to c. A cast is part of the expression, except the
// spellings of one value the databases echo back: a string literal or a
// column cast to text or character varying, an integer literal cast to an
// integer type, and a number cast to numeric, none with a modifier; and an
// array of string literals cast to an array of text, as = ANY prints IN.
func castOf(n *predNode, c castTo) *predNode {
	plain := !c.mod && !c.array
	textual := c.base == "text" || c.base == "character varying" || c.base == "varchar"
	v, lit := n.text, n.kind == nodeLit
	if n.str {
		v = n.val
	}
	switch {
	case plain && textual && (n.str || n.kind == nodeName):
		return n
	case plain && lit && intTypes[c.base] && integer.MatchString(v),
		plain && lit && (c.base == "numeric" || c.base == "decimal") && plainNumber.MatchString(v):
		return &predNode{kind: nodeLit, text: v}
	case !c.mod && c.array && textual && n.kind == nodeArray &&
		!slices.ContainsFunc(n.kids, func(k *predNode) bool { return !k.str }):
		return n
	}
	return &predNode{kind: nodeCast, text: c.text, cast: c, kids: []*predNode{n}}
}

var (
	plainNumber = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)
	integer     = regexp.MustCompile(`^-?[0-9]+$`)
)

// intTypes are the integer types.
var intTypes = map[string]bool{
	"smallint": true, "integer": true, "int": true, "bigint": true, "int2": true, "int4": true, "int8": true,
}

// typeWords are the words after a type's first that are part of its name.
var typeWords = map[string]bool{"varying": true, "precision": true, "with": true, "without": true, "time": true, "zone": true}

// castType reads the type after ::.
func (p *predParser) castType() castTo {
	var c castTo
	var parts []predName
	for {
		t := p.peek(0)
		if t.kind != tokWord && t.kind != tokQuoted {
			p.fail()
			return c
		}
		p.i++
		parts = append(parts, predName{name: t.text, quoted: t.kind == tokQuoted})
		if !p.accept(".") {
			break
		}
	}
	c.text = p.name(parts)
	if len(parts) == 1 && !parts[0].quoted {
		c.base = strings.ToLower(parts[0].name)
	}
	for t := p.peek(0); t.kind == tokWord && typeWords[strings.ToLower(t.text)]; t = p.peek(0) {
		p.i++
		w := strings.ToLower(t.text)
		c.text += " " + w
		if c.base != "" {
			c.base += " " + w
		}
	}
	if p.accept("(") {
		c.mod = true
		var mods []string
		for p.ok && p.peek(0).kind == tokNumber {
			mods = append(mods, p.peek(0).text)
			p.i++
			if !p.accept(",") {
				break
			}
		}
		p.expect(")")
		c.text += "(" + strings.Join(mods, ", ") + ")"
	}
	for p.ok && p.accept("[") {
		p.expect("]")
		c.array = true
		c.text += "[]"
	}
	return c
}

// startless are the keywords a primary cannot be: those the parser reads
// elsewhere, and the forms it does not read.
var startless = map[string]bool{
	"and": true, "or": true, "not": true, "is": true, "in": true, "like": true, "ilike": true, "between": true,
	"any": true, "all": true, "some": true, "case": true, "when": true, "then": true, "else": true, "end": true,
	"cast": true, "exists": true, "select": true, "from": true, "where": true, "distinct": true, "similar": true,
	"escape": true, "collate": true, "overlaps": true, "isnull": true, "notnull": true, "interval": true,
	"array": true, "row": true,
}

func (p *predParser) primary() *predNode {
	t := p.peek(0)
	switch {
	case t.kind == tokOp && t.text == "(":
		p.i++
		n := p.or()
		p.expect(")")
		return n
	case t.kind == tokString:
		p.i++
		return &predNode{kind: nodeLit, text: ddl.Literal(p.d, t.text), str: true, val: t.text}
	case t.kind == tokNumber:
		p.i++
		return &predNode{kind: nodeLit, text: t.text}
	case t.kind == tokWord && (strings.EqualFold(t.text, "true") || strings.EqualFold(t.text, "false") || strings.EqualFold(t.text, "null")):
		p.i++
		return &predNode{kind: nodeLit, text: strings.ToLower(t.text)}
	case t.kind == tokWord && strings.EqualFold(t.text, "array") && p.is(1, "["):
		p.i += 2
		n := &predNode{kind: nodeArray}
		for p.ok && !p.is(0, "]") {
			n.kids = append(n.kids, p.or())
			if !p.accept(",") {
				break
			}
		}
		p.expect("]")
		return n
	case t.kind == tokWord && !startless[strings.ToLower(t.text)] || t.kind == tokQuoted:
		n := &predNode{kind: nodeName}
		for {
			t := p.peek(0)
			if t.kind != tokWord && t.kind != tokQuoted {
				p.fail()
				return n
			}
			p.i++
			n.parts = append(n.parts, predName{name: t.text, quoted: t.kind == tokQuoted})
			if !p.accept(".") {
				break
			}
		}
		if p.accept("(") {
			n.kind = nodeCall
			for p.ok && !p.is(0, ")") {
				n.kids = append(n.kids, p.or())
				if !p.accept(",") {
					break
				}
			}
			p.expect(")")
		}
		return n
	}
	p.fail()
	return &predNode{}
}

func prec(n *predNode) int {
	switch n.kind {
	case nodeUnary:
		if n.op == "not" {
			return precNot
		}
		return precNeg
	case nodeIs, nodeIn:
		return precCompare
	case nodeBinary:
		switch n.op {
		case "or":
			return precOr
		case "and":
			return precAnd
		case "+", "-":
			return precAdd
		case "*", "/":
			return precMul
		}
		return precCompare
	case nodeLit:
		if strings.HasPrefix(n.text, "-") {
			return precNeg
		}
	}
	return precPrimary
}

// print is n as text, in parentheses when its precedence is below least.
func (p *predParser) print(n *predNode, least int) string {
	s := p.text(n)
	if prec(n) < least {
		return "(" + s + ")"
	}
	return s
}

func (p *predParser) list(ns []*predNode) string {
	parts := make([]string, len(ns))
	for i, k := range ns {
		parts[i] = p.print(k, 0)
	}
	return strings.Join(parts, ", ")
}

func (p *predParser) text(n *predNode) string {
	switch n.kind {
	case nodeLit:
		return n.text
	case nodeName:
		return p.name(n.parts)
	case nodeCall:
		return p.name(n.parts) + "(" + p.list(n.kids) + ")"
	case nodeArray:
		return "array[" + p.list(n.kids) + "]"
	case nodeUnary:
		if n.op == "not" {
			return "not " + p.print(n.kids[0], precNot)
		}
		// A negative operand is parenthesised, so - -1 never reads as a
		// comment.
		return "-" + p.print(n.kids[0], precPrimary)
	case nodeIs:
		return p.print(n.kids[0], precCompare+1) + " " + n.op
	case nodeIn:
		return p.print(n.kids[0], precCompare+1) + " " + n.op + " (" + p.list(n.kids[1:]) + ")"
	case nodeCast:
		return p.print(n.kids[0], precPrimary) + "::" + n.text
	}
	// A binary operator: the left operand of one level needs no
	// parentheses, except a comparison's, and the right one does, except
	// and's and or's, which associate.
	pr := prec(n)
	left, right := pr, pr+1
	if pr == precCompare {
		left = pr + 1
	}
	if pr == precAnd || pr == precOr {
		right = pr
	}
	return p.print(n.kids[0], left) + " " + n.op + " " + p.print(n.kids[1], right)
}

// name prints a name's parts: unquoted ones lower-cased, quoted ones bare
// when they need no quotes. A function's name is a name too.
func (p *predParser) name(parts []predName) string {
	out := make([]string, len(parts))
	for i, n := range parts {
		switch {
		case !n.quoted:
			out[i] = strings.ToLower(n.name)
		case plainName(n.name) && !reservedWords[n.name] && !startless[n.name] && n.name != "true" && n.name != "false" && n.name != "null":
			out[i] = n.name
		default:
			out[i] = ddl.Ident(p.d, n.name)
		}
	}
	return strings.Join(out, ".")
}

// reservedWords are PostgreSQL's keywords that a name must be quoted to be.
var reservedWords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`all analyse analyze and any array as asc asymmetric authorization binary both case cast
		check collate collation column concurrently constraint create cross current_catalog current_date current_role
		current_schema current_time current_timestamp current_user default deferrable desc distinct do else end except
		false fetch for foreign freeze from full grant group having ilike in initially inner intersect into is isnull
		join lateral leading left like limit localtime localtimestamp natural not notnull null offset on only or order
		outer overlaps placing primary references returning right select session_user similar some symmetric
		system_user table tablesample then to trailing true union unique user using variadic verbose when where window
		with`) {
		m[w] = true
	}
	return m
}()
