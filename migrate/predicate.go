package migrate

import (
	"regexp"
	"strings"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/ddl"
)

// normalizePredicate is expr in one canonical spelling, for comparing what a
// database prints with what was written. It reads the predicates velox
// users write: and, or, not, comparisons, IS [NOT] NULL, [NOT] IN, [NOT]
// LIKE and ILIKE (PostgreSQL's ~~ forms), BETWEEN, = ANY and <> ALL of an
// ARRAY, arithmetic, function calls, literals and casts. It drops every
// cast, unquotes the names that need no quotes, lower-cases keywords,
// function names and unquoted names, and prints one space around each binary
// operator and only the parentheses precedence needs, so two predicates of
// one meaning in different spellings print alike and two of different
// meanings never do. Text it cannot parse comes back with its white space
// collapsed outside quotes.
func normalizePredicate(d velox.Dialect, expr string) string {
	s, _ := normalized(d, expr)
	return s
}

// normalized is normalizePredicate, and whether expr parsed.
func normalized(d velox.Dialect, expr string) (string, bool) {
	toks, ok := lexPredicate(d, expr)
	if ok && len(toks) > 0 {
		p := &predParser{d: d, toks: toks, ok: true}
		n := p.or()
		if p.ok && p.i == len(p.toks) {
			return p.print(n, 0), true
		}
	}
	return collapseSpace(d, expr), false
}

// samePredicate reports whether a and b are one predicate: alike after
// normalizing when both parse, else alike with their white space collapsed.
// comparable is whether both parsed.
func samePredicate(d velox.Dialect, a, b string) (same, comparable bool) {
	na, oka := normalized(d, a)
	nb, okb := normalized(d, b)
	if oka && okb {
		return na == nb, true
	}
	return collapseSpace(d, a) == collapseSpace(d, b), false
}

// collapseSpace is s with each run of white space outside quotes made one
// space, and none at either end. A MySQL literal's \ escapes.
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
		if c == '\'' || c == '"' || c == '`' {
			j = stringEnd(s, i, c != '`' && d == velox.MySQL)
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
				// MySQL's character set before a literal, _utf8mb4'a', is
				// dropped; PostgreSQL's E'' and the like are not read.
				if d != velox.MySQL || word[0] != '_' {
					return nil, false
				}
				i = j
				continue
			}
			toks = append(toks, predToken{tokWord, word})
			i = j
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
			switch e := expr[j]; e {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '0':
				b.WriteByte(0)
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
)

type predNode struct {
	kind  int
	op    string
	text  string
	str   bool   // a string literal, whose value is val
	val   string // a string literal's value
	parts []predName
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

func (p *predParser) compare() *predNode {
	n := p.add()
	for p.ok {
		t := p.peek(0)
		switch {
		case t.kind == tokOp && compareOps[t.text] != "":
			p.i++
			op := compareOps[t.text]
			if p.is(0, "any") || p.is(0, "all") || p.is(0, "some") {
				n = p.quantified(n, op)
				continue
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
		default:
			return n
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
		return &predNode{kind: nodeUnary, op: "-", kids: []*predNode{p.unary()}}
	}
	n := p.primary()
	for p.ok && p.accept("::") {
		if p.castType() && n.str && plainNumber.MatchString(n.val) {
			n = &predNode{kind: nodeLit, text: n.val}
		}
	}
	return n
}

var plainNumber = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// numericTypes are the type names a literal cast to becomes a number.
var numericTypes = map[string]bool{
	"smallint": true, "integer": true, "int": true, "bigint": true, "int2": true, "int4": true, "int8": true,
	"numeric": true, "decimal": true, "real": true, "float4": true, "float8": true, "double": true,
}

// typeWords are the words after a type's first that are part of its name.
var typeWords = map[string]bool{"varying": true, "precision": true, "with": true, "without": true, "time": true, "zone": true}

// castType reads the type after ::, and reports whether it is numeric.
func (p *predParser) castType() bool {
	t := p.peek(0)
	if t.kind != tokWord && t.kind != tokQuoted {
		p.fail()
		return false
	}
	p.i++
	first := strings.ToLower(t.text)
	if p.accept(".") {
		if u := p.peek(0); u.kind == tokWord || u.kind == tokQuoted {
			p.i++
			first = ""
		} else {
			p.fail()
		}
	}
	for t := p.peek(0); t.kind == tokWord && typeWords[strings.ToLower(t.text)]; t = p.peek(0) {
		p.i++
	}
	if p.accept("(") {
		for p.ok && p.peek(0).kind == tokNumber {
			p.i++
			if !p.accept(",") {
				break
			}
		}
		p.expect(")")
	}
	for p.ok && p.accept("[") {
		p.expect("]")
	}
	return t.kind == tokWord && numericTypes[first]
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
