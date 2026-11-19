package sdl

import (
	"fmt"
	"maps"

	"github.com/syssam/velox/plugin"
)

// enumShares records, for each Go enum type velox-gen declares, the GraphQL
// values the first field of that type gave it. velox-gen gives two fields
// one Go type when their enum types get one name and hold the same values,
// in any order, so one GraphQL enum serves both, declared once, in the Go
// type's order of values.
type enumShares struct {
	order map[string][]string    // each type's stored values, in the Go type's order
	seen  map[string]*sharedEnum // by type, once a field claims it
}

// sharedEnum is the GraphQL enum of a Go enum type, as its first field made it.
type sharedEnum struct {
	where    string            // the first field's place, for errors
	byStored map[string]string // the GraphQL value of each stored value
}

func newEnumShares(m *plugin.Model) *enumShares {
	s := &enumShares{order: map[string][]string{}, seen: map[string]*sharedEnum{}}
	for _, en := range m.Enums {
		s.order[en.Type] = en.Values
	}
	return s
}

// claim records the GraphQL values gql of the field at where, of the Go enum
// type typ, one for each of its stored values. It returns the GraphQL values
// in the Go type's order, and whether an earlier field of the type declares
// the enum already. Two fields of one type whose graphql.EnumValues name a
// value differently are refused: they are one GraphQL enum.
func (s *enumShares) claim(typ, where string, stored, gql []string) ([]string, bool, error) {
	return s.claimAs(typ, typ, where, stored, gql)
}

// claimAs is claim with the enum known by key: a GraphQL name and its Go
// type, when graphql.Type names the enum.
func (s *enumShares) claimAs(key, typ, where string, stored, gql []string) ([]string, bool, error) {
	by := make(map[string]string, len(stored))
	for i, v := range stored {
		by[v] = gql[i]
	}
	if prev := s.seen[key]; prev != nil {
		if !maps.Equal(prev.byStored, by) {
			return nil, false, fmt.Errorf("%s: its enum type %s is also that of %s, which names its GraphQL values otherwise: they are one GraphQL enum, so give both the same graphql.EnumValues", where, typ, prev.where)
		}
		return s.ordered(typ, by, gql), true, nil
	}
	s.seen[key] = &sharedEnum{where: where, byStored: by}
	return s.ordered(typ, by, gql), false, nil
}

// ordered is the GraphQL values of by in the Go type's order of typ, or gql
// when the type's values are not the field's.
func (s *enumShares) ordered(typ string, by map[string]string, gql []string) []string {
	order := s.order[typ]
	if len(order) != len(by) {
		return gql
	}
	out := make([]string, 0, len(order))
	for _, v := range order {
		g, ok := by[v]
		if !ok {
			return gql
		}
		out = append(out, g)
	}
	return out
}
