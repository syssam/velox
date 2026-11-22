package engine

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sync"
)

// DefaultMaxShapes bounds a cache whose MaxShapes is 0: the number of
// shapes it holds, across all entities. Programs have a fixed set of query
// shapes, and In lists are padded to a few lengths (inLen). Both bounds
// apply; past either, a new shape replaces shapes chosen at random.
const DefaultMaxShapes = 4096

// DefaultMaxCacheBytes bounds the memory of a cache whose MaxBytes is 0: the
// bytes of keys and texts it holds, across entities: each shape's key, text
// and fingerprint. In lists of many lengths are the shapes that grow it.
const DefaultMaxCacheBytes = 4 << 20

// MaxCachedRows is the most rows an INSERT may have and be cached: each row
// count is a shape of its own, and a batch of thousands is rendered once.
const MaxCachedRows = 32

// cached is what the cache keeps for a shape: its SQL text and its
// fingerprint.
type cached struct{ Text, Fp string }

// Fingerprint names a statement's shape: the first 8 bytes, as 16 lowercase hex
// characters, of the SHA-256 of its SQL text without any comment. velox sends
// every value as a parameter but SQLite's LIMIT and a bool column's constant
// (boolCmp), which the shape key holds, so the text is the shape.
func Fingerprint(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}

// RenderCache keeps the SQL text and fingerprint of each statement shape:
// everything about a statement except its argument values. A statement whose
// shape is cached only collects its arguments.
//
// It is keyed first by the statement's *EntityInfo. A key holds the pointer,
// so an EntityInfo in the cache is never freed and its address never reused
// for another while it has shapes; one whose last shape is evicted leaves
// the map. DB.Run does not use the cache for a statement whose entity a
// rewrite hook replaced, so a hook's per-statement EntityInfo never fills it.
//
// A full cache evicts at random: a miss on a full cache replaces shapes
// chosen by map iteration, which Go randomizes, until the new one fits. So
// a program with more shapes than the bounds keeps most of its hot ones,
// where a cache that stopped adding would render every later shape each
// time. Eviction runs on a miss only, which renders anyway.
type RenderCache struct {
	mu    sync.RWMutex
	M     map[*EntityInfo]map[string]cached // entity, then shape
	n     int                               // shapes held, across entities
	Bytes int                               // key, text and fingerprint bytes held
	// MaxShapes and MaxBytes bound the cache; 0 is DefaultMaxShapes and
	// DefaultMaxCacheBytes, and below 0 caches nothing. Set before first use.
	MaxShapes, MaxBytes int
}

// Get returns the shape's cached text and fingerprint, and the statement's
// arguments, or RenderStmt's error. An insert of more than MaxCachedRows
// rows, an insert with DEFAULT cells, and a shape larger than the byte
// bound, is rendered each time, and gets its fingerprint each time. A shape
// whose rendering fails is not cached.
func (c *RenderCache) Get(d Dialect, st *Statement) (cached, []any, error) {
	// An insert with DEFAULT cells (Statement.dflt) is rendered each time:
	// where they fall is data, and a shape per pattern would fill the cache.
	if st.Op == OpInsert && (len(st.Rows) > MaxCachedRows || st.dflt) {
		text, args, err := RenderStmt(d, st)
		if err != nil {
			return cached{}, nil, err
		}
		return cached{text, Fingerprint(text)}, args, nil
	}
	var buf [256]byte
	key := ShapeKey(buf[:0], d, st)
	c.mu.RLock()
	e, ok := c.M[st.Entity][string(key)]
	c.mu.RUnlock()
	if ok {
		w := writer{d: d, in: st.Entity, argsOnly: true, args: make([]any, 0, 8)}
		w.statement(st)
		if w.err != nil {
			return cached{}, nil, w.err
		}
		return e, w.args, nil
	}
	text, args, err := RenderStmt(d, st)
	if err != nil {
		return cached{}, nil, err
	}
	e = cached{text, Fingerprint(text)}
	c.mu.Lock()
	c.add(st.Entity, string(key), e)
	c.mu.Unlock()
	return e, args, nil
}

// add holds e as in's shape key, evicting shapes at random until it fits
// the bounds. A shape larger than the byte bound, or a cache bounded to
// nothing, holds nothing. c.mu is held.
func (c *RenderCache) add(in *EntityInfo, key string, e cached) {
	size := len(key) + len(e.Text) + len(e.Fp)
	maxN, maxB := c.maxShapes(), c.maxBytes()
	if maxN == 0 || size > maxB {
		return
	}
	if _, dup := c.M[in][key]; dup { // another goroutine may have added it
		return
	}
	for c.n >= maxN || c.Bytes+size > maxB {
		c.evict()
	}
	if c.M == nil {
		c.M = make(map[*EntityInfo]map[string]cached)
	}
	shapes := c.M[in]
	if shapes == nil {
		shapes = make(map[string]cached)
		c.M[in] = shapes
	}
	shapes[key] = e
	c.n++
	c.Bytes += size
}

