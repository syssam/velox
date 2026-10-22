package veloxgql

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

// maxCost is where a cost saturates: far past any limit, and far enough
// from the int range that adding two saturated costs cannot wrap.
const maxCost = 1 << 62

// Cost is what op costs under l, with vars its variables:
//   - each field costs 1, plus its children's cost times its multiplier;
//   - a connection field (one with first and last arguments whose type has
//     edges and pageInfo) multiplies by first or last, or by l.MaxPage when
//     neither is given; the edges field of a connection type multiplies by 1;
//   - the root field nodes multiplies by the number of its ids;
//   - any other field of a list of objects multiplies by l.MaxList;
//   - every branch of a fragment counts, @skip and @include are not read,
//     and __schema and __type cost nothing.
//
// The sum saturates at 1<<62, so huge arguments cannot wrap it. A named
// fragment is costed once, however often it is spread, so the walk is linear
// in the document. schema is the server's; op is a parsed and validated
// operation. Extension refuses a request whose Cost is over Limits.MaxCost
// before any statement runs.
func Cost(schema *ast.Schema, op *ast.OperationDefinition, vars map[string]any, l Limits) int {
	if schema == nil || op == nil {
		return 0
	}
	c := &coster{schema: schema, vars: vars, l: l, frags: map[string]int{}}
	var root *ast.Definition
	switch op.Operation {
	case ast.Mutation:
		root = schema.Mutation
	case ast.Subscription:
		root = schema.Subscription
	default:
		root = schema.Query
	}
	return int(c.set(op.SelectionSet, root, true))
}

// coster is one Cost call's state.
type coster struct {
	schema *ast.Schema
	vars   map[string]any
	l      Limits
	// frags are the costs of the fragments costed so far, by name; a
	// fragment in progress is 0, so a cycle (which validation refuses) ends.
	frags map[string]int
}

// set is the cost of the selections of sels on a value of type parent.
func (c *coster) set(sels ast.SelectionSet, parent *ast.Definition, root bool) int64 {
	var sum int64
	for _, s := range sels {
		switch s := s.(type) {
		case *ast.Field:
			sum = add(sum, c.field(s, parent, root))
		case *ast.InlineFragment:
			p := parent
			if s.TypeCondition != "" {
				if d := c.schema.Types[s.TypeCondition]; d != nil {
					p = d
				}
			}
			sum = add(sum, c.set(s.SelectionSet, p, false))
		case *ast.FragmentSpread:
			sum = add(sum, c.spread(s))
		}
	}
	return sum
}

// spread is the cost of a named fragment's selections, computed once.
func (c *coster) spread(s *ast.FragmentSpread) int64 {
	if n, ok := c.frags[s.Name]; ok {
		return int64(n)
	}
	c.frags[s.Name] = 0
	def := s.Definition
	if def == nil {
		return 0
	}
	p := c.schema.Types[def.TypeCondition]
	n := c.set(def.SelectionSet, p, false)
	c.frags[s.Name] = int(n)
	return n
}

// field is the cost of one field.
func (c *coster) field(f *ast.Field, parent *ast.Definition, root bool) int64 {
	if f.Name == "__schema" || f.Name == "__type" {
		return 0
	}
	def := f.Definition
	if def == nil && parent != nil {
		def = parent.Fields.ForName(f.Name)
	}
	if def == nil {
		return 1
	}
	var child *ast.Definition
	for t := def.Type; t != nil; t = t.Elem {
		if t.Elem == nil {
			child = c.schema.Types[t.NamedType]
		}
	}
	children := c.set(f.SelectionSet, child, false)
	return add(1, mul(children, c.multiplier(f, def, parent, child, root)))
}

// multiplier is how many times f's children are read.
func (c *coster) multiplier(f *ast.Field, def *ast.FieldDefinition, parent, child *ast.Definition, root bool) int64 {
	switch {
	case isConnection(child) && def.Arguments.ForName("first") != nil && def.Arguments.ForName("last") != nil:
		for _, name := range []string{"first", "last"} {
			if n, ok := c.intArg(f, name); ok {
				return n
			}
		}
		return int64(c.l.MaxPage)
	case f.Name == "edges" && isConnection(parent):
		return 1
	case root && f.Name == "nodes" && def.Arguments.ForName("ids") != nil:
		if a := f.Arguments.ForName("ids"); a != nil {
			if v, err := a.Value.Value(c.vars); err == nil && v != nil {
				if rv := reflect.ValueOf(v); rv.Kind() == reflect.Slice {
					return int64(rv.Len())
				}
			}
		}
	}
	n := int64(1)
	for t := def.Type; t != nil && child != nil && child.Kind != ast.Scalar && child.Kind != ast.Enum && child.Kind != ast.InputObject; t = t.Elem {
		if t.Elem != nil {
			n = mul(n, int64(c.l.MaxList))
		}
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
func add(a, b int64) int64 { return min(a+b, maxCost) }

// mul is a*b, saturating at maxCost. Both are in [0, maxCost].
func mul(a, b int64) int64 {
	if a == 0 || b == 0 {
		return 0
	}
	if a > math.MaxInt64/b || a*b > maxCost {
		return maxCost
	}
	return a * b
}
