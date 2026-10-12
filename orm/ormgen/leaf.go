package ormgen

import (
	"bytes"
	"fmt"
	"slices"
	"strconv"
	"strings"

	vgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

var opConst = map[vgen.Op]string{
	vgen.EQ: "orm.OpEQ", vgen.NEQ: "orm.OpNEQ", vgen.GT: "orm.OpGT", vgen.GTE: "orm.OpGTE",
	vgen.LT: "orm.OpLT", vgen.LTE: "orm.OpLTE", vgen.IsNil: "orm.OpIsNil", vgen.NotNil: "orm.OpNotNil",
	vgen.In: "orm.OpIn", vgen.NotIn: "orm.OpNotIn", vgen.EqualFold: "orm.OpEqualFold",
	vgen.Contains: "orm.OpContains", vgen.ContainsFold: "orm.OpContainsFold",
	vgen.HasPrefix: "orm.OpHasPrefix", vgen.HasSuffix: "orm.OpHasSuffix",
}

// takesString reports whether the operator's operand is a substring, whatever
// the Go type of the field.
func takesString(o vgen.Op) bool {
	switch o {
	case vgen.EqualFold, vgen.Contains, vgen.ContainsFold, vgen.HasPrefix, vgen.HasSuffix:
		return true
	}
	return false
}

// columnTypeName names the descriptor type of columns of one Go type, e.g.
// StringColumn, NullableTimeColumn, Int64KeyColumn, UserStatusColumn.
func columnTypeName(gotype string, nullable, key bool) string {
	t := gotype
	switch {
	case t == "[]byte":
		t = "Bytes"
	case strings.HasPrefix(t, "map["):
		t = "Map"
	}
	// A slice is named after its element: []string is StringListColumn.
	plural := ""
	for strings.HasPrefix(t, "[]") || strings.HasPrefix(t, "*") {
		if t[0] == '[' {
			plural += "List"
		}
		t = strings.TrimPrefix(strings.TrimPrefix(t, "[]"), "*")
	}
	if strings.ContainsAny(t, "[{( ") {
		t, plural = "Value", "List" // a slice of something without a name
	}
	if i := strings.LastIndexByte(t, '.'); i >= 0 {
		t = t[i+1:]
	}
	t += plural
	var b strings.Builder
	for _, r := range t {
		if r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			b.WriteRune(r)
		}
	}
	t = b.String()
	if t == "" {
		t = "Value"
	}
	t = strings.ToUpper(t[:1]) + t[1:]
	if nullable {
		t = "Nullable" + t
	}
	if key {
		t += "Key"
	}
	return t + "Column"
}

// opDoc completes the doc comment of each predicate method: "EQ matches ...".
var opDoc = map[string]string{
	"EQ": "matches the rows whose column equals v.", "NEQ": "matches the rows whose column differs from v.",
	"In": "matches the rows whose column is one of vs; without any, none.", "NotIn": "matches the rows whose column is none of vs.",
	"GT": "matches the rows whose column is greater than v.", "GTE": "matches the rows whose column is v or greater.",
	"LT": "matches the rows whose column is less than v.", "LTE": "matches the rows whose column is v or less.",
	"Contains": "matches the rows whose column contains v.", "ContainsFold": "matches the rows whose column contains v, whatever the case.",
	"HasPrefix": "matches the rows whose column starts with v.", "HasSuffix": "matches the rows whose column ends with v.",
	"EqualFold": "matches the rows whose column equals v, whatever the case.",
	"IsNil":     "matches the rows whose column is NULL.", "NotNil": "matches the rows whose column is not NULL.",
}

// leafReserved are the identifiers every entity package declares itself.
// ModelName, not TypeName or EntityName: a schema has fields called
// type_name and entity_name (one and two of them in a real schema of 455
// entities, none called model_name), and the field would then lose its
// plain name to the constant.
var leafReserved = []string{"And", "Or", "Not", "Table", "ModelName", "EdgeMetas"}

// namer hands out the identifiers of one entity package: the first to ask for
// a name gets it, the next gets the name with a suffix.
type namer struct {
	taken map[string]bool
	// orderType is the name of the type of the fields a list can be ordered
	// by, in a tree with a GraphQL schema.
	orderType string
}

