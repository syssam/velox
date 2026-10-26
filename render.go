package velox

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// writer renders a statement. With argsOnly it collects the arguments and
// writes no text: the text is cached (render_cache.go), and one code path for
// both keeps the arguments in the order of the placeholders.
type writer struct {
	d        Dialect
	in       *EntityInfo
	b        strings.Builder
	args     []any
	argsOnly bool
	alias    string // the alias of the subquery being written, and its nesting
	depth    int
	// qualify names the table before each column of a condition outside any
	// subquery: an upsert's condition is on the existing row, and on
	// PostgreSQL a bare column there may also be EXCLUDED's.
	qualify bool
	// err is the first error writing found: a JSON value that does not
	// encode, or a condition comparing a JSON column. The text and
	// arguments are then not used.
	err error
}

func (w *writer) fail(err error) {
	if w.err == nil {
		w.err = err
	}
}

func (w *writer) str(s string) {
	if !w.argsOnly {
		w.b.WriteString(s)
	}
}

func (w *writer) ident(s string) {
	if !w.argsOnly {
		w.d.quote(&w.b, s)
	}
}

func (w *writer) col(i int32) { w.ident(w.in.Columns[i].Name) }

// predCol writes a condition's column, qualified when w.qualify says so.
func (w *writer) predCol(i int32) {
	if w.qualify && w.depth == 0 {
		w.table()
		w.str(".")
	}
	w.col(i)
}

func (w *writer) arg(v any) {
	w.args = append(w.args, v)
	if !w.argsOnly {
		w.d.placeholder(&w.b, len(w.args))
	}
}

// colArg binds v, a value of column col: as it is, or when the column is an
// encoded one, as the bytes its codec returns, which on SQLite and MySQL are
// bound as a string when they are valid UTF-8 (textArg). A nil []byte in a
// nullable column binds an untyped nil, NULL whatever the driver makes of a
// nil []byte. Every value written to a column goes through it, so that only
// the arguments hold the bytes (hooks see v).
func (w *writer) colArg(col int, v any) {
	if bs, ok := v.([]byte); ok && bs == nil && w.in.Columns[col].Nullable {
		v = nil
	}
	if w.in.Columns[col].Codec {
		b, err := encodeCol(w.in, col, v)
		if err != nil {
			w.fail(err)
		}
		v = b
		if bs, ok := b.([]byte); ok && textArg(w.d, bs) {
			v = string(bs)
		}
	}
	w.arg(v)
}

// textArg reports whether the encoded value bs is bound as a string on
// dialect d. SQLite stores a []byte as a BLOB whatever the column's affinity,
// and its JSON functions read a BLOB as JSONB; a string is stored as TEXT.
// MySQL's driver, with interpolateParams=true, writes a []byte as a binary
// string, which a JSON column refuses (error 3144); a string it writes as
// text. Bytes that are not valid UTF-8, which only a binary codec returns,
// stay []byte (a BLOB on SQLite) rather than becoming a string that is not
// text. PostgreSQL's drivers take a []byte for jsonb and json as the
// document.
func textArg(d Dialect, bs []byte) bool {
	return (d == SQLite || d == MySQL) && utf8.Valid(bs)
}

var cmpText = [...]string{opEQ: " = ", opNEQ: " <> ", opGT: " > ", opGTE: " >= ", opLT: " < ", opLTE: " <= "}

func (w *writer) selectStmt(st *Statement) {
	if st.Each != nil {
		w.eachSelect(st)
		return
	}
	w.str("SELECT ")
	switch {
	case st.Count:
		w.str("COUNT(*)")
	case st.Exists:
		w.str("1")
	case st.Aggregate != 0:
		w.str(aggregateSQL[st.Aggregate])
		w.str("(")
		if len(st.Columns) != 1 {
			w.fail(fmt.Errorf("velox: %s: an aggregate reads one column, not %d", w.in.Name, len(st.Columns)))
		} else {
			w.col(int32(st.Columns[0]))
		}
		w.str(")")
	default:
		w.selectColumns(st.Columns, false)
	}
	w.str(" FROM ")
	w.table()
	w.where(st.Where)
	w.orderBy(st.OrderBy, false)
	w.selectTail(st)
}

// selectColumns writes the columns cs of a SELECT, every column when cs is
// nil, each qualified by the table when qualified says so.
func (w *writer) selectColumns(cs []int, qualified bool) {
	n := len(cs)
	if cs == nil {
		n = len(w.in.Columns)
	}
	for i := range n {
		if i > 0 {
			w.str(", ")
		}
		if qualified {
			w.table()
			w.str(".")
		}
		if cs == nil {
			w.col(int32(i))
		} else {
			w.col(int32(cs[i]))
		}
	}
}

