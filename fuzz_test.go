package velox

import (
	"strings"
	"testing"

	"github.com/syssam/velox2/internal/engine"
)

// fuzzStatement builds a statement on invInfo from fuzz input: each byte picks
// an operation, a column and whether the value is s.
func fuzzStatement(ops []byte, s string) *Statement {
	st := &Statement{Op: OpSelect, Entity: invInfo}
	n := len(invInfo.Columns)
	var preds []engine.Node
	for i, b := range ops {
		col := int32(int(b>>4) % n)
		var v any = s
		if b&1 == 1 {
			v = int64(i)
		}
		switch b % 7 {
		case 0:
			preds = append(preds, engine.Node{Op: engine.OpEQ, Col: col, V: v})
		case 1:
			in := engine.Node{Op: engine.OpIn, Col: col, V: []any{v, s}}
			if b&2 == 2 { // Of through a join entity, invInfo itself
				in = engine.Node{Op: engine.OpThrough, Col: col, V: invInfo, W: int(b>>5) % n, Kids: []engine.Node{in}}
			}
			preds = append(preds, in)
		case 2:
			preds = append(preds, engine.Node{Op: engine.OpLike, Col: col, V: s})
		case 3:
			preds = append(preds, engine.Node{Op: engine.OpBetween, Col: col, V: v, W: s})
		case 4:
			if len(preds) > 1 {
				preds = []engine.Node{{Op: engine.OpOr, Kids: preds}}
			}
		case 5:
			if len(preds) > 0 {
				preds[len(preds)-1] = engine.Node{Op: engine.OpNot, Kids: []engine.Node{preds[len(preds)-1]}}
			}
		case 6:
			st.OrderBy = append(st.OrderBy, OrderTerm{Column: int(col), Desc: b&2 == 2})
		}
	}
	for _, p := range preds {
		st.Where = append(st.Where, engine.NewPredicate(p))
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
	f.Add([]byte{15, 43, 4}, "j") // Of through a join entity, in an Or
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
			var c engine.RenderCache
			cachedRender(&c, d, st)
			cached, cargs := cachedRender(&c, d, fuzzStatement(ops, s))
			if cached != sql || !argsEqual(cargs, args) {
				t.Fatalf("%v: the cache renders differently:\n%s\n%s", d, sql, cached)
			}
		}
	})
}