func (n *namer) unique(name, suffix string) string {
	if n.taken[name] {
		name += suffix
	}
	for n.taken[name] {
		name += "X"
	}
	n.taken[name] = true
	return name
}

// enumNames names what an entity package declares for its fields, ahead of
// everything else in it. A column is named like the field of the entity's
// struct, user.Name, because it is what application code writes most: every
// condition, assignment and order goes through it. Then the values of the
// enums, user.RoleAdmin, and their types, named after the field with Enum
// (post.StatusEnum: post.Status is the column). A name that is taken gets a
// suffix. Package entity and the entity package both need the result.
func (g *generator) enumNames(e *ent) (*namer, map[*fld]string, map[*fld][]string, map[*fld]string) {
	n := &namer{taken: map[string]bool{}}
	for _, s := range leafReserved {
		n.taken[s] = true
	}
	columns := map[*fld]string{}
	for _, f := range e.Fields {
		if !f.Hidden {
			columns[f] = n.unique(f.GoName, "Field")
		}
	}
	types := map[*fld]string{}
	consts := map[*fld][]string{}
	var enums []*fld
	for _, f := range e.Fields {
		if f.Enum == "" {
			continue
		}
		enums = append(enums, f)
		for _, en := range f.F.Enums {
			consts[f] = append(consts[f], n.unique(en.Name, "Value"))
		}
	}
	for _, f := range enums {
		types[f] = n.unique(f.F.StructField()+"Enum", "Type")
	}
	// With a GraphQL schema, the fields a list can be ordered by are named
	// here too (see gqlOrder for why not in package entity).
	if g.cfg.GraphQL != nil {
		n.orderType = n.unique("OrderField", "Type")
		if q := g.gql[e]; q != nil {
			for _, o := range q.Order {
				n.taken[o.Const] = true
			}
		}
	}
	return n, types, consts, columns
}

// columnsFile is the file of the column types in the package of e.
func columnsFile(e *ent) string {
	if fileName(e) == "columns" {
		return "columns_gen"
	}
	return "columns"
}

