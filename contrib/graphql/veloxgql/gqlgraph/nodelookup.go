package gqlgraph

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/syssam/velox"
	"github.com/syssam/velox/untyped"
)

// The lookup of node(id:) and nodes(ids:) finds which types of Node hold
// the IDs with one statement, a UNION ALL of one SELECT per type:
//
//	SELECT 0 FROM "badges" WHERE "id" IN (?, ?) UNION ALL SELECT 1 FROM "people" WHERE "id" IN (?, ?)
//
// Each SELECT yields its type's index once per row it finds. The lookup
// reads keys only and applies no policy, scope or interceptor, so its rows
// never reach the caller: they only choose the types that ResolveNode and
// ResolveNodes then read through LoadNode and loadNodes, under each type's
// policies, as before. A type the lookup does not find has no row of the
// IDs, visible or not, so skipping it changes no result.

// lookupReason is the lookup's reason, which raw SQL needs and the Around
// hooks see.
const lookupReason = "velox-graphql: which types of Node hold the IDs; each row found is then read under its type's policies"

// maxCompound is the most SELECTs a statement of the lookup holds on SQLite:
// SQLITE_MAX_COMPOUND_SELECT's default. PostgreSQL and MySQL have no such
// limit.
const maxCompound = 500

// maxPlans bounds the cache of plans, one per list of types: generated code
// passes one list, and an application that passes a new list each call
// gets plans built per call past the bound.
const maxPlans = 32

// maxStatements bounds each plan's cache of rendered statements, one per
// dialect and number of IDs.
const maxStatements = 64

// nodePlan is what the lookup of a list of types needs, built once per list.
type nodePlan struct {
	types  []*Type         // the list, compared on a hit
	tables []untyped.Table // by type
	look   []int           // the types the lookup reads, by index in types
	names  []tableName     // by type, for those in look
	// always is by type: true for one the lookup cannot read, which is
	// read as before, whatever the lookup finds.
	always []bool

	mu  sync.Mutex
	sql map[stmtKey][]chunk // the lookup's statements when every type takes n keys
}

// stmtKey is a dialect and the number of keys each type of a plan takes.
type stmtKey struct {
	d velox.Dialect
	n int
}

// chunk is one statement of a lookup and the parts it reads, parts[lo:hi].
type chunk struct {
	sql    string
	lo, hi int
}

// part is one SELECT of a lookup: a type, by index in the plan's types, and
// its keys.
type part struct {
	typ  int
	name tableName
	keys []any
}

// tableName is the names a type's SELECT needs: its table's schema, empty
// for the default, the table, and its key column.
type tableName struct {
	schema, table, key string
}

// planKey is a list of types by its first element's address and its
// length; a hit is compared with the plan's copy, so a list changed in
// place is planned again.
type planKey struct {
	first **Type
	n     int
}

var plans struct {
	mu sync.Mutex
	m  map[planKey]*nodePlan
}

// planFor returns types' plan, from the cache when it has it. A type whose
// key is not one column is an error, before any statement.
func planFor(types []*Type) (*nodePlan, error) {
	for _, c := range types {
		if len(c.Key) != 1 {
			return nil, fmt.Errorf("gqlgraph: type %s has %d key columns, and a Node has one", c.Name, len(c.Key))
		}
	}
	if len(types) == 0 {
		return &nodePlan{}, nil
	}
	k := planKey{&types[0], len(types)}
	plans.mu.Lock()
	p := plans.m[k]
	plans.mu.Unlock()
	if p != nil && slices.Equal(p.types, types) {
		return p, nil
	}
	p = &nodePlan{
		types: slices.Clone(types), tables: make([]untyped.Table, len(types)),
		names: make([]tableName, len(types)), always: make([]bool, len(types)),
	}
	for i, c := range types {
		t := c.table()
		p.tables[i] = t
		if col := c.Key[0].Index; lookable(t, col) {
			info := t.Info()
			p.names[i] = tableName{schema: info.Schema, table: info.Table, key: info.Columns[col].Name}
			p.look = append(p.look, i)
		} else {
			p.always[i] = true
		}
	}
	plans.mu.Lock()
	if plans.m == nil {
		plans.m = map[planKey]*nodePlan{}
	}
	if len(plans.m) < maxPlans || plans.m[k] != nil {
		plans.m[k] = p
	}
	plans.mu.Unlock()
	return p, nil
}

