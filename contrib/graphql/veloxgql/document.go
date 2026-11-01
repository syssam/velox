package veloxgql

import (
	"fmt"

	"github.com/99designs/gqlgen/graphql/errcode"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

const (
	// mergePairsPerField is the pairs of fields sharing a response name that a
	// document may hold, for each of Limits.MaxFields. gqlparser's
	// OverlappingFieldsCanBeMerged compares each pair, and one that differs costs
	// about 3.5 KB through gqlgen: under the default MaxFields, 10000 pairs stay
	// under 50 MB.
	mergePairsPerField = 5
	// mergeStepsPerField is the fields the walk of checkDocument may visit, each
	// fragment once for each place it is spread, for each of Limits.MaxFields.
	mergeStepsPerField = 100
)

// checkDocument parses query under l.MaxTokens, and refuses a document over
// l.MaxFields, as validation, which runs next, is quadratic in the fields that
// share a response name. It reads each selection once for each place a fragment
// is spread, up to a bound, so its work is linear.
func checkDocument(query string, l Limits) *gqlerror.Error {
	doc, err := parser.ParseQueryWithTokenLimit(&ast.Source{Input: query}, l.MaxTokens)
	if err != nil {
		// What gqlgen makes of a parser's error, as it would have failed the same.
		gqlErr := gqlerror.WrapIfUnwrapped(err)
		errcode.Set(gqlErr, errcode.ParseFailed)
		return gqlErr
	}
	n := 0
	for _, op := range doc.Operations {
		n += countFields(op.SelectionSet)
	}
	for _, f := range doc.Fragments {
		n += countFields(f.SelectionSet)
	}
	if n > l.MaxFields {
		return tooLarge(fmt.Sprintf("it has %d fields, and the most is %d", n, l.MaxFields))
	}
	m := &merger{
		frags:    make(map[string]*ast.FragmentDefinition, len(doc.Fragments)),
		maxPairs: l.MaxFields * mergePairsPerField,
		maxSteps: l.MaxFields * mergeStepsPerField,
	}
	for _, f := range doc.Fragments {
		m.frags[f.Name] = f
	}
	ok := true
	for _, op := range doc.Operations {
		ok = ok && m.merge([]ast.SelectionSet{op.SelectionSet})
	}
	for _, f := range doc.Fragments {
		ok = ok && m.merge([]ast.SelectionSet{f.SelectionSet})
	}
	switch {
	case ok:
		return nil
	case m.steps > m.maxSteps:
		return tooLarge(fmt.Sprintf("its fragments spread to over %d fields", m.maxSteps))
	}
	return tooLarge(fmt.Sprintf("over %d pairs of its fields share a response name", m.maxPairs))
}

func tooLarge(reason string) *gqlerror.Error {
	err := &gqlcore.Error{Code: CodeBadUserInput, Msg: fmt.Sprintf("%v: %s", ErrRequestTooLarge, reason), Err: ErrRequestTooLarge}
	return &gqlerror.Error{Err: err, Message: err.Error(), Extensions: err.Extensions()}
}

// countFields is the number of fields set holds, as written.
func countFields(set ast.SelectionSet) int {
	n := 0
	for _, s := range set {
		switch s := s.(type) {
		case *ast.Field:
			n += 1 + countFields(s.SelectionSet)
		case *ast.InlineFragment:
			n += countFields(s.SelectionSet)
		}
	}
	return n
}

// merger counts what merging a document's selection sets takes: fields under one
// response name of selection sets that merge are the pairs validation compares.
type merger struct {
	frags              map[string]*ast.FragmentDefinition
	pairs, steps       int
	maxPairs, maxSteps int
}

// group is the fields of one response name among the selection sets that merge.
type group struct {
	n    int
	subs []ast.SelectionSet
}

// merge counts sets, which merge, and what merges under each response name. It
// reports whether the counts stayed within the bounds; a fragment cycle, which
// validation refuses, passes them.
func (m *merger) merge(sets []ast.SelectionSet) bool {
	groups := map[string]*group{}
	var spreads map[string]bool
	var visit func(set ast.SelectionSet)
	visit = func(set ast.SelectionSet) {
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
				m.pairs += g.n // the pairs it makes with those before it
				g.n++
				if len(s.SelectionSet) > 0 {
					g.subs = append(g.subs, s.SelectionSet)
				}
			case *ast.InlineFragment:
				visit(s.SelectionSet)
			case *ast.FragmentSpread:
				if spreads[s.Name] {
					continue
				}
				if spreads == nil {
					spreads = map[string]bool{}
				}
				spreads[s.Name] = true
				if f := m.frags[s.Name]; f != nil {
					visit(f.SelectionSet)
				}
			}
			if m.steps > m.maxSteps || m.pairs > m.maxPairs {
				return
			}
		}
	}
	for _, set := range sets {
		visit(set)
		if m.steps > m.maxSteps || m.pairs > m.maxPairs {
			return false
		}
	}
	for _, g := range groups {
		if len(g.subs) > 0 && !m.merge(g.subs) {
			return false
		}
	}
	return true
}
