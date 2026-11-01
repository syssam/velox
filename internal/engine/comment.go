package engine

import (
	"encoding/binary"
	"fmt"
	"slices"
	"strconv"
	"sync"
)

// WithSQLComment appends a sqlcommenter comment to every statement's text:
// app, the call site when WithCallers is on, then each tag in tags that the
// context sets (WithTags), in that order. An empty app panics, as does a tag
// that is empty, repeated, or named app or caller.
//
//	SELECT … /*app='billing',caller='billing%2Finvoice.go%3A42',route='GET%20%2Finvoices'*/
//
// Every byte of a key or value outside A–Z a–z 0–9 - . _ ~ is percent-encoded,
// so no value can end the comment. Statement.SQL is the text sent, comment
// included; the fingerprint is of the text without it. A Rewrite hook sees no
// comment, since it is rendered after them, from the Caller and Tags they
// leave; an Around hook sees the text sent. SQL(ctx) includes the comment,
// with the call site of the SQL call.
//
// List only tags whose values are stable per code path, such as a route
// template, never an ID: pgx caches prepared statements by their text, so a
// per-request value would prepare a statement per request, and it would
// make pg_stat_activity hard to read. (pg_stat_statements and MySQL's
// digest normalise comments away, so their statistics are not split.)
//
// With WithCallers, each call site of a shape is a distinct text, so pgx
// prepares one statement per call site per connection. A shape called from
// many sites multiplies statements against pgx's per-connection cache,
// which holds 512 by default: size pgxpool.Config.ConnConfig's
// StatementCacheCapacity for it, or use WithSQLComment without WithCallers.
//
// Comments are cached per call site and tag values, up to 4,096 of them and
// 4 MiB, evicting the oldest. Appending one to a statement's text costs one
// allocation, and a cache key over 256 bytes (a long file path with long
// tag values) one more.
func WithSQLComment(app string, tags ...string) Option {
	if app == "" {
		panic("velox: WithSQLComment: an empty app name")
	}
	for i, t := range tags {
		if t == "" || t == "app" || t == "caller" || slices.Contains(tags[:i], t) {
			panic(fmt.Sprintf("velox: WithSQLComment: tag %q is empty, repeated, or a key velox writes", t))
		}
	}
	tags = slices.Clone(tags)
	return Option{func(c *config) { c.Comment = newCommenter(app, tags) }}
}

// commenter renders and caches a DB's SQL comments.
type commenter struct {
	prefix string   // "app='<encoded app>'"
	tags   []string // the listed tag keys, in order
	keys   []string // the same keys, encoded

	mu    sync.RWMutex
	m     map[string]string // key → " /*…*/"
	order []string          // keys, oldest first, for eviction
	bytes int               // len(key) + len(text) held
}

// maxComments and maxCommentBytes bound a commenter's cache: the entries it
// holds, and their keys' and texts' bytes. Past either, the oldest entry is
// evicted.
var maxComments, maxCommentBytes = 4096, 4 << 20

func newCommenter(app string, tags []string) *commenter {
	c := &commenter{prefix: "app='" + encode(app) + "'", tags: tags}
	for _, t := range tags {
		c.keys = append(c.keys, encode(t))
	}
	return c
}

// Text returns the comment, with its leading space, for the statement's
// Caller and Tags. It does not keep st.
func (c *commenter) Text(st *Statement) string {
	var buf [256]byte
	key := c.key(buf[:0], st)
	c.mu.RLock()
	t, ok := c.m[string(key)]
	c.mu.RUnlock()
	if ok {
		return t
	}
	t = c.render(st)
	c.add(string(key), t)
	return t
}

// key encodes what the comment depends on: the call site, then, for each
// listed tag, whether it is set and its value. Each variable-length part is
// preceded by its length, so no two call sites or tag lists share a key.
func (c *commenter) key(k []byte, st *Statement) []byte {
	k = binary.AppendUvarint(k, uint64(len(st.Caller.File)))
	k = append(k, st.Caller.File...)
	k = binary.AppendUvarint(k, uint64(st.Caller.Line))
	for _, name := range c.tags {
		v, ok := tagValue(st.Tags, name)
		if !ok {
			k = append(k, 0)
			continue
		}
		k = append(k, 1)
		k = binary.AppendUvarint(k, uint64(len(v)))
		k = append(k, v...)
	}
	return k
}

// render writes the comment for st.
func (c *commenter) render(st *Statement) string {
	b := make([]byte, 0, 64)
	b = append(b, " /*"...)
	b = append(b, c.prefix...)
	if f := st.Caller.File; f != "" {
		b = append(b, ",caller='"...)
		b = appendEncoded(b, f)
		b = append(b, "%3A"...) // ':'
		b = strconv.AppendInt(b, int64(st.Caller.Line), 10)
		b = append(b, '\'')
	}
	for i, name := range c.tags {
		if v, ok := tagValue(st.Tags, name); ok {
			b = append(b, ',')
			b = append(b, c.keys[i]...)
			b = append(b, "='"...)
			b = appendEncoded(b, v)
			b = append(b, '\'')
		}
	}
	b = append(b, "*/"...)
	return string(b)
}

// add caches text under key, evicting the oldest entries to make room. An
// entry over the byte bound alone is not cached.
func (c *commenter) add(key, text string) {
	size := len(key) + len(text)
	if size > maxCommentBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, dup := c.m[key]; dup { // another goroutine may have added it
		return
	}
	for len(c.order) > 0 && (len(c.m) >= maxComments || c.bytes+size > maxCommentBytes) {
		old := c.order[0]
		c.order[0] = "" // let the evicted key be freed
		c.order = c.order[1:]
		c.bytes -= len(old) + len(c.m[old])
		delete(c.m, old)
	}
	if len(c.m) >= maxComments {
		return // a bound of 0
	}
	if c.m == nil {
		c.m = make(map[string]string)
	}
	c.m[key] = text
	c.order = append(c.order, key)
	c.bytes += size
}

// tagValue returns the value of the first tag named key, and whether there
// is one.
func tagValue(tags []Tag, key string) (string, bool) {
	for _, t := range tags {
		if t.Key == key {
			return t.Value, true
		}
	}
	return "", false
}

// encode percent-encodes, with uppercase hex, every byte of s outside the
// unreserved set A–Z a–z 0–9 - . _ ~.
func encode(s string) string { return string(appendEncoded(nil, s)) }

func appendEncoded(b []byte, s string) []byte {
	const hex = "0123456789ABCDEF"
	for i := range len(s) {
		switch ch := s[i]; {
		case 'A' <= ch && ch <= 'Z', 'a' <= ch && ch <= 'z', '0' <= ch && ch <= '9',
			ch == '-', ch == '.', ch == '_', ch == '~':
			b = append(b, ch)
		default:
			b = append(b, '%', hex[ch>>4], hex[ch&15])
		}
	}
	return b
}
