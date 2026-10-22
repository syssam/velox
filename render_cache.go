package velox

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sync"
)

// maxShapes bounds the cache: the number of shapes it holds, across all
// entities. Programs have a fixed set of query shapes, except that In lists
// of every length are shapes of their own. maxShapes and maxCacheBytes both
// apply; past either bound, statements are rendered without the cache.
var maxShapes = 4096

// maxCacheBytes bounds the cache's memory: the bytes of keys and texts it
// holds, across entities: each shape's key, text and fingerprint. In lists of
// many lengths are the shapes that grow it.
var maxCacheBytes = 4 << 20

// cached is what the cache keeps for a shape: its SQL text and its
// fingerprint.
type cached struct{ text, fp string }

// fingerprint names a statement's shape: the first 8 bytes, as 16 lowercase hex
// characters, of the SHA-256 of its SQL text without any comment. velox sends
// every value as a parameter, so the text is the shape.
func fingerprint(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}

// renderCache keeps the SQL text and fingerprint of each statement shape:
// everything about a statement except its argument values. A statement whose
// shape is cached only collects its arguments.
//
// It is keyed first by the statement's *EntityInfo. A key holds the pointer,
// so an EntityInfo in the cache is never freed and its address never reused
// for another. DB.run does not use the cache for a statement whose entity a
// rewrite hook replaced, so a hook's per-statement EntityInfo never fills it.
type renderCache struct {
	mu    sync.RWMutex
	m     map[*EntityInfo]map[string]cached // entity, then shape
	n     int                               // shapes held, across entities
	bytes int                               // key, text and fingerprint bytes held
}

// get returns the shape's cached text and fingerprint, and the statement's
// arguments, or renderStmt's error. A shape past the cache's bounds is
// rendered each time, and gets its fingerprint each time. A shape whose
// rendering fails is not cached.
func (c *renderCache) get(d Dialect, st *Statement) (cached, []any, error) {
	var buf [256]byte
	key := shapeKey(buf[:0], d, st)
	c.mu.RLock()
	e, ok := c.m[st.Entity][string(key)]
	c.mu.RUnlock()
	if ok {
		w := writer{d: d, in: st.Entity, argsOnly: true, args: make([]any, 0, 8)}
		w.statement(st)
		if w.err != nil {
			return cached{}, nil, w.err
		}
		return e, w.args, nil
	}
	text, args, err := renderStmt(d, st)
	if err != nil {
		return cached{}, nil, err
	}
	e = cached{text, fingerprint(text)}
	c.mu.Lock()
	if c.n < maxShapes && c.bytes+len(key)+len(e.text)+len(e.fp) <= maxCacheBytes {
		if c.m == nil {
			c.m = make(map[*EntityInfo]map[string]cached)
		}
		shapes := c.m[st.Entity]
		if shapes == nil {
			shapes = make(map[string]cached)
			c.m[st.Entity] = shapes
		}
		if _, dup := shapes[string(key)]; !dup { // another goroutine may have added it
			shapes[string(key)] = e
			c.n++
			c.bytes += len(key) + len(e.text) + len(e.fp)
		}
	}
	c.mu.Unlock()
	return e, args, nil
}

// size returns the number of shapes held.
func (c *renderCache) size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.n
}

func flag(b bool) byte {
	if b {
		return 1
	}
	return 0
}

// shapeKey encodes everything about a statement's shape except its entity,
// which keys the outer map.
func shapeKey(k []byte, d Dialect, st *Statement) []byte {
	k = append(k, byte(d), byte(st.Op), flag(st.Count), flag(st.Exists), byte(st.Aggregate), flag(st.Columns != nil), flag(st.Limit > 0), flag(st.Offset > 0))
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
		k = append(k, flag(o.Desc))
	}
	k = binary.AppendUvarint(k, uint64(len(st.Set)))
	for _, a := range st.Set {
		k = binary.AppendUvarint(k, uint64(a.Column))
		k = append(k, byte(a.Op))
	}
	k = binary.AppendUvarint(k, uint64(len(st.Rows)))
	if cf := st.Conflict; cf != nil {
		k = append(k, 1, flag(cf.DoNothing))
		k = appendInts(k, cf.Columns)
		k = appendInts(k, cf.Update)
		k = binary.AppendUvarint(k, uint64(len(cf.Where)))
		for _, p := range cf.Where {
			k = nodeKey(k, p.n)
		}
	} else {
		k = append(k, 0)
	}
	k = appendInts(k, st.Returning)
	return k
}

func appendInts(k []byte, xs []int) []byte {
	k = binary.AppendUvarint(k, uint64(len(xs)))
	for _, x := range xs {
		k = binary.AppendUvarint(k, uint64(x))
	}
	return k
}

// nodeKey encodes a condition. For opExists, op, col (the relation index) and
// the kids are enough: the outer entity keys the map, and the relation index
// then fixes the target. An opThrough names its join entity, which the outer
// entity does not fix: two relations to one target may go through different
// joins. The entity's Name is its Go type without the package path, so its
// schema and table are keyed too.
func nodeKey(k []byte, n node) []byte {
	k = append(k, byte(n.op))
	k = binary.AppendUvarint(k, uint64(n.col))
	switch n.op {
	case opEQ, opNEQ:
		k = append(k, flag(isNilArg(n.v)))
	case opIn, opNotIn:
		k = binary.AppendUvarint(k, uint64(len(n.v.([]any))))
	case opThrough:
		j := n.v.(*EntityInfo)
		for _, s := range [...]string{j.Name, j.Schema, j.Table} {
			k = binary.AppendUvarint(k, uint64(len(s)))
			k = append(k, s...)
		}
		k = binary.AppendUvarint(k, uint64(n.w.(int)))
	}
	k = binary.AppendUvarint(k, uint64(len(n.kids)))
	for _, c := range n.kids {
		k = nodeKey(k, c)
	}
	return k
}
