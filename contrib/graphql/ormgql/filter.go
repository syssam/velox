package ormgql

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/syssam/velox/contrib/graphql/graphqlgen"
	"github.com/syssam/velox/internal/ormgen"
)

func lowerFirst(s string) string { return strings.ToLower(s[:1]) + s[1:] }

// filterType is the Go type of a column as package filter writes it; the
// package it comes from is imported under filterImports.
func (ext *extension) filterType(c *ormgen.Field) string {
	if c.Enum != "" {
		// The type's name is the same everywhere; the package's alias is
		// this package's own.
		owner := ext.enumOwner[c]
		return ext.filterIm.Use(ext.g.Package()+"/"+owner.Pkg, ext.filterAlias(owner)) + "." + ext.enumType[c]
	}
	ref, err := ormgen.GoTypeOf(c.F)
	if err != nil {
		return "any"
	}
	if ref.PkgPath != "" {
		ext.filterIm.Use(ref.PkgPath, ref.PkgName)
	}
	return ref.Expr
}

// filterAlias is the name package filter imports an entity's package under:
// its own, unless a name of the generated code already has it.
func (ext *extension) filterAlias(e *ormgen.Entity) string {
	switch e.Pkg {
	case "orm", "predicate", "filter", "i", "p":
		return e.Pkg + "pkg"
	}
	return e.Pkg
}

