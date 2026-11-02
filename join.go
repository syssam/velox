package velox

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"slices"
	"unsafe"

	"github.com/syssam/velox/internal/engine"
)

// joinSet is how a query reads its joins out of a row; it holds nothing generic,
// so that the query's type does not reach a joined entity's: bindOwner and the
// fills' closures are made by Join, which only a package that joins compiles.
type joinSet struct {
	fills []joinFill
	// bindOwner points dst at the owner's own columns, cols as in Statement.Columns.
	bindOwner func(cols []int, dst []any, owner unsafe.Pointer)
}

// joinFill reads one join's columns out of a row: the columns follow the
// entity's own, in the order of the query's joins.
type joinFill struct {
	width int // the target's column count
	// bind points dst, width long, at the fields of a new target, and returns
	// the function that, once the row is scanned, stores the target in the
	// owner's relation field.
	bind func(dst []any) func(owner unsafe.Pointer)
}

// width is how many columns the joins add to a row; 0 for no joins.
func (s *joinSet) width() int {
	if s == nil {
		return 0
	}
	n := 0
	for i := range s.fills {
		n += s.fills[i].width
	}
	return n
}

// scan scans the current row: the entity's own columns (rootN of them) into
// the owner, then the columns of each join into a new target stored in the
// owner's relation field.
func (s *joinSet) scan(r engine.Rows, cols []int, rootN int, dst []any, owner unsafe.Pointer) error {
	s.bindOwner(cols, dst[:rootN], owner)
	attach := make([]func(unsafe.Pointer), len(s.fills))
	off := rootN
	for i := range s.fills {
		attach[i] = s.fills[i].bind(dst[off : off+s.fills[i].width])
		off += s.fills[i].width
	}
	if err := r.Scan(dst...); err != nil {
		return err
	}
	for _, f := range attach {
		f(owner)
	}
	return nil
}

// Join returns the query also reading to-one relation r in the same
// statement, as JOIN: the related entity fills r's field (One) in every row,
// and a row with no related entity, or none that the target's Read policy lets
// through, is left out. Conditions and orderings on its columns are made by r:
//
//	db.Query[gen.PostFeed]().
//		Join(gen.PostFeeds.Post).
//		Where(gen.PostFeeds.UserID.EQ(me), gen.PostFeeds.Post.Is(gen.Posts.DeletedAt.IsNull())).
//		OrderBy(gen.PostFeeds.Rank.Desc(), gen.PostFeeds.Post.By(gen.Posts.CreatedAt.Desc())).
//		Limit(20).All(ctx)
//
// The handle may carry With, which loads relations of the joined entity as With
// loads them (gen.PostFeeds.Post.With(gen.Posts.Author)); its Where, OrderBy,
// LimitEach and MaxRows say how to load and are refused, as a join is one statement.
//
// A relation that can hold several rows (Many) is refused: a join would read an
// owner once for each, and Limit, Count and After would count joined rows.
// Load it with With, or test it with Has or HasWhere. A join reads all of the
// target's columns; Columns and Omit act on the entity's own. The target's
// Read policy is in the join's ON clause. A query joins a relation once, and
// a locking read and an aggregate cannot join. After pages over a joined column
// from the joined entity of the row it is given, which must have been read with
// the same join and have one. With still loads other relations in its own queries.
func (q Query[E]) Join[T any](r Rel[E, T]) Query[E] { return q.join(r, false) }

// LeftJoin is Join as LEFT JOIN: a row with no related entity stays, with r's
// field loaded and empty. A condition made by r.Is is on the joined row, so it
// leaves out a row that has none, as in SQL; put a condition that must keep
// such a row in r.Is(Or(…, IsNull)), or in the Read policy.
func (q Query[E]) LeftJoin[T any](r Rel[E, T]) Query[E] { return q.join(r, true) }

