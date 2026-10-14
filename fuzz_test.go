package velox

import (
	"strings"
	"testing"
)

// unquote reverses Dialect.quote: it strips the outer quote characters and
// halves the doubled ones; ok is false when s is not one quoted identifier.
func unquote(d Dialect, s string) (string, bool) {
	q := byte('"')
	if d == MySQL {
		q = '`'
	}
	if len(s) < 2 || s[0] != q || s[len(s)-1] != q {
		return "", false
	}
	var b strings.Builder
	in := s[1 : len(s)-1]
	for i := 0; i < len(in); i++ {
		if in[i] == q {
			if i+1 >= len(in) || in[i+1] != q {
				return "", false // a lone quote would end the identifier
			}
			i++
		}
		b.WriteByte(in[i])
	}
	return b.String(), true
}

func FuzzQuote(f *testing.F) {
	for _, s := range []string{"id", `a"b`, "a`b", `""`, "``", "", "x y", `\`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, ident string) {
		for _, d := range []Dialect{Postgres, MySQL, SQLite} {
			var b strings.Builder
			d.quote(&b, ident)
			got, ok := unquote(d, b.String())
			if !ok || got != ident {
				t.Fatalf("%v: %q quoted as %s does not read back (%q, %v)", d, ident, b.String(), got, ok)
			}
		}
	})
}

// fuzzStatement builds a statement on invInfo from fuzz input: each byte picks
// an operation, a column and whether the value is s.
func fuzzStatement(ops []byte, s string) *Statement {
	st := &Statement{Op: OpSelect, Entity: invInfo}
	n := len(invInfo.Columns)
	var preds []node
	for i, b := range ops {
		col := int32(int(b>>4) % n)
		var v any = s
		if b&1 == 1 {
			v = int64(i)
		}
		switch b % 7 {
		case 0:
			preds = append(preds, node{op: opEQ, col: col, v: v})
		case 1:
			preds = append(preds, node{op: opIn, col: col, v: []any{v, s}})
		case 2:
			preds = append(preds, node{op: opLike, col: col, v: s})
		case 3:
			preds = append(preds, node{op: opBetween, col: col, v: v, w: s})
		case 4:
			if len(preds) > 1 {
				preds = []node{{op: opOr, kids: preds}}
			}
		case 5:
			if len(preds) > 0 {
				preds[len(preds)-1] = node{op: opNot, kids: []node{preds[len(preds)-1]}}
			}
		case 6:
			st.OrderBy = append(st.OrderBy, OrderTerm{Column: int(col), Desc: b&2 == 2})
		}
	}
	for _, p := range preds {
		st.Where = append(st.Where, Predicate{p})
	}
	if len(ops) > 0 && ops[0]&8 == 8 {
		st.Op, st.OrderBy = OpUpdate, nil
		st.Set = []Assignment{{Column: 1, Op: AssignSet, Value: s}}
	}
	if len(ops) > 0 && ops[0]&64 == 64 {
		st.Limit = len(ops)
	}
	return st
}

// placeholders counts the placeholders outside quoted identifiers.
func placeholders(d Dialect, sql string) int {
	q := byte('"')
	if d == MySQL {
		q = '`'
	}
	n, quoted := 0, false
	for i := 0; i < len(sql); i++ {
		c := sql[i]
		switch {
		case c == q:
			quoted = !quoted
		case quoted:
		case d == Postgres && c == '$':
			n++
		case d != Postgres && c == '?':
			n++
		}
	}
	return n
}

// FuzzRender is Review Focus 4: whatever the input, the SQL has as many
// placeholders as arguments, does not depend on any argument's value, keeps
// its quoted identifiers balanced, and the cache renders it identically.
func FuzzRender(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6}, `x'; DROP TABLE invoices; --`)
	f.Add([]byte{8 | 64, 16, 33}, "a\"b`c?$1")
	f.Add([]byte{}, "")
	// Values equal to parts of the statement itself.
	f.Add([]byte{0, 1, 2, 3, 6, 64}, `FROM "invoices" WHERE`)
	f.Add([]byte{0, 1, 2, 3, 6, 64}, "invoices")
	f.Fuzz(func(t *testing.T, ops []byte, s string) {
		if len(ops) > 64 {
			ops = ops[:64]
		}
		for _, d := range []Dialect{Postgres, MySQL, SQLite} {
			st := fuzzStatement(ops, s)
			sql, args := render(d, st)
			if got := placeholders(d, sql); got != len(args) {
				t.Fatalf("%v: %d placeholders for %d arguments: %s", d, got, len(args), sql)
			}
			q := `"`
			if d == MySQL {
				q = "`"
			}
			if strings.Count(sql, q)%2 != 0 {
				t.Fatalf("%v: unbalanced quotes: %s", d, sql)
			}
			// Argument text never reaches the SQL: the same statement built
			// with a different value renders to the same SQL. (A "the value is
			// not a substring of the SQL" check is wrong: a value such as
			// `invoices` or `FROM "invoices"` is a substring of the statement
			// itself.)
			other := s + "\x00~"
			if osql, _ := render(d, fuzzStatement(ops, other)); osql != sql {
				t.Fatalf("%v: the SQL depends on an argument's value:\n%s\n%s", d, sql, osql)
			}
			var c renderCache
			c.render(d, st)
			cached, cargs := c.render(d, fuzzStatement(ops, s))
			if cached != sql || !argsEqual(cargs, args) {
				t.Fatalf("%v: the cache renders differently:\n%s\n%s", d, sql, cached)
			}
		}
	})
}
