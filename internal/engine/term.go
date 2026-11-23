package engine

import (
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
)

// Projection is what a statement reads in place of its entity's columns: a
// list of terms, one result column each, grouped and filtered by terms and
// conditions of the same entity. The rows are read by position (Query.Rows).
// The entity's Read policy, scope and interceptors still filter them, as
// they filter every statement.
type Projection struct {
	Terms   []Term      // the result columns, in order
	GroupBy []Term      // GROUP BY, when not empty
	Having  []Predicate // HAVING: all must hold
}

type termKind uint8

const (
	termNone termKind = iota
	termColumn
	termExpr
	termAgg
	termInvalid
	termExcluded
	termJSON
	termRel      // an aggregate of a relation's rows: a correlated subquery (RelTerm)
	termDate     // a time truncated to a unit (DateTruncTerm)
	termGrouping // ROLLUP or CUBE of keys, a key of GROUP BY (GroupingTerm)
)

// TermFn is an aggregate function of a term.
type TermFn uint8

// The aggregate functions of a term. TermCountAll is COUNT(*), of no term.
const (
	TermSum TermFn = iota + 1
	TermAvg
	TermMin
	TermMax
	TermCount
	TermCountDistinct
	TermCountAll
)

var termFnSQL = [...]string{TermSum: "SUM", TermAvg: "AVG", TermMin: "MIN", TermMax: "MAX", TermCount: "COUNT", TermCountDistinct: "COUNT", TermCountAll: "COUNT"}

// Term is one SQL value of a statement's entity: a column, an expression
// (ExprTerm) or an aggregate of either (AggTerm). It is a result column of a
// Projection, a GROUP BY key, an operand of an Expr, or an ordering. The
// zero Term is no term.
type Term struct {
	kind  termKind
	col   int32
	x     *exprData
	fn    TermFn
	of    *Term
	path  *JSONPath // a termJSON's
	name  string    // the result column's name, NamedTerm's; "" for none
	err   error
	r     *relAgg  // a termRel's relation
	ext   *termExt // a termDate's or a termGrouping's
	shape string   // what shapes it in the render cache's key: see seal
}

// relAgg is the relation of a termRel: the EXISTS HasWith would make, whose
// kids are the conditions on the target, and, for After's value of one row,
// the owner's key the subquery is correlated with in place of the outer row.
type relAgg struct {
	n     Node // an OpExists of a relation: Col its index, V its RelRef or, once scoped, its Link
	bound bool // correlated with key, not with the outer row
	key   any
}

// seal sets t's shape from its kind, function, column and the shapes of what
// it holds, which are sealed already. The key builder appends it as it is, so
// that it does not recurse through terms and expressions: a cycle of
// functions that pass the key along makes it escape to the heap, and an
// allocation on every statement.
func (t Term) seal() Term {
	b := append(make([]byte, 0, 8+len(t.shape)), byte(t.kind), byte(t.fn))
	b = binary.AppendUvarint(b, uint64(t.col+1))
	switch t.kind {
	case termExpr:
		b = append(b, t.x.shape...)
	case termAgg:
		if t.of != nil {
			b = append(b, 1)
			b = append(b, t.of.shape...)
		} else {
			b = append(b, 0)
		}
	case termJSON:
		// The keys are arguments; the kind and their number shape the text.
		b = append(b, byte(t.path.Kind))
		b = binary.AppendUvarint(b, uint64(len(t.path.Keys)))
	case termRel:
		b = append(b, bit(t.r.bound))
		b = nodeKey(b, 0, t.r.n) // no dialect yet: the key holds for each
		b = append(b, t.of.shape...)
	case termDate:
		b = extKey(b, t.ext)
		b = append(b, t.of.shape...)
	case termGrouping:
		b = extKey(b, t.ext)
	}
	b = binary.AppendUvarint(b, uint64(len(t.name)))
	b = append(b, t.name...)
	t.shape = string(b)
	return t
}

// JSONTerm is the value at path p in the JSON column col, by p's kind, as
// the conditions of an OpJSON read it. A path that cannot be written alike
// on every dialect is an InvalidTerm.
func JSONTerm(col int32, p JSONPath) Term {
	if err := p.check(); err != nil {
		return InvalidTerm(err)
	}
	return Term{kind: termJSON, col: col, path: &p}.seal()
}