// lookable reports whether the lookup can read t's key column col: one of
// an integer or a string type, which every dialect compares with a bound
// int64 or string, and with no codec, which raw SQL would skip. Any other
// key, a uint64 among them, is read as before.
func lookable(t untyped.Table, col int) bool {
	if t.Err() != nil {
		return false
	}
	info := t.Info()
	if info == nil || col < 0 || col >= len(info.Columns) || info.Columns[col].Codec {
		return false
	}
	rt, _ := info.Columns[col].Type.(reflect.Type)
	if rt == nil {
		return false
	}
	switch rt.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.String:
		return true
	}
	return false
}

// bindKey is k, of a lookable column's Go type, as the lookup binds it: an
// int64 or a string, which every driver takes.
func bindKey(k any) any {
	v := reflect.ValueOf(k)
	switch {
	case v.CanInt():
		return v.Int()
	case v.CanUint():
		return int64(v.Uint()) //nolint:gosec // lookable admits uints of 32 bits at most
	}
	return v.String()
}

// lookupNodes returns, by type, whether ResolveNode or ResolveNodes must
// read the type for ids: true for a type the lookup found a row of, and
// for one it cannot read. A lookup that a hook denies finds every type, so
// each is read as before; any other error is returned. On a DB with a
// Rewrite hook, which may point a type at another table than the one the
// lookup's raw SQL names, there is no lookup and every type is read.
func lookupNodes(ctx context.Context, db *velox.DB, types []*Type, ids []any) ([]bool, error) {
	p, err := planFor(types)
	if err != nil {
		return nil, err
	}
	if untyped.Rewrites(db) {
		read := make([]bool, len(types))
		for i := range read {
			read[i] = true
		}
		return read, nil
	}
	read := slices.Clone(p.always)
	parts, uniform := p.parts(ids)
	if len(parts) == 0 {
		return read, nil
	}
	var chunks []chunk
	if uniform {
		chunks = p.statements(db.Dialect(), parts)
	} else {
		chunks = render(db.Dialect(), parts)
	}
	for _, c := range chunks {
		var args []any
		for _, pt := range parts[c.lo:c.hi] {
			args = append(args, pt.keys...)
		}
		err := scanTypes(ctx, db, c.sql, args, read)
		switch {
		case errors.Is(err, velox.ErrDenied):
			for i := range read {
				read[i] = true
			}
			return read, nil
		case err != nil:
			return nil, err
		}
	}
	return read, nil
}

// scanTypes runs one statement of a lookup and sets read[i] for each type
// index i it yields.
func scanTypes(ctx context.Context, db *velox.DB, sql string, args []any, read []bool) error {
	for row, err := range db.Rows(ctx, lookupReason, sql, args...) {
		if err != nil {
			return err
		}
		var i int64
		if err := row.Scan(&i); err != nil {
			return err
		}
		if i >= 0 && i < int64(len(read)) {
			read[i] = true
		}
	}
	return nil
}

