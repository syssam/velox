package engine

import (
	"cmp"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"
)

// Source is the DB an entity was read or written through: an entity's edge
// reads (QueryX) run on it. An entity holds a *Source in one unexported
// field, which the engine sets through EntityInfo's offset of it; a DB holds
// the *Source its statements give their rows (DB.src), so that setting it is
// one pointer store per row, with no allocation.
//
// A Source never resolves to a DB with a WithoutPolicy reason: an edge read
// is under the entities' policies. A DB outside a transaction has one Source
// for its life, made with it; a WithoutPolicy or Record copy of it shares
// it. A transaction has one Source, made at BEGIN, which resolves to the
// transaction's handle while the transaction is live and to the Source of the
// DB it was begun on once it has ended, committed or rolled back; the end
// clears its pointer to the handle, so that an entity kept after the
// transaction does not keep its *sql.Tx and statement cache alive.
type Source struct {
	// db is, for a DB outside a transaction, its handle without a
	// WithoutPolicy reason; nil for a transaction's Source.
	db *DB
	// tx is, for a transaction's Source, its handle without a WithoutPolicy
	// reason while the transaction is live; nil once it has ended. It is
	// an unsafe.Pointer read and written with sync/atomic's functions, not
	// an atomic.Pointer[DB]: a generic type's descriptor is emitted in each
	// package that names a type holding a DB, which every velox.Query[E]
	// does, and cost 40 KB per entity package.
	tx unsafe.Pointer
	// up is, for a transaction's Source, the Source of the DB the
	// transaction (or savepoint) was begun on, which it falls back to.
	up *Source
	// kept is, for a transaction begun WithoutPolicy, the copy of its handle
	// without the reason (bypassSource), cleared when it ends.
	kept *DB
}

// DB returns the DB s reads on: nil for a nil s, or when the DB it falls
// back to has none (a DB the engine made without one).
func (s *Source) DB() *DB {
	for ; s != nil; s = s.up {
		if s.db != nil {
			return s.db
		}
		if t := s.liveTx(); t != nil {
			return t
		}
	}
	return nil
}

// liveTx is s.tx: its transaction's handle while it is live, else nil.
func (s *Source) liveTx() *DB { return (*DB)(atomic.LoadPointer(&s.tx)) }

// end ends a transaction's Source: from now on it resolves to up, and it
// keeps nothing of the transaction.
func (s *Source) end() {
	atomic.StorePointer(&s.tx, nil)
	if s.kept != nil {
		*s.kept = DB{}
		s.kept = nil
	}
}

// OwnSource gives db, a DB outside a transaction at its final address, a
// Source of its own. velox's constructors call it on the DB they return.
func OwnSource(db *DB) {
	db.src = &Source{db: db}
}

// Source returns the Source db's statements give the entities they read or
// write; nil on a DB that has none.
func (db *DB) Source() *Source { return db.src }

// txSource gives h, a transaction's (or savepoint's) handle, its Source:
// one that resolves to h, or to a copy of h without its WithoutPolicy
// reason, while the transaction is live, and to up after. The caller ends
// it (Source.end) when the transaction ends. The copy is in the Source's
// own allocation, so a transaction makes one either way.
func txSource(h *DB, up *Source) *Source {
	if h.bypass == "" {
		s := &Source{up: up}
		h.src = s
		atomic.StorePointer(&s.tx, unsafe.Pointer(h))
		return s
	}
	b := &bypassSource{}
	b.up = up
	h.src = &b.Source
	b.kept = *h
	b.kept.owned, b.kept.bypass = false, ""
	atomic.StorePointer(&b.tx, unsafe.Pointer(&b.kept))
	b.Source.kept = &b.kept
	return &b.Source
}

// bypassSource is the Source of a transaction begun WithoutPolicy, with the
// copy of its handle without the reason that it resolves to.
type bypassSource struct {
	Source
	kept DB
}

// adoptedSource gives h, the handle of a transaction the caller began
// (UseTx), a Source that resolves to h, without its WithoutPolicy reason:
// velox never sees that transaction end.
func adoptedSource(h *DB) {
	s := &Source{}
	h.src = s
	if h.bypass == "" {
		s.db = h
		return
	}
	c := *h
	c.owned, c.bypass = false, ""
	s.db = &c
}

// Put stores s as the Source of the entity of in at p. It is small enough
// to inline, so that &e in a generic caller does not escape.
func (s *Source) Put(in *EntityInfo, p unsafe.Pointer) {
	if in.srcOff != 0 {
		*(**Source)(unsafe.Add(p, in.srcOff-1)) = s
	}
}

// SourceOf returns the Source stored in the entity of in at p; nil when it
// has none or in has no Source field.
func SourceOf(in *EntityInfo, p unsafe.Pointer) *Source {
	if in.srcOff == 0 {
		return nil
	}
	return *(**Source)(unsafe.Add(p, in.srcOff-1))
}

// PutRows stores s in each of n entities of in, size bytes apart from p,
// and in the targets their joins (fills) stored in them.
func (s *Source) PutRows(in *EntityInfo, p unsafe.Pointer, size uintptr, n int, fills []JoinFill) {
	if in.srcOff == 0 && len(fills) == 0 {
		return
	}
	for i := range n {
		q := unsafe.Add(p, uintptr(i)*size)
		s.Put(in, q)
		s.PutJoined(fills, q)
	}
}

