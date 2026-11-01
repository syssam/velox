package veloxgql

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

// maxCost is where a cost saturates: far past any limit. add and mul check
// before they compute, so no sum or product of costs in [0, maxCost] wraps.
const maxCost = 1 << 62

// statementCost is what a field that runs a statement adds to a request's cost,
// however many rows it reads: the default MaxCost allows 1000 of them.
const statementCost = 100

// Cost is what op costs under l, with vars its variables. Two parts add up:
//
// The rows part is what the response holds:
//   - each field costs 1, plus its children's cost times its multiplier;
//   - the edges of a connection (a field whose type has edges and pageInfo)
//     multiply by its first or last, or by l.MaxPage when neither is given or
//     the field has no such arguments, the safer count; its totalCount and
//     pageInfo are read once;
//   - the root field nodes multiplies by the number of its ids;
//   - any other field of a list of objects multiplies by l.MaxList;
//   - every branch of a fragment counts, but a selection that @skip(if: true)
//     or @include(if: false) leaves out costs nothing, its condition a
//     literal or a variable's value (a missing variable's default, when it
//     has one); one whose condition is not a boolean (a variable that is
//     missing with no default, or null) counts, as the cost is an upper
//     bound;
//   - __schema and __type cost what they select, counted over the schema
//     itself: one for each field of the response. A walk that passes
//     l.MaxCost stops, saturated, so it does no more work than the limit.
//
// The statements part is what the request sends, and takes no multiplier, as
// the rows of a batch share one statement however many parents they have:
//   - a field that runs a statement adds 100: a root field, a connection, its
//     totalCount (a COUNT of its own) and any other field of an object or a list
//     of objects. first: 0 still counts: a COUNT or a filter still runs;
//   - an input object argument (a where, an orderBy, a mutation's input) adds
//     its size at each use: one for each object and value in it, nested inputs
//     and lists included, read from the variables. A variable used twice counts
//     twice.
//
// Sums and products saturate at 1<<62 (math.MaxInt where int is smaller),
// checked before they are computed, so huge arguments and many aliases cannot
// wrap the cost to a small or negative number. A named
// fragment is costed once for each page size it is spread under, so the walk is
// linear in the document. schema is the server's; op is a parsed and validated
// operation. Extension refuses a request whose Cost is over Limits.MaxCost
// before any statement runs.
func Cost(schema *ast.Schema, op *ast.OperationDefinition, vars map[string]any, l Limits) int {
	if schema == nil || op == nil {
		return 0
	}
	c := &coster{schema: schema, vars: vars, l: l, frags: map[fragKey]cost{}, sizes: map[string]int64{}}
	var root *ast.Definition
	switch op.Operation {
	case ast.Mutation:
		root = schema.Mutation
	case ast.Subscription:
		root = schema.Subscription
	default:
		root = schema.Query
	}
	t := c.set(op.SelectionSet, root, 0)
	// On a platform whose int is 32 bits a cost over math.MaxInt is
	// math.MaxInt, which is over any limit.
	return int(min(add(t.rows, t.statements), math.MaxInt))
}

// cost is a selection's cost, in its two parts: a multiplier applies to rows
// alone.
type cost struct{ rows, statements int64 }

func (a cost) plus(b cost) cost { return cost{add(a.rows, b.rows), add(a.statements, b.statements)} }

// fragKey is a named fragment under a page size: one on a connection's type
// costs its edges by the page of the connection it is spread in.
type fragKey struct {
	name string
	page int64
}

// coster is one Cost call's state.
type coster struct {
	schema *ast.Schema
	vars   map[string]any
	l      Limits
	// frags are the costs of the fragments costed so far; a fragment in
	// progress is 0, so a cycle (which validation refuses) ends.
	frags map[fragKey]cost
	// sizes are the sizes of the variables read so far, by name.
	sizes map[string]int64
	// meta is the walk of __schema and __type, once one is asked for.
	meta *metaWalk
}

// set is the cost of the selections of sels on a value of type parent. page
// multiplies the edges of a parent that is a connection.
func (c *coster) set(sels ast.SelectionSet, parent *ast.Definition, page int64) cost {
	var sum cost
	for _, s := range sels {
		switch s := s.(type) {
		case *ast.Field:
			if c.skipped(s.Directives) {
				continue
			}
			sum = sum.plus(c.field(s, parent, page))
		case *ast.InlineFragment:
			if c.skipped(s.Directives) {
				continue
			}
			p := parent
			if s.TypeCondition != "" {
				if d := c.schema.Types[s.TypeCondition]; d != nil {
					p = d
				}
			}
			sum = sum.plus(c.set(s.SelectionSet, p, page))
		case *ast.FragmentSpread:
			// Checked before spread, so the per-name cost it caches is the
			// fragment's own, whichever spread reaches it first.
			if c.skipped(s.Directives) {
				continue
			}
			sum = sum.plus(c.spread(s, page))
		}
	}
	return sum
}

