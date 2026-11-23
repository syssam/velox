package engine

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// ExprSlot is what fills one ? of an expression's text: a column of the
// statement's entity (Col, when not negative), a term (Term, when not nil, and
// Col is then ignored), or, for Col -1 and no Term, the next of the values.
type ExprSlot struct {
	Col  int32
	Term *Term
}

// exprData is the V of an OpExpr and the text of an expression Term: the
// text split at each ?, and what fills each ? in order.
type exprData struct {
	parts []string   // len(slots)+1 pieces of text
	slots []ExprSlot // what fills each ?
	args  []any      // the values of the slots that are neither, in order
	shape string     // what shapes it in the render cache's key: see seal
	// window says the text holds the word OVER: a window function, which
	// only a result column or an ordering may hold (hasWindow).
	window bool
	// rel says a slot's term is or holds a relation's aggregate (RelTerm),
	// which the scope gives its target's row condition.
	rel bool
	// agg says the text calls an aggregate function, such as SUM(?), and is
	// no window: it is an aggregate as an aggregate Term is (hasAgg).
	agg bool
}

// seal sets d's shape from its text pieces and what each slot holds. The
// terms among the slots are sealed already.
func (d *exprData) seal() *exprData {
	b := binary.AppendUvarint(make([]byte, 0, 32), uint64(len(d.slots)))
	for i, p := range d.parts {
		b = binary.AppendUvarint(b, uint64(len(p)))
		b = append(b, p...)
		if i == len(d.slots) {
			break
		}
		switch s := d.slots[i]; {
		case s.Term != nil:
			b = append(b, 2)
			b = append(b, s.Term.shape...)
		case s.Col >= 0:
			b = append(b, 1)
			b = binary.AppendUvarint(b, uint64(s.Col))
		default:
			b = append(b, 0)
		}
	}
	d.shape = string(b)
	return d
}

// exprBanned are the words whose presence in an Expr's text would let it
// read a table the entity's policies do not guard: a subquery starts with
// one of them. FROM is not among them, since EXTRACT, TRIM and SUBSTRING
// use it, and it cannot start a subquery.
//
// The functions after them read a table, a file or a server's state, write
// a sequence, run SQL from a string, or stall the connection, without a
// subquery: PostgreSQL's query_to_xml family, ts_rewrite and ts_stat,
// MySQL's LOAD_FILE and SLEEP, SQLite's load_extension.
//
// The list is a best-effort guard against a developer's mistake, not a
// sandbox: the text is the program's, never a user's, and a function the
// database's owner defined can still read what it likes.
var exprBanned = map[string]bool{
	"SELECT": true, "WITH": true, "TABLE": true, "VALUES": true,
	"UNION": true, "INTERSECT": true, "EXCEPT": true,
	"INTO": true, "RETURNING": true, "PRAGMA": true, "ATTACH": true,
	// PostgreSQL
	"CURSOR_TO_XML": true, "CURSOR_TO_XMLSCHEMA": true,
	"NEXTVAL": true, "SETVAL": true, "CURRVAL": true, "LASTVAL": true,
	"SET_CONFIG": true, "CURRENT_SETTING": true,
	"TS_REWRITE": true, "TS_STAT": true,
	// MySQL
	"LOAD_FILE": true, "SLEEP": true, "BENCHMARK": true,
	"GET_LOCK": true, "RELEASE_LOCK": true, "RELEASE_ALL_LOCKS": true,
	// SQLite
	"LOAD_EXTENSION": true, "READFILE": true, "WRITEFILE": true, "EDIT": true,
}

// exprBannedPrefix are the starts of the names exprBanned refuses as a
// family: PostgreSQL's catalog and admin functions (pg_), large objects
// (lo_), dblink, and the X_to_xml functions that read a query, a table, a
// schema or a database.
var exprBannedPrefix = []string{"PG_", "LO_", "DBLINK", "QUERY_TO_", "TABLE_TO_", "SCHEMA_TO_", "DATABASE_TO_"}

