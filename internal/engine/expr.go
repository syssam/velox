package engine

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// exprData is the V of an OpExpr: the text split at each ?, and what fills
// each ? in order. A slot whose col is not negative is that column of the
// condition's entity; any other slot is the next value of args.
type exprData struct {
	parts []string // len(cols)+1 pieces of text
	cols  []int32  // -1 for a value
	args  []any    // the values, in order
}

// exprBanned are the words whose presence in an Expr's text would let it
// read a table the entity's policies do not guard: a subquery starts with
// one of them. FROM is not among them, since EXTRACT, TRIM and SUBSTRING
// use it, and it cannot start a subquery.
var exprBanned = map[string]bool{
	"SELECT": true, "WITH": true, "TABLE": true, "VALUES": true,
	"UNION": true, "INTERSECT": true, "EXCEPT": true,
	"INTO": true, "RETURNING": true, "PRAGMA": true, "ATTACH": true,
}

// ExprNode makes the condition text, in which each ? is filled by the next
// of cols: a column index of the statement's entity, or -1 for the next of
// args, which is bound. It returns an OpInvalid node, never an error, as
// the other typed conditions do, so the statement that holds it fails.
//
// The text is written as given, in parentheses, and must be what a
// condition needs and no more (checkExprText): a string literal of plain
// characters, but no other quote, semicolon, comment, backslash or $, no
// unbalanced parenthesis, and no word that starts a subquery (exprBanned). A
// condition on another table is HasWith or InSelect, whose target's policies
// apply.
func ExprNode(text string, cols []int32, args []any) Node {
	if err := checkExprText(text); err != nil {
		return Node{Op: OpInvalid, V: err}
	}
	if n := strings.Count(text, "?"); n != len(cols) {
		return Node{Op: OpInvalid, V: fmt.Errorf("velox: Expr %q has %d ?, and %d arguments", text, n, len(cols))}
	}
	return Node{Op: OpExpr, V: &exprData{parts: strings.Split(text, "?"), cols: cols, args: args}}
}

// checkExprText refuses text that could name another table, end the
// condition or hide what it holds. It reads the text once. A string literal
// is 'x' with x holding no quote, backslash, ? or control character, so its
// end is the same in every dialect (MySQL and PostgreSQL differ on a
// backslash and on a doubled quote), and what is inside it is data: not
// scanned for words, comments or parentheses. Outside a literal the text may
// hold no other quote, semicolon, backslash, $ or #, no comment, no
// unbalanced parenthesis and no exprBanned word.
func checkExprText(text string) error {
	bad := func(what string) error {
		return fmt.Errorf("velox: Expr %q: %s: write a value as an argument, and a condition on another table as HasWith or InSelect", text, what)
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("velox: Expr: the text is empty")
	}
	depth := 0
	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case c == '\'':
			end, why := exprLiteral(text, i)
			if why != "" {
				return bad("a string literal " + why + " (it is 'x' with no quote, backslash, ? or control character inside; pass anything else as an argument)")
			}
			i = end
		case isIdentStart(c):
			j := i
			for j < len(text) && (isIdentStart(text[j]) || text[j] >= '0' && text[j] <= '9') {
				j++
			}
			if exprBanned[strings.ToUpper(text[i:j])] {
				return bad(fmt.Sprintf("the word %s is not allowed", text[i:j]))
			}
			i = j
		default:
			if err := exprChar(text, i, &depth, bad); err != nil {
				return err
			}
			i++
		}
	}
	if depth != 0 {
		return bad("a parenthesis is never closed")
	}
	return nil
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

// expr writes the OpExpr n: its text in parentheses, with its columns
// written as a condition's column is and its values bound.
func (w *writer) expr(n Node) {
	d := n.V.(*exprData)
	w.str("(")
	next := 0
	for i, p := range d.parts {
		w.str(p)
		if i == len(d.cols) {
			break
		}
		if c := d.cols[i]; c >= 0 {
			if w.in.Columns[c].Codec {
				w.fail(errCodecExpr(w.in, int(c)))
			}
			w.predCol(c)
			continue
		}
		w.arg(d.args[next])
		next++
	}
	w.str(")")
}

// errCodecExpr is the error for an Expr that names the encoded column col
// of in: its stored bytes are not the value, and equality and order of them
// differ by dialect.
func errCodecExpr(in *EntityInfo, col int) error {
	return fmt.Errorf("velox: %s: column %s is an encoded column (JSON in velox-gen), which an Expr cannot name; a JSON handle's Text, Number and HasKey read a value inside it", in.Name, in.Columns[col].Name)
}

// exprKey appends what shapes an OpExpr's text to the render cache's key:
// the text pieces and which column each slot holds. The values are
// arguments.
func exprKey(k []byte, d *exprData) []byte {
	k = binary.AppendUvarint(k, uint64(len(d.cols)))
	for i, p := range d.parts {
		k = binary.AppendUvarint(k, uint64(len(p)))
		k = append(k, p...)
		if i < len(d.cols) {
			k = binary.AppendUvarint(k, uint64(d.cols[i]+1))
		}
	}
	return k
}

// checkExprCols refuses an OpExpr that names a column its entity does not
// have.
func checkExprCols(n Node, bad func(int) error) error {
	for _, c := range n.V.(*exprData).cols {
		if c >= 0 {
			if err := bad(int(c)); err != nil {
				return err
			}
		}
	}
	return nil
}

// swapExpr is swapCols of an OpExpr: its columns 0 and 1 trade places.
func swapExpr(n Node) Node {
	d := *n.V.(*exprData)
	d.cols = make([]int32, len(d.cols))
	copy(d.cols, n.V.(*exprData).cols)
	for i, c := range d.cols {
		switch c {
		case 0:
			d.cols[i] = 1
		case 1:
			d.cols[i] = 0
		}
	}
	n.V = &d
	return n
}
