package velox

import (
	"encoding/binary"
	"sync"
)

// maxShapes bounds the cache: the number of shapes it holds, across all
// entities. Programs have a fixed set of query shapes, except that In lists
// of every length are shapes of their own; past the bound, statements are
// rendered without the cache.
var maxShapes = 4096

// renderCache keeps the SQL text of each statement shape: everything about a
// statement except its argument values. A statement whose shape is cached
// only collects its arguments.
//
// It is keyed first by the statement's *EntityInfo. A key holds the pointer,
// so an EntityInfo in the cache is never freed and its address never reused
// for another. DB.run does not use the cache for a statement whose entity a
// rewrite hook replaced, so a hook's per-statement EntityInfo never fills it.
type renderCache struct {
	mu sync.RWMutex
	m  map[*EntityInfo]map[string]string // entity, then shape
	n  int                               // shapes held, across entities
}

func (c *renderCache) render(d Dialect, st *Statement) (string, []any) {
	var buf [256]byte
	key := shapeKey(buf[:0], d, st)
	c.mu.RLock()
	text, ok := c.m[st.Entity][string(key)]
	c.mu.RUnlock()
	if ok {
		w := writer{d: d, in: st.Entity, argsOnly: true, args: make([]any, 0, 8)}
		w.statement(st)
		return text, w.args
	}
	text, args := render(d, st)
	c.mu.Lock()
	if c.n < maxShapes {
		if c.m == nil {
			c.m = make(map[*EntityInfo]map[string]string)
		}
		shapes := c.m[st.Entity]
		if shapes == nil {
			shapes = make(map[string]string)
			c.m[st.Entity] = shapes
		}
		if _, dup := shapes[string(key)]; !dup { // another goroutine may have added it
			shapes[string(key)] = text
			c.n++
		}
	}
	c.mu.Unlock()
	return text, args
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
	k = append(k, byte(d), byte(st.Op), flag(st.Count), flag(st.Columns != nil), flag(st.Limit > 0), flag(st.Offset > 0))
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
// then fixes the target.
func nodeKey(k []byte, n node) []byte {
	k = append(k, byte(n.op))
	k = binary.AppendUvarint(k, uint64(n.col))
	if n.op == opIn || n.op == opNotIn {
		k = binary.AppendUvarint(k, uint64(len(n.v.([]any))))
	}
	k = binary.AppendUvarint(k, uint64(len(n.kids)))
	for _, c := range n.kids {
		k = nodeKey(k, c)
	}
	return k
}