// orderBy writes ORDER BY os, when there are any, each column qualified by
// the table when qualified says so.
func (w *writer) orderBy(os []OrderTerm, qualified bool) {
	for i, o := range os {
		if i == 0 {
			w.str(" ORDER BY ")
		} else {
			w.str(", ")
		}
		if w.in.Columns[o.Column].Codec {
			w.fail(errCodecOrderBy(w.in, o.Column))
		}
		if qualified {
			w.table()
			w.str(".")
		}
		w.col(int32(o.Column))
		if o.Desc {
			w.str(" DESC")
		}
	}
}

// selectTail writes a SELECT's LIMIT, OFFSET and lock.
func (w *writer) selectTail(st *Statement) {
	if st.Limit > 0 {
		w.str(" LIMIT ")
		w.arg(st.Limit)
	}
	if st.Offset > 0 {
		if st.Limit == 0 && w.d != Postgres {
			w.str(" LIMIT ")
			w.str(w.d.noLimit())
		}
		w.str(" OFFSET ")
		w.arg(st.Offset)
	}
	if st.Lock != 0 {
		w.str(lockSQL[st.Lock])
		if st.LockWait != 0 {
			w.str(lockWaitSQL[st.LockWait])
		}
	}
}

// The aliases of a per-owner read: the join entity, the owner's key read
// through it, the row's rank within its owner, and the ranked rows. The rank
// and the owner's key share the outer query's names with the target's
// columns, so a windowed read refuses a target column of either name.
const (
	eachJoin  = "vx_j"
	eachOwner = "vx_owner"
	eachRank  = "vx_rn"
	eachRows  = "vx"
)

// eachSelect writes a SELECT with Each: the rows of each owner apart. With
// a per-owner Limit, a derived table ranks each owner's rows with
// ROW_NUMBER() in the statement's order, and the outer query keeps the first
// Limit of each; every condition, the scope's included, is inside it, so a
// row they leave out takes no rank. A many-to-many read joins the join entity
// and reads the owner's key from it, last; its columns are qualified by the
// target's table, as are the top-level columns of its conditions. With
// Count, it counts each owner's rows instead (eachCount).
func (w *writer) eachSelect(st *Statement) {
	c := st.Each
	if st.Exists || st.Aggregate != 0 {
		w.fail(fmt.Errorf("velox: %s: a per-owner read cannot test for a row or aggregate", w.in.Name))
		return
	}
	if st.Count {
		w.eachCount(st)
		return
	}
	window, join := c.windowed(w.in), c.Join != nil
	if window {
		w.reserved(st.Columns)
		w.str("SELECT ")
		w.selectColumns(st.Columns, false)
		if join {
			w.str(", ")
			w.ident(eachOwner)
		}
		w.str(" FROM (")
	}
	w.str("SELECT ")
	w.selectColumns(st.Columns, join)
	if join {
		w.str(", ")
		w.eachKey(c)
		w.str(" AS ")
		w.ident(eachOwner)
	}
	if window {
		w.str(", ROW_NUMBER() OVER (PARTITION BY ")
		w.eachKey(c)
		w.orderBy(st.OrderBy, join)
		w.str(") AS ")
		w.ident(eachRank)
	}
	w.eachFrom(st)
	if window {
		w.str(") AS ")
		w.ident(eachRows)
		w.str(" WHERE ")
		w.ident(eachRank)
		w.str(" <= ")
		w.arg(c.Limit)
		w.str(" ORDER BY ")
		w.ident(eachRank)
	} else {
		w.orderBy(st.OrderBy, join)
	}
	w.selectTail(st)
}

// eachCount writes CountEach's statement: SELECT <owner key>, COUNT(*) …
// GROUP BY <owner key>, a row for each owner with rows. An ordering a Rewrite
// hook sets is not written: the rows are counts, one per owner.
func (w *writer) eachCount(st *Statement) {
	w.str("SELECT ")
	w.eachKey(st.Each)
	w.str(", COUNT(*)")
	w.eachFrom(st)
	w.str(" GROUP BY ")
	w.eachKey(st.Each)
	w.selectTail(st)
}

// reserved refuses a selected column (every column when cs is nil) named as
// one of the window's aliases.
func (w *writer) reserved(cs []int) {
	n := len(cs)
	if cs == nil {
		n = len(w.in.Columns)
	}
	for i := range n {
		c := i
		if cs != nil {
			c = cs[i]
		}
		if name := w.in.Columns[c].Name; name == eachRank || name == eachOwner {
			w.fail(fmt.Errorf("velox: %s: the column %q has the name of an alias velox gives a per-owner read; it cannot be read with Rel.LimitEach", w.in.Name, name))
		}
	}
}