// exprWordBanned reports whether the word w may not appear in an Expr.
func exprWordBanned(w string) bool {
	u := strings.ToUpper(w)
	if exprBanned[u] {
		return true
	}
	for _, p := range exprBannedPrefix {
		if strings.HasPrefix(u, p) {
			return true
		}
	}
	return false
}

// ExprNode makes the condition text, in which each ? is filled by the next
// of slots: a column index of the statement's entity, a term, or the next
// of args, which is bound. It returns an OpInvalid node, never an error, as
// the other typed conditions do, so the statement that holds it fails.
//
// The text is written as given, in parentheses, and must be what a
// condition needs and no more (checkExprText): a string literal of plain
// characters, but no other quote, semicolon, comment, backslash or $, no
// unbalanced parenthesis, and no word that starts a subquery or names a
// function that reads past the row (exprWordBanned). A condition on another
// table is HasWith or InSelect, whose target's policies apply.
//
// A condition holds no window function, in its text or a term's: SQL
// computes windows after WHERE and HAVING, and refuses one there.
func ExprNode(text string, slots []ExprSlot, args []any) Node {
	d, err := newExpr(text, slots, args)
	if err == nil && exprHasWindow(d) {
		err = fmt.Errorf("velox: Expr %q holds a window function (OVER): SQL computes windows after WHERE and HAVING; select it with Calc, or filter on it in an outer query with Raw", text)
	}
	if err != nil {
		return Node{Op: OpInvalid, V: err}
	}
	// Deep: the scope walks it, as an EXISTS, and Group marks what holds it.
	return Node{Op: OpExpr, V: d, Deep: d.rel}
}

// exprHasWindow reports whether d's text, or a term among its slots, holds
// a window function.
func exprHasWindow(d *exprData) bool {
	if d.window {
		return true
	}
	for _, s := range d.slots {
		if s.Term != nil && hasWindow(*s.Term) {
			return true
		}
	}
	return false
}

// newExpr checks text and makes the expression of it and its slots.
func newExpr(text string, slots []ExprSlot, args []any) (*exprData, error) {
	window, err := checkExprText(text)
	if err != nil {
		return nil, err
	}
	if n := strings.Count(text, "?"); n != len(slots) {
		if n > len(slots) {
			return nil, fmt.Errorf("velox: Expr %q has %d ?, and %d arguments: every ? is an argument's place, "+
				"so PostgreSQL's jsonb ?, ?| and ?& cannot be written; use the JSON column's HasKey, "+
				"or jsonb_exists, jsonb_exists_any or jsonb_exists_all", text, n, len(slots))
		}
		return nil, fmt.Errorf("velox: Expr %q has %d ?, and %d arguments", text, n, len(slots))
	}
	d := &exprData{parts: strings.Split(text, "?"), slots: slots, args: args, window: window, agg: !window && exprCallsAgg(text)}
	for _, s := range slots {
		d.rel = d.rel || s.Term != nil && hasRel(*s.Term)
	}
	return d.seal(), nil
}