// PutPtrs stores s in each entity of in at ps, and in the targets their
// joins (fills) stored in them.
func (s *Source) PutPtrs(in *EntityInfo, ps []unsafe.Pointer, fills []JoinFill) {
	if in.srcOff == 0 && len(fills) == 0 {
		return
	}
	for _, q := range ps {
		s.Put(in, q)
		s.PutJoined(fills, q)
	}
}

// PutJoined stores s in the targets that fills, a query's joins, stored in
// the owner at p: a left join that found none stored nothing.
func (s *Source) PutJoined(fills []JoinFill, p unsafe.Pointer) {
	for i := range fills {
		f := &fills[i]
		if f.Target == nil {
			continue
		}
		h := (*Holder)(unsafe.Add(p, f.Off))
		if h.V != nil {
			s.Put(f.Target, dataWord(h.V))
		}
	}
}

// putBlocks stores s in every row of part, a *blocks of entities of in,
// and in the targets their joins (fills) stored in them.
func (s *Source) putBlocks(in *EntityInfo, part any, fills []JoinFill) {
	b, ok := part.(*blocks)
	if !ok {
		return
	}
	for _, k := range b.bs {
		s.PutRows(in, k.at, b.size, k.used, fills)
	}
}

// ErrNoSource fails every statement of an edge read (QueryX) of an entity
// that no statement read or wrote: one built by hand, by LoadedOne or
// LoadedMany, or decoded from JSON.
var ErrNoSource = errors.New("the entity was not read or written by a DB, so it has none to read its edge on; query the edge on a DB with db.Query")

// noSourceDB is the DB an edge read of an entity with no Source runs on:
// each of its statements fails with ErrNoSource.
var noSourceDB = func() *DB {
	db := Open(SQLite, nil, nil)
	db.misuse = ErrNoSource
	return &db
}()

// EdgeDB returns the DB an edge read of an entity with Source s runs on:
// s's, or, when it has none, a DB whose statements fail with ErrNoSource,
// and that error.
func EdgeDB(s *Source) (*DB, error) {
	if db := s.DB(); db != nil {
		return db, nil
	}
	return noSourceDB, ErrNoSource
}

// entityTypes maps the type word of each entity pointer type *E whose
// metadata BuildDescriptor built to its EntityInfo, for EdgeQuery: an
// entity that has a Source was read or written by a statement, which built
// its type's metadata first.
var entityTypes sync.Map // unsafe.Pointer → *EntityInfo

// registerEntity records info as the metadata of x's type, a *E.
func registerEntity(x any, info *EntityInfo) { entityTypes.Store(typeWord(x), info) }

// EdgeQuery is the state of an edge read (velox.QueryEdge): the query of
// the rows of the target T, whose metadata is info, related to owner, a *E,
// through E's relation edge, on owner's Source s; one and many are the
// reflect.Types of One[T] and Many[T], which the relation's field must be.
// info is nil when the target's metadata failed, whose error the query
// returns. It is not generic, so that velox's QueryEdge, compiled for each
// target type, holds no more than this call.
func EdgeQuery(s *Source, owner any, edge int32, info *EntityInfo, one, many any) QueryState {
	db, err := EdgeDB(s)
	q := QueryState{DB: db, St: Statement{Op: OpSelect, Entity: info, InTx: db.InTx}}
	if info == nil {
		return q
	}
	v, _ := entityTypes.Load(typeWord(owner))
	in, _ := v.(*EntityInfo)
	if err != nil || in == nil {
		name := fmt.Sprintf("%v", ElemType(owner))
		if in != nil && edge >= 0 && int(edge) < len(in.rels) {
			name = in.Name + "." + in.rels[edge].Name
		}
		q.Err = fmt.Errorf("velox: %s: %w", name, cmp.Or(err, ErrNoSource))
		return q
	}
	target := RelTarget{Get: func() (*EntityInfo, error) { return info, nil }, One: one, Many: many}
	ref := RelRef{Owner: func() (*EntityInfo, error) { return in, nil }, Target: target, I: edge}
	pred := NewPredicate(OfNode(ref, nil, nil, []unsafe.Pointer{dataWord(owner)}))
	q.St.Where = []Predicate{pred}
	q.Err = PredErr(pred.n)
	return q
}

// PutInserted stores db's Source in the rows of an insert of in that
// succeeded: the one row at one, or the n rows size bytes apart from many,
// and the rows of an InsertBulk that set holds (BulkSetters).
func PutInserted(db *DB, in *EntityInfo, one, many unsafe.Pointer, size uintptr, n int, set Setters) {
	s := db.src
	if set.bulk != nil {
		for _, r := range set.bulk.rows {
			if r.p != nil {
				s.Put(in, r.p)
			}
		}
	}
	if one != nil {
		s.Put(in, one)
		return
	}
	s.PutRows(in, many, size, n, nil)
}

// TableSourceDB returns the DB that row, a *E of t's entity, reads its edges
// on: untyped.SourceDB. It is nil when row is not such a row or has no
// Source.
func TableSourceDB(t Table, row any) *DB {
	s := t.state()
	p, err := s.rowOf(row)
	if err != nil {
		return nil
	}
	return SourceOf(s.info, p).DB()
}
