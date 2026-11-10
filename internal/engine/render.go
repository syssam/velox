package engine

import (
	"fmt"
	"math"
	"reflect"
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
	// joins are the statement's joins; joinAlias is the alias of the one whose
	// target's conditions are being written, "" outside it.
	joins     []JoinClause
	joinAlias string
	// path is the JSON path of the OpJSON whose kid is being written: its
	// column is then the value at the path. nil outside one.
	path *JSONPath
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
		quote(w.d, &w.b, s)
	}
}

func (w *writer) col(i int32) { w.ident(w.in.Columns[i].Name) }

// predCol writes a condition's column, qualified when w.qualify says so.
func (w *writer) predCol(i int32) {
	if w.path != nil {
		w.jsonValue(i, *w.path)
		return
	}
	w.colRef(i)
}

// colRef writes column i of a condition, qualified as the condition's place
// needs.
func (w *writer) colRef(i int32) {
	switch {
	case w.joinAlias != "" && w.depth == 0:
		w.ident(w.joinAlias)
		w.str(".")
	case w.qualify && w.depth == 0:
		w.table()
		w.str(".")
	}
	w.col(i)
}

func (w *writer) arg(v any) {
	w.args = append(w.args, v)
	if !w.argsOnly {
		placeholder(w.d, &w.b, len(w.args))
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

var cmpText = [...]string{OpEQ: " = ", OpNEQ: " <> ", OpGT: " > ", OpGTE: " >= ", OpLT: " < ", OpLTE: " <= "}

func (w *writer) selectStmt(st *Statement) {
	if st.Each != nil {
		w.eachSelect(st)
		return
	}
	joined := len(st.Joins) > 0
	w.joins, w.qualify = st.Joins, joined
	w.str("SELECT ")
	if st.GroupBy != 0 {
		w.groupKey(st.GroupBy - 1)
		w.str(", ")
	}
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
		w.selectColumns(st.Columns, joined)
		w.joinColumns()
	}
	w.str(" FROM ")
	w.table()
	w.joinClauses()
	w.where(st.Where)
	if st.GroupBy != 0 {
		w.str(" GROUP BY ")
		w.groupKey(st.GroupBy - 1)
	}
	w.orderBy(st.OrderBy, joined)
	w.qualify = false
	w.selectTail(st)
}

// joinColumns writes the columns of every joined target, each under the join's
// alias, after the statement's own.
func (w *writer) joinColumns() {
	for i := range w.joins {
		t := w.joins[i].Link.Target
		for c := range t.Columns {
			w.str(", ")
			w.ident(joinAlias(i + 1))
			w.str(".")
			w.ident(t.Columns[c].Name)
		}
	}
}

// joinColumnAlias is the name a joined column goes by inside a per-owner
// window, whose derived table could not hold two columns of one name.
func joinColumnAlias(join, col int) string { return joinAlias(join+1) + "_" + strconv.Itoa(col) }

// joinColumnsAs is joinColumns for the inside of a window: each column under
// its own name, which the outer query reads.
func (w *writer) joinColumnsAs() {
	for i := range w.joins {
		t := w.joins[i].Link.Target
		for c := range t.Columns {
			w.str(", ")
			w.ident(joinAlias(i + 1))
			w.str(".")
			w.ident(t.Columns[c].Name)
			w.str(" AS ")
			w.ident(joinColumnAlias(i, c))
		}
	}
}

// joinColumnsOuter reads, in the query around a window, the columns joinColumnsAs named.
func (w *writer) joinColumnsOuter() {
	for i := range w.joins {
		for c := range w.joins[i].Link.Target.Columns {
			w.str(", ")
			w.ident(joinColumnAlias(i, c))
		}
	}
}

// joinClauses writes the joins after the FROM: each target under its alias,
// on the owner's key equal to the target's, and the target's Read conditions.
// A condition of a left join belongs here and not in the WHERE: a row without
// a visible target stays.
func (w *writer) joinClauses() {
	for i := range w.joins {
		j := &w.joins[i]
		alias := joinAlias(i + 1)
		if j.Left {
			w.str(" LEFT JOIN ")
		} else {
			w.str(" JOIN ")
		}
		owner := w.in
		w.in = j.Link.Target
		w.table()
		w.str(" AS ")
		w.ident(alias)
		w.str(" ON ")
		w.ident(alias)
		w.str(".")
		w.col(int32(j.Link.TargetCol))
		w.str(" = ")
		w.in = owner
		w.table()
		w.str(".")
		w.col(int32(j.Link.OwnerCol))
		w.in, w.joinAlias, w.alias = j.Link.Target, alias, alias
		for _, p := range j.On {
			w.str(" AND ")
			w.pred(p.n)
		}
		w.in, w.joinAlias, w.alias = owner, "", ""
	}
}

// joined writes the conditions of an OpJoined node, on the target of the join
// the node names. They are written under the join's alias, and are the
// statement's own conditions: on a left join they keep only rows that have
// a target.
func (w *writer) joined(n Node) {
	i, ok := joinOf(w.joins, n.Col)
	if !ok || w.depth != 0 || w.joinAlias != "" {
		w.fail(fmt.Errorf("velox: %s: a condition on a joined relation needs Join or LeftJoin of it in the same query, and cannot be inside Has or another joined condition", w.in.Name))
		return
	}
	in, alias := w.in, w.alias
	w.in, w.joinAlias, w.alias = w.joins[i].Link.Target, joinAlias(i+1), joinAlias(i+1)
	if len(n.Kids) == 0 {
		w.str("1 = 1")
	}
	for k, c := range n.Kids {
		if k > 0 {
			w.str(" AND ")
		}
		w.pred(c)
	}
	w.in, w.joinAlias, w.alias = in, "", alias
}

// groupKey writes a grouped aggregate's key column.
func (w *writer) groupKey(col int) {
	if w.in.Columns[col].Codec {
		w.fail(errCodecGroupBy(w.in, col))
	}
	w.col(int32(col))
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
		if o.Rel != 0 {
			w.joinedOrder(o)
			continue
		}
		if w.in.Columns[o.Column].Codec {
			w.fail(errCodecOrderBy(w.in, o.Column))
		}
		// NULL sorts after every value, ascending, as PostgreSQL sorts it:
		// MySQL and SQLite sort it first, so a term of IS NULL goes before.
		// After's condition counts on it.
		null := w.in.Columns[o.Column].Nullable && w.d != Postgres
		for k := range 2 {
			if k == 0 && !null {
				continue
			}
			if qualified {
				w.table()
				w.str(".")
			}
			w.col(int32(o.Column))
			if k == 0 {
				w.str(" IS NULL")
			}
			if o.Desc {
				w.str(" DESC")
			}
			if k == 0 {
				w.str(", ")
			}
		}
	}
}