// evict drops one shape, of the entity and with the key Go's randomized map
// iteration gives first, and the entity's map once it is empty. The cache is
// not empty. c.mu is held.
func (c *RenderCache) evict() {
	for in, shapes := range c.M {
		for k, e := range shapes {
			delete(shapes, k)
			c.n--
			c.Bytes -= len(k) + len(e.Text) + len(e.Fp)
			break
		}
		if len(shapes) == 0 {
			delete(c.M, in)
		}
		return
	}
}

// maxShapes is the most shapes c holds.
func (c *RenderCache) maxShapes() int {
	if c.MaxShapes != 0 {
		return max(c.MaxShapes, 0)
	}
	return DefaultMaxShapes
}

// maxBytes is the most bytes c holds.
func (c *RenderCache) maxBytes() int {
	if c.MaxBytes != 0 {
		return max(c.MaxBytes, 0)
	}
	return DefaultMaxCacheBytes
}

// Size returns the number of shapes held.
func (c *RenderCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.n
}

func bit(b bool) byte {
	if b {
		return 1
	}
	return 0
}

// ShapeKey encodes everything about a statement's shape except its entity,
// which keys the outer map.
func ShapeKey(k []byte, d Dialect, st *Statement) []byte {
	// Seven flags in one byte: the key is built in a fixed buffer, and one
	// that outgrows it costs an allocation on every statement.
	flags := bit(st.Count) | bit(st.Exists)<<1 | bit(st.Columns != nil)<<2 | bit(st.Limit > 0)<<3 | bit(st.Offset > 0)<<4 | bit(st.Project != nil)<<5 |
		bit(st.Distinct)<<6
	k = append(k, byte(d), byte(st.Op), flags, byte(st.Aggregate), byte(st.Lock), byte(st.LockWait))
	if d == SQLite && st.Limit > 0 {
		// SQLite's LIMIT is in the text (selectTail), so its value is shape.
		k = binary.AppendUvarint(k, uint64(st.Limit))
	}
	k = binary.AppendUvarint(k, uint64(st.GroupBy))
	k = binary.AppendUvarint(k, uint64(len(st.Columns)))
	for _, c := range st.Columns {
		k = binary.AppendUvarint(k, uint64(c))
	}
	k = binary.AppendUvarint(k, uint64(len(st.Where)))
	for _, p := range st.Where {
		k = nodeKey(k, p.n)
	}
	k = binary.AppendUvarint(k, uint64(len(st.OrderBy)))
	for _, o := range st.OrderBy {
		k = binary.AppendUvarint(k, uint64(o.Column))
		k = binary.AppendUvarint(k, uint64(o.Rel))
		t, expr := o.Expr.(Term)
		k = append(k, bit(o.Desc)|bit(expr)<<1)
		if expr {
			k = termKey(k, t)
		}
	}
	if st.Project != nil {
		k = projectionKey(k, st.Project)
	}
	k = binary.AppendUvarint(k, uint64(len(st.Joins)))
	for _, j := range st.Joins {
		k = binary.AppendUvarint(k, uint64(j.Ref.I))
		k = append(k, bit(j.Left))
		k = binary.AppendUvarint(k, uint64(len(j.On)))
		for _, p := range j.On {
			k = nodeKey(k, p.n)
		}
	}
	k = binary.AppendUvarint(k, uint64(len(st.Set)))
	for _, a := range st.Set {
		k = binary.AppendUvarint(k, uint64(a.Column))
		k = append(k, byte(a.Op))
		switch a.Op {
		case AssignExpr:
			k = termKey(k, exprAssigned(a))
		case AssignAppend:
			if d == SQLite {
				// SQLite's text has a json(?) per element.
				k = binary.AppendUvarint(k, uint64(appendLen(a.Value)))
			}
		}
	}
	k = binary.AppendUvarint(k, uint64(len(st.Rows)))
	if st.Op == OpInsert {
		k = nullsKey(k, st)
	}
	if cf := st.Conflict; cf != nil {
		k = append(k, 1, bit(cf.DoNothing), bit(cf.ignores()))
		k = appendInts(k, cf.Columns)
		k = appendInts(k, cf.Update)
		k = binary.AppendUvarint(k, uint64(len(cf.Where)))
		for _, p := range cf.Where {
			k = nodeKey(k, p.n)
		}
		k = binary.AppendUvarint(k, uint64(len(cf.Set)))
		for _, a := range cf.Set {
			k = binary.AppendUvarint(k, uint64(a.Column))
			k = append(k, byte(a.Op))
			if a.Op == AssignExpr {
				k = termKey(k, exprAssigned(a))
			}
		}
		k = binary.AppendUvarint(k, uint64(len(cf.TargetWhere)))
		for _, p := range cf.TargetWhere {
			k = nodeKey(k, p.n)
		}
		k = binary.AppendUvarint(k, uint64(len(cf.Constraint)))
		k = append(k, cf.Constraint...)
	} else {
		k = append(k, 0)
	}
	k = appendInts(k, st.Returning)
	k = append(k, bit(st.returnOld)|bit(st.recorded)<<1) // a delete renders RETURNING only when recorded
	return eachKey(k, st.Each)
}