// eachFrom writes a per-owner read's FROM and WHERE: the statement's table,
// joined to the join entity for a many-to-many read, its rows limited to the
// owners' keys, and the statement's conditions, qualified by the table when
// it joins.
//
// A join entity that is unique on its owner and target columns (its primary
// key, or a unique index, holds only them) is joined as it is. Any other may
// hold a link twice; it is joined as SELECT DISTINCT over the two columns of
// the owners' rows, so that a target is each owner's once, as a plain With
// loads it, and takes one rank or one count.
func (w *writer) eachFrom(st *Statement) {
	c := st.Each
	w.str(" FROM ")
	w.table()
	keysWritten := false
	if c.Join != nil {
		w.str(" JOIN ")
		in := w.in
		w.in = c.Join
		if pairUnique(c.Join, c.JoinOwner, c.JoinTarget) {
			w.table()
		} else {
			w.str("(SELECT DISTINCT ")
			w.col(int32(c.JoinOwner))
			w.str(", ")
			w.col(int32(c.JoinTarget))
			w.str(" FROM ")
			w.table()
			w.str(" WHERE ")
			w.eachKeys(c, func() { w.col(int32(c.JoinOwner)) })
			w.str(")")
			keysWritten = true
		}
		w.in = in
		w.str(" AS ")
		w.ident(eachJoin)
		w.str(" ON ")
		w.ident(eachJoin)
		w.str(".")
		w.ident(c.Join.Columns[c.JoinTarget].Name)
		w.str(" = ")
		w.table()
		w.str(".")
		w.col(int32(c.Target))
	}
	sep := " WHERE "
	if !keysWritten {
		w.str(sep)
		w.eachKeys(c, func() { w.eachKey(c) })
		sep = " AND "
	}
	w.qualify = c.Join != nil
	for _, p := range st.Where {
		w.str(sep)
		w.pred(p.n)
		sep = " AND "
	}
	w.qualify = false
}

// eachKeys writes the condition that a row is an owner's: the column key
// writes IN the owners' keys, or 1 = 0 when there are none.
func (w *writer) eachKeys(c *EachClause, key func()) {
	if len(c.Keys) == 0 {
		w.str("1 = 0")
		return
	}
	key()
	w.str(" IN (")
	for i, k := range c.Keys {
		if i > 0 {
			w.str(", ")
		}
		w.arg(k)
	}
	w.str(")")
}

// pairUnique reports whether no two of j's rows can hold one pair of values
// in columns a and b: j's primary key, or a unique index, is made of them.
func pairUnique(j *EntityInfo, a, b int) bool {
	within := func(cols []int) bool {
		for _, c := range cols {
			if c != a && c != b {
				return false
			}
		}
		return len(cols) > 0
	}
	if within(j.PrimaryKey) {
		return true
	}
	for _, ix := range j.Indexes {
		if ix.Unique && within(ix.Columns) {
			return true
		}
	}
	return false
}

// eachKey writes the column that holds a row's owner's key: the join
// entity's, through its alias, or the statement's entity's own.
func (w *writer) eachKey(c *EachClause) {
	if c.Join != nil {
		w.ident(eachJoin)
		w.str(".")
		w.ident(c.Join.Columns[c.JoinOwner].Name)
		return
	}
	w.col(int32(c.Column))
}

// isNilArg reports a value that SQL compares with IS NULL, not =. It
// recognizes an untyped nil and a nil []byte only: a nil value of another type
// (a nil *int64 in an any, say) is compared with =, which matches nothing.
func isNilArg(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case []byte:
		return x == nil
	}
	return false
}