// checkExprText refuses text that could name another table, end the
// condition or hide what it holds. It reads the text once. A string literal
// is 'x' with x holding no quote, backslash, ? or control character, so its
// end is the same in every dialect (MySQL and PostgreSQL differ on a
// backslash and on a doubled quote), and what is inside it is data: not
// scanned for words, comments or parentheses. Outside a literal the text may
// hold no other quote, semicolon, backslash, $ or #, no comment, no
// unbalanced parenthesis, no @ before a name (a MySQL variable) and no word
// exprWordBanned refuses.
//
// window reports whether the text holds the word OVER outside a literal: a
// window function, such as COUNT(*) OVER () or ROW_NUMBER() OVER
// (PARTITION BY ? ORDER BY ?). What its parentheses hold is checked as the
// rest of the text is, so a subquery or a refused function inside OVER is
// refused too.
func checkExprText(text string) (window bool, err error) {
	bad := func(what string) error {
		return fmt.Errorf("velox: Expr %q: %s: write a value as an argument, and a condition on another table as HasWith or InSelect", text, what)
	}
	if strings.TrimSpace(text) == "" {
		return false, errors.New("velox: Expr: the text is empty")
	}
	depth := 0
	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case c == '\'':
			end, why := exprLiteral(text, i)
			if why != "" {
				return false, bad("a string literal " + why + " (it is 'x' with no quote, backslash, ? or control character inside; pass anything else as an argument)")
			}
			i = end
		case isIdentStart(c):
			j := i
			for j < len(text) && (isIdentStart(text[j]) || text[j] >= '0' && text[j] <= '9') {
				j++
			}
			if exprWordBanned(text[i:j]) {
				return false, bad(fmt.Sprintf("the word %s is not allowed", text[i:j]))
			}
			window = window || strings.EqualFold(text[i:j], "OVER")
			i = j
		default:
			if err := exprChar(text, i, &depth, bad); err != nil {
				return false, err
			}
			i++
		}
	}
	if depth != 0 {
		return false, bad("a parenthesis is never closed")
	}
	return window, nil
}

// aggFuncs are the aggregate functions of the three dialects that
// exprCallsAgg finds in an expression's text, upper case.
var aggFuncs = map[string]bool{
	"SUM": true, "AVG": true, "MIN": true, "MAX": true, "COUNT": true, "TOTAL": true,
	"ARRAY_AGG": true, "STRING_AGG": true, "GROUP_CONCAT": true, "JSON_AGG": true, "JSONB_AGG": true,
	"JSON_GROUP_ARRAY": true, "JSON_GROUP_OBJECT": true, "JSON_ARRAYAGG": true, "JSON_OBJECTAGG": true,
	"BOOL_AND": true, "BOOL_OR": true, "EVERY": true, "BIT_AND": true, "BIT_OR": true, "BIT_XOR": true,
	"STDDEV": true, "STDDEV_POP": true, "STDDEV_SAMP": true, "VARIANCE": true, "VAR_POP": true, "VAR_SAMP": true,
}

// exprCallsAgg reports whether text, which checkExprText accepted, calls an
// aggregate function outside a literal: one of aggFuncs, whatever its case,
// followed by an opening parenthesis.
func exprCallsAgg(text string) bool {
	for i := 0; i < len(text); {
		switch c := text[i]; {
		case c == '\'':
			end, _ := exprLiteral(text, i)
			i = max(end, i+1)
		case isIdentStart(c):
			j := i
			for j < len(text) && (isIdentStart(text[j]) || text[j] >= '0' && text[j] <= '9') {
				j++
			}
			k := j
			for k < len(text) && (text[k] == ' ' || text[k] == '\t' || text[k] == '\n' || text[k] == '\r') {
				k++
			}
			if k < len(text) && text[k] == '(' && aggFuncs[strings.ToUpper(text[i:j])] {
				return true
			}
			i = j
		default:
			i++
		}
	}
	return false
}

// exprChar checks the byte of text at i, outside a literal and not a word's:
// a character that is never allowed, the start of a comment, or a
// parenthesis, which it counts in depth.
func exprChar(text string, i int, depth *int, bad func(string) error) error {
	c := text[i]
	switch {
	case c < ' ' && c != '\t' && c != '\n' && c != '\r':
		return bad("a control character")
	case strings.IndexByte("\"`;\\$#", c) >= 0:
		return bad(fmt.Sprintf("%q is not allowed", c))
	case c == '@' && exprVariable(text[i+1:]):
		return bad("'@' before a name (a MySQL variable) is not allowed")
	case strings.HasPrefix(text[i:], "--"), strings.HasPrefix(text[i:], "/*"), strings.HasPrefix(text[i:], "*/"):
		return bad(fmt.Sprintf("%q is not allowed", text[i:i+2]))
	case c == '(':
		*depth++
	case c == ')':
		if *depth--; *depth < 0 {
			return bad("a parenthesis closes what it did not open")
		}
	}
	return nil
}

