// Package cost is the cost model gqlgenx.Cost documents: what an operation
// reads and sends, counted from its document before any statement runs.
package cost

import (
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

// maxCost is where a cost saturates: far past any limit. add and mul check
// before they compute, so no sum or product of costs in [0, maxCost] wraps.
const maxCost = 1 << 62

// statementCost is what a field that runs a statement adds to a request's cost,
// however many rows it reads: the default MaxCost allows 1500 of them.
const statementCost = 100

// scanCost is what a where predicate adds that no index serves, as it reads the
// table: Contains, HasPrefix, HasSuffix, EqualFold or ContainsFold of a
// <T>WhereInput (the folds compare LOWER of the column, which no plain index
// serves), Has, HasSome, HasEvery or IsEmpty of a JSON array (a containment or
// length test, which only an index of the database's own kind could serve), and
// has<Edge>With,
// whose subquery runs for each row. It is 20 statements: 700 aliases of a
// Contains over 90000 rows took 12.5 s, and the default MaxCost allows about 70.
const scanCost = 20 * statementCost

// countOrderDoc starts the description velox-graphql gives a value of an
// XOrderField that orders by the number of a to-many edge's nodes
// (graphql.OrderField on the edge): a subquery for each row read, as
// has<Edge>With's, so ordering by it adds scanCost.
const countOrderDoc = "Orders by the number of "

// stringUnit is the bytes of a string in an input that cost one more: the default
// MaxCost allows 19 MB of them, each use counted.
const stringUnit = 128

// Limits is what the cost reads of veloxgql.Limits.
type Limits struct{ MaxPage, MaxList, MaxIntrospectionCost int }

// Total is gqlgenx.Cost: what op costs under l, with vars its variables.
func Total(schema *ast.Schema, op *ast.OperationDefinition, vars map[string]any, l Limits) int {
	data, meta, _ := Of(schema, op, vars, l)
	// On a platform whose int is 32 bits a cost over math.MaxInt is
	// math.MaxInt, which is over any limit.
	return int(min(add(data, meta), math.MaxInt))
}

// Of is Total by the parts two limits hold: data, the rows and statements, which
// MaxCost holds, and meta, introspection's, which MaxIntrospectionCost holds; and
// list, the first list of objects the operation selects, or "".
func Of(schema *ast.Schema, op *ast.OperationDefinition, vars map[string]any, l Limits) (data, meta int64, list string) {
	c, list := costOf(schema, op, vars, l)
	return add(c.rows, c.statements), c.meta, list
}

// costOf is Total, by its parts, and the first list of objects the operation selects.
func costOf(schema *ast.Schema, op *ast.OperationDefinition, vars map[string]any, l Limits) (cost, string) {
	if schema == nil || op == nil {
		return cost{}, ""
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
	return c.set(op.SelectionSet, root, 0), c.list
}

// cost is a selection's cost, by its parts: a multiplier applies to rows alone,
// and meta, introspection's, has its own limit.
type cost struct{ rows, statements, meta int64 }

func (a cost) plus(b cost) cost {
	return cost{add(a.rows, b.rows), add(a.statements, b.statements), add(a.meta, b.meta)}
}

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
	// list is the first list of objects the operation selects, "Type.field".
	list string
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
		return cost{meta: c.introspection(f)}
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
	if len(f.SelectionSet) > 0 {
		// A directive leaves a selection out of the response, and each value it
		// was on is read all the same.
		children.rows = max(children.rows, 1)
	}
	n := cost{
		rows:       add(1, mul(children.rows, c.multiplier(f, def, parent, child, page))),
		statements: add(children.statements, c.inputs(f, def)),
		meta:       children.meta,
	}
	if c.runsStatement(f, parent, child) {
		n.statements = add(n.statements, mul(max(c.nodeTypes(parent, child), 1), statementCost))
	}
	if parent != nil && parent == c.schema.Mutation {
		n.statements = add(n.statements, add(c.writes(f, def), c.deletes(f)))
	}
	return n
}

// deletes is what f, a mutation's field, sends beyond its one statement when it is
// a delete<T>: with M2M edges, a check of the row, and a delete of the links of each.
// The type has a field for each, which an O2M edge's looks like: the cost is an
// upper bound.
func (c *coster) deletes(f *ast.Field) int64 {
	name, ok := strings.CutPrefix(f.Name, "delete")
	if !ok {
		return 0
	}
	d := c.schema.Types[name]
	if d == nil {
		return 0
	}
	var joins int64
	for _, fd := range d.Fields {
		child := c.schema.Types[fd.Type.Name()]
		if isConnection(child) || fd.Type.Elem != nil && isObjectKind(child) {
			joins++
		}
	}
	if joins == 0 {
		return 0
	}
	return mul(1+joins, statementCost)
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
	}
	n := int64(1)
	for t := def.Type; t != nil && isObjectKind(child); t = t.Elem {
		if t.Elem != nil {
			rows := c.listRows(def)
			if c.nodeTypes(parent, child) > 0 {
				rows = c.idCount(f)
			}
			n = mul(n, rows)
			if c.list == "" && parent != nil {
				c.list = parent.Name + "." + def.Name
			}
		}
	}
	return n
}