func (q Query[E]) join[T any](r Rel[E, T], left bool) Query[E] {
	if q.meta.err != nil {
		return q
	}
	fail := func(err error) Query[E] {
		if q.err == nil {
			q.err = err
		}
		return q
	}
	ref := engine.RelRef{Owner: ownerOf[E], Target: targetOf[T](), I: r.I}
	l, err := ref.Link()
	if err != nil {
		return fail(err)
	}
	if l.Many || l.Join != nil {
		return fail(fmt.Errorf("velox: %s: %s is a to-many relation, and Join reads a to-one relation; load it with With, or test it with Has or HasWhere", q.meta.info.Name, l.Name))
	}
	// The handle says how to join, not how to load: only its With applies.
	if r.err != nil {
		return fail(r.err)
	}
	if r.where != nil || r.order != nil || r.each != 0 || r.maxRows != 0 {
		return fail(fmt.Errorf("velox: %s: Join takes %s with only With: its Where, OrderBy, LimitEach and MaxRows say how to load, and a join is one statement; make the condition with %s.Is, the ordering with %s.By", q.meta.info.Name, l.Name, l.Name, l.Name))
	}
	if slices.ContainsFunc(q.st.Joins, func(j engine.JoinClause) bool { return j.Ref.I == r.I }) {
		return fail(fmt.Errorf("velox: %s: the query joins %s twice", q.meta.info.Name, l.Name))
	}
	off, err := engine.RelOffset(q.meta.info, r.I)
	if err != nil {
		return fail(err)
	}
	mt, err := metaOf[T]()
	if err != nil {
		return fail(err)
	}
	if len(r.with) > 0 {
		if err := engine.CheckLoads(mt.info, nil, r.with); err != nil {
			return fail(err)
		}
	}
	q.st.Joins = append(slices.Clip(q.st.Joins), engine.JoinClause{Ref: ref, Left: left})
	n := len(mt.info.Columns)
	set := joinSet{bindOwner: func(cols []int, dst []any, owner unsafe.Pointer) { bindRow(q.meta, cols, dst, (*E)(owner)) }}
	if q.joins != nil {
		set.fills = slices.Clip(q.joins.fills)
	}
	set.fills = append(set.fills, joinFill{width: n, bind: func(dst []any) func(unsafe.Pointer) {
		t := new(T)
		nulls := 0
		for c := range n {
			var d any
			if mt.nilCols != nil && mt.nilCols[c] {
				d = &nilScanner{dst: mt.addr(t, c)}
			} else {
				d = engine.ScanDst(mt.info, c, mt.addr(t, c))
			}
			if left {
				d = &nullProbe{dst: d, nulls: &nulls}
			}
			dst[c] = d
		}
		return func(owner unsafe.Pointer) {
			h := (*engine.Holder)(unsafe.Add(owner, off))
			h.Loaded = true
			if left && nulls == n {
				h.V = nil // a left join that found nothing: loaded, and empty
				return
			}
			h.V = t
		}
	}})
	q.joins = &set
	if len(r.with) > 0 {
		// The joined rows are read; their relations load as a With's nested ones do.
		q.loads = append(slices.Clip(q.loads), engine.LoadSpec{Rel: r.I, Target: targetOf[T](), Joined: true, With: r.with})
	}
	return q
}

// Is is true when the related entity of a joined relation meets every
// condition. The query must Join or LeftJoin r; a statement without the join
// fails. It is the condition on the joined row, not an EXISTS: use HasWhere
// for a relation that is not joined.
func (r Rel[E, T]) Is[_ Lazy[T]](ps ...Pred[T]) Pred[E] {
	for _, p := range ps {
		if err := engine.PredErr(engine.PredNode(Predicate(p))); err != nil {
			return Pred[E](engine.NewPredicate(engine.Node{Op: engine.OpInvalid, V: err}))
		}
	}
	return Pred[E](engine.NewPredicate(engine.JoinedNode(r.I, nodes(ps))))
}

// By is the ordering o of the joined relation's entity, as an ordering of the
// query's: OrderBy(rel.By(gen.Posts.CreatedAt.Desc())). The query must Join or
// LeftJoin r. After cannot page over it.
func (r Rel[E, T]) By[_ Lazy[T]](o Order[T]) Order[E] {
	t := o.t
	t.Rel = r.I + 1
	return Order[E]{t}
}

// nullProbe scans a joined column of a left join, counting NULLs: a row whose
// columns are all NULL has no related entity. A NULL leaves the field as it
// was, and any other value is read as database/sql would.
type nullProbe struct {
	dst   any
	nulls *int
}

func (p *nullProbe) Scan(src any) error {
	if src == nil {
		*p.nulls++
	}
	if s, ok := p.dst.(sql.Scanner); ok {
		return s.Scan(src)
	}
	if src == nil {
		return nil
	}
	return sql.ConvertAssign(driver.ScanContext{}, p.dst, src)
}

// bindRow points dst at the fields of the entity at p that cols (all, for
// nil) name: a JSON column through a decoder (ScanDst), and one of nilColumns
// through nilScanner.
func bindRow[E any](m *Meta[E], cols []int, dst []any, p *E) {
	for i := range dst {
		c := i
		if cols != nil {
			c = cols[i]
		}
		if m.nilCols != nil && m.nilCols[c] {
			dst[i] = &nilScanner{dst: m.addr(p, c)}
			continue
		}
		dst[i] = engine.ScanDst(m.info, c, m.addr(p, c))
	}
}