// leaf generates the package of one entity: a descriptor per field and per
// edge, so that application code reads
// db.User.Query(tx).Where(user.Name.EQ(...), user.Posts.Has()). The
// builders are reached through the root package, not from here.
//
// It returns the files of the package by name: the entity's own, and the one
// of its column types.
//
// It imports the predicate package and not package entity (nor another entity's package): a
// change to one entity rebuilds its package, package entity and what uses them,
// and not the other 453 per-entity packages.
func (g *generator) leaf(e *ent) (map[string][]byte, error) {
	im := newImports()
	im.use(ormPath, "orm")
	im.use(g.cfg.Package+"/predicate", "predicate")
	var b bytes.Buffer
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	names, enumType, enumConsts, columns := g.enumNames(e)
	unique := names.unique
	taken := names.taken
	var enums []*fld
	for _, f := range e.Fields {
		if f.Enum != "" {
			enums = append(enums, f)
		}
	}

	// localType is the Go type of a field in this package's terms.
	localType := func(f *fld) (string, error) {
		if f.Enum != "" {
			return enumType[f], nil
		}
		ref, err := goTypeOf(f.F)
		if err != nil {
			return "", err
		}
		if ref.PkgPath != "" {
			if got := im.use(ref.PkgPath, ref.PkgName); got != ref.PkgName && ref.PkgName != "" {
				return "", fmt.Errorf("field %q: two packages named %q are used as field types", f.F.Name, ref.PkgName)
			}
		}
		return ref.Expr, nil
	}

	// One descriptor type per distinct (Go type, operators, capabilities).
	type shape struct {
		name     string
		gotype   string
		ops      []vgen.Op
		nullable bool
		clear    bool
		add      bool
		order    bool
		keep     bool
	}
	shapes := map[string]*shape{}
	var order []*shape
	desc := map[*fld]*shape{}
	for _, f := range e.Fields {
		if f.Hidden {
			continue
		}
		gt, err := localType(f)
		if err != nil {
			return nil, err
		}
		ops := f.F.Ops()
		// The key of an edge is compared against a bound like any other
		// column of its type. velox (as Ent) lists only equality for an
		// edge field, and its generic column types had the rest anyway:
		// code that applies one numeric filter to every int64 column,
		// keys included, was written against that.
		if f.F.IsEdgeField() && !slices.Contains(ops, vgen.GT) && f.F.Type != nil {
			switch t := f.F.Type.Type; {
			case t.Numeric(), t == field.TypeString, t == field.TypeTime, t == field.TypeUUID:
				at := slices.Index(ops, vgen.NotIn) + 1
				ops = slices.Insert(slices.Clone(ops), at, vgen.GT, vgen.GTE, vgen.LT, vgen.LTE)
			}
		}
		var names []string
		ordered := false
		for _, o := range ops {
			names = append(names, o.Name())
			if o == vgen.GT {
				ordered = true
			}
		}
		// Every column can be asked whether it is NULL, one that cannot be
		// included (it never is): code written once for every column of a
		// type, a filter chosen at run time, asks without knowing which it
		// has, as it could of velox's columns. Measured on a schema of 455
		// entities, the two methods on every column type cost nothing a
		// build shows (91.0 and 91.2 CPU-s for the generated tree). Only a
		// column that accepts NULL can be cleared.
		nullable := f.Null
		if !slices.Contains(ops, vgen.IsNil) {
			ops = append(ops[:len(ops):len(ops)], vgen.IsNil, vgen.NotNil)
			names = append(names, vgen.IsNil.Name(), vgen.NotNil.Name())
		}
		// Anything the database can compare can be ordered by, a foreign key
		// included, though it has no range predicates.
		sortable := ordered || f.IsID || (f.F.Type != nil && f.F.Type.Type != field.TypeJSON && f.F.Type.Type != field.TypeBytes && f.F.Type.Type != field.TypeOther)
		// What a column offers follows from its Go type, whether it can be
		// NULL, and whether it is the key of an edge (a key is not
		// incremented): those three also name its type. Add on the id is
		// refused when it runs.
		isKey := f.F.IsEdgeField()
		add := !isKey && (f.F.Type != nil && f.F.Type.Numeric() || f.F.IsOther())
		s := &shape{gotype: gt, ops: ops, nullable: nullable, clear: nullable && f.F.Optional, add: add, order: sortable, keep: f.F.UpdateDefault}
		key := fmt.Sprintf("%s|%s|%t|%t|%t|%t|%t", gt, strings.Join(names, ","), s.nullable, s.clear, s.add, s.order, s.keep)
		if have, ok := shapes[key]; ok {
			desc[f] = have
			continue
		}
		// The name says what sets the type apart from the plain column of
		// its Go type, so that two of them in one package are not Column
		// and Column2: Auto for a column with an update default (it has
		// Keep), Required for one that can hold NULL and cannot be cleared
		// (Nillable without Optional), ID for an id that is not compared
		// like other columns of its type (a string id has no Contains).
		base := columnTypeName(gt, s.nullable, isKey)
		qualifier := ""
		if s.keep {
			qualifier += "Auto"
		}
		if s.nullable && !s.clear {
			qualifier += "Required"
		}
		if f.IsID && !isKey && f.F.Type != nil && f.F.Type.Type == field.TypeString {
			base = strings.TrimSuffix(base, "Column") + "IDColumn"
		}
		base = qualifier + base
		s.name = base
		// A number for what the above does not tell apart: two Go types
		// with one name (map[string]int and map[string]any), a field called
		// like the type (auto_time_column).
		for i := 2; taken[s.name]; i++ {
			s.name = fmt.Sprintf("%s%d", base, i)
		}
		taken[s.name] = true
		shapes[key] = s
		order = append(order, s)
		desc[f] = s
	}

	w("// Table is the table of %s, and ModelName the name of the entity: what the\n// Type method of a query or a mutation of it returns.\nconst (\n\tTable = %q\n\tModelName = %q\n)\n\n", e.Name, e.T.Table(), e.Name)
	for _, f := range enums {
		w("// %[1]s is the type of the %[2]s field.\ntype %[1]s string\n\n// Values of the %[2]s field.\nconst (\n", enumType[f], f.F.Name)
		for i, en := range f.F.Enums {
			w("\t%s %s = %q\n", enumConsts[f][i], enumType[f], en.Value)
		}
		w(")\n\n")
		// The value a create writes when the field is not set, by name: an
		// application shows it in a form, or tells "left as it was" from
		// "chosen". (velox has it as a variable of the same name.) A
		// function, not a constant: a second constant of the enum's type
		// with the value of one of its values is a second name for that
		// value, and what binds a GraphQL enum to the constants of the type
		// (gqlc) can no longer tell which is the value's.
		if def, ok := f.F.DefaultValue().(string); ok && f.F.Default && !f.F.DefaultFunc() {
			for i, en := range f.F.Enums {
				if en.Value == def {
					w("// %[1]s returns the value of the %[2]s field of a row created without one.\nfunc %[1]s() %[4]s { return %[3]s }\n\n", unique("Default"+f.F.StructField(), "Value"), f.F.Name, enumConsts[f][i], enumType[f])
				}
			}
		}
		values := unique(f.F.StructField()+"Values", "List")
		w("// %s returns the values of the %s field, in the order of the schema.\nfunc %s() []%s {\n\treturn []%s{%s}\n}\n\n", values, f.F.Name, values, enumType[f], enumType[f], strings.Join(enumConsts[f], ", "))
	}
	if q := g.gql[e]; q != nil && q.HasOrder {
		w("// %[1]s is a field a list of %[2]s can be ordered by, as the GraphQL schema\n// names it: the Field of an entity.%[2]sOrder.\ntype %[1]s string\n\n// The fields a list of %[2]s can be ordered by.\nconst (\n", names.orderType, e.Name)
		for _, o := range q.Order {
			w("\t%s %s = %q\n", o.Const, names.orderType, o.Value)
		}
		w(")\n\n")
	}

	pred, set, ord := e.pred(), e.set(), e.order()
	// The column types go to a file of their own: they are the same methods
	// for every Go type a column has, most of the package and nothing a
	// reader looks for. What the entity is (its columns, its enums, its
	// edges) stays in the file named after it.
	typesFrom := b.Len()
	for _, s := range order {
		w("// %s is a column of Go type %s: its methods build the predicates,\n// assignments and order terms of that column. They have pointer receivers so\n// that the compiler emits each once.\ntype %s string\n\n", s.name, s.gotype, s.name)
		for _, o := range s.ops {
			w("// %s %s\n", o.Name(), opDoc[o.Name()])
			switch {
			case o.Niladic():
				w("func (f *%s) %s() %s { return %s{Op: %s, Col: string(*f)} }\n\n", s.name, o.Name(), pred, pred, opConst[o])
			case o.Variadic():
				w("func (f *%s) %s(vs ...%s) %s {\n\ta := make([]any, len(vs))\n\tfor i, v := range vs {\n\t\ta[i] = v\n\t}\n\treturn %s{Op: %s, Col: string(*f), Val: a}\n}\n\n", s.name, o.Name(), s.gotype, pred, pred, opConst[o])
			case takesString(o):
				w("func (f *%s) %s(v string) %s { return %s{Op: %s, Col: string(*f), Val: v} }\n\n", s.name, o.Name(), pred, pred, opConst[o])
			default:
				w("func (f *%s) %s(v %s) %s { return %s{Op: %s, Col: string(*f), Val: v} }\n\n", s.name, o.Name(), s.gotype, pred, pred, opConst[o])
			}
		}
		w("// Set assigns v to the column.\nfunc (f *%s) Set(v %s) %s { return %s{Kind: orm.SetValue, Col: string(*f), Val: v} }\n\n", s.name, s.gotype, set, set)
		w("// SetNillable assigns *v to the column, or nothing when v is nil: the setter\n// for an input field the caller may have left out.\nfunc (f *%s) SetNillable(v *%s) %s {\n\tif v == nil {\n\t\treturn %s{}\n\t}\n\treturn %s{Kind: orm.SetValue, Col: string(*f), Val: *v}\n}\n\n", s.name, s.gotype, set, set, set)
		if s.clear {
			w("// Clear sets the column to NULL.\nfunc (f *%s) Clear() %s { return %s{Kind: orm.SetNull, Col: string(*f)} }\n\n", s.name, set, set)
		}
		if s.keep {
			w("// Keep leaves the column as it is in an update: its update default (for an\n// updated_at, the time of the update) is not applied. For a change that is\n// not the user's, such as a recomputed total.\nfunc (f *%s) Keep() %s { return %s{Kind: orm.SetKeep, Col: string(*f)} }\n\n", s.name, set, set)
		}
		if s.add {
			w("// Add increments the column by v.\nfunc (f *%s) Add(v %s) %s { return %s{Kind: orm.SetAdd, Col: string(*f), Val: v} }\n\n", s.name, s.gotype, set, set)
		}
		if s.order {
			w("// Asc orders by the column, smallest first.\nfunc (f *%s) Asc() %s { return %s{Col: string(*f)} }\n\n// Desc orders by the column, greatest first.\nfunc (f *%s) Desc() %s { return %s{Col: string(*f), Desc: true} }\n\n", s.name, ord, ord, s.name, ord, ord)
		}
	}

	typesTo := b.Len()

	// A column has two names: user.Name is its descriptor, whose methods
	// build conditions, assignments and order terms, and user.ColumnName is
	// its name in the table, a string constant for the places that take one
	// (Select, GroupBy, an aggregate, a hook's m.Field).
	consts := map[*fld]string{}
	w("// The column names of %s.\nconst (\n", e.Name)
	for _, f := range e.Fields {
		if f.Hidden {
			continue
		}
		consts[f] = unique("Column"+f.GoName, "Name")
		w("\t%s = %q\n", consts[f], f.Col)
	}
	w(")\n\n// The columns of %s.\nvar (\n", e.Name)
	for _, f := range e.Fields {
		if f.Hidden {
			continue
		}
		f.Desc = columns[f]
		w("\t%s = %s(%s)\n", f.Desc, desc[f].name, consts[f])
	}
	w(")\n\n")
	if len(e.Edges) > 0 {
		if err := g.leafEdges(e, im, taken, &b); err != nil {
			return nil, err
		}
	}

	for _, c := range []struct{ fn, op, doc string }{
		{"And", "orm.OpAnd", "And matches the rows every one of ps matches."},
		{"Or", "orm.OpOr", "Or matches the rows at least one of ps matches; without any, none."},
		{"Not", "orm.OpNot", "Not matches the rows that do not match all of ps; without any, none."},
	} {
		w("// %s\n", c.doc)
		w("func %s(ps ...%s) %s {\n\tsubs := make([]orm.P, len(ps))\n\tfor i, p := range ps {\n\t\tsubs[i] = orm.P(p)\n\t}\n\treturn %s{Op: %s, Subs: subs}\n}\n\n", c.fn, pred, pred, pred, c.op)
	}

	var out bytes.Buffer
	out.WriteString(header)
	fmt.Fprintf(&out, "// Package %s describes the columns and edges of %s. Its queries and\n// mutations start at the root package's %s.\npackage %s\n\n", e.Pkg, e.Name, e.Name, e.Pkg)
	out.WriteString(im.block())
	out.WriteString("\n")
	src := b.Bytes()
	out.Write(src[:typesFrom])
	out.Write(src[typesTo:])
	fmt.Fprintf(&out, "\n%s%s\n\n", fileMarker, columnsFile(e))
	out.Write(src[typesFrom:typesTo])
	return split(out.Bytes(), fileName(e))
}