// NamedTerm is t named name as a result column of a Projection: SELECT t AS
// name. A name is a letter or an underscore, then letters, digits and
// underscores, at most 63 of them, so that it is the same identifier on
// every dialect. The name is written only in a Projection's result columns;
// t is the same term wherever else it stands.
func NamedTerm(t Term, name string) Term {
	if t.kind == termInvalid || t.kind == termNone {
		return t
	}
	if !plainName(name) {
		return InvalidTerm(fmt.Errorf("velox: the term name %q is not a letter or an underscore followed by up to 62 letters, digits or underscores", name))
	}
	t.name = name
	return t.seal()
}

// plainName reports whether s is a letter or an underscore, then letters,
// digits and underscores, 63 bytes at most: PostgreSQL's longest identifier.
func plainName(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for i := range len(s) {
		c := s[i]
		letter := c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !letter && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// unnamed is t without its name, the term wherever it is not a result
// column.
func unnamed(t Term) Term {
	if t.name == "" {
		return t
	}
	t.name = ""
	return t.seal()
}

// ColumnTerm is column col of the statement's entity.
func ColumnTerm(col int32) Term { return Term{kind: termColumn, col: col}.seal() }

// ExcludedTerm is column col of the row an upsert proposes: EXCLUDED.col on
// PostgreSQL and SQLite, new.col on MySQL. Only an upsert's update branch,
// its assignments and its condition, may hold it; anywhere else it fails
// the statement.
func ExcludedTerm(col int32) Term { return Term{kind: termExcluded, col: col}.seal() }

// InvalidTerm is a term that fails the statement holding it with err.
func InvalidTerm(err error) Term { return Term{kind: termInvalid, err: err}.seal() }

// ExprTerm is the expression text, in which each ? is filled by the next of
// slots, as ExprNode's. A bad text is an InvalidTerm.
func ExprTerm(text string, slots []ExprSlot, args []any) Term {
	d, err := newExpr(text, slots, args)
	if err != nil {
		return InvalidTerm(err)
	}
	return Term{kind: termExpr, x: d}.seal()
}

// AggTerm is fn applied to of; for TermCountAll, of is ignored. An aggregate
// of an aggregate, or of no term, is an InvalidTerm.
func AggTerm(fn TermFn, of Term) Term {
	if fn == TermCountAll {
		return Term{kind: termAgg, fn: fn}.seal()
	}
	switch of.kind {
	case termNone:
		return InvalidTerm(errors.New("velox: an aggregate of no term"))
	case termInvalid:
		return of
	case termAgg:
		return InvalidTerm(errors.New("velox: an aggregate of an aggregate: SUM(MAX(x)) is not allowed in one statement"))
	}
	if hasRel(of) {
		return InvalidTerm(errors.New("velox: an aggregate of a relation's aggregate, such as SUM of a Rel's Count, is not allowed"))
	}
	if hasWindow(of) {
		return InvalidTerm(errors.New("velox: an aggregate of a window function: SQL computes windows after aggregates"))
	}
	return Term{kind: termAgg, fn: fn, of: &of}.seal()
}

// RelTerm is agg, an aggregate of the target of the relation rel (HasWith's
// EXISTS, whose kids are conditions on the target), over each row's related
// rows: (SELECT agg FROM target WHERE key = the row's key AND kids…). Over
// no rows COUNT is 0 and the other aggregates NULL. The scope adds the
// target's row condition to the kids, as to an EXISTS's. agg must be an
// aggregate of the target, which AggTerm keeps from holding a relation's
// term; anything else is an InvalidTerm.
func RelTerm(rel Node, agg Term) Term {
	switch {
	case rel.Op == OpInvalid:
		return InvalidTerm(rel.V.(error))
	case rel.Op != OpExists || rel.Col < 0:
		return InvalidTerm(errors.New("velox: a relation's aggregate is of a relation"))
	case agg.kind == termInvalid:
		return agg
	case agg.kind != termAgg:
		return InvalidTerm(errors.New("velox: a relation's term is an aggregate of its target: Count, Sum, Avg, Min or Max"))
	}
	for _, k := range rel.Kids {
		if hasZero(k) {
			return InvalidTerm(errors.New("velox: a relation's aggregate: a condition holds the zero Pred"))
		}
		if named(k) {
			return InvalidTerm(errors.New("velox: a relation's aggregate takes typed conditions, not conditions by name"))
		}
		if err := PredErr(k); err != nil {
			return InvalidTerm(err)
		}
	}
	return Term{kind: termRel, fn: agg.fn, of: &agg, r: &relAgg{n: rel}}.seal()
}

// RelAgg is RelTerm of the relation ref with the conditions where, or err's
// InvalidTerm when the relation's handle carries one: Rel's Count and Agg.
// It is not generic, so that the work is compiled once.
func RelAgg(ref RelRef, where []Predicate, err error, agg Term) Term {
	if err != nil {
		return InvalidTerm(err)
	}
	kids := make([]Node, len(where))
	for i, p := range where {
		kids[i] = p.n
	}
	return RelTerm(Node{Op: OpExists, Col: ref.I, V: ref, Kids: kids}, agg)
}

// hasRel reports whether t is or holds a relation's aggregate (RelTerm).
func hasRel(t Term) bool {
	switch t.kind {
	case termRel:
		return true
	case termAgg:
		return t.of != nil && hasRel(*t.of)
	case termExpr:
		return t.x.rel
	}
	return false
}

// relNullable reports whether t, a termRel, can be NULL: every aggregate
// but a count is NULL over no rows.
func relNullable(t Term) bool {
	switch t.fn {
	case TermCount, TermCountAll, TermCountDistinct:
		return false
	}
	return true
}

// boundRel is t, a termRel, correlated with the owner's key value key in
// place of the outer row: the value t has for the row whose key it is.
func boundRel(t Term, key any) Term {
	r := *t.r
	r.bound, r.key = true, key
	t.r = &r
	return t.seal()
}

// problem is the error t carries: an invalid expression, an aggregate of an
// aggregate, or the zero Term used as one, or nil. It is not exported: an
// exported method of Term is in the type information of every package that
// mentions a Statement.
func (t Term) problem() error {
	switch t.kind {
	case termNone:
		return errors.New("velox: the zero Term; make terms with Pick, Calc and CountAll")
	case termInvalid:
		return t.err
	case termAgg, termRel, termDate:
		if t.of != nil {
			return t.of.problem()
		}
	case termGrouping:
		for _, k := range t.ext.set {
			if err := k.problem(); err != nil {
				return err
			}
		}
	case termExpr:
		for _, s := range t.x.slots {
			if s.Term != nil {
				if err := s.Term.problem(); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// term writes t: a column as a condition writes it, an expression in
// parentheses, an aggregate as FN(x).
func (w *writer) term(t Term) {
	switch t.kind {
	case termColumn:
		if w.in.Columns[t.col].Codec {
			w.fail(errCodecExpr(w.in, int(t.col)))
		}
		w.predCol(t.col)
	case termExcluded:
		switch {
		case !w.upsert:
			w.fail(fmt.Errorf("velox: %s: Excluded names the row an upsert proposes; only the upsert's UpdateSet and UpdateWhere may hold it", w.in.Name))
		case w.in.Columns[t.col].Codec:
			w.fail(errCodecExpr(w.in, int(t.col)))
		}
		w.excluded(t.col)
	case termExpr:
		w.exprBody(t.x)
	case termJSON:
		if !w.in.Columns[t.col].Codec {
			w.fail(fmt.Errorf("velox: %s: column %s is not a JSON column, which a JSON path needs", w.in.Name, w.in.Columns[t.col].Name))
			return
		}
		w.jsonValue(t.col, *t.path)
	case termAgg:
		if t.of != nil && t.of.kind == termColumn && decimalAgg(w.d, w.in, t.fn == TermSum || t.fn == TermAvg, t.of.col) {
			w.sqliteDecimalAgg(func() { w.aggTerm(t) })
			return
		}
		w.aggTerm(t)
	case termRel:
		w.relTerm(t)
	case termDate:
		w.dateTrunc(t)
	case termGrouping:
		w.fail(errGroupingPlace)
	default:
		w.fail(t.problem())
	}
}

// aggTerm writes the termAgg t as FN(x).
func (w *writer) aggTerm(t Term) {
	w.str(termFnSQL[t.fn])
	w.str("(")
	switch t.fn {
	case TermCountAll:
		w.str("*")
	default:
		if t.fn == TermCountDistinct {
			w.str("DISTINCT ")
		}
		w.term(*t.of)
	}
	w.str(")")
}

// relTerm writes the termRel t: a subquery on the relation's target, aliased
// as an EXISTS's is, correlated with the outer row's key (or, bound, with a
// value), holding the conditions on the target. A many-to-many relation reads
// the target's rows that a row of the join entity links to the owner: a link
// the join table holds twice counts once, as a load reads it.
func (w *writer) relTerm(t Term) {
	l, err := existsLink(t.r.n)
	if err != nil {
		w.fail(err)
		return
	}
	owner, outer, depth := w.in, w.alias, w.depth
	w.depth++
	alias := "vx" + strconv.Itoa(w.depth)
	w.in = l.Target
	w.str("(SELECT ")
	w.term(*t.of)
	w.str(" FROM ")
	w.table()
	w.str(" AS ")
	w.ident(alias)
	w.str(" WHERE ")
	if l.Join == nil {
		w.ident(alias)
		w.str(".")
		w.col(int32(l.TargetCol))
		w.str(" = ")
		w.relOwner(t, owner, outer, l.OwnerCol, l.TextKey)
	} else {
		w.depth++
		inner := "vx" + strconv.Itoa(w.depth)
		w.str("EXISTS (SELECT 1 FROM ")
		w.in = l.Join
		w.table()
		w.str(" AS ")
		w.ident(inner)
		w.str(" WHERE ")
		w.ident(inner)
		w.str(".")
		w.col(int32(l.JoinTarget))
		w.str(" = ")
		w.ident(alias)
		w.str(".")
		w.ident(l.Target.Columns[l.TargetCol].Name)
		w.str(" AND ")
		w.ident(inner)
		w.str(".")
		w.col(int32(l.JoinOwner))
		w.str(" = ")
		if t.r.bound {
			w.argOf(int32(l.JoinOwner), t.r.key)
		} else {
			w.relOwner(t, owner, outer, l.OwnerCol, false)
		}
		w.str(")")
		w.in = l.Target
	}
	w.alias = alias
	for _, k := range t.r.n.Kids {
		w.str(" AND ")
		w.pred(k)
	}
	w.str(")")
	w.in, w.alias, w.depth = owner, outer, depth
}

// relOwner writes the owner's side of a relation term's correlation: the
// bound key, or the outer row's column col, named by outer, the alias of
// the table around, or by the owner's table at the top.
func (w *writer) relOwner(t Term, owner *EntityInfo, outer string, col int, text bool) {
	if t.r.bound {
		in := w.in
		w.in = owner
		w.argOf(int32(col), t.r.key)
		w.in = in
		return
	}
	in := w.in
	w.in = owner
	w.outerCol(outer, col, text)
	w.in = in
}

// terms writes ts, separated by commas.
func (w *writer) terms(ts []Term) {
	for i, t := range ts {
		if i > 0 {
			w.str(", ")
		}
		w.term(t)
	}
}

// projectStmt writes a SELECT of st's Projection.
func (w *writer) projectStmt(st *Statement) {
	p := st.Project
	if w.d == MySQL && len(p.GroupBy) > 0 {
		w.keys = p
	}
	w.str("SELECT ")
	if st.Distinct {
		w.str("DISTINCT ")
	}
	for i, t := range p.Terms {
		if i > 0 {
			w.str(", ")
		}
		w.writeSpan(t, true, st.Distinct)
	}
	w.str(" FROM ")
	w.table()
	w.where(st.Where)
	if len(p.GroupBy) > 0 {
		w.groupBy(p.GroupBy)
	}
	for i, h := range p.Having {
		if i == 0 {
			w.str(" HAVING ")
		} else {
			w.str(" AND ")
		}
		w.pred(h.n)
	}
	w.grouped = len(p.GroupBy) > 0
	w.orderBy(st.OrderBy, false)
	w.grouped = false
	w.keys = nil
	w.qualify = false
	w.selectTail(st)
}

// resultTerm writes t as a result column of a projection: as term writes it,
// then AS its name, when it has one. An encoded column is read as it is
// stored, for velox.Scan to decode, unless the read is DISTINCT, which would
// compare its encodings.
func (w *writer) resultTerm(t Term, distinct bool) {
	switch {
	case t.kind == termColumn && w.in.Columns[t.col].Codec && !distinct:
		w.predCol(t.col)
	case w.anyKey(t):
		w.anyValue(t)
	default:
		w.term(t)
	}
	if t.name != "" {
		w.str(" AS ")
		w.ident(t.name)
	}
}

// orderTerm writes the ordering o by an expression term. NULL sorts after
// every value, ascending, as orderBy puts it: PostgreSQL sorts so, and where
// the database sorts it first, a term of IS NULL goes before. A term may be
// NULL whatever its columns are, so there it always does.
func (w *writer) orderTerm(o OrderTerm) {
	t, _ := o.Expr.(Term)
	// A relation's count is never NULL.
	null := w.d != Postgres && (t.kind != termRel || relNullable(t))
	// MySQL's ONLY_FULL_GROUP_BY takes an expression of the GROUP BY keys
	// only as GROUP BY writes it: x IS NULL is refused where the key is x.
	// ANY_VALUE of a key is the group's value, and lifts the check.
	// A term with a bound value that repeats a GROUP BY key is a copy whose
	// ? is another parameter: it is ANY_VALUE of it as well (anyKey).
	nullAny := w.d == MySQL && w.grouped && !hasAgg(t)
	key := w.anyKey(t)
	for k := range 2 {
		if k == 0 && !null {
			continue
		}
		switch {
		case k == 0 && nullAny, k == 1 && key:
			w.anyValue(t)
		case null || !w.repeatSpan(t): // PostgreSQL writes the term once
			w.term(t)
		}
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

// termKey appends what shapes t to the render cache's key: its kind, its
// function, its column and its text pieces, sealed with it. Values are
// arguments.
func termKey(k []byte, t Term) []byte { return append(k, t.shape...) }

// projectionKey appends a Projection's shape to the render cache's key; the
// key's flags say whether a statement has one.
func projectionKey(k []byte, d Dialect, p *Projection) []byte {
	k = binary.AppendUvarint(k, uint64(len(p.Terms)))
	for _, t := range p.Terms {
		k = termKey(k, t)
	}
	k = binary.AppendUvarint(k, uint64(len(p.GroupBy)))
	for _, t := range p.GroupBy {
		k = termKey(k, t)
	}
	k = binary.AppendUvarint(k, uint64(len(p.Having)))
	for _, h := range p.Having {
		k = nodeKey(k, d, h.n)
	}
	return k
}

// checkTermCols calls bad for each column t names, and returns the first
// error: bad's, or t's own.
func checkTermCols(t Term, bad func(int) error) error {
	if err := t.problem(); err != nil {
		return err
	}
	switch t.kind {
	case termColumn, termExcluded, termJSON:
		return bad(int(t.col))
	case termExpr:
		return checkExprCols(t.x, bad)
	case termAgg, termDate:
		if t.of != nil {
			return checkTermCols(*t.of, bad)
		}
	case termGrouping:
		for _, k := range t.ext.set {
			if err := checkTermCols(k, bad); err != nil {
				return err
			}
		}
	case termRel:
		l, err := existsLink(t.r.n)
		if err != nil {
			return err
		}
		if err := bad(l.OwnerCol); err != nil {
			return err
		}
		return checkTermCols(*t.of, func(c int) error {
			if c < 0 || c >= len(l.Target.Columns) {
				return fmt.Errorf("velox: a relation's aggregate names column %d of %s, which has %d", c, l.Target.Name, len(l.Target.Columns))
			}
			return nil
		})
	}
	return nil
}

// excludedCols appends to cols each column an ExcludedTerm in t names.
func excludedCols(cols []int, t Term) []int {
	switch t.kind {
	case termExcluded:
		if !slices.Contains(cols, int(t.col)) {
			cols = append(cols, int(t.col))
		}
	case termExpr:
		for _, s := range t.x.slots {
			if s.Term != nil {
				cols = excludedCols(cols, *s.Term)
			}
		}
	case termAgg, termDate:
		if t.of != nil {
			cols = excludedCols(cols, *t.of)
		}
	}
	return cols
}

// nodeExcluded appends to cols each column an ExcludedTerm in the
// expressions of n, or of its kids, names.
func nodeExcluded(cols []int, n Node) []int {
	if n.Op == OpExpr {
		if d, ok := n.V.(*exprData); ok {
			for _, s := range d.slots {
				if s.Term != nil {
					cols = excludedCols(cols, *s.Term)
				}
			}
		}
	}
	for _, k := range n.Kids {
		cols = nodeExcluded(cols, k)
	}
	return cols
}

// swapTerm is swapCols of a term: columns 0 and 1 trade places.
func swapTerm(t Term) Term {
	switch t.kind {
	case termColumn, termExcluded, termJSON:
		t.col = swapCol(t.col)
	case termExpr:
		t.x = swapExprData(t.x)
	case termAgg, termDate:
		if t.of != nil {
			o := swapTerm(*t.of)
			t.of = &o
		}
	case termGrouping:
		x := *t.ext
		x.set = make([]Term, len(t.ext.set))
		for i, k := range t.ext.set {
			x.set[i] = swapTerm(k)
		}
		t.ext = &x
	}
	return t.seal()
}

func swapCol(c int32) int32 {
	switch c {
	case 0:
		return 1
	case 1:
		return 0
	}
	return c
}

// checkProject checks st's Projection: it is a plain SELECT, its terms and
// conditions name only columns of the entity, and none is invalid. who says
// who made the statement, as in checkColumns.
func checkProject(st *Statement, who string) error {
	p := st.Project
	if p == nil {
		return nil
	}
	name := st.Entity.Name
	switch {
	case st.Op != OpSelect:
		return fmt.Errorf("velox: %s: a projection is a SELECT", name)
	case st.Count || st.Exists || st.Aggregate != 0 || st.GroupBy != 0:
		return fmt.Errorf("velox: %s: a projection is not Count, Exists or an aggregate; read its terms with Rows", name)
	case st.Each != nil || len(st.Joins) > 0:
		return fmt.Errorf("velox: %s: a projection cannot be read with Join, With or LimitEach", name)
	case st.Columns != nil:
		return fmt.Errorf("velox: %s: a projection cannot be read with Columns or Omit; its terms are its columns", name)
	case st.Lock != 0 || st.LockWait != 0:
		return fmt.Errorf("velox: %s: a projection cannot take a lock; GROUP BY and a lock do not mix", name)
	case len(p.Terms) == 0:
		return fmt.Errorf("velox: %s: a projection has no terms", name)
	}
	if slices.ContainsFunc(p.GroupBy, hasWindow) {
		return fmt.Errorf("velox: %s: GroupBy holds a window function, which SQL computes after GROUP BY; group by its terms and select the window with Calc", name)
	}
	if slices.ContainsFunc(p.Terms, hasRel) && (len(p.GroupBy) > 0 || slices.ContainsFunc(p.Terms, hasAgg)) || slices.ContainsFunc(p.GroupBy, hasRel) {
		return fmt.Errorf("velox: %s: a relation's aggregate (Rel's Count, Sum, …) is a value of each row, and cannot be selected beside GroupBy or an aggregate, nor grouped by", name)
	}
	bad := func(c int) error { return badColumn(st, who, c) }
	for _, ts := range [][]Term{p.Terms, p.GroupBy} {
		for _, t := range ts {
			if err := checkTermCols(t, bad); err != nil {
				return err
			}
		}
	}
	for _, h := range p.Having {
		if hasSubquery(h.n) {
			return fmt.Errorf("velox: %s: Having holds a relation or a subquery; it filters the groups, so test relations in Where", name)
		}
	}
	return checkConds(st, who, p.Having)
}

// hasSubquery reports whether n or a kid at any depth is an EXISTS (a
// relation, typed or by name, or a column's subquery) or an expression
// holding a relation's aggregate. Having refuses one: the
// scope adds a target's Read conditions to the EXISTS of Where only.
func hasSubquery(n Node) bool {
	switch n.Op {
	case OpExists, OpThrough, opNamedEdge, opNamedSub:
		return true
	case OpExpr:
		return n.V.(*exprData).rel
	}
	return slices.ContainsFunc(n.Kids, hasSubquery)
}

// checkOrderExprs checks the ordering terms of st that are expressions.
func checkOrderExprs(st *Statement, who string) error {
	for _, o := range st.OrderBy {
		if o.Expr == nil {
			continue
		}
		t, ok := o.Expr.(Term)
		if !ok {
			return fmt.Errorf("velox: %s: %s an ordering by an expression that is not a term", st.Entity.Name, who)
		}
		if err := checkTermCols(t, func(c int) error { return badColumn(st, who, c) }); err != nil {
			return err
		}
	}
	return nil
}

// cloneProject is a copy of p whose slices may be edited.
func cloneProject(p *Projection) *Projection {
	if p == nil {
		return nil
	}
	c := *p
	c.Terms, c.GroupBy, c.Having = slices.Clone(p.Terms), slices.Clone(p.GroupBy), slices.Clone(p.Having)
	return &c
}

// sameProject reports whether a and b are the same projection.
func sameProject(a, b *Projection) bool {
	if a == nil || b == nil {
		return a == b
	}
	return reflect.DeepEqual(a, b)
}

// errAfterExpr is the refusal of After, and of a cursor, on an ordering by an
// expression: they read the value of each ordering term from a row.
func errAfterExpr(entity string) error {
	return fmt.Errorf("velox: %s: After and a cursor cannot follow an ordering by an expression, which is not a column of a row; order by columns, or page with Limit and Offset", entity)
}
