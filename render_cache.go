package velox

import (
	"encoding/binary"
	"sync"
	"unsafe"
)

// maxShapes bounds the cache. Programs have a fixed set of query shapes,
// except that In lists of every length are shapes of their own; past the
// bound, statements are rendered without the cache.
var maxShapes = 4096

// renderCache keeps the SQL text of each statement shape: everything about a
// statement except its argument values. A statement whose shape is cached
// only collects its arguments.
type renderCache struct {
	mu sync.RWMutex
	m  map[string]string
}

func (c *renderCache) render(d Dialect, st *Statement) (string, []any) {
	var buf [256]byte
	key := shapeKey(buf[:0], d, st)
	c.mu.RLock()
	text, ok := c.m[string(key)]
	c.mu.RUnlock()
	if ok {
		w := writer{d: d, in: st.Entity, argsOnly: true, args: make([]any, 0, 8)}
		w.selectStmt(st)
		return text, w.args
	}
	text, args := render(d, st)
	c.mu.Lock()
	if c.m == nil {
		c.m = make(map[string]string)
	}
	if len(c.m) < maxShapes {
		c.m[string(key)] = text
	}
	c.mu.Unlock()
	return text, args
}

func flag(b bool) byte {
	if b {
		return 1
	}
	return 0
}

func shapeKey(k []byte, d Dialect, st *Statement) []byte {
	k = append(k, byte(d), byte(st.Op), flag(st.Count), flag(st.Columns != nil), flag(st.Limit > 0), flag(st.Offset > 0))
	k = binary.LittleEndian.AppendUint64(k, uint64(uintptr(unsafe.Pointer(st.Entity))))
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
	return k
}

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
