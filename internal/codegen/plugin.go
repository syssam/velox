package codegen

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/ddl"
	"github.com/syssam/velox2/plugin"
)

// PluginModel is the model velox-gen hands to a plugin: g and o in the plugin
// package's types. It copies and never recomputes. Names, handles, Go types,
// join entities and enums are o's; relation kinds, annotations and the flags
// the schema declares are g's. Entities and fields are matched by name.
//
// The annotation maps in the result are shared with g, not copied: they are
// read-only. o must be the result of lowering g.
func PluginModel(g *Graph, o *Out) *plugin.Model {
	m := &plugin.Model{
		Version:    plugin.ModelVersion,
		SchemaPath: o.SchemaPath,
		Entities:   make([]*plugin.Entity, 0, len(o.Entities)),
		Enums:      make([]*plugin.Enum, 0, len(o.Enums)),
	}
	enums := make(map[string]bool, len(o.Enums))
	for _, en := range o.Enums {
		enums[en.Type] = true
		m.Enums = append(m.Enums, &plugin.Enum{Type: en.Type, Values: append([]string{}, en.Values...)})
	}
	// The join entity of a many-to-many edge is the one on its table.
	joins := map[string]string{}
	for _, oe := range o.Entities {
		if oe.Join {
			joins[oe.Table] = oe.Name
		}
	}
	for _, oe := range o.Entities {
		pe := &plugin.Entity{
			Name: oe.Name, Label: oe.Label, Handles: oe.Handles, Table: oe.Table, TableSchema: oe.TableSchema, Join: oe.Join,
			Comment: oe.Comment, Fields: make([]*plugin.Field, 0, len(oe.Fields)), Edges: []*plugin.Edge{}, Indexes: []*plugin.Index{},
			PrimaryKey: append([]int{}, oe.PrimaryKey...), Tenant: oe.Tenant,
		}
		if oe.Join {
			pe.Fields = joinFields(g, oe)
			m.Entities = append(m.Entities, pe)
			continue
		}
		ge := g.Entity(oe.Name)
		pe.Annotations = ge.Annotations
		pe.Indexes = indexes(ge)
		for _, of := range oe.Fields {
			gf := ge.Field(of.Name)
			pe.Fields = append(pe.Fields, &plugin.Field{
				Name: of.Name, GoName: of.GoName, Column: of.Column,
				Type: gf.Type, GoType: of.GoType, ElemType: of.ElemType, PkgPath: pkgPathOf(gf), Imports: importsOf(gf), Kind: goKindOf(gf),
				Pointer: of.Pointer, Nullable: nullable(gf), Optional: gf.Optional,
				Immutable: of.Immutable, Sensitive: of.Sensitive, Unique: gf.Unique,
				AutoID: gf.AutoID, Size: gf.Size, HasDefault: of.DefaultCode != "" || of.DefaultFuncCode != "", Default: literalDefault(gf),
				UpdateDefault: gf.UpdateDefault, Implicit: gf.Implicit,
				Enum: enumOf(gf, of, enums), EnumValues: enumValues(gf), Checks: checks(gf), SchemaType: maps.Clone(gf.SchemaType),
				Comment: gf.Comment, Annotations: gf.Annotations,
			})
		}
		for _, ed := range ge.Edges {
			pe.Edges = append(pe.Edges, &plugin.Edge{
				Name: ed.Name, GoName: ed.GoName, Target: ed.Target, Ref: ed.Ref,
				Kind: plugin.EdgeKind(ed.Rel.Kind), Unique: ed.Unique, Required: ed.Required, Inverse: ed.Inverse,
				Immutable: ed.Immutable, Field: ed.Field, FieldOn: fieldOn(ge, ed), OwnsKey: ed.Field != "" && ownFK(ed), Join: joinOf(ed, joins),
				Comment: ed.Comment, Annotations: ed.Annotations,
			})
		}
		m.Entities = append(m.Entities, pe)
	}
	return m
}

// enumOf is the name of the enum type of the field, or "": the type velox-gen
// declares, or for a custom Go type that type's name without its package.
func enumOf(gf *Field, of *OutField, enums map[string]bool) string {
	switch {
	case gf.Type != "enum":
		return ""
	case enums[of.ElemType]:
		return of.ElemType
	case gf.GoType != nil:
		_, name := cutLast(gf.GoType.Ident, ".")
		return name
	}
	return ""
}

// pkgPathOf is the import path of the named Go type of gf, or "": the
// schema's Go type's package, else time's for a time field, the one other
// standard type with a package.
func pkgPathOf(gf *Field) string {
	switch {
	case gf.GoType != nil:
		return gf.GoType.PkgPath
	case gf.Type == "time":
		return "time"
	}
	return ""
}

// importsOf are the import paths the Go type of gf mentions, sorted: the
// schema's Go type's, else time's for a time field; nil for a builtin type
// and velox-gen's own enum types.
func importsOf(gf *Field) []string {
	switch {
	case gf.GoType != nil:
		if len(gf.GoType.Imports) == 0 {
			return nil
		}
		return slices.Sorted(slices.Values(gf.GoType.Imports))
	case gf.Type == "time":
		return []string{"time"}
	}
	return nil
}