// joinedOrder writes an ordering by a column of a joined target.
func (w *writer) joinedOrder(o OrderTerm) {
	i, ok := joinOf(w.joins, o.Rel-1)
	if !ok {
		w.fail(fmt.Errorf("velox: %s: an ordering by a joined relation needs Join or LeftJoin of it in the same query", w.in.Name))
		return
	}
	t := w.joins[i].Link.Target
	if o.Column < 0 || o.Column >= len(t.Columns) {
		w.fail(fmt.Errorf("velox: %s: an ordering names column %d of %s, which has %d", w.in.Name, o.Column, t.Name, len(t.Columns)))
		return
	}
	if t.Columns[o.Column].Codec {
		w.fail(errCodecOrderBy(t, o.Column))
	}
	// NULL sorts after every value here too, as orderBy puts it. The column
	// is NULL when it is nullable, or when a left join found no target.
	null := (t.Columns[o.Column].Nullable || w.joins[i].Left) && w.d != Postgres
	for k := range 2 {
		if k == 0 && !null {
			continue
		}
		w.ident(joinAlias(i + 1))
		w.str(".")
		w.ident(t.Columns[o.Column].Name)
		if k == 0 {
			w.str(" IS NULL")
		}
		if o.Desc {
			w.str(" DESC")
		}
		if k == 0 {
			w.str(", ")
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
			w.str(noLimit(w.d))
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
	// A joined relation: its columns follow the target's own, and every column
	// is named by its table.
	w.joins = st.Joins
	qual := join || len(st.Joins) > 0
	if window {
		w.reserved(st.Columns)
		w.str("SELECT ")
		w.selectColumns(st.Columns, false)
		w.joinColumnsOuter()
		if join {
			w.str(", ")
			w.ident(eachOwner)
		}
		w.str(" FROM (")
	}
	w.str("SELECT ")
	w.selectColumns(st.Columns, qual)
	if window {
		w.joinColumnsAs()
	} else {
		w.joinColumns()
	}
	if join {
		w.str(", ")
		w.eachKey(c)
		w.str(" AS ")
		w.ident(eachOwner)
	}
	if window {
		w.str(", ROW_NUMBER() OVER (PARTITION BY ")
		w.eachKey(c)
		w.orderBy(st.OrderBy, qual)
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
		w.orderBy(st.OrderBy, qual)
	}
	w.selectTail(st)
}

// eachCount writes CountEach's statement: SELECT <owner key>, COUNT(*) …
// GROUP BY <owner key>, a row for each owner with rows. An ordering a Rewrite
// hook sets is not written: the rows are counts, one per owner.
func (w *writer) eachCount(st *Statement) {
	w.joins = st.Joins
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
		name := w.in.Columns[c].Name
		for j := range w.joins {
			for k := range w.joins[j].Link.Target.Columns {
				if name == joinColumnAlias(j, k) {
					w.fail(fmt.Errorf("velox: %s: the column %q has the name of an alias velox gives a joined column in a per-owner read", w.in.Name, name))
				}
			}
		}
		if name == eachRank || name == eachOwner {
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
	w.joinClauses()
	sep := " WHERE "
	if !keysWritten {
		w.str(sep)
		w.eachKeys(c, func() { w.eachKey(c) })
		sep = " AND "
	}
	w.qualify = c.Join != nil || len(w.joins) > 0
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
	if len(w.joins) > 0 {
		w.table()
		w.str(".")
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

func (w *writer) pred(n Node) {
	if n.Op == OpJSON {
		w.json(n)
		return
	}
	if n.Op >= OpEQ && n.Op <= OpFold && w.in.Columns[n.Col].Codec && w.path == nil {
		// Every condition that holds a value; IsNull and NotNull hold none.
		w.fail(errCodecCondition(w.in, int(n.Col)))
	}
	if (n.Op == OpEQ || n.Op == OpNEQ) && isNilArg(n.V) {
		w.predCol(n.Col)
		if n.Op == OpEQ {
			w.str(" IS NULL")
		} else {
			w.str(" IS NOT NULL")
		}
		return
	}
	switch n.Op {
	case OpEQ, OpNEQ, OpGT, OpGTE, OpLT, OpLTE:
		w.predCol(n.Col)
		w.str(cmpText[n.Op])
		w.arg(n.V)
	case OpIn, OpNotIn:
		vs := n.V.([]any)
		if len(vs) == 0 {
			if n.Op == OpIn {
				w.str("1 = 0")
			} else {
				w.str("1 = 1")
			}
			return
		}
		w.predCol(n.Col)
		if n.Op == OpNotIn {
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
	case OpBetween:
		w.predCol(n.Col)
		w.str(" BETWEEN ")
		w.arg(n.V)
		w.str(" AND ")
		w.arg(n.W)
	case OpLike:
		w.predCol(n.Col)
		w.str(" LIKE ")
		w.arg(n.V)
		// ! escapes on every dialect: a backslash would read differently
		// under MySQL's NO_BACKSLASH_ESCAPES and PostgreSQL's
		// standard_conforming_strings, and ! means the same under both.
		w.str(" ESCAPE '!'")
	case OpFold:
		w.str("LOWER(")
		w.predCol(n.Col)
		w.str(") = LOWER(")
		w.arg(n.V)
		w.str(")")
	case OpIsNull:
		w.predCol(n.Col)
		w.str(" IS NULL")
	case OpNotNull:
		w.predCol(n.Col)
		w.str(" IS NOT NULL")
	case OpAllow:
		// Only an Allow written into a Where or a Not reaches here: a
		// policy's Allow is dropped before rendering.
		w.str("1 = 1")
	case OpAnd, OpOr:
		if len(n.Kids) == 0 {
			if n.Op == OpAnd {
				w.str("1 = 1")
			} else {
				w.str("1 = 0")
			}
			return
		}
		w.str("(")
		for i, k := range n.Kids {
			if i > 0 {
				if n.Op == OpAnd {
					w.str(" AND ")
				} else {
					w.str(" OR ")
				}
			}
			w.pred(k)
		}
		w.str(")")
	case OpNot:
		w.str("NOT (")
		w.pred(n.Kids[0])
		w.str(")")
	case OpExists:
		w.exists(n)
	case OpThrough:
		w.through(n)
	case OpJoined:
		w.joined(n)
	default:
		w.fail(fmt.Errorf("velox: %s: a condition of unknown kind %d", w.in.Name, n.Op))
	}
}

// through writes Of on a many-to-many relation: an EXISTS on the join entity,
// correlated with the current table's column n.col, holding the conditions on
// the join's rows.
func (w *writer) through(n Node) {
	join := n.V.(*EntityInfo)
	in, outer, depth := w.in, w.alias, w.depth
	w.subquery(join, n.W.(int), in, int(n.Col))
	for _, k := range n.Kids {
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
// scope adds the target's Read conditions to it.
func (w *writer) exists(n Node) {
	l := n.V.(Link)
	owner, outer, depth := w.in, w.alias, w.depth
	if l.Join != nil {
		w.subquery(l.Join, l.JoinOwner, owner, l.OwnerCol)
		w.str(" AND ")
		w.subquery(l.Target, l.TargetCol, l.Join, l.JoinTarget)
	} else {
		w.subquery(l.Target, l.TargetCol, owner, l.OwnerCol)
	}
	for _, k := range n.Kids {
		w.str(" AND ")
		w.pred(k)
	}
	if l.Join != nil {
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

// RenderStmt returns a statement's SQL and arguments, or why it cannot be
// sent: a JSON value that does not encode, or a condition comparing a JSON
// column.
func RenderStmt(d Dialect, st *Statement) (string, []any, error) {
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

// nullCell reports whether v, the value of column col, binds as NULL: a nil
// pointer, a nil interface or, as colArg binds it, a nil []byte, in a nullable
// column without a codec. A codec encodes nil, as JSON null say, so such a
// column never holds the SQL NULL here.
func nullCell(in *EntityInfo, col int, v any) bool {
	c := &in.Columns[col]
	if !c.Nullable || c.Codec {
		return false
	}
	switch v := v.(type) {
	case nil:
		return true
	case []byte:
		return v == nil
	}
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Pointer && rv.IsNil()
}

// insertNulls says, for each entry of st.Columns, whether every row of the
// INSERT holds NULL there; nil when no column does. Such a column is written
// as the literal NULL and not as a parameter per row: the column is still
// written, so a DEFAULT of the column does not apply, as before, and the
// statement carries fewer arguments. buf is where the result lives when it
// fits, so that most statements do not allocate for it.
func insertNulls(st *Statement, buf []bool) []bool {
	if len(st.Rows) == 0 {
		return nil
	}
	var mask []bool
	for i, col := range st.Columns {
		if c := &st.Entity.Columns[col]; !c.Nullable || c.Codec {
			continue
		}
		all := true
		for _, row := range st.Rows {
			if i >= len(row) || !nullCell(st.Entity, col, row[i]) {
				all = false
				break
			}
		}
		if !all {
			continue
		}
		if mask == nil {
			if len(st.Columns) <= len(buf) {
				mask = buf[:len(st.Columns)]
			} else {
				mask = make([]bool, len(st.Columns))
			}
		}
		mask[i] = true
	}
	return mask
}

func (w *writer) insertStmt(st *Statement) {
	w.str("INSERT INTO ")
	w.table()
	w.str(" (")
	w.columns(st.Columns)
	w.str(") VALUES ")
	var buf [64]bool
	nulls := insertNulls(st, buf[:])
	for r, row := range st.Rows {
		if r > 0 {
			w.str(", ")
		}
		w.str("(")
		for i, v := range row {
			if i > 0 {
				w.str(", ")
			}
			if nulls != nil && nulls[i] {
				w.str("NULL")
				continue
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
		w.bumpVersion()
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
	w.bumpVersion()
	w.qualify = true
	w.where(cf.Where)
	w.qualify = false
}

// bumpVersion adds 1 to the existing row's Version column in an upsert's
// update branch, naming it by its table: on MySQL a bare name is ambiguous
// beside the alias new, and on PostgreSQL it may be EXCLUDED's.
func (w *writer) bumpVersion() {
	v, ok := w.in.VersionColumn()
	if !ok {
		return
	}
	w.str(", ")
	w.col(int32(v))
	w.str(" = ")
	w.table()
	w.str(".")
	w.col(int32(v))
	w.str(" + 1")
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
			w.arith(a, " + ")
			continue
		case AssignSub:
			w.arith(a, " - ")
			continue
		}
		w.colArg(a.Column, a.Value)
	}
	w.where(st.Where)
}

// arith writes the column plus or minus the value. PostgreSQL and MySQL raise on
// an integer that overflows. SQLite makes it a REAL and stores it, and a later Scan
// of the row fails; nor does it know the field's range. So there an integer column
// writes CASE WHEN … THEN abs(-9223372036854775808) ELSE x END, as abs of the least
// integer is SQLite's error of integer overflow. An int64's condition is
// typeof(x) = 'real', as the REAL below the least integer rounds to it and compares
// equal; a narrower field's is x NOT BETWEEN its range. A NULL column stays NULL.
func (w *writer) arith(a Assignment, op string) {
	if w.in.Columns[a.Column].Codec {
		w.fail(errCodecAdd(w.in, a.Column))
	}
	x := func() {
		w.col(int32(a.Column))
		w.str(op)
		w.colArg(a.Column, a.Value)
	}
	lo, hi, ok := intRange(w.in.Columns[a.Column].Type)
	if w.d != SQLite || !ok {
		x()
		return
	}
	if lo == math.MinInt64 {
		w.str("CASE WHEN typeof(")
		x()
		w.str(") = 'real'")
	} else {
		w.str("CASE WHEN ")
		x()
		w.str(" NOT BETWEEN " + strconv.FormatInt(lo, 10) + " AND " + strconv.FormatInt(hi, 10))
	}
	w.str(" THEN abs(-9223372036854775808) ELSE ")
	x()
	w.str(" END")
}

// intRange is what a field of type t, a reflect.Type, can hold that SQLite can: the
// range of an integer kind, or of a pointer to one; ok is false for any other kind.
// A uint or uint64 stops at SQLite's largest integer.
func intRange(t any) (lo, hi int64, ok bool) {
	if rtype(t) == nil {
		return 0, 0, false
	}
	b := base(t)
	switch b.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return -1 << (b.Bits() - 1), 1<<(b.Bits()-1) - 1, true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if b.Bits() == 64 {
			return 0, math.MaxInt64, true
		}
		return 0, 1<<b.Bits() - 1, true
	}
	return 0, 0, false
}

func (w *writer) deleteStmt(st *Statement) {
	w.str("DELETE FROM ")
	w.table()
	w.where(st.Where)
}