func (w *writer) pred(n node) {
	if n.op >= opEQ && n.op <= opFold && w.in.Columns[n.col].Codec {
		// Every condition that holds a value; IsNull and NotNull hold none.
		w.fail(errCodecCondition(w.in, int(n.col)))
	}
	if (n.op == opEQ || n.op == opNEQ) && isNilArg(n.v) {
		w.predCol(n.col)
		if n.op == opEQ {
			w.str(" IS NULL")
		} else {
			w.str(" IS NOT NULL")
		}
		return
	}
	switch n.op {
	case opEQ, opNEQ, opGT, opGTE, opLT, opLTE:
		w.predCol(n.col)
		w.str(cmpText[n.op])
		w.arg(n.v)
	case opIn, opNotIn:
		vs := n.v.([]any)
		if len(vs) == 0 {
			if n.op == opIn {
				w.str("1 = 0")
			} else {
				w.str("1 = 1")
			}
			return
		}
		w.predCol(n.col)
		if n.op == opNotIn {
			w.str(" NOT IN (")
		} else {
			w.str(" IN (")
		}
		for i, v := range vs {
			if i > 0 {
				w.str(", ")
			}
			w.arg(v)
		}
		w.str(")")
	case opBetween:
		w.predCol(n.col)
		w.str(" BETWEEN ")
		w.arg(n.v)
		w.str(" AND ")
		w.arg(n.w)
	case opLike:
		w.predCol(n.col)
		w.str(" LIKE ")
		w.arg(n.v)
		// ! escapes on every dialect: a backslash would read differently
		// under MySQL's NO_BACKSLASH_ESCAPES and PostgreSQL's
		// standard_conforming_strings, and ! means the same under both.
		w.str(" ESCAPE '!'")
	case opFold:
		w.str("LOWER(")
		w.predCol(n.col)
		w.str(") = LOWER(")
		w.arg(n.v)
		w.str(")")
	case opIsNull:
		w.predCol(n.col)
		w.str(" IS NULL")
	case opNotNull:
		w.predCol(n.col)
		w.str(" IS NOT NULL")
	case opAllow:
		// Only an Allow written into a Where or a Not reaches here: a
		// policy's Allow is dropped before rendering.
		w.str("1 = 1")
	case opAnd, opOr:
		if len(n.kids) == 0 {
			if n.op == opAnd {
				w.str("1 = 1")
			} else {
				w.str("1 = 0")
			}
			return
		}
		w.str("(")
		for i, k := range n.kids {
			if i > 0 {
				if n.op == opAnd {
					w.str(" AND ")
				} else {
					w.str(" OR ")
				}
			}
			w.pred(k)
		}
		w.str(")")
	case opNot:
		w.str("NOT (")
		w.pred(n.kids[0])
		w.str(")")
	case opExists:
		w.exists(n)
	case opThrough:
		w.through(n)
	default:
		w.fail(fmt.Errorf("velox: %s: a condition of unknown kind %d", w.in.Name, n.op))
	}
}

// through writes Of on a many-to-many relation: an EXISTS on the join entity,
// correlated with the current table's column n.col, holding the conditions on
// the join's rows.
func (w *writer) through(n node) {
	join := n.v.(*EntityInfo)
	in, outer, depth := w.in, w.alias, w.depth
	w.subquery(join, n.w.(int), in, int(n.col))
	for _, k := range n.kids {
		w.str(" AND ")
		w.pred(k)
	}
	w.str(")")
	w.in, w.alias, w.depth = in, outer, depth
}

// exists writes EXISTS over a relation. The subquery's table is aliased
// vx<depth>, so it is told apart from the outer table even when they are the
// same table; the correlation names the outer side by its alias, or by its
// table at the top; the target's conditions are unqualified and bind to the
// innermost table. A many-to-many relation is an EXISTS on the join entity
// holding one on the target, which holds the conditions; the inner EXISTS is
// written even with none: it asserts that the target row exists, and the
// scope adds the target's tenant and Read conditions to it.
func (w *writer) exists(n node) {
	l := n.v.(link)
	owner, outer, depth := w.in, w.alias, w.depth
	if l.join != nil {
		w.subquery(l.join, l.joinOwner, owner, l.ownerCol)
		w.str(" AND ")
		w.subquery(l.target, l.targetCol, l.join, l.joinTarget)
	} else {
		w.subquery(l.target, l.targetCol, owner, l.ownerCol)
	}
	for _, k := range n.kids {
		w.str(" AND ")
		w.pred(k)
	}
	if l.join != nil {
		w.str(")")
	}
	w.str(")")
	w.in, w.alias, w.depth = owner, outer, depth
}

// subquery opens an EXISTS on in, aliased one level deeper, correlated by
// in.Columns[col] = from.Columns[fromCol], where from is the table of w.alias,
// or the statement's table when there is no alias yet. It leaves w writing
// conditions on in.
func (w *writer) subquery(in *EntityInfo, col int, from *EntityInfo, fromCol int) {
	w.depth++
	alias := "vx" + strconv.Itoa(w.depth)
	w.str("EXISTS (SELECT 1 FROM ")
	w.in = in
	w.table()
	w.str(" AS ")
	w.ident(alias)
	w.str(" WHERE ")
	w.ident(alias)
	w.str(".")
	w.col(int32(col))
	w.str(" = ")
	if w.alias != "" {
		w.ident(w.alias)
	} else {
		w.in = from
		w.table()
		w.in = in
	}
	w.str(".")
	w.ident(from.Columns[fromCol].Name)
	w.alias = alias
}

