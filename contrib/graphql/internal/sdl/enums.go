package sdl

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/plugin"
)

// EntityEnums are the enum fields of an entity whose Go type velox-gen
// declares, in the fields' order.
type EntityEnums struct {
	Entity *plugin.Entity
	Enums  []Enum
}

// Enums computes the GraphQL values of each enum field whose Go type
// velox-gen declares, for an application that writes its own SDL. A value
// is the stored one upper-cased, or as graphql.EnumValues names it, as in
// the SDL Render writes. A field with graphql.Skip(graphql.SkipEnumField) is
// a String of its stored values.
//
// It leaves out a join entity, an entity with graphql.Skip(), a field with
// graphql.Skip() and an entity with no such field, and keeps the model's
// order of entities. It checks only the annotations it reads: the SDL is
// the application's, so the checks Build makes of names, keys and types do
// not apply.
func Enums(m *plugin.Model) ([]EntityEnums, error) {
	goEnums := map[string]bool{}
	for _, en := range m.Enums {
		goEnums[en.Type] = true
	}
	shares := newEnumShares(m)
	var out []EntityEnums
	for _, e := range m.Entities {
		where := "schema " + e.Name
		a, err := decode(e.Annotations, where)
		if err != nil {
			return nil, err
		}
		if e.Join || a.Skip&graphql.SkipType != 0 {
			continue
		}
		ee := EntityEnums{Entity: e}
		for i, f := range e.Fields {
			if f.Type != "enum" || !goEnums[f.Enum] || f.PkgPath != "" {
				continue
			}
			fw := where + ", field " + f.Name
			fa, err := decode(f.Annotations, fw)
			if err != nil {
				return nil, err
			}
			switch {
			case fa.Skip == graphql.SkipAll:
				continue
			case fa.Skip&graphql.SkipEnumField != 0:
				ee.Enums = append(ee.Enums, Enum{Field: i})
				continue
			}
			values, err := enumValues(f, fa, fw)
			if err != nil {
				return nil, err
			}
			values, shared, err := shares.claim(f.Enum, fw, f.EnumValues, values)
			if err != nil {
				return nil, err
			}
			ee.Enums = append(ee.Enums, Enum{Name: f.Enum, Field: i, Values: values, Shared: shared})
		}
		if len(ee.Enums) > 0 {
			out = append(out, ee)
		}
	}
	return out, nil
}
