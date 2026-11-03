package veloxotel

import (
	"maps"
	"sync"
	"sync/atomic"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/syssam/velox"
)

// maxShapes bounds the shape cache, and maxTagKeys the tag-key cache. An
// application has as many shapes as the statements its code builds, and as
// many tag keys as it names: bounded sets. Past the bound, a new one is
// built afresh each time, as without a cache.
const (
	maxShapes  = 2048
	maxTagKeys = 256
)

// cache is a bounded map from string keys whose reads take no lock and
// allocate nothing: an insert copies the map, which happens once per key.
type cache[V any] struct {
	m   atomic.Pointer[map[string]V]
	mu  sync.Mutex // serializes inserts
	max int
}

// get is k's value and whether there is one, and whether the cache is full.
func (c *cache[V]) get(k string) (v V, ok, full bool) {
	m := c.m.Load()
	if m == nil {
		return v, false, c.max == 0
	}
	v, ok = (*m)[k]
	return v, ok, len(*m) >= c.max
}

// put sets k's value, unless the cache is full.
func (c *cache[V]) put(k string, v V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	old := c.m.Load()
	n := 0
	if old != nil {
		n = len(*old)
	}
	if n >= c.max {
		return
	}
	next := make(map[string]V, n+1)
	if old != nil {
		maps.Copy(next, *old)
	}
	next[k] = v
	c.m.Store(&next)
}

// shape is what every execution of one builder statement's shape shares:
// its collection and summary, and the attribute set of a success's metric
// points when that does not depend on the route.
type shape struct {
	entity    *velox.EntityInfo // the shape's, checked on a hit
	coll, sum string
	// ok is the attribute set of a successful execution's points on an
	// unsharded DB without TargetFor; nil when the route names the server.
	ok metric.MeasurementOption
}

// shapeOf is st's shape: cached by fingerprint for a builder statement,
// built afresh for raw SQL (which has no collection or summary, and whose
// fingerprints need not be bounded) and past maxShapes.
func (a *adapter) shapeOf(st *velox.Statement) *shape {
	if st.Op.Raw() || st.Fingerprint == "" {
		return a.newShape(st, false)
	}
	sh, ok, full := a.shapes.get(st.Fingerprint)
	if ok && sh.entity == st.Entity {
		return sh
	}
	if full {
		return a.newShape(st, false)
	}
	sh = a.newShape(st, true)
	a.shapes.put(st.Fingerprint, sh)
	return sh
}

// newShape describes st's shape; with cached, also the attribute set of a
// success's points, which only a shape that is kept is worth building ahead.
func (a *adapter) newShape(st *velox.Statement, cached bool) *shape {
	coll := collection(st)
	sh := &shape{entity: st.Entity, coll: coll, sum: summary(st, coll)}
	if cached && a.cfg.TargetFor == nil {
		sh.ok = metric.WithAttributeSet(attribute.NewSet(a.pointAttrs(st, sh, "", a.target, failure{})...))
	}
	return sh
}

// tagKey is the span attribute key of the tag named key: velox.tag.<key>.
func (a *adapter) tagKey(key string) attribute.Key {
	k, ok, full := a.tagKeys.get(key)
	if ok {
		return k
	}
	k = attribute.Key(attrTagPrefix + key)
	if !full {
		a.tagKeys.put(key, k)
	}
	return k
}