// exprVariable reports whether rest, the text after an @, starts a MySQL
// variable: a name, a quoted name, or @ and a name (a system variable).
// PostgreSQL's operators @@, @> and @ are followed by a space or an
// operand's other start, so they pass.
func exprVariable(rest string) bool {
	rest = strings.TrimPrefix(rest, "@")
	return rest != "" && (isIdentStart(rest[0]) || rest[0] == '\'')
}

// exprLiteral reads the string literal that starts at text[i], a quote, and
// returns the index after its closing quote. why is not empty when it is
// not a literal Expr allows: unterminated, holding a backslash, ? or control
// character, or followed by a quote (a doubled quote).
func exprLiteral(text string, i int) (end int, why string) {
	for j := i + 1; j < len(text); j++ {
		switch c := text[j]; {
		case c == '\'':
			if j+1 < len(text) && text[j+1] == '\'' {
				return 0, "holds a doubled quote"
			}
			return j + 1, ""
		case c == '\\', c == '?', c < ' ':
			return 0, fmt.Sprintf("holds %q", c)
		}
	}
	return 0, "is not closed"
}

func isIdentStart(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// expr writes the OpExpr n.
func (w *writer) expr(n Node) { w.exprBody(n.V.(*exprData)) }

// exprBody writes d: its text in parentheses, with its columns written as a
// condition's column is, its terms as terms and its values bound.
func (w *writer) exprBody(d *exprData) {
	w.str("(")
	next := 0
	for i, p := range d.parts {
		w.str(p)
		if i == len(d.slots) {
			break
		}
		switch s := d.slots[i]; {
		case s.Term != nil:
			w.term(*s.Term)
		case s.Col >= 0:
			if w.in.Columns[s.Col].Codec {
				w.fail(errCodecExpr(w.in, int(s.Col)))
			}
			w.predCol(s.Col)
		default:
			w.arg(w.exprArg(d.args[next]))
			next++
		}
	}
	w.str(")")
}

// errCodecExpr is the error for an Expr that names the encoded column col
// of in: its stored bytes are not the value, and equality and order of them
// differ by dialect.
func errCodecExpr(in *EntityInfo, col int) error {
	return fmt.Errorf("velox: %s: column %s is an encoded column (JSON in velox-gen), which an Expr cannot name; a JSON handle's Text, Number and HasKey read a value inside it", in.Name, in.Columns[col].Name)
}

// exprKey appends what shapes an expression's text to the render cache's
// key: the text pieces and what each slot holds, sealed with it. The values
// are arguments.
func exprKey(k []byte, d *exprData) []byte { return append(k, d.shape...) }

// checkExprCols calls bad for each column the expression d names, in its
// slots and in the terms among them, and returns the first error.
func checkExprCols(d *exprData, bad func(int) error) error {
	for _, s := range d.slots {
		switch {
		case s.Term != nil:
			if err := checkTermCols(*s.Term, bad); err != nil {
				return err
			}
		case s.Col >= 0:
			if err := bad(int(s.Col)); err != nil {
				return err
			}
		}
	}
	return nil
}

// swapExpr is swapCols of an OpExpr: its columns 0 and 1 trade places.
func swapExpr(n Node) Node {
	n.V = swapExprData(n.V.(*exprData))
	return n
}

// swapExprData is a copy of d with columns 0 and 1 traded, in its terms too.
func swapExprData(d *exprData) *exprData {
	c := *d
	c.slots = make([]ExprSlot, len(d.slots))
	for i, s := range d.slots {
		switch {
		case s.Term != nil:
			t := swapTerm(*s.Term)
			c.slots[i] = ExprSlot{Col: s.Col, Term: &t}
		case s.Col >= 0:
			c.slots[i] = ExprSlot{Col: swapCol(s.Col)}
		default:
			c.slots[i] = s
		}
	}
	return c.seal()
}