// parts returns the lookup's parts for ids: one per lookable type that can
// hold one of them, with the distinct keys it can hold. uniform says every
// lookable type has a part, each of the same number of keys, so the
// statements are the plan's cached ones.
func (p *nodePlan) parts(ids []any) (parts []part, uniform bool) {
	uniform = true
	for _, i := range p.look {
		t, col := p.tables[i], p.types[i].Key[0].Index
		var keys []any
		seen := map[any]bool{}
		for _, id := range ids {
			k, err := t.Convert(col, id)
			if err != nil || k == nil {
				continue // names no row of the type
			}
			b := bindKey(k)
			if !seen[b] {
				seen[b] = true
				keys = append(keys, b)
			}
		}
		if len(keys) == 0 {
			uniform = false
			continue
		}
		if len(parts) > 0 && len(keys) != len(parts[0].keys) {
			uniform = false
		}
		parts = append(parts, part{typ: i, name: p.names[i], keys: keys})
	}
	return parts, uniform
}

// statements returns the plan's lookup statements for d, when parts has a
// part for every lookable type, each of n keys, rendering them on first use.
func (p *nodePlan) statements(d velox.Dialect, parts []part) []chunk {
	k := stmtKey{d, len(parts[0].keys)}
	p.mu.Lock()
	cs, ok := p.sql[k]
	p.mu.Unlock()
	if ok {
		return cs
	}
	cs = render(d, parts)
	p.mu.Lock()
	if p.sql == nil {
		p.sql = map[stmtKey][]chunk{}
	}
	if len(p.sql) < maxStatements {
		p.sql[k] = cs
	}
	p.mu.Unlock()
	return cs
}

// render writes the lookup of parts, as few statements as hold them: each
// binds at most maxFilterNodes parameters, under every dialect's limit, and
// on SQLite holds at most maxCompound SELECTs. A type's index is its
// SELECT's one column.
func render(d velox.Dialect, parts []part) []chunk {
	var out []chunk
	var b strings.Builder
	lo, params := 0, 0
	flush := func(hi int) {
		if hi > lo {
			out = append(out, chunk{sql: b.String(), lo: lo, hi: hi})
		}
		b.Reset()
		lo, params = hi, 0
	}
	for i, pt := range parts {
		full := i > lo && (params+len(pt.keys) > maxFilterNodes || d == velox.SQLite && i-lo >= maxCompound)
		if full {
			flush(i)
		}
		if i > lo {
			b.WriteString(" UNION ALL ")
		}
		params = renderPart(d, &b, pt, params)
	}
	flush(len(parts))
	return out
}

// renderPart writes pt's SELECT, "SELECT typ FROM table WHERE key IN
// (...)", whose placeholders follow the first n of its statement, and
// returns n plus its own.
func renderPart(d velox.Dialect, b *strings.Builder, pt part, n int) int {
	b.WriteString("SELECT ")
	b.WriteString(strconv.Itoa(pt.typ))
	b.WriteString(" FROM ")
	if pt.name.schema != "" {
		quote(d, b, pt.name.schema)
		b.WriteByte('.')
	}
	quote(d, b, pt.name.table)
	b.WriteString(" WHERE ")
	quote(d, b, pt.name.key)
	if len(pt.keys) == 1 {
		b.WriteString(" = ")
		n++
		placeholder(d, b, n)
		return n
	}
	b.WriteString(" IN (")
	for j := range pt.keys {
		if j > 0 {
			b.WriteString(", ")
		}
		n++
		placeholder(d, b, n)
	}
	b.WriteByte(')')
	return n
}

// quote writes ident quoted for d, as velox quotes its own: in backquotes
// on MySQL, else in double quotes, the quote doubled inside it.
func quote(d velox.Dialect, b *strings.Builder, ident string) {
	q := byte('"')
	if d == velox.MySQL {
		q = '`'
	}
	b.WriteByte(q)
	for i := range len(ident) {
		if ident[i] == q {
			b.WriteByte(q)
		}
		b.WriteByte(ident[i])
	}
	b.WriteByte(q)
}

// placeholder writes the n-th (from 1) parameter's placeholder for d.
func placeholder(d velox.Dialect, b *strings.Builder, n int) {
	if d == velox.Postgres {
		b.WriteByte('$')
		b.WriteString(strconv.Itoa(n))
		return
	}
	b.WriteByte('?')
}