// fuzzWrites builds a write of each kind on invInfo from fuzz input, indexed
// by fuzzInsert, fuzzUpdate and fuzzDelete. The bytes of ops choose, and s is
// every string value:
//   - INSERT: 1-3 distinct columns from ops[0:3], 1-3 rows, and by ops[0]'s
//     bits an ON CONFLICT clause (DoNothing, Update of every column written,
//     fuzzStatement's predicates as its Where) and RETURNING;
//   - UPDATE: 1-3 assignments, each AssignSet, AssignAdd or AssignSub, with
//     fuzzStatement's predicates as Where;
//   - DELETE: fuzzStatement's predicates as Where.
func fuzzWrites(ops []byte, s string) [3]*Statement {
	n := len(invInfo.Columns)
	pick := func(k int) byte {
		if len(ops) == 0 {
			return 0
		}
		return ops[k%len(ops)]
	}
	val := func(k int) any {
		if pick(k)&1 == 1 {
			return int64(k)
		}
		return s
	}
	where := fuzzStatement(ops, s).Where
	head := pick(0)

	ins := &Statement{Op: OpInsert, Entity: invInfo}
	var seen uint
	for k := 0; k < 3 && k < len(ops); k++ {
		if c := int(ops[k]>>4) % n; seen&(1<<c) == 0 {
			seen |= 1 << c
			ins.Columns = append(ins.Columns, c)
		}
	}
	if len(ins.Columns) == 0 {
		ins.Columns = []int{1}
	}
	for r := range 1 + int(head>>6)%3 {
		row := make([]any, len(ins.Columns))
		for i := range row {
			row[i] = val(r*3 + i + 1)
		}
		ins.Rows = append(ins.Rows, row)
	}
	if head&1 == 1 {
		ins.Conflict = &ConflictClause{Columns: []int{ins.Columns[0]}, DoNothing: head&2 == 2, Where: where}
		if head&4 == 4 {
			ins.Conflict.Update = ins.Columns
		}
	}
	if head&16 == 16 {
		ins.Returning = []int{0, int(pick(1)) % n}
	}

	upd := &Statement{Op: OpUpdate, Entity: invInfo, Where: where}
	for i := range 1 + int(pick(1))%3 {
		b := pick(i + 2)
		upd.Set = append(upd.Set, Assignment{Column: int(b>>4) % n, Op: AssignSet + AssignOp(b%3), Value: val(i + 2)})
	}

	return [3]*Statement{ins, upd, {Op: OpDelete, Entity: invInfo, Where: where}}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

const (
	fuzzInsert = iota
	fuzzUpdate
	fuzzDelete
)

// FuzzRenderWrite is FuzzRender for INSERT, UPDATE and DELETE. Whatever the
// input, on every dialect: the SQL has as many placeholders as arguments, its
// quoted identifiers balance, it does not depend on any argument's value, and
// the cache renders it identically. Beyond these, which a statement that drops
// an argument would still pass: the statement keeps its kind's shape (the
// verb, the table, RETURNING and the conflict clause exactly where the
// statement and dialect have them), and its arguments begin with the values it
// writes, in order, the Where's after them. A statement render refuses is
// skipped.
func FuzzRenderWrite(f *testing.F) {
	f.Add([]byte{1 | 2 | 16, 0x21, 0x32}, "x") // DO NOTHING, RETURNING
	f.Add([]byte{1 | 4 | 16, 0x20, 0x41, 0x52, 0, 17}, "x")
	f.Add([]byte{5 | 64, 2, 0x11, 0x22, 7, 14, 17}, "y") // upsert with a Where; a Sub, three rows
	f.Add([]byte{0, 1, 2, 0x13}, "z")                    // plain; an Add
	f.Add([]byte{0, 2, 1, 0x13, 0x25}, "z")              // three assignments
	f.Add([]byte{0, 3, 0, 0x14, 0x25, 7, 8, 9, 10, 11}, "k")
	f.Add([]byte{}, "")
	f.Add([]byte{1 | 4 | 16, 0, 7, 21, 33, 45}, `x'; DROP TABLE invoices; --`)
	f.Add([]byte{8 | 64, 16, 33}, "a\"b`c?$1")
	f.Add([]byte{0, 1, 2, 3, 6, 64}, `FROM "invoices" WHERE`)
	f.Add([]byte{0, 1, 2, 3, 6, 64}, "invoices")
	f.Add([]byte{15, 43, 4}, "j")
	f.Fuzz(func(t *testing.T, ops []byte, s string) {
		if len(ops) > 64 {
			ops = ops[:64]
		}
		verbs := [...]string{"INSERT INTO ", "UPDATE ", "DELETE FROM "}
		for kind, verb := range verbs {
			for _, d := range []Dialect{Postgres, MySQL, SQLite} {
				st := fuzzWrites(ops, s)[kind]
				sql, args, err := engine.RenderStmt(d, st)
				if err != nil {
					continue
				}
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
				var quoted strings.Builder
				engine.QuoteIdent(d, &quoted, invInfo.Table)
				if !strings.HasPrefix(sql, verb+quoted.String()) {
					t.Fatalf("%v: not a %s on %s: %s", d, verb, quoted.String(), sql)
				}
				// The leading arguments are the values written, in order.
				var written []any
				for _, row := range st.Rows {
					written = append(written, row...)
				}
				for _, a := range st.Set {
					written = append(written, a.Value)
				}
				if len(args) < len(written) || !argsEqual(args[:len(written)], written) {
					t.Fatalf("%v: arguments %v do not begin with the values written %v: %s", d, args, written, sql)
				}
				upsert := st.Conflict != nil
				// An upsert's Where is its update branch's: not on MySQL, and not
				// when it keeps the existing row.
				hasWhere := len(st.Where) > 0
				if upsert {
					hasWhere = len(st.Conflict.Where) > 0 && d != MySQL && !st.Conflict.DoNothing && len(st.Conflict.Update) > 0
				}
				for _, c := range []struct {
					text string
					want bool
				}{
					{" RETURNING ", len(st.Returning) > 0 && d != MySQL},
					{" ON CONFLICT (", upsert && d != MySQL},
					{" ON DUPLICATE KEY UPDATE ", upsert && d == MySQL},
					{" WHERE ", hasWhere},
				} {
					if got := strings.Contains(sql, c.text); got != c.want {
						t.Fatalf("%v: %q present = %v, want %v: %s", d, c.text, got, c.want, sql)
					}
				}
				if kind == fuzzUpdate {
					set, _, _ := strings.Cut(sql, " WHERE ")
					var adds, subs int
					for _, a := range st.Set {
						adds += btoi(a.Op == AssignAdd)
						subs += btoi(a.Op == AssignSub)
					}
					if strings.Count(set, " + ") != adds || strings.Count(set, " - ") != subs {
						t.Fatalf("%v: %d additions and %d subtractions expected: %s", d, adds, subs, sql)
					}
				}
				other := s + "\x00~"
				if osql, _, err := engine.RenderStmt(d, fuzzWrites(ops, other)[kind]); err != nil || osql != sql {
					t.Fatalf("%v: the SQL depends on an argument's value (%v):\n%s\n%s", d, err, sql, osql)
				}
				var c engine.RenderCache
				cachedRender(&c, d, st)
				cached, cargs := cachedRender(&c, d, fuzzWrites(ops, s)[kind])
				if cached != sql || !argsEqual(cargs, args) {
					t.Fatalf("%v: the cache renders differently:\n%s\n%s", d, sql, cached)
				}
			}
		}
	})
}