// renderStmt returns a statement's SQL and arguments, or why it cannot be
// sent: a JSON value that does not encode, or a condition comparing a JSON
// column.
func renderStmt(d Dialect, st *Statement) (string, []any, error) {
	w := writer{d: d, in: st.Entity}
	w.statement(st)
	if w.err != nil {
		return "", nil, w.err
	}
	return w.b.String(), w.args, nil
}

// statement writes a statement of any kind.
func (w *writer) statement(st *Statement) {
	switch st.Op {
	case OpSelect:
		w.selectStmt(st)
	case OpInsert:
		w.insertStmt(st)
	case OpUpdate:
		w.updateStmt(st)
	case OpDelete:
		w.deleteStmt(st)
	default:
		panic(fmt.Sprintf("velox: render: unknown statement kind %d", st.Op))
	}
}

// table writes the entity's table, qualified by its schema when it has one.
func (w *writer) table() {
	if w.in.Schema != "" {
		w.ident(w.in.Schema)
		w.str(".")
	}
	w.ident(w.in.Table)
}

func (w *writer) where(ps []Predicate) {
	for i, p := range ps {
		if i == 0 {
			w.str(" WHERE ")
		} else {
			w.str(" AND ")
		}
		w.pred(p.n)
	}
}

func (w *writer) columns(cs []int) {
	for i, c := range cs {
		if i > 0 {
			w.str(", ")
		}
		w.col(int32(c))
	}
}

func (w *writer) insertStmt(st *Statement) {
	w.str("INSERT INTO ")
	w.table()
	w.str(" (")
	w.columns(st.Columns)
	w.str(") VALUES ")
	for r, row := range st.Rows {
		if r > 0 {
			w.str(", ")
		}
		w.str("(")
		for i, v := range row {
			if i > 0 {
				w.str(", ")
			}
			w.colArg(st.Columns[i], v)
		}
		w.str(")")
	}
	if st.Conflict != nil {
		w.conflict(st.Conflict)
	}
	if len(st.Returning) > 0 && w.d != MySQL {
		w.str(" RETURNING ")
		w.columns(st.Returning)
	}
}

// conflict writes the upsert clause. On PostgreSQL and SQLite the update
// branch's condition names the existing row's columns by its table. MySQL
// has neither a conflict target nor DO NOTHING: ON DUPLICATE KEY UPDATE
// applies to any unique key, keeping the existing row is a no-op assignment,
// and the inserted values come from the row alias new (MySQL 8.4 and
// later). The alias makes an unqualified column name on the right side
// ambiguous (error 1052), so the no-op assignment qualifies it with the
// table.
func (w *writer) conflict(cf *ConflictClause) {
	keep := cf.DoNothing || len(cf.Update) == 0
	if w.d == MySQL {
		w.str(" AS new ON DUPLICATE KEY UPDATE ")
		if keep {
			w.col(int32(cf.Columns[0]))
			w.str(" = ")
			w.table()
			w.str(".")
			w.col(int32(cf.Columns[0]))
			return
		}
		for i, c := range cf.Update {
			if i > 0 {
				w.str(", ")
			}
			w.col(int32(c))
			w.str(" = new.")
			w.col(int32(c))
		}
		return
	}
	w.str(" ON CONFLICT (")
	w.columns(cf.Columns)
	w.str(")")
	if keep {
		w.str(" DO NOTHING")
		return
	}
	w.str(" DO UPDATE SET ")
	for i, c := range cf.Update {
		if i > 0 {
			w.str(", ")
		}
		w.col(int32(c))
		w.str(" = EXCLUDED.")
		w.col(int32(c))
	}
	w.qualify = true
	w.where(cf.Where)
	w.qualify = false
}

func (w *writer) updateStmt(st *Statement) {
	w.str("UPDATE ")
	w.table()
	w.str(" SET ")
	for i, a := range st.Set {
		if i > 0 {
			w.str(", ")
		}
		w.col(int32(a.Column))
		w.str(" = ")
		switch a.Op {
		case AssignAdd:
			w.col(int32(a.Column))
			w.str(" + ")
		case AssignSub:
			w.col(int32(a.Column))
			w.str(" - ")
		}
		if a.Op != AssignSet && w.in.Columns[a.Column].Codec {
			w.fail(errCodecAdd(w.in, a.Column))
		}
		w.colArg(a.Column, a.Value)
	}
	w.where(st.Where)
}

func (w *writer) deleteStmt(st *Statement) {
	w.str("DELETE FROM ")
	w.table()
	w.where(st.Where)
}
