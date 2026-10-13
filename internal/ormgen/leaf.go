package ormgen

import (
	"bytes"
	"fmt"
	"slices"
	"strconv"
	"strings"

	vgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/internal/colgen"
	"github.com/syssam/velox/schema/field"
)

// leafReserved are the identifiers every entity package declares itself.
// ModelName, not TypeName or EntityName: a schema has fields called
// type_name and entity_name (one and two of them in a real schema of 455
// entities, none called model_name), and the field would then lose its
// plain name to the constant.
var leafReserved = []string{"And", "Or", "Not", "Table", "ModelName", "EdgeMetas"}

// Namer hands out the identifiers of one entity package: the first to ask for
// a name gets it, the next gets the name with a suffix.
type Namer struct {
	taken map[string]bool
	// Ext holds what an extension notes of the names it was given, by a key
	// of its own.
	Ext map[string]string
}

func (n *Namer) unique(name, suffix string) string {
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
func (g *Generator) enumNames(e *Entity) (*Namer, map[*Field]string, map[*Field][]string, map[*Field]string) {
	n := &Namer{taken: map[string]bool{}, Ext: map[string]string{}}
	for _, s := range leafReserved {
		n.taken[s] = true
	}
	columns := map[*Field]string{}
	for _, f := range e.Fields {
		if !f.Hidden {
			columns[f] = n.unique(f.GoName, "Field")
		}
	}
	types := map[*Field]string{}
	consts := map[*Field][]string{}
	var enums []*Field
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
	// What an extension declares in the package is named here too, so that
	// what the package names later gives way to it.
	for _, x := range g.cfg.Extensions {
		x.LeafNames(e, n)
	}
	return n, types, consts, columns
}

// leaf generates the package of one entity: a descriptor per field and per
// edge, so that application code reads
// db.User.Query(tx).Where(user.Name.EQ(...), user.Posts.Has()). The
// builders are reached through the root package, not from here.
//
// It returns the package's one file, by name.
//
// It imports the predicate package and not package entity (nor another entity's package): a
// change to one entity rebuilds its package, package entity and what uses them,
// and not the other 453 per-entity packages.
func (g *Generator) leaf(e *Entity) (map[string][]byte, error) {
	im := g.Imports()
	im.use(ormPath, "orm")
	im.use(g.cfg.Package+"/predicate", "predicate")
	var b bytes.Buffer
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	names, enumType, enumConsts, columns := g.enumNames(e)
	unique := names.unique
	taken := names.taken
	var enums []*Field
	for _, f := range e.Fields {
		if f.Enum != "" {
			enums = append(enums, f)
		}
	}

	// localType is the Go type of a field in this package's terms.
	localType := func(f *Field) (string, error) {
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

	// The type of each column: one of the column types of package orm, by
	// what the column can be asked (orm/columns.go).
	colType := map[*Field]string{}
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
		isKey := f.F.IsEdgeField()
		if isKey && !slices.Contains(ops, vgen.GT) && f.F.Type != nil {
			switch t := f.F.Type.Type; {
			case t.Numeric(), t == field.TypeString, t == field.TypeTime, t == field.TypeUUID:
				ops = append(slices.Clone(ops), vgen.GT)
			}
		}
		// Every column can be asked whether it is NULL, one that cannot be
		// included (it never is): code written once for every column of a
		// type, a filter chosen at run time, asks without knowing which it
		// has. Only a column that accepts NULL can be cleared.
		level := "Value"
		switch {
		case slices.Contains(ops, vgen.Contains):
			level = "String"
		case slices.Contains(ops, vgen.GT):
			level = "Ordered"
			// A number that is not the key of an edge can be added to. Add on
			// the id is refused when it runs.
			if !isKey && f.F.Type != nil && (f.F.Type.Numeric() || f.F.IsOther()) {
				level = "Number"
			}
		case slices.Contains(ops, vgen.In):
			level = "Enum"
		case slices.Contains(ops, vgen.EQ):
			level = "Bool"
		}
		colType[f] = fmt.Sprintf("orm.%s[%s, %s]", colgen.Name(level, f.Null && f.F.Optional, f.F.UpdateDefault), gt, e.pred())
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
	pred := e.pred()
	// A column has two names: user.Name is its descriptor, whose methods
	// build conditions, assignments and order terms, and user.ColumnName is
	// its name in the table, a string constant for the places that take one
	// (Select, GroupBy, an aggregate, a hook's m.Field).
	consts := map[*Field]string{}
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
		w("\t%s = %s(%s)\n", f.Desc, colType[f], consts[f])
	}
	w(")\n\n")
	if len(e.Edges) > 0 {
		if err := g.leafEdges(e, im, taken, &b); err != nil {
			return nil, err
		}
	}

	// And, Or and Not are written out, with a helper of the package's own,
	// where they could forward to orm.And, orm.Or and orm.Not: those are
	// generic, and a generic function is compiled in the package that
	// instantiates it. Forwarding took a seventh of the compile of an entity
	// package (9 of 65 ms) and 36 KB of its object code, which every program
	// that uses the tree links.
	for _, c := range []struct{ fn, op, doc string }{
		{"And", "OpAnd", "And matches the rows every one of ps matches."},
		{"Or", "OpOr", "Or matches the rows at least one of ps matches; without any, none."},
		{"Not", "OpNot", "Not matches the rows that do not match all of ps; without any, none."},
	} {
		w("// %s\nfunc %s(ps ...%s) %s { return group(orm.%s, ps) }\n\n", c.doc, c.fn, pred, pred, c.op)
	}
	w("// group joins conditions on %[1]s under one operator.\nfunc group(op orm.Op, ps []%[2]s) %[2]s {\n\tsubs := make([]orm.P, len(ps))\n\tfor i, p := range ps {\n\t\tsubs[i] = orm.P(p)\n\t}\n\treturn %[2]s{Op: op, Subs: subs}\n}\n\n", e.Name, pred)

	var out bytes.Buffer
	out.WriteString(header)
	fmt.Fprintf(&out, "// Package %s describes the columns and edges of %s. Its queries and\n// mutations start at the root package's %s.\npackage %s\n\n", e.Pkg, e.Name, e.Name, e.Pkg)
	out.WriteString(im.block())
	out.WriteString("\n")
	out.Write(b.Bytes())
	return split(out.Bytes(), fileName(e))
}

// leafEdges writes the description and the descriptor of each edge of e. The
// description (orm.EdgeMeta) is data; package entity, which can name both
// ends, connects it to its owner and target in its init.
func (g *Generator) leafEdges(e *Entity, im *Imports, taken map[string]bool, b *bytes.Buffer) error {
	w := func(format string, a ...any) { fmt.Fprintf(b, format, a...) }
	n := &Namer{taken: taken}
	var metas []string
	for _, x := range e.Edges {
		x.Desc = n.unique(x.GoName, "Edge")
	}
	for _, x := range e.Edges {
		meta := strings.ToLower(x.Desc[:1]) + x.Desc[1:] + "Meta"
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
		// The edge's type is one of the two of package orm: what its methods
		// build is typed by the predicate types of both ends and by the
		// neighbor's id.
		kind, does := "ToMany", "Has, HasWith) and changes to it (Add, Remove, Clear"
		if x.E.Unique {
			kind, does = "ToOne", "Has, HasWith) and changes to it (Set, Clear"
		}
		w("// %[1]s is the %[2]q edge of %[3]s, to %[4]s: conditions on it (%[5]s). A query eager-loads it with With(%[12]s.%[1]s).\nvar %[1]s = &orm.%[7]s[%[8]s, %[9]s, %[10]s]{Meta: &%[11]s}\n\n",
			x.Desc, x.E.Name, e.Name, x.Target.Name, does, x.GoName, kind, e.pred(), x.Target.pred(), ref.Expr, meta, e.Pkg)
	}
	w("// EdgeMetas returns the descriptions of the edges of %s, in order. The generated\n// entity package connects them to the entities they join; nothing else needs them.\nfunc EdgeMetas() []*orm.EdgeMeta { return []*orm.EdgeMeta{%s} }\n\n", e.Name, strings.Join(metas, ", "))
	return nil
}