// leafEdges writes the description and the descriptor of each edge of e. The
// description (orm.EdgeMeta) is data; package entity, which can name both
// ends, connects it to its owner and target in its init.
func (g *generator) leafEdges(e *ent, im *imports, taken map[string]bool, b *bytes.Buffer) error {
	w := func(format string, a ...any) { fmt.Fprintf(b, format, a...) }
	n := &namer{taken: taken}
	var metas []string
	for _, x := range e.Edges {
		x.Desc = n.unique(x.GoName, "Edge")
	}
	for _, x := range e.Edges {
		x.Type = n.unique("Edge"+x.GoName, "Type")
		meta := fmt.Sprintf("meta%d", x.Index)
		metas = append(metas, "&"+meta)
		var cols []string
		for _, c := range x.E.Rel.Columns {
			cols = append(cols, strconv.Quote(c))
		}
		var more string
		if x.E.Immutable {
			more += ", Immutable: true"
		}
		if !x.E.Optional {
			more += ", Required: true"
		}
		w("var %s = orm.EdgeMeta{Name: %q, Index: %d, Rel: %s, Inverse: %t, Bidi: %t, Table: %q, Columns: []string{%s}%s}\n\n",
			meta, x.E.Name, x.Index, relConst(x.E.Rel.Type), x.E.IsInverse(), x.E.Bidi, x.E.Rel.Table, strings.Join(cols, ", "), more)

		ref, err := goTypeOf(x.Target.ID.F)
		if err != nil {
			return fmt.Errorf("edge %q: %w", x.E.Name, err)
		}
		if ref.PkgPath != "" {
			im.use(ref.PkgPath, ref.PkgName)
		}
		tid := ref.Expr
		// The descriptor methods have pointer receivers: a value receiver
		// makes the compiler emit a second, pointer-receiver copy of each.
		w(`// %[1]s is the %[2]q edge of %[3]s, from %[3]s to %[4]s.
type %[1]s struct{}

// %[5]s is the %[2]q edge of %[3]s: conditions on it and changes to it. A
// query eager-loads it with With%[10]s.
var %[5]s = &%[1]s{}

// Has matches a %[3]s that has at least one %[2]q neighbor.
func (e *%[1]s) Has() %[8]s { return %[8]s{Op: orm.OpHasEdge, Edge: &%[6]s} }

// HasWith matches a %[3]s that has a %[2]q neighbor satisfying the predicates.
func (e *%[1]s) HasWith(ps ...%[9]s) %[8]s {
	subs := make([]orm.P, len(ps))
	for i, p := range ps {
		subs[i] = orm.P(p)
	}
	return %[8]s{Op: orm.OpHasEdge, Edge: &%[6]s, Subs: subs}
}

`, x.Type, x.E.Name, e.Name, x.Target.Name, x.Desc, meta, "", e.pred(), x.Target.pred(), x.GoName)
		if x.E.Unique {
			w("// Set connects the %[2]s to the %[3]s with the given id.\nfunc (e *%[1]s) Set(id %[4]s) %[5]s {\n\treturn %[5]s{Kind: orm.EdgeAdd, Edge: &%[6]s, IDs: []any{id}}\n}\n\n", x.Type, e.Name, x.Target.Name, tid, e.set(), meta)
		} else {
			w("// Add connects the %[2]s to the %[3]s rows with the given ids.\nfunc (e *%[1]s) Add(ids ...%[4]s) %[5]s {\n\tvs := make([]any, len(ids))\n\tfor i, id := range ids {\n\t\tvs[i] = id\n\t}\n\treturn %[5]s{Kind: orm.EdgeAdd, Edge: &%[6]s, IDs: vs}\n}\n\n", x.Type, e.Name, x.Target.Name, tid, e.set(), meta)
			w("// Remove disconnects the %[2]s from the %[3]s rows with the given ids.\nfunc (e *%[1]s) Remove(ids ...%[4]s) %[5]s {\n\tvs := make([]any, len(ids))\n\tfor i, id := range ids {\n\t\tvs[i] = id\n\t}\n\treturn %[5]s{Kind: orm.EdgeRemove, Edge: &%[6]s, IDs: vs}\n}\n\n", x.Type, e.Name, x.Target.Name, tid, e.set(), meta)
		}
		w("// Clear disconnects the %[2]s from every %[3]q neighbor.\nfunc (e *%[1]s) Clear() %[4]s { return %[4]s{Kind: orm.EdgeClear, Edge: &%[5]s} }\n\n", x.Type, e.Name, x.E.Name, e.set(), meta)
	}
	w("// EdgeMetas returns the descriptions of the edges of %s, in order. The generated\n// entity package connects them to the entities they join; nothing else needs them.\nfunc EdgeMetas() []*orm.EdgeMeta { return []*orm.EdgeMeta{%s} }\n\n", e.Name, strings.Join(metas, ", "))
	return nil
}