// skipped reports whether ds leave their selection out: @skip(if: true) or
// @include(if: false), the condition a literal or a variable's value, or a
// missing variable's default. A condition that is not a boolean, a variable
// that is null or missing with no default among them, does not skip.
func (c *coster) skipped(ds ast.DirectiveList) bool {
	for _, d := range ds {
		var leaves bool
		switch d.Name {
		case "skip":
			leaves = true
		case "include":
			leaves = false
		default:
			continue
		}
		a := d.Arguments.ForName("if")
		if a == nil || a.Value == nil {
			continue
		}
		if v, err := a.Value.Value(c.vars); err == nil {
			if b, ok := v.(bool); ok && b == leaves {
				return true
			}
		}
	}
	return false
}

// spread is the cost of a named fragment's selections, computed once for each
// page size.
func (c *coster) spread(s *ast.FragmentSpread, page int64) cost {
	key := fragKey{s.Name, page}
	if n, ok := c.frags[key]; ok {
		return n
	}
	c.frags[key] = cost{}
	def := s.Definition
	if def == nil {
		return cost{}
	}
	p := c.schema.Types[def.TypeCondition]
	n := c.set(def.SelectionSet, p, page)
	c.frags[key] = n
	return n
}

// field is the cost of one field. page is that of parent, when it is a
// connection.
func (c *coster) field(f *ast.Field, parent *ast.Definition, page int64) cost {
	switch f.Name {
	case "__schema", "__type":
		return cost{rows: c.introspection(f)}
	case "__typename":
		return cost{rows: 1}
	}
	def := f.Definition
	if def == nil && parent != nil {
		def = parent.Fields.ForName(f.Name)
	}
	if def == nil {
		return cost{rows: 1}
	}
	var child *ast.Definition
	for t := def.Type; t != nil; t = t.Elem {
		if t.Elem == nil {
			child = c.schema.Types[t.NamedType]
		}
	}
	var childPage int64
	if isConnection(child) {
		childPage = c.pageOf(f)
	}
	children := c.set(f.SelectionSet, child, childPage)
	n := cost{
		rows:       add(1, mul(children.rows, c.multiplier(f, def, parent, child, page))),
		statements: add(children.statements, c.inputs(f, def)),
	}
	if c.runsStatement(f, parent, child) {
		n.statements = add(n.statements, statementCost)
	}
	return n
}

// pageOf is the page size of f, a connection field: its first or last, or
// MaxPage.
func (c *coster) pageOf(f *ast.Field) int64 {
	for _, name := range []string{"first", "last"} {
		if n, ok := c.intArg(f, name); ok {
			return n
		}
	}
	return int64(c.l.MaxPage)
}

// multiplier is how many times f's children are read. page is that of parent,
// when it is a connection.
func (c *coster) multiplier(f *ast.Field, def *ast.FieldDefinition, parent, child *ast.Definition, page int64) int64 {
	switch {
	case isConnection(child):
		return 1 // its edges multiply by the page, and its totalCount does not
	case f.Name == "edges" && isConnection(parent):
		return page
	case parent == c.schema.Query && f.Name == "nodes" && def.Arguments.ForName("ids") != nil:
		if a := f.Arguments.ForName("ids"); a != nil {
			if v, err := a.Value.Value(c.vars); err == nil && v != nil {
				if rv := reflect.ValueOf(v); rv.Kind() == reflect.Slice {
					return int64(rv.Len())
				}
			}
		}
	}
	n := int64(1)
	for t := def.Type; t != nil && isObjectKind(child); t = t.Elem {
		if t.Elem != nil {
			n = mul(n, int64(c.l.MaxList))
		}
	}
	return n
}

// runsStatement reports whether f sends a statement of its own: a root field,
// a connection, its totalCount, or an edge. The edges, pageInfo and an edge
// type's node are read with their connection's page. A field the application
// resolves without one counts too: the cost is an upper bound.
func (c *coster) runsStatement(f *ast.Field, parent, child *ast.Definition) bool {
	switch {
	case parent == nil:
		return false
	case parent == c.schema.Query || parent == c.schema.Mutation || parent == c.schema.Subscription:
		return true
	case isConnection(parent):
		return f.Name == "totalCount"
	case f.Name == "node" && parent.Fields.ForName("cursor") != nil:
		return false
	}
	return isObjectKind(child)
}