// nodeTypes is the number of types of Node when f, of type child, is a
// root query's field of the interface Node, as node(id:) and nodes(ids:)
// are: it reads each type with a statement of its own (gqlgraph's
// ResolveNode and ResolveNodes). It is 0 for any other field.
func (c *coster) nodeTypes(parent, child *ast.Definition) int64 {
	if parent == nil || parent != c.schema.Query || child == nil || child.Kind != ast.Interface || child.Name != "Node" {
		return 0
	}
	return int64(len(c.schema.GetPossibleTypes(child)))
}

// idCount is how many rows f, a list field of Node such as nodes(ids:),
// reads: the length of its list argument, or MaxPage, which caps it, when
// the argument's value is not known. A single value is a list of one, as
// GraphQL coerces it.
func (c *coster) idCount(f *ast.Field) int64 {
	for _, a := range f.Arguments {
		if a.Value == nil {
			continue
		}
		v, err := a.Value.Value(c.vars)
		if err != nil || v == nil {
			continue
		}
		if l, ok := v.([]any); ok {
			return int64(len(l))
		}
		return 1
	}
	return int64(c.l.MaxPage)
}

// listRows is what a list field reads for each parent: l.MaxList, or the edge's
// graphql.MaxList when that is lower, which the SDL holds in its @goField.
func (c *coster) listRows(def *ast.FieldDefinition) int64 {
	most := int64(c.l.MaxList)
	if d := def.Directives.ForName("goField"); d != nil {
		if a := d.Arguments.ForName("maxList"); a != nil && a.Value != nil {
			if n, err := strconv.ParseInt(a.Value.Raw, 10, 64); err == nil && n > 0 {
				most = min(most, n)
			}
		}
	}
	return most
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
		d := c.schema.Types[named.NamedType]
		if d == nil || d.Kind != ast.InputObject {
			continue
		}
		if a := f.Arguments.ForName(ad.Name); a != nil {
			sum = add(sum, c.size(a.Value, d))
		}
	}
	return sum
}

// writes is what the input objects of f, a mutation's field, make it send beyond
// its one statement: each object's, and in a list each element's, as a bulk
// mutation of the application's writes a row for each.
func (c *coster) writes(f *ast.Field, def *ast.FieldDefinition) int64 {
	var sum int64
	for _, ad := range def.Arguments {
		named := ad.Type
		for named.Elem != nil {
			named = named.Elem
		}
		d := c.schema.Types[named.NamedType]
		a := f.Arguments.ForName(ad.Name)
		if d == nil || d.Kind != ast.InputObject || a == nil || a.Value == nil {
			continue
		}
		v, err := a.Value.Value(c.vars)
		if err != nil {
			continue
		}
		sum = add(sum, writesOf(v, d))
	}
	return sum
}

// writesOf is what v, a value of the input object d or a list of them, sends.
func writesOf(v any, d *ast.Definition) int64 {
	switch v := v.(type) {
	case map[string]any:
		return mul(objectWrites(v, d), statementCost)
	case []any:
		var sum int64
		for _, e := range v {
			sum = add(sum, writesOf(e, d))
		}
		return sum
	}
	return 0
}

// objectWrites is the statements m, a value of the input object d, sends: the row
// read back, and the read of its key and its lock, an update's UpdateQuery.One,
// which an update with edges sends as well; then what its edges send.
func objectWrites(m map[string]any, d *ast.Definition) int64 {
	n := int64(2)
	for k, val := range m {
		switch {
		case strings.HasPrefix(k, "clear") && len(k) > 5:
			// clear<Field> of a column is part of the UPDATE.
			if val == true && d.Fields.ForName(strings.ToLower(k[5:6])+k[6:]) == nil {
				n += 3
			}
		case strings.HasSuffix(k, "IDs"):
			if list, ok := val.([]any); ok && len(list) > 0 {
				n += 2
			}
		case strings.HasSuffix(k, "ID") && val != nil:
			// A one-to-one edge's re-point: the check of the child, the
			// release of the one held, its check, the link and its check.
			n += 5
		}
	}
	return n
}

