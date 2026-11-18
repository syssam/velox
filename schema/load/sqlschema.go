package load

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/internal/codegen/loadjson"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/internal/desc"
)

// mergedIndexAnnotation is the sqlschema.IndexAnnotation of an index's
// annotations, the several combined in order, and whether it has one.
func mergedIndexAnnotation(list []schema.Annotation) (sqlschema.IndexAnnotation, bool) {
	var out sqlschema.IndexAnnotation
	found := false
	for _, a := range list {
		var v sqlschema.IndexAnnotation
		switch x := a.(type) {
		case sqlschema.IndexAnnotation:
			v = x
		case *sqlschema.IndexAnnotation:
			if x == nil {
				continue
			}
			v = *x
		default:
			continue
		}
		out, _ = out.Merge(v).(sqlschema.IndexAnnotation)
		found = true
	}
	return out, found
}

// lowerIndex sets the Method, Desc, OpClass and Where of out, the loaded form
// of index d, from d's sqlschema.IndexAnnotation, as velox-gen's index rules
// read them, and returns what it cannot hold or what contradicts the builder.
func lowerIndex(d *desc.Index, out *loadjson.Index) error {
	if err := misplaced(d.Annotations, true); err != nil {
		return err
	}
	a, ok := mergedIndexAnnotation(d.Annotations)
	if !ok {
		return nil
	}
	switch {
	case a.StorageParams != "":
		return fmt.Errorf("sqlschema.StorageParams is not held by velox")
	case a.Prefix != 0 || len(a.PrefixColumns) > 0:
		return fmt.Errorf("sqlschema.Prefix and PrefixColumn are not held by velox: a MySQL index prefix is not modeled")
	case len(a.IncludeColumns) > 0:
		return fmt.Errorf("sqlschema.IncludeColumns is not held by velox: a covering index is not modeled")
	}
	out.Method = a.Type
	for _, k := range slices.Sorted(maps.Keys(a.Types)) {
		t := a.Types[k]
		switch {
		case k != "postgres":
			return fmt.Errorf("sqlschema.IndexTypes names the %q dialect, and only postgres has index types in velox", k)
		case out.Method != "" && !strings.EqualFold(out.Method, t):
			return fmt.Errorf("sqlschema.IndexType %q and IndexTypes postgres %q disagree", out.Method, t)
		}
		out.Method = t
	}
	if a.Where != "" {
		if d.Where != "" {
			return fmt.Errorf("the index has Where(%q) and sqlschema.IndexWhere(%q); give it one", d.Where, a.Where)
		}
		out.Where = a.Where
	}
	names := slices.Concat(d.Fields, d.Edges)
	if a.Desc {
		out.Desc = slices.Clone(names)
	}
	for _, n := range slices.Sorted(maps.Keys(a.DescColumns)) {
		if a.DescColumns[n] && !slices.Contains(out.Desc, n) {
			out.Desc = append(out.Desc, n)
		}
	}
	for _, n := range slices.Sorted(maps.Keys(a.OpClassColumns)) {
		out.OpClass = append(out.OpClass, loadjson.OpClass{Name: n, Class: a.OpClassColumns[n]})
	}
	if a.OpClass != "" {
		for _, n := range names {
			if _, own := a.OpClassColumns[n]; !own {
				out.OpClass = append(out.OpClass, loadjson.OpClass{Name: n, Class: a.OpClass})
			}
		}
	}
	return nil
}

// fieldDialects are the names DefaultExprs may use, with sqlite3 read as
// sqlite, as ent spells it.
var fieldDialects = map[string]string{"postgres": "postgres", "mysql": "mysql", "sqlite": "sqlite", "sqlite3": "sqlite"}

// lowerDefault returns the SchemaDefault of field d: the one its builder gave,
// or the one its sqlschema annotation gives, as text for every dialect or for
// each, and what contradicts.
func lowerDefault(d *desc.Field) (map[string]string, error) {
	if err := misplaced(d.Annotations, false); err != nil {
		return nil, err
	}
	var a sqlschema.Annotation
	found := false
	for _, x := range d.Annotations {
		switch v := x.(type) {
		case sqlschema.Annotation:
			a, found = a.Merge(v).(sqlschema.Annotation), true
		case *sqlschema.Annotation:
			if v != nil {
				a, found = a.Merge(*v).(sqlschema.Annotation), true
			}
		}
	}
	if !found || a.Default == "" && a.DefaultExpr == "" && len(a.DefaultExprs) == 0 {
		return d.SchemaDefault, nil
	}
	if len(d.SchemaDefault) > 0 {
		return nil, fmt.Errorf("the field has SchemaDefault and a sqlschema default; give it one")
	}
	if a.Default != "" && a.DefaultExpr != "" {
		return nil, fmt.Errorf("sqlschema.Default and DefaultExpr are both set; give one")
	}
	out := map[string]string{}
	if text := cmp.Or(a.Default, a.DefaultExpr); text != "" {
		for _, k := range []string{"postgres", "mysql", "sqlite"} {
			out[k] = text
		}
	}
	for _, k := range slices.Sorted(maps.Keys(a.DefaultExprs)) {
		dialect, ok := fieldDialects[k]
		switch {
		case !ok:
			return nil, fmt.Errorf("sqlschema.DefaultExprs names the dialect %q; the names are postgres, mysql and sqlite", k)
		case strings.TrimSpace(a.DefaultExprs[k]) == "":
			return nil, fmt.Errorf("sqlschema.DefaultExprs: the %s default is empty", k)
		}
		out[dialect] = a.DefaultExprs[k]
	}
	return out, nil
}

// misplaced returns the first sqlschema annotation of list that is for the
// other kind of element: an Annotation on an index or an IndexAnnotation on a
// field. The old velox ignored one without a word, so a port's field that says
// Desc() did nothing; here it is an error that names where it belongs.
func misplaced(list []schema.Annotation, onIndex bool) error {
	for _, a := range list {
		switch a.(type) {
		case sqlschema.Annotation, *sqlschema.Annotation:
			if onIndex {
				return fmt.Errorf("a sqlschema.Annotation (Default, DefaultExpr or DefaultExprs) is a field's: put it on the field, not the index")
			}
		case sqlschema.IndexAnnotation, *sqlschema.IndexAnnotation:
			if !onIndex {
				return fmt.Errorf("a sqlschema.IndexAnnotation (IndexType, Desc, OpClass, IndexWhere and the like) is an index's: put it on index.Fields(...).Annotations, not the field")
			}
		}
	}
	return nil
}
