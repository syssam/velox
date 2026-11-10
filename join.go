package velox

import (
	"fmt"
	"slices"
	"unsafe"

	"github.com/syssam/velox/internal/engine"
)

// Join returns the query also reading to-one relation r in the same
// statement, as JOIN: the related entity fills r's field (One) in every row,
// and a row with no related entity, or none that the target's Read policy lets
// through, is left out. Conditions and orderings on its columns are made by r:
//
//	db.Query[veloxgen.PostFeed]().
//		Join(veloxgen.PostFeeds.Post).
//		Where(veloxgen.PostFeeds.UserID.EQ(me), veloxgen.PostFeeds.Post.Is(veloxgen.Posts.DeletedAt.IsNull())).
//		OrderBy(veloxgen.PostFeeds.Rank.Desc(), veloxgen.PostFeeds.Post.By(veloxgen.Posts.CreatedAt.Desc())).
//		Limit(20).All(ctx)
//
// The handle may carry With, which loads relations of the joined entity as With
// loads them (veloxgen.PostFeeds.Post.With(veloxgen.Posts.Author)); its Where, OrderBy,
// LimitEach and MaxRows say how to load and are refused, as a join is one statement.
//
// A relation that can hold several rows (Many) is refused: a join would read an
// owner once for each, and Limit, Count and After would count joined rows.
// Load it with With, or test it with Has or HasWith. A join reads all of the
// target's columns; Columns and Omit act on the entity's own. The target's
// Read policy is in the join's ON clause. A query joins a relation once, and
// a locking read, an aggregate and a per-owner read through a join table cannot join. After pages over a joined column
// from the joined entity of the row it is given, which must have been read with
// the same join and have one. With still loads other relations in its own queries.
func (q Query[E]) Join[T any](r Rel[E, T]) Query[E] { return q.join(r, false) }

// LeftJoin is Join as LEFT JOIN: a row with no related entity stays, with r's
// field loaded and empty. A condition made by r.Is is on the joined row, so it
// leaves out a row that has none, as in SQL; put a condition that must keep
// such a row in r.Is(Or(…, IsNull)), or in the Read policy.
func (q Query[E]) LeftJoin[T any](r Rel[E, T]) Query[E] { return q.join(r, true) }