// size is the size of v, a literal or a variable, of the input type d (nil within a
// scalar): one for each value, with a string one more for each stringUnit of its
// bytes; an object, inputWeight of its type, and its values; a list, its elements.
func (c *coster) size(v *ast.Value, d *ast.Definition) int64 {
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
			n = c.sizeOf(reflect.ValueOf(val), d)
		} else if v.VariableDefinition != nil {
			n = c.size(v.VariableDefinition.DefaultValue, d)
		}
		c.sizes[v.Raw] = n
		return n
	case ast.ObjectValue:
		n := InputWeight(d)
		for _, child := range v.Children {
			n = add(n, c.size(child.Value, c.inputField(d, child.Name)))
			if child.Value != nil && child.Value.Kind != ast.NullValue && (child.Value.Kind != ast.ListValue || len(child.Value.Children) > 0) {
				n = add(n, scans(d, child.Name))
			}
		}
		return n
	case ast.ListValue:
		var n int64
		for _, child := range v.Children {
			n = add(n, c.size(child.Value, d))
		}
		return n
	case ast.StringValue, ast.BlockValue:
		return 1 + int64(len(v.Raw))/stringUnit
	case ast.EnumValue:
		return 1 + countScan(d, v.Raw)
	}
	return 1
}

// countScan is scanCost when value is a value of the enum d that orders by
// the number of an edge's nodes (countOrderDoc); else 0.
func countScan(d *ast.Definition, value string) int64 {
	if d == nil || d.Kind != ast.Enum {
		return 0
	}
	if ev := d.EnumValues.ForName(value); ev != nil && strings.HasPrefix(ev.Description, countOrderDoc) {
		return scanCost
	}
	return 0
}

// sizeOf is size of a variable's value.
func (c *coster) sizeOf(v reflect.Value, d *ast.Definition) int64 {
	for v.IsValid() && (v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer) {
		v = v.Elem()
	}
	if !v.IsValid() {
		return 1 // null
	}
	var n int64
	switch v.Kind() {
	case reflect.Map:
		n = InputWeight(d)
		for it := v.MapRange(); it.Next(); {
			var fd *ast.Definition
			if it.Key().Kind() == reflect.String {
				fd = c.inputField(d, it.Key().String())
			}
			n = add(n, c.sizeOf(it.Value(), fd))
			if it.Key().Kind() == reflect.String && given(it.Value()) {
				n = add(n, scans(d, it.Key().String()))
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			n = add(n, c.sizeOf(v.Index(i), d))
		}
	case reflect.String:
		n = 1 + int64(v.Len())/stringUnit + countScan(d, v.String())
	default:
		n = 1
	}
	return n
}

// given reports whether v, a variable's value of an input field, adds a
// predicate: it is not null, nor an empty list.
func given(v reflect.Value) bool {
	for v.IsValid() && (v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer) {
		v = v.Elem()
	}
	return v.IsValid() && (v.Kind() != reflect.Slice && v.Kind() != reflect.Array || v.Len() > 0)
}

// scans is what the field name of the input type d adds as a scan: scanCost for
// a predicate of a <T>WhereInput that no index serves, the names velox-graphql
// gives Contains, HasPrefix, HasSuffix, EqualFold and ContainsFold, and the
// four of a JSON array, Has, HasSome, HasEvery and IsEmpty, and for
// has<Edge>With; else 0.
func scans(d *ast.Definition, name string) int64 {
	if d == nil || d.Kind != ast.InputObject || !strings.HasSuffix(d.Name, "WhereInput") || d.Fields.ForName(name) == nil {
		return 0
	}
	for _, s := range []string{"Contains", "HasPrefix", "HasSuffix", "EqualFold", "ContainsFold", "Has", "HasSome", "HasEvery", "IsEmpty"} {
		if len(name) > len(s) && strings.HasSuffix(name, s) {
			return scanCost
		}
	}
	if len(name) > len("hasWith") && strings.HasPrefix(name, "has") && strings.HasSuffix(name, "With") {
		return scanCost
	}
	return 0
}

// inputField is the input type of the field name of the input type d, or nil.
func (c *coster) inputField(d *ast.Definition, name string) *ast.Definition {
	if d == nil || d.Kind != ast.InputObject {
		return nil
	}
	f := d.Fields.ForName(name)
	if f == nil {
		return nil
	}
	return c.schema.Types[f.Type.Name()]
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

// inputFieldsPerUnit is the fields of an input object that weigh one more:
// gqlgen's coercion and its unmarshalInput each do work for every field of the
// type, set or not, so an empty object of a rich where input takes 6.6 KB.
const inputFieldsPerUnit = 16

// InputWeight is what an object of the input type d weighs, among a request's
// variables and in its cost.
func InputWeight(d *ast.Definition) int64 {
	if d == nil || d.Kind != ast.InputObject {
		return 1
	}
	return 1 + int64(len(d.Fields))/inputFieldsPerUnit
}