// filters generates package filter: a where input per entity that has one in
// the schema.
//
// A where input is a struct with a field per filter the schema offers,
// because that is what a GraphQL engine fills in: it binds an input object
// field by field. What the fields mean is data (an gqlinput.WhereField each), and
// one function of the runtime turns any of them into a predicate, so the
// package holds no code per field. As a type that only held a predicate it
// would have cost the 312 services of the benchmark 5 to 8% less to compile,
// and every application that reads or sets a filter in Go its code.
func (ext *extension) filters() (map[string][]byte, error) {
	// The where fields were typed while the schema was read: the imports
	// their types need are registered already.
	im := ext.filterIm
	im.Use(ormgen.ORMPath, "orm")
	im.Use(gqlinputPath, "gqlinput")
	im.Use(ext.g.Package()+"/predicate", "predicate")
	var b bytes.Buffer
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	inputs := 0
	for _, e := range ext.g.Entities() {
		q := ext.ents[e]
		if q == nil || !q.HasWhere {
			continue
		}
		inputs++
		w("\n%s%s\n\n", ormgen.FileMarker, ormgen.FileName(e))
		w("// %[1]sWhereInput is the filter a request gives for a list of %[1]s: the where\n// argument of a GraphQL connection. A field that is nil, or false for a flag,\n// was not given.\ntype %[1]sWhereInput struct {\n", e.Name)
		w("\t// Predicates are conditions the application adds in Go.\n\tPredicates []%s `json:\"-\"`\n", e.Predicate())
		w("\tNot *%[1]sWhereInput `json:\"not,omitempty\"`\n\tOr []*%[1]sWhereInput `json:\"or,omitempty\"`\n\tAnd []*%[1]sWhereInput `json:\"and,omitempty\"`\n\n", e.Name)
		hasEdge := false
		for _, f := range q.Where {
			w("\t%s %s `json:\"%s,omitempty\"`\n", f.GoName, f.GoType, f.Name)
			hasEdge = hasEdge || strings.Contains(f.Plan, "Edges[")
		}
		w("}\n\n")
		if hasEdge {
			w("var %sEdges = %s.EdgeMetas()\n\n", lowerFirst(e.Name), im.Use(ext.g.Package()+"/"+e.Pkg, ext.filterAlias(e)))
		}
		if len(q.Enums) > 0 {
			w("// %sEnums are the values of the enums %sWhereInput filters on, by their names in the schema.\nvar %sEnums = [...]map[string]string{\n", lowerFirst(e.Name), e.Name, lowerFirst(e.Name))
			for _, values := range q.Enums {
				w("\t{")
				for i, v := range values {
					if i > 0 {
						w(", ")
					}
					w("%q: %q", v.Name, v.Value)
				}
				w("},\n")
			}
			w("}\n\n")
		}
		w("// %sWhere says what each field of %sWhereInput means, in the order of the struct.\nvar %sWhere = []gqlinput.WhereField{\n", lowerFirst(e.Name), e.Name, lowerFirst(e.Name))
		for _, f := range q.Where {
			w("\t%s, // %s\n", f.Plan, f.Name)
		}
		w("}\n\n")
		if len(q.Neighbors) > 0 {
			// After every plan exists: two inputs may filter through each
			// other, which an initializer could not say.
			w("func init() {\n")
			for _, n := range q.Neighbors {
				w("\t%sWhere[%d].Neighbor = %sWhere // %s\n", lowerFirst(e.Name), n.At, lowerFirst(n.Target.Name), q.Where[n.At].Name)
			}
			w("}\n\n")
		}
		w("// P returns the condition the input describes: none for a nil or an empty\n// input, and one that fails the query for an input that cannot be a condition\n// (an empty input nested in not, and, or; nesting deeper than gqlinput.MaxWhereDepth).\n//\n//go:noinline\nfunc (i *%[1]sWhereInput) P() %[2]s { return %[2]s(gqlinput.Where(i, %[3]sWhere)) }\n\n", e.Name, e.Predicate(), lowerFirst(e.Name))
		w(`// UnmarshalGQL reads the input from the object given for it: the where argument
// of a request as the engine hands it over, or a filter kept as a document. See
// gqlinput.DecodeWhere for what it accepts and what it refuses.
//
//go:noinline
func (i *%[1]sWhereInput) UnmarshalGQL(v any) error { return gqlinput.DecodeWhere(i, %[2]sWhere, v) }

// MarshalGQL writes the input as the document UnmarshalGQL reads. With
// UnmarshalGQL it tells an engine that the type reads itself, so that it
// generates no decoder of its own for it.
//
//go:noinline
func (i %[1]sWhereInput) MarshalGQL(w %[3]s.Writer) { gqlinput.WriteWhere(w, &i, %[2]sWhere) }

// UnmarshalJSON reads the input from the JSON text of its document, as
// UnmarshalGQL reads the object: json.Unmarshal refuses a key the input does
// not have, where the struct alone would let it pass.
//
//go:noinline
func (i *%[1]sWhereInput) UnmarshalJSON(b []byte) error { return gqlinput.DecodeWhere(i, %[2]sWhere, b) }

// MarshalJSON writes the document of the input: json.Marshal of an input is
// what UnmarshalJSON reads back as the same conditions. See gqlinput.EncodeWhere.
//
//go:noinline
func (i %[1]sWhereInput) MarshalJSON() ([]byte, error) { return gqlinput.EncodeWhere(&i, %[2]sWhere) }

`, e.Name, lowerFirst(e.Name), im.Use("io", "io"))
	}
	if inputs == 0 {
		return nil, nil
	}
	var out bytes.Buffer
	out.WriteString(ormgen.Header)
	out.WriteString("// Package filter holds the where inputs of the GraphQL schema: the filter a request\n// gives for a list of an entity, and its conversion to a predicate.\npackage filter\n\n")
	out.WriteString(im.Block())
	out.WriteString("\n")
	out.Write(b.Bytes())
	return ormgen.Split(out.Bytes(), "filter")
}

// distinctEnum reports an enum the schema cannot tell the values of apart:
// two values under one name, or a value stored as what another is called. A
// where input reads a value by its name or as it is stored, and for such an
// enum one text would be two values.
func distinctEnum(values []graphqlgen.EnumValue) error {
	byName := make(map[string]string, len(values))
	for _, v := range values {
		if other, taken := byName[v.Name]; taken {
			return fmt.Errorf("the values %q and %q are both called %s in the schema", other, v.Value, v.Name)
		}
		byName[v.Name] = v.Value
	}
	for _, v := range values {
		if other, named := byName[v.Value]; named && other != v.Value {
			return fmt.Errorf("the value %q is what the value %q is called in the schema (%s)", v.Value, other, v.Value)
		}
	}
	return nil
}
