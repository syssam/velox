package gen

import (
	"strings"

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
		Version:    plugin.Version,
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
			Name: oe.Name, Label: oe.Label, Handles: oe.Handles, Table: oe.Table, Join: oe.Join,
			Comment: oe.Comment, Imports: append([]string{}, oe.Imports...),
			Fields: make([]*plugin.Field, 0, len(oe.Fields)), Edges: []*plugin.Edge{},
			PrimaryKey: append([]int{}, oe.PrimaryKey...), Tenant: oe.Tenant,
		}
		if oe.Join {
			pe.Fields = joinFields(g, oe)
			m.Entities = append(m.Entities, pe)
			continue
		}
		ge := g.Entity(oe.Name)
		pe.Annotations = ge.Annotations
		for _, of := range oe.Fields {
			gf := ge.Field(of.Name)
			pe.Fields = append(pe.Fields, &plugin.Field{
				Name: of.Name, GoName: of.GoName, Column: of.Column,
				Type: gf.Type, GoType: of.GoType, ElemType: of.ElemType,
				Pointer: of.Pointer, Nullable: gf.Optional || gf.Nillable, Optional: gf.Optional, Nillable: gf.Nillable,
				Immutable: of.Immutable, Sensitive: of.Sensitive, Unique: gf.Unique,
				AutoID: gf.AutoID, HasDefault: of.DefaultCode != "" || of.DefaultFuncCode != "",
				UpdateDefault: gf.UpdateDefault, Implicit: gf.Implicit,
				Enum: enumOf(gf, of, enums), EnumValues: enumValues(gf), Comment: gf.Comment,
				Annotations: gf.Annotations,
			})
		}
		for _, ed := range ge.Edges {
			pe.Edges = append(pe.Edges, &plugin.Edge{
				Name: ed.Name, GoName: ed.GoName, Target: ed.Target, Ref: ed.Ref,
				Kind: ed.Rel.Kind, Unique: ed.Unique, Required: ed.Required, Inverse: ed.Inverse,
				Immutable: ed.Immutable, Field: ed.Field, FieldOn: fieldOn(ge, ed), Join: joinOf(ed, joins),
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
	types := make([]string, len(oe.Fields))
	for _, e := range g.Entities {
		for _, ed := range e.Edges {
			if ed.Rel.Kind != "M2M" || ed.Inverse || ed.Rel.Table != oe.Table || types[0] != "" {
				continue
			}
			types[0] = e.Fields[0].Type
			if t := g.Entity(ed.Target); t != nil {
				types[1] = t.Fields[0].Type
			}
		}
	}
	fields := make([]*plugin.Field, 0, len(oe.Fields))
	for i, of := range oe.Fields {
		fields = append(fields, &plugin.Field{
			Name: of.Name, GoName: of.GoName, Column: of.Column,
			Type: types[i], GoType: of.GoType, ElemType: of.ElemType,
			Pointer: of.Pointer, Immutable: of.Immutable, Sensitive: of.Sensitive, Comment: of.Comment,
		})
	}
	return fields
}