// literalDefault is the literal Default of gf as compact JSON, or nil. Only
// a bool, number, string or enum field has one, whose JSON is then that of
// its base value, unless its Go type has its own encoding. A func default
// has none, and a DefaultFunc replaces a literal.
func literalDefault(gf *Field) json.RawMessage {
	switch {
	case gf.DefaultFunc || len(gf.Default) == 0:
		return nil
	case gf.GoType != nil && gf.GoType.Marshaler:
		return nil
	case gf.Type == "bool", numeric[gf.Type], gf.Type == "string", gf.Type == "enum":
		return json.RawMessage(compact(gf.Default))
	}
	return nil
}

// checks are the built-in validators of gf, in declaration order.
func checks(gf *Field) []plugin.Check {
	if len(gf.Checks) == 0 {
		return nil
	}
	cs := make([]plugin.Check, 0, len(gf.Checks))
	for _, c := range gf.Checks {
		var args []json.RawMessage
		for _, a := range c.Args {
			args = append(args, json.RawMessage(compact(a)))
		}
		cs = append(cs, plugin.Check{Kind: c.Kind, Args: args})
	}
	return cs
}

// indexes are the declared indexes of e, by the Names of the fields that hold
// their columns, with the names Build gave them, a derived one capped as on
// PostgreSQL.
func indexes(e *Entity) []*plugin.Index {
	byColumn := make(map[string]string, len(e.Fields))
	for _, f := range e.Fields {
		if _, ok := byColumn[f.Column]; !ok {
			byColumn[f.Column] = f.Name
		}
	}
	out := make([]*plugin.Index, 0, len(e.Indexes))
	for _, ix := range e.Indexes {
		name := ix.Name
		if ix.Derived {
			name = ddl.Symbol(velox.Postgres, name)
		}
		pi := &plugin.Index{Name: name, Fields: make([]string, 0, len(ix.Columns)), Unique: ix.Unique, Where: ix.Where}
		for _, c := range ix.Columns {
			pi.Fields = append(pi.Fields, byColumn[c])
		}
		out = append(out, pi)
	}
	return out
}

// goKindOf is the reflect kind of the schema's Go type of gf, or "" when the
// schema gives none.
func goKindOf(gf *Field) string {
	if gf.GoType == nil {
		return ""
	}
	return gf.GoType.Kind
}

// cutLast splits s around the last sep; before is "" when there is none.
func cutLast(s, sep string) (before, after string) {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):]
	}
	return "", s
}

// enumValues are the stored values of an enum field, in declaration order.
func enumValues(gf *Field) []string {
	if gf.Type != "enum" {
		return nil
	}
	vs := make([]string, 0, len(gf.Enums))
	for _, v := range gf.Enums {
		vs = append(vs, v.Value)
	}
	return vs
}

// fieldOn is the name of the entity whose table holds the foreign key of ed,
// an edge of e: the owner when ownFK says so, else the target. M2M has none.
func fieldOn(e *Entity, ed *Edge) string {
	switch {
	case ed.Field == "":
		return ""
	case ownFK(ed):
		return e.Name
	}
	return ed.Target
}

// joinOf is the name of the join entity of the many-to-many edge ed, or "".
func joinOf(ed *Edge, joins map[string]string) string {
	if ed.Rel.Kind != "M2M" {
		return ""
	}
	return joins[ed.Rel.Table]
}

// joinFields are the fields of the join entity oe. The schema holds no field
// for them: each has the type of the ID of the entity it points to, which is
// found through the many-to-many edge that makes the join entity. That edge's
// owner side lists the columns in the join entity's order.
func joinFields(g *Graph, oe *OutEntity) []*plugin.Field {
	ids := make([]*Field, len(oe.Fields))
	for _, e := range g.Entities {
		for _, ed := range e.Edges {
			if ed.Rel.Kind != "M2M" || ed.Inverse || ed.Rel.Table != oe.Table || ids[0] != nil {
				continue
			}
			ids[0] = e.Fields[0]
			if t := g.Entity(ed.Target); t != nil {
				ids[1] = t.Fields[0]
			}
		}
	}
	fields := make([]*plugin.Field, 0, len(oe.Fields))
	for i, of := range oe.Fields {
		pf := &plugin.Field{
			Name: of.Name, GoName: of.GoName, Column: of.Column,
			GoType: of.GoType, ElemType: of.ElemType,
			Pointer: of.Pointer, Immutable: of.Immutable, Sensitive: of.Sensitive, Comment: of.Comment,
		}
		if id := ids[i]; id != nil {
			pf.Type, pf.PkgPath, pf.Imports, pf.Kind = id.Type, pkgPathOf(id), importsOf(id), goKindOf(id)
		}
		fields = append(fields, pf)
	}
	return fields
}

// nullable reports whether the column of gf holds NULL: a Nillable field's,
// and an Optional JSON field's whose Go type holds nil: a slice, a map or a
// pointer (rule 48 refuses one of another type, and rule 49 a func, a chan or
// an interface). It is the one nullability rule, as the core's
// ColumnDesc.Nullable and pointers say, and the planner's.
func nullable(gf *Field) bool {
	return gf.Nillable || gf.Optional && gf.Type == "json" && gf.GoType != nil && nilKinds[gf.GoType.Kind]
}