// nullsKey encodes which columns of an INSERT are written as the literal NULL
// (insertNulls): the text differs with them. It is one byte, 0, when none is,
// and otherwise 1 and a bit per column.
func nullsKey(k []byte, st *Statement) []byte {
	var buf [64]bool
	nulls := insertNulls(st, buf[:])
	if nulls == nil {
		return append(k, 0)
	}
	k = append(k, 1)
	for i := 0; i < len(nulls); i += 8 {
		var b byte
		for j := 0; j < 8 && i+j < len(nulls); j++ {
			b |= bit(nulls[i+j]) << j
		}
		k = append(k, b)
	}
	return k
}

// eachKey encodes a statement's Each: its presence, its columns, whether it
// has a Limit, the number of its keys (an In list's length, as nodeKey keys
// it) and its join entity, by name, schema and table as nodeKey keys an
// OpThrough's. The keys and the Limit are arguments.
func eachKey(k []byte, c *EachClause) []byte {
	if c == nil {
		return append(k, 0)
	}
	k = append(k, 1, bit(c.Limit > 0))
	k = binary.AppendUvarint(k, uint64(c.Column))
	k = binary.AppendUvarint(k, uint64(inLen(len(c.Keys))))
	if j := c.Join; j != nil {
		k = append(k, 1)
		for _, s := range [...]string{j.Name, j.Schema, j.Table} {
			k = binary.AppendUvarint(k, uint64(len(s)))
			k = append(k, s...)
		}
		k = binary.AppendUvarint(k, uint64(c.JoinOwner))
		k = binary.AppendUvarint(k, uint64(c.JoinTarget))
		k = binary.AppendUvarint(k, uint64(c.Target))
	} else {
		k = append(k, 0)
	}
	return k
}

func appendInts(k []byte, xs []int) []byte {
	k = binary.AppendUvarint(k, uint64(len(xs)))
	for _, x := range xs {
		k = binary.AppendUvarint(k, uint64(x))
	}
	return k
}

// nodeKey encodes a condition. For a relation's OpExists, op, col (the
// relation index) and the kids are enough: the outer entity keys the map, and
// the relation index then fixes the target. A ColRef's (col -1) keys its
// target and both columns. An OpThrough names its join entity, which the outer
// entity does not fix: two relations to one target may go through different
// joins. The entity's Name is its Go type without the package path, so its
// schema and table are keyed too.
func nodeKey(k []byte, n Node) []byte {
	k = append(k, byte(n.Op))
	k = binary.AppendUvarint(k, uint64(n.Col))
	switch n.Op {
	case OpEQ, OpNEQ:
		// A nil is IS NULL and a bool is written as text (boolCmp), so
		// both shape it: 1 for nil, 2 for false, 3 for true. A bool
		// compared with a column of another type is keyed too, though it
		// is bound: that only splits its shape in two.
		var v byte
		switch b, ok := n.V.(bool); {
		case isNilArg(n.V):
			v = 1
		case ok:
			v = 2 + bit(b)
		}
		k = append(k, v)
	case OpIn, OpNotIn:
		k = binary.AppendUvarint(k, uint64(inLen(len(n.V.([]any)))))
	case OpJSON:
		// The keys are arguments; the kind and their number shape the text.
		p := n.V.(JSONPath)
		k = append(k, byte(p.Kind))
		k = binary.AppendUvarint(k, uint64(len(p.Keys)))
	case OpJSONHas:
		// The element is an argument; its kind shapes SQLite's text.
		k = append(k, byte(n.V.(*jsonElem).kind))
	case OpExpr:
		k = exprKey(k, n.V.(*exprData))
	case opRowIn:
		k = appendInts(k, n.V.([]int))
		k = binary.AppendUvarint(k, uint64(inLen(len(n.W.([][]any)))))
	case OpExists:
		if n.Col < 0 {
			// A ColRef's EXISTS: the outer entity does not fix its target
			// or columns, so they are keyed. It is resolved before
			// rendering, so the link has no error here.
			l, _ := existsLink(n)
			if t := l.Target; t != nil {
				for _, s := range [...]string{t.Name, t.Schema, t.Table} {
					k = binary.AppendUvarint(k, uint64(len(s)))
					k = append(k, s...)
				}
			}
			k = binary.AppendUvarint(k, uint64(l.OwnerCol))
			k = binary.AppendUvarint(k, uint64(l.TargetCol))
			k = append(k, bit(l.TextKey)) // the columns' types fix it; keyed so the text never depends on that
		}
	case OpThrough:
		j := n.V.(*EntityInfo)
		for _, s := range [...]string{j.Name, j.Schema, j.Table} {
			k = binary.AppendUvarint(k, uint64(len(s)))
			k = append(k, s...)
		}
		k = binary.AppendUvarint(k, uint64(n.W.(int)))
	}
	k = binary.AppendUvarint(k, uint64(len(n.Kids)))
	for _, c := range n.Kids {
		k = nodeKey(k, c)
	}
	return k
}