// isObjectKind reports whether a value of type d has fields to select.
func isObjectKind(d *ast.Definition) bool {
	return d != nil && d.Kind != ast.Scalar && d.Kind != ast.Enum && d.Kind != ast.InputObject
}

// inputs is the size of f's input object arguments.
func (c *coster) inputs(f *ast.Field, def *ast.FieldDefinition) int64 {
	var sum int64
	for _, ad := range def.Arguments {
		named := ad.Type
		for named.Elem != nil {
			named = named.Elem
		}
		if d := c.schema.Types[named.NamedType]; d == nil || d.Kind != ast.InputObject {
			continue
		}
		if a := f.Arguments.ForName(ad.Name); a != nil {
			sum = add(sum, c.size(a.Value))
		}
	}
	return sum
}

// size is the number of objects and values in v, a literal or a variable.
func (c *coster) size(v *ast.Value) int64 {
	if v == nil {
		return 0
	}
	switch v.Kind {
	case ast.Variable:
		if n, ok := c.sizes[v.Raw]; ok {
			return n
		}
		var n int64
		if val, ok := c.vars[v.Raw]; ok {
			n = sizeOf(reflect.ValueOf(val))
		} else if v.VariableDefinition != nil {
			n = c.size(v.VariableDefinition.DefaultValue)
		}
		c.sizes[v.Raw] = n
		return n
	case ast.ObjectValue, ast.ListValue:
		n := boolInt(v.Kind == ast.ObjectValue)
		for _, child := range v.Children {
			n = add(n, c.size(child.Value))
		}
		return n
	}
	return 1
}

// sizeOf is size of a variable's value: a map is one and its values, a slice its
// elements, anything else one.
func sizeOf(v reflect.Value) int64 {
	for v.IsValid() && (v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer) {
		v = v.Elem()
	}
	if !v.IsValid() {
		return 1 // null
	}
	var n int64
	switch v.Kind() {
	case reflect.Map:
		n = 1
		for it := v.MapRange(); it.Next(); {
			n = add(n, sizeOf(it.Value()))
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			n = add(n, sizeOf(v.Index(i)))
		}
	default:
		n = 1
	}
	return n
}

// isConnection reports whether d has edges and pageInfo fields.
func isConnection(d *ast.Definition) bool {
	return d != nil && d.Kind == ast.Object && d.Fields.ForName("edges") != nil && d.Fields.ForName("pageInfo") != nil
}

// intArg is the integer value of f's argument name, from a literal or a
// variable. A negative one counts as 0 (the resolver refuses it); ok is false
// when f has no such argument or it is null.
func (c *coster) intArg(f *ast.Field, name string) (n int64, ok bool) {
	a := f.Arguments.ForName(name)
	if a == nil || a.Value == nil {
		return 0, false
	}
	v, err := a.Value.Value(c.vars)
	if err != nil {
		if a.Value.Kind == ast.IntValue && strings.Trim(strings.TrimPrefix(a.Value.Raw, "-"), "0123456789") == "" {
			// An integer literal over int64: saturated, or 0 when negative.
			return maxCost * boolInt(!strings.HasPrefix(a.Value.Raw, "-")), true
		}
		return 0, false
	}
	if v == nil {
		return 0, false
	}
	switch v := v.(type) {
	case int:
		n = int64(v)
	case int32:
		n = int64(v)
	case int64:
		n = v
	case float64:
		if v >= maxCost {
			return maxCost, true
		}
		n = int64(v)
	case json.Number:
		var err error
		if n, err = v.Int64(); err != nil {
			// A number too large for int64 is saturated; any other text is not
			// an integer, which the resolver refuses.
			if s := string(v); s != "" && strings.Trim(s, "0123456789") == "" {
				return maxCost, true
			}
			return 0, false
		}
	default:
		return 0, false
	}
	return min(max(n, 0), maxCost), true
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// add is a+b, saturating at maxCost. Both are in [0, maxCost].
func add(a, b int64) int64 {
	if a > maxCost-b {
		return maxCost
	}
	return a + b
}

// mul is a*b, saturating at maxCost. Both are in [0, maxCost].
func mul(a, b int64) int64 {
	if a == 0 || b == 0 {
		return 0
	}
	if a > maxCost/b {
		return maxCost
	}
	return a * b
}