func (q Query[E]) join[T any](r Rel[E, T], left bool) Query[E] {
	if q.meta.Err != nil {
		return q
	}
	fail := func(err error) Query[E] {
		if q.s.Err == nil {
			q.s.Err = err
		}
		return q
	}
	ref := engine.RelRef{Owner: ownerOf[E], Target: targetOf[T](), I: r.I}
	l, err := ref.Link()
	if err != nil {
		return fail(err)
	}
	if l.Many || l.Join != nil {
		return fail(fmt.Errorf("velox: %s: %s is a to-many relation, and Join reads a to-one relation; load it with With, or test it with Has or HasWith", q.meta.Info.Name, l.Name))
	}
	// The handle says how to join, not how to load: only its With applies.
	if r.err != nil {
		return fail(r.err)
	}
	if r.where != nil || r.order != nil || r.each != 0 || r.maxRows != 0 {
		return fail(fmt.Errorf("velox: %s: Join takes %s with only With: its Where, OrderBy, LimitEach and MaxRows say how to load, and a join is one statement; make the condition with %s.Is, the ordering with %s.By", q.meta.Info.Name, l.Name, l.Name, l.Name))
	}
	if slices.ContainsFunc(q.s.St.Joins, func(j engine.JoinClause) bool { return j.Ref.I == r.I }) {
		return fail(fmt.Errorf("velox: %s: the query joins %s twice", q.meta.Info.Name, l.Name))
	}
	off, err := engine.RelOffset(q.meta.Info, r.I)
	if err != nil {
		return fail(err)
	}
	mt, err := metaOf[T]()
	if err != nil {
		return fail(err)
	}
	if len(r.with) > 0 {
		if err := engine.CheckLoads(mt.Info, nil, r.with); err != nil {
			return fail(err)
		}
	}
	q.s.St.Joins = append(slices.Clip(q.s.St.Joins), engine.JoinClause{Ref: ref, Left: left})
	n := len(mt.Info.Columns)
	set := engine.JoinSet{BindOwner: func(cols []int, dst []any, owner unsafe.Pointer) { bindRow(q.meta, cols, dst, (*E)(owner)) }}
	if q.s.Joins != nil {
		set.Fills = slices.Clip(q.s.Joins.Fills)
	}
	set.Fills = append(set.Fills, engine.JoinFill{Width: n, Bind: func(dst []any) func(unsafe.Pointer) {
		t := new(T)
		nulls := 0
		for c := range n {
			var d any
			if mt.NilCols != nil && mt.NilCols[c] {
				d = &engine.NilScanner{Dst: mt.Addr(t, c)}
			} else {
				d = engine.ScanDst(mt.Info, c, mt.Addr(t, c))
			}
			if left {
				d = &engine.NullProbe{Dst: d, Nulls: &nulls}
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
	q.s.Joins = &set
	if len(r.with) > 0 {
		// The joined rows are read; their relations load as a With's nested ones do.
		q.s.Loads = append(slices.Clip(q.s.Loads), engine.LoadSpec{Rel: r.I, Target: targetOf[T](), Joined: true, With: r.with})
	}
	return q
}

// Is is true when the related entity of a joined relation meets every
// condition. The query must Join or LeftJoin r; a statement without the join
// fails. It is the condition on the joined row, not an EXISTS: use HasWith
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
// query's: OrderBy(rel.By(veloxgen.Posts.CreatedAt.Desc())). The query must Join or
// LeftJoin r. After pages over it from the row's joined entity (see Join).
// NULL sorts after every value ascending, as OrderBy sorts it, on every
// dialect; a left join's row with no target counts as NULL.
func (r Rel[E, T]) By[_ Lazy[T]](o Order[T]) Order[E] {
	t := o.t
	t.Rel = r.I + 1
	return Order[E]{t}
}

// Fill stores t as the entity of r's field in owner, loaded, as a Join does
// when it reads a row: the row After pages from needs its joined entity, and
// code that builds such a row (a cursor's) sets one. Only the columns After
// orders by need a value. It fails when r is not a relation of E held in a field.
func (r Rel[E, T]) Fill[_ Lazy[T]](owner *E, t *T) error {
	m, err := metaOf[E]()
	if err != nil {
		return err
	}
	off, err := engine.RelOffset(m.Info, r.I)
	if err != nil {
		return err
	}
	h := (*engine.Holder)(unsafe.Add(unsafe.Pointer(owner), off))
	h.Loaded = true
	h.V = t
	return nil
}

// Filled returns the entity of r's field in owner, and whether it holds one: a row
// read with Join has it, and so has one Fill was given.
func (r Rel[E, T]) Filled[_ Lazy[T]](owner *E) (*T, bool) {
	m, err := metaOf[E]()
	if err != nil {
		return nil, false
	}
	off, err := engine.RelOffset(m.Info, r.I)
	if err != nil {
		return nil, false
	}
	h := (*engine.Holder)(unsafe.Add(unsafe.Pointer(owner), off))
	t, _ := h.V.(*T)
	return t, t != nil
}

// bindRow points dst at the fields of the entity at p that cols (all, for
// nil) name: a JSON column through a decoder (ScanDst), and one of nilColumns
// through engine.NilScanner.
func bindRow[E any](m *metaState[E], cols []int, dst []any, p *E) {
	for i := range dst {
		c := i
		if cols != nil {
			c = cols[i]
		}
		if m.NilCols != nil && m.NilCols[c] {
			dst[i] = &engine.NilScanner{Dst: m.Addr(p, c)}
			continue
		}
		dst[i] = engine.ScanDst(m.Info, c, m.Addr(p, c))
	}
}
