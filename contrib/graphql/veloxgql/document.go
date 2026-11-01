package veloxgql

import (
	"fmt"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/errcode"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

const (
	// conflictPairsPerField is the pairs of fields under one response name that
	// differ that a document may hold, for each of Limits.MaxFields. Validation
	// makes an error of each, whether it is returned or not.
	conflictPairsPerField = 5
	// agreeingPairsPerField is the same of pairs that agree, which validation only
	// compares: colocated fragments repeat fields, and need many.
	agreeingPairsPerField = 200
	// mergeStepsPerField is the fields and spreads the walk may visit, each fragment
	// once for each place it is spread, for each of Limits.MaxFields.
	mergeStepsPerField = 100
	// inputFieldsPerUnit is the fields of an input object that weigh one more:
	// gqlgen's coercion and its unmarshalInput each do work for every field of the
	// type, set or not, so an empty object of a rich where input takes 6.6 KB.
	inputFieldsPerUnit = 16
)

// inputWeight is what an object of the input type d weighs, among a request's
// variables and in its cost.
func inputWeight(d *ast.Definition) int64 {
	if d == nil || d.Kind != ast.InputObject {
		return 1
	}
	return 1 + int64(len(d.Fields))/inputFieldsPerUnit
}

// bounds is what the one check of a request holds it to: the limits, and what the
// schema makes of them.
type bounds struct {
	Limits
	schema *ast.Schema
	// names is the longest name: MaxNameBytes, or the schema's longest.
	names int
	// widest is the weight of the schema's widest input type: an object among the
	// variables of a query sent by its hash has no known type.
	widest int64
}

// newBounds is l's bounds on schema, which may be nil.
func newBounds(l Limits, schema *ast.Schema) bounds {
	b := bounds{Limits: l, schema: schema, names: l.MaxNameBytes, widest: 1}
	if schema == nil {
		return b
	}
	name := func(s string) { b.names = max(b.names, len(s)) }
	for n, d := range schema.Types {
		name(n)
		b.widest = max(b.widest, inputWeight(d))
		for _, f := range d.Fields {
			name(f.Name)
			for _, a := range f.Arguments {
				name(a.Name)
			}
		}
		for _, v := range d.EnumValues {
			name(v.Name)
		}
	}
	for n := range schema.Directives {
		name(n)
	}
	return b
}

// checkRequest refuses a request whose document or variables any later stage would
// do more than linear work for, or more than the limits say. A query sent by its
// hash, that an APQ extension after this one fills, has only its variables here: it
// was checked when it was stored.
func checkRequest(p *graphql.RawParams, b bounds) *gqlerror.Error {
	if len(p.OperationName) > b.names {
		return tooLarge(fmt.Sprintf("it has a name of %d bytes, and the most is %d", len(p.OperationName), b.names))
	}
	var op *ast.OperationDefinition
	if p.Query != "" {
		doc, err := checkDocument(p.Query, b)
		if err != nil {
			return err
		}
		op = doc.Operations.ForName(p.OperationName)
	}
	return checkVariables(p.Variables, op, p.Query == "", b)
}

// checkDocument parses query under the limits' MaxTokens and bounds it: everything
// in it is read once, and then each selection once for each place a fragment is
// spread, up to a bound, so its work is linear. Each thing validation or a resolver
// does more for has its limit:
//
//   - the document's bytes (a websocket's messages have no other);
//   - the tokens, which bound what is not named here: the directives and arguments
//     of a field, the variables and operations, and any list in a literal;
//   - fields, and spreads and inline fragments, as written;
//   - fragments, as gqlparser's OverlappingFieldsCanBeMerged stores a pair of any two
//     that may be spread together;
//   - how deep selections, values and types nest, as ValuesOfCorrectType reads a value
//     again at each level above it;
//   - the bytes of any name or alias, which each row and error repeats, and of a
//     string literal, which errors repeat;
//   - pairs of fields under one response name, and the fields spreads reach.
func checkDocument(query string, b bounds) (*ast.QueryDocument, *gqlerror.Error) {
	if len(query) > b.MaxBodyBytes {
		return nil, tooLarge(fmt.Sprintf("its query is over %d bytes", b.MaxBodyBytes))
	}
	doc, err := parser.ParseQueryWithTokenLimit(&ast.Source{Input: query}, b.MaxTokens)
	if err != nil {
		// What gqlgen makes of a parser's error, as it would have failed the same.
		gqlErr := gqlerror.WrapIfUnwrapped(err)
		errcode.Set(gqlErr, errcode.ParseFailed)
		return nil, gqlErr
	}
	w := &docWalk{bounds: b}
	if !w.document(doc) {
		return nil, tooLarge(w.why)
	}
	m := &merger{
		frags:        make(map[string]*ast.FragmentDefinition, len(doc.Fragments)),
		maxConflicts: b.MaxFields * conflictPairsPerField,
		maxAgreeing:  b.MaxFields * agreeingPairsPerField,
		maxSteps:     b.MaxFields * mergeStepsPerField,
		maxDepth:     b.MaxDepth,
	}
	for _, f := range doc.Fragments {
		m.frags[f.Name] = f
	}
	ok := true
	for _, op := range doc.Operations {
		ok = ok && m.merge([]ast.SelectionSet{op.SelectionSet}, 1)
	}
	for _, f := range doc.Fragments {
		ok = ok && m.merge([]ast.SelectionSet{f.SelectionSet}, 1)
	}
	if !ok {
		return nil, tooLarge(m.why())
	}
	return doc, nil
}

func tooLarge(reason string) *gqlerror.Error {
	err := &gqlcore.Error{Code: CodeBadUserInput, Msg: fmt.Sprintf("%v: %s", ErrRequestTooLarge, reason), Err: ErrRequestTooLarge}
	return &gqlerror.Error{Err: err, Message: err.Error(), Extensions: err.Extensions()}
}

// docWalk reads a document as written. Its methods report whether it stayed within
// the bounds, and why not is its why.
type docWalk struct {
	bounds
	fields, spreads int
	frags           map[string]bool
	why             string
}

func (w *docWalk) refuse(format string, args ...any) bool {
	w.why = fmt.Sprintf(format, args...)
	return false
}

func (w *docWalk) document(doc *ast.QueryDocument) bool {
	for _, op := range doc.Operations {
		if !w.name(op.Name) || !w.directives(op.Directives) {
			return false
		}
		for _, v := range op.VariableDefinitions {
			if !w.name(v.Variable) || !w.typeRef(v.Type) || !w.value(v.DefaultValue, 1) || !w.directives(v.Directives) {
				return false
			}
		}
		if !w.set(op.SelectionSet, 1) {
			return false
		}
	}
	for _, f := range doc.Fragments {
		if !w.fragment(f.Name) || !w.name(f.TypeCondition) || !w.directives(f.Directives) || !w.set(f.SelectionSet, 1) {
			return false
		}
	}
	return true
}

func (w *docWalk) name(s string) bool {
	if len(s) > w.names {
		return w.refuse("it has a name of %d bytes, and the most is %d", len(s), w.names)
	}
	return true
}

// fragment counts the fragment name, defined or spread.
func (w *docWalk) fragment(name string) bool {
	if !w.name(name) {
		return false
	}
	if !w.frags[name] {
		if w.frags == nil {
			w.frags = map[string]bool{}
		}
		w.frags[name] = true
		if len(w.frags) > w.MaxFragments {
			return w.refuse("it has %d fragments, and the most is %d", len(w.frags), w.MaxFragments)
		}
	}
	return true
}

func (w *docWalk) deep(depth int) bool {
	if depth > w.MaxDepth {
		return w.refuse("it nests over %d levels", w.MaxDepth)
	}
	return true
}

func (w *docWalk) typeRef(t *ast.Type) bool {
	for depth := 1; t != nil; depth++ {
		if !w.deep(depth) || !w.name(t.NamedType) {
			return false
		}
		t = t.Elem
	}
	return true
}

func (w *docWalk) spread() bool {
	if w.spreads++; w.spreads > w.MaxFields {
		return w.refuse("it has %d spreads and inline fragments, and the most is %d", w.spreads, w.MaxFields)
	}
	return true
}

func (w *docWalk) set(set ast.SelectionSet, depth int) bool {
	if len(set) > 0 && !w.deep(depth) {
		return false
	}
	for _, s := range set {
		ok := true
		switch s := s.(type) {
		case *ast.Field:
			if w.fields++; w.fields > w.MaxFields {
				return w.refuse("it has %d fields, and the most is %d", w.fields, w.MaxFields)
			}
			ok = w.name(s.Alias) && w.name(s.Name) && w.arguments(s.Arguments) && w.directives(s.Directives) && w.set(s.SelectionSet, depth+1)
		case *ast.InlineFragment:
			ok = w.spread() && w.name(s.TypeCondition) && w.directives(s.Directives) && w.set(s.SelectionSet, depth+1)
		case *ast.FragmentSpread:
			ok = w.spread() && w.fragment(s.Name) && w.directives(s.Directives)
		}
		if !ok {
			return false
		}
	}
	return true
}

func (w *docWalk) arguments(as ast.ArgumentList) bool {
	for _, a := range as {
		if !w.name(a.Name) || !w.value(a.Value, 1) {
			return false
		}
	}
	return true
}

func (w *docWalk) directives(ds ast.DirectiveList) bool {
	for _, d := range ds {
		if !w.name(d.Name) || !w.arguments(d.Arguments) {
			return false
		}
	}
	return true
}

func (w *docWalk) value(v *ast.Value, depth int) bool {
	if v == nil {
		return true
	}
	if !w.deep(depth) {
		return false
	}
	switch v.Kind {
	case ast.StringValue, ast.BlockValue:
		if len(v.Raw) > w.MaxLiteralBytes {
			return w.refuse("it has a string of %d bytes, and the most is %d: give it as a variable", len(v.Raw), w.MaxLiteralBytes)
		}
	case ast.ObjectValue, ast.ListValue:
		for _, c := range v.Children {
			if !w.name(c.Name) || !w.value(c.Value, depth+1) {
				return false
			}
		}
	default:
		return w.name(v.Raw)
	}
	return true
}

// merger counts what merging a document's selection sets takes: fields under one
// response name of selection sets that merge are the pairs validation compares.
type merger struct {
	frags                               map[string]*ast.FragmentDefinition
	conflicts, agreeing, steps          int
	maxConflicts, maxAgreeing, maxSteps int
	maxDepth                            int
	deep                                bool
	sigs                                map[string]int // a field's name and arguments, as a number
	fieldSigs                           map[*ast.Field]int
}

// group is the fields of one response name among the selection sets that merge.
type group struct {
	n     int
	first *ast.Field
	sigs  []sigCount // read once a second field comes, as few groups have one
	one   [1]sigCount
	subs  []ast.SelectionSet
}

// sigCount is how many of a group's fields have one signature. A group has few: each
// new one is pairs that differ, which stop the walk.
type sigCount struct{ sig, n int }

// over reports whether a count passed its bound.
func (m *merger) over() bool {
	return m.deep || m.steps > m.maxSteps || m.conflicts > m.maxConflicts || m.agreeing > m.maxAgreeing
}

// why says which bound it was.
func (m *merger) why() string {
	switch {
	case m.deep:
		return fmt.Sprintf("it nests over %d levels", m.maxDepth)
	case m.steps > m.maxSteps:
		return fmt.Sprintf("its fragments spread to over %d fields", m.maxSteps)
	case m.conflicts > m.maxConflicts:
		return fmt.Sprintf("over %d pairs of its fields differ under one response name", m.maxConflicts)
	}
	return fmt.Sprintf("over %d pairs of its fields share a response name", m.maxAgreeing)
}

// sig is the number of f's name and arguments: validation refuses two fields under a
// response name whose differ. Each field is read once.
func (m *merger) sig(f *ast.Field) int {
	if len(f.Arguments) == 0 {
		return m.number(f.Name)
	}
	if s, ok := m.fieldSigs[f]; ok {
		return s
	}
	var key strings.Builder
	key.WriteString(f.Name)
	for _, a := range f.Arguments {
		key.WriteString(" " + a.Name + ":" + a.Value.String())
	}
	s := m.number(key.String())
	if m.fieldSigs == nil {
		m.fieldSigs = map[*ast.Field]int{}
	}
	m.fieldSigs[f] = s
	return s
}

// number is key's number, as sigs holds it.
func (m *merger) number(key string) int {
	s, ok := m.sigs[key]
	if !ok {
		if m.sigs == nil {
			m.sigs = map[string]int{}
		}
		s = len(m.sigs)
		m.sigs[key] = s
	}
	return s
}

// merge counts sets, which merge at depth, and what merges under each response name.
// It reports whether the counts stayed within the bounds; a fragment cycle, which
// validation refuses, ends at the depth.
func (m *merger) merge(sets []ast.SelectionSet, depth int) bool {
	if depth > m.maxDepth {
		m.deep = true
		return false
	}
	groups := map[string]*group{}
	var spreads map[string]bool
	var visit func(set ast.SelectionSet, inline int)
	visit = func(set ast.SelectionSet, inline int) {
		for _, s := range set {
			switch s := s.(type) {
			case *ast.Field:
				m.steps++
				name := s.Alias
				if name == "" {
					name = s.Name
				}
				g := groups[name]
				if g == nil {
					g = &group{}
					groups[name] = g
				}
				m.pair(g, s, inline)
				if len(s.SelectionSet) > 0 {
					g.subs = append(g.subs, s.SelectionSet)
				}
			case *ast.InlineFragment:
				// Validation compares what one holds again, at each level.
				visit(s.SelectionSet, inline+1)
			case *ast.FragmentSpread:
				m.steps++
				if spreads[s.Name] {
					continue
				}
				if spreads == nil {
					spreads = map[string]bool{}
				}
				spreads[s.Name] = true
				if f := m.frags[s.Name]; f != nil {
					visit(f.SelectionSet, inline)
				}
			}
			if m.over() {
				return
			}
		}
	}
	for _, set := range sets {
		visit(set, 0)
		if m.over() {
			return false
		}
	}
	for _, g := range groups {
		if len(g.subs) > 0 && !m.merge(g.subs, depth+1) {
			return false
		}
	}
	return true
}

// pair counts the pairs f makes with the fields of g before it, as validation will
// compare them: once, and again for each inline fragment around them.
func (m *merger) pair(g *group, f *ast.Field, inline int) {
	if g.n == 0 {
		g.first, g.n = f, 1
		return
	}
	if g.n == 1 {
		g.sigs = append(g.one[:0], sigCount{sig: m.sig(g.first), n: 1})
	}
	sig := m.sig(f)
	same := -1
	for i := range g.sigs {
		if g.sigs[i].sig == sig {
			same = i
			break
		}
	}
	if same < 0 {
		same = len(g.sigs)
		g.sigs = append(g.sigs, sigCount{sig: sig})
	}
	m.agreeing += g.sigs[same].n * (1 + inline)
	m.conflicts += (g.n - g.sigs[same].n) * (1 + inline)
	g.sigs[same].n++
	g.n++
}

// checkVariables bounds vars, the variables of op, before gqlgen coerces them and any
// resolver unmarshals them: how deep they nest, their bytes (a websocket's messages
// have no other limit) and their nodes, each object weighing what its input type
// does. A variable that op does not declare is never coerced, and weighs by its
// nodes alone. unknown is true when there is no op to read yet, a query sent by its
// hash, when an object weighs as the schema's widest input does.
func checkVariables(vars map[string]any, op *ast.OperationDefinition, unknown bool, b bounds) *gqlerror.Error {
	w := &varWalk{bounds: b, unknown: unknown}
	for name, v := range vars {
		var d *ast.Definition
		if op != nil && b.schema != nil {
			if vd := op.VariableDefinitions.ForName(name); vd != nil {
				d = b.schema.Types[vd.Type.Name()]
			}
		}
		if w.bytes += len(name); !w.value(v, d, 1) {
			return tooLarge(w.why)
		}
	}
	return nil
}

// varWalk reads variables, as docWalk does a document.
type varWalk struct {
	bounds
	unknown      bool
	nodes, bytes int
	why          string
}

func (w *varWalk) value(v any, d *ast.Definition, depth int) bool {
	if depth > w.MaxDepth {
		w.why = fmt.Sprintf("its variables nest over %d levels", w.MaxDepth)
		return false
	}
	weight := int64(1)
	switch x := v.(type) {
	case string:
		w.bytes += len(x)
	case map[string]any:
		weight = inputWeight(d)
		if w.unknown {
			weight = w.widest
		}
	}
	w.nodes += int(weight)
	if w.nodes > w.MaxVariableNodes {
		w.why = fmt.Sprintf("its variables hold over %d nodes", w.MaxVariableNodes)
		return false
	}
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			if !w.value(e, d, depth+1) {
				return false
			}
		}
	case map[string]any:
		for k, e := range x {
			w.bytes += len(k)
			var fd *ast.Definition
			if d != nil && w.schema != nil && d.Kind == ast.InputObject {
				if f := d.Fields.ForName(k); f != nil {
					fd = w.schema.Types[f.Type.Name()]
				}
			}
			if !w.value(e, fd, depth+1) {
				return false
			}
		}
	}
	if w.bytes > w.MaxBodyBytes {
		w.why = fmt.Sprintf("its variables are over %d bytes", w.MaxBodyBytes)
		return false
	}
	return true
}
