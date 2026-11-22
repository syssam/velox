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
// read them, and returns what contradicts.
func lowerIndex(d *desc.Index, out *loadjson.Index) error {
	if err := checkPlacement(d.Annotations, "index"); err != nil {
		return err
	}
	a, ok := mergedIndexAnnotation(d.Annotations)
	if !ok {
		return nil
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
	out.Where = a.Where
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

// lowerDefault returns the database default of field d: the one a mixin of
// velox's gave, or the one its sqlschema annotation gives, as text for every
// dialect or for each, and what contradicts.
func lowerDefault(d *desc.Field) (map[string]string, error) {
	if err := checkPlacement(d.Annotations, "field"); err != nil {
		return nil, err
	}
	a, found := mergedAnnotation(d.Annotations)
	if !found || a.Default == "" && a.DefaultExpr == "" && len(a.DefaultExprs) == 0 {
		return d.SchemaDefault, nil
	}
	if len(d.SchemaDefault) > 0 {
		return nil, fmt.Errorf("the field has a database default of velox's mixin and a sqlschema default; give it one")
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

// mergedAnnotation is the sqlschema.Annotation of list, the several combined in
// order, and whether it has one.
func mergedAnnotation(list []schema.Annotation) (sqlschema.Annotation, bool) {
	var out sqlschema.Annotation
	found := false
	for _, x := range list {
		switch v := x.(type) {
		case sqlschema.Annotation:
			out, _ = out.Merge(v).(sqlschema.Annotation)
			found = true
		case *sqlschema.Annotation:
			if v != nil {
				out, _ = out.Merge(*v).(sqlschema.Annotation)
				found = true
			}
		}
	}
	return out, found
}

// checkPlacement returns why the sqlschema annotations of list, on an element
// of kind "schema", "field", "edge" or "index", are not that element's. The
// old velox ignored a misplaced one
// without a word, so a port's field that says Desc() did nothing; here it is an
// error that names where it belongs.
func checkPlacement(list []schema.Annotation, kind string) error {
	for _, x := range list {
		switch x.(type) {
		case sqlschema.IndexAnnotation, *sqlschema.IndexAnnotation:
			if kind != "index" {
				return fmt.Errorf("a sqlschema.IndexAnnotation (IndexType, Desc, OpClass, IndexWhere and the like) is an index's: put it on index.Fields(...).Annotations, not on %s", article(kind))
			}
		}
	}
	a, ok := mergedAnnotation(list)
	if !ok {
		return nil
	}
	if kind == "index" {
		return fmt.Errorf("a sqlschema.Annotation is a schema's or a field's: put it there, not on an index")
	}
	if kind != "edge" && a.NonUniqueFK {
		return fmt.Errorf("sqlschema.NonUniqueFK is an edge's: put it on the edge whose foreign key it keeps from being unique, not on %s", article(kind))
	}
	if kind != "schema" && (a.Table != "" || a.Schema != "" || a.Collation != "" || len(a.Checks) > 0) {
		return fmt.Errorf("sqlschema Table, Schema, Collation and Checks are a schema's: put them in the schema's Annotations, not on %s", article(kind))
	}
	if kind != "field" && (a.Default != "" || a.DefaultExpr != "" || len(a.DefaultExprs) > 0) {
		return fmt.Errorf("sqlschema Default, DefaultExpr and DefaultExprs are a field's: put them on the field, not on %s", article(kind))
	}
	return nil
}

// article is kind with its indefinite article: "a field", "an edge".
func article(kind string) string {
	if kind == "edge" {
		return "an edge"
	}
	return "a " + kind
}

// lowerSchema returns the annotations of a schema or mixin with its
// sqlschema.Annotation replaced by the annotations velox-gen reads: the
// table, its collation and its checks.
func lowerSchema(list []schema.Annotation) ([]schema.Annotation, error) {
	if err := checkPlacement(list, "schema"); err != nil {
		return nil, err
	}
	a, ok := mergedAnnotation(list)
	if !ok {
		return list, nil
	}
	out := slices.DeleteFunc(slices.Clone(list), func(x schema.Annotation) bool {
		switch x.(type) {
		case sqlschema.Annotation, *sqlschema.Annotation:
			return true
		}
		return false
	})
	switch {
	case a.Table != "":
		out = append(out, &tableAnnotation{Schema: a.Schema, Table: a.Table})
	case a.Schema != "":
		return nil, fmt.Errorf("sqlschema.Schema without a Table: velox names the table with the schema, as sqlschema.SchemaTable")
	}
	if a.Collation != "" {
		out = append(out, &collationAnnotation{Collation: a.Collation})
	}
	if len(a.Checks) > 0 {
		c := &checkAnnotation{}
		for _, n := range slices.Sorted(maps.Keys(a.Checks)) {
			c.Checks = append(c.Checks, checkConstraint{Name: n, Expr: a.Checks[n]})
		}
		out = append(out, c)
	}
	return out, nil
}

// tableAnnotation is the table of a schema, as velox-gen reads it: the
// "Table" annotation of the model.
type tableAnnotation struct {
	Schema string `json:"schema,omitempty"` // the database schema; "" for the connection's default
	Table  string `json:"table"`
}

func (*tableAnnotation) Name() string { return "Table" }

// collationAnnotation is the collation of a schema's table on MySQL: the
// "Collation" annotation of the model.
type collationAnnotation struct {
	Collation string `json:"collation"`
}

func (*collationAnnotation) Name() string { return "Collation" }

// checkAnnotation holds a schema's CHECK constraints: the "Check"
// annotation of the model.
type checkAnnotation struct {
	Checks []checkConstraint `json:"checks"`
}

// checkConstraint is one CHECK constraint.
type checkConstraint struct {
	Name string `json:"name"`
	Expr string `json:"expr"`
}

func (*checkAnnotation) Name() string { return "Check" }

// Merge returns the constraints of a and of other, as a mixin's and its
// schema's combine. velox-gen refuses one name given two expressions.
func (a *checkAnnotation) Merge(other schema.Annotation) schema.Annotation {
	o, ok := other.(*checkAnnotation)
	if !ok {
		return a
	}
	return &checkAnnotation{Checks: slices.Concat(o.Checks, a.Checks)}
}

// fragmentProblem says why text, a column default or an index predicate that
// velox writes into DDL as it is, is not one expression: it holds a statement
// separator or a comment outside its string literals. The old velox refused
// these too. A schema is written by the application, so this catches a mistake
// (a pasted statement, a trailing comment that swallows the rest of the line)
// and not an attack.
func fragmentProblem(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] != '\'' {
			out.WriteByte(text[i])
			continue
		}
		// Skip a string literal; '' inside it is a quote.
		for i++; i < len(text); i++ {
			if text[i] == '\'' {
				if i+1 < len(text) && text[i+1] == '\'' {
					i++
					continue
				}
				break
			}
		}
		out.WriteString("''")
	}
	s := out.String()
	switch {
	case strings.Contains(s, ";"):
		return "a statement separator ';'"
	case strings.Contains(s, "--"):
		return "a comment '--'"
	case strings.Contains(s, "/*"):
		return "a comment '/*'"
	}
	return ""
}

// allSame reports whether every value of m is the same text.
func allSame(m map[string]string) bool {
	var first string
	i := 0
	for _, v := range m {
		if i == 0 {
			first = v
		} else if v != first {
			return false
		}
		i++
	}
	return true
}
