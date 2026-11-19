package gogen

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/syssam/velox/contrib/graphql/internal/sdl"
	"github.com/syssam/velox/plugin"
)

// writeEnum writes the marshalers of velox-gen's Go enum type of the field
// en: gqlgen binds the GraphQL enum, or the String, to the Go type, and they
// turn its values into the stored ones and back. An enum of the
// application's own Go type brings its own.
func (r *renderer) writeEnum(f *file, e *plugin.Entity, en sdl.Enum) error {
	fd := e.Fields[en.Field]
	typ := fd.Enum
	ve := r.veloxEnums[typ]
	if ve == nil || en.Shared {
		return nil // an application's own type, or another field's marshalers
	}
	if len(ve.Consts) == 0 && len(ve.Values) > 0 {
		// velox-gen writes the constants' names into the model since the
		// enum marshalers switch on them; an older one leaves them out.
		return fmt.Errorf("schema %s, field %s: the model has no Go constants of %s: velox-gen is too old for this velox-graphql; update velox-gen to the same velox version and regenerate", e.Name, fd.Name, typ)
	}
	if len(ve.Consts) != len(ve.Values) || !sameValues(ve.Values, fd.EnumValues) {
		return fmt.Errorf("schema %s, field %s: the model gives no Go constant of each value of %s; regenerate with velox-gen", e.Name, fd.Name, typ)
	}
	if err := r.declare(typ+".MarshalGQL", "schema "+e.Name+", field "+fd.Name); err != nil {
		return err
	}
	if en.Name != "" && en.Name != typ {
		// graphql.Type names the enum: gqlgen binds it by that name.
		if err := r.declare(en.Name, "schema "+e.Name+", field "+fd.Name+"'s enum"); err != nil {
			return err
		}
		f.p("// %s is %s, under the name graphql.Type gives the GraphQL enum.", en.Name, typ)
		f.p("type %s = %s", en.Name, typ)
		f.p("")
	}
	io := f.use("io", "io")
	gql := en.Values
	if en.Name == "" {
		// A String: graphql.Skip(SkipEnumField) shows the stored values.
		gql = fd.EnumValues
		f.doc("", fmt.Sprintf("MarshalGQL writes e as a GraphQL String, its stored value: the field %s of %s is a String, as graphql.Skip(graphql.SkipEnumField) asks.", fd.Name, e.Name))
	} else {
		f.p("// MarshalGQL writes e as its value of the GraphQL enum %s.", en.Name)
	}
	f.p("func (e %s) MarshalGQL(w %s.Writer) {", typ, io)
	f.p("\tswitch e {")
	for i, c := range ve.Consts {
		f.p("\tcase %s:", c)
		f.p("\t\t_, _ = %s.WriteString(w, %s)", io, jsonLiteral(gql[i]))
	}
	f.p("\tdefault: // not a value of the enum")
	f.p("\t\t%s.WriteString(w, string(e))", f.gqlgraph())
	f.p("\t}")
	f.p("}")
	f.p("")
	if en.Name == "" {
		f.p("// UnmarshalGQL sets e from v, a String that is one of its stored values.")
	} else {
		f.p("// UnmarshalGQL sets e from v, a value of the GraphQL enum %s.", en.Name)
	}
	f.p("func (e *%s) UnmarshalGQL(v any) error {", typ)
	f.p("\tswitch v {")
	for i, c := range ve.Consts {
		f.p("\tcase %s:", strconv.Quote(gql[i]))
		f.p("\t\t*e = %s", c)
	}
	f.p("\tdefault:")
	f.p("\t\treturn %s.InputErrorf(%q, v)", f.gqlgraph(), strings.ReplaceAll(typ+" must be "+orList(gql), "%", "%%")+", not %v")
	f.p("\t}")
	f.p("\treturn nil")
	f.p("}")
	f.p("")
	return nil
}

// sameValues reports whether a and b hold the same values, in any order: a
// field shares its Go enum type with another field, whose order the type
// has, when their values are the same.
func sameValues(a, b []string) bool {
	return len(a) == len(b) && !slices.ContainsFunc(b, func(v string) bool { return !slices.Contains(a, v) })
}

// jsonLiteral is the Go literal of s as a JSON string: `"DRAFT"`, or a
// quoted literal when s needs escaping in JSON or holds a backquote.
func jsonLiteral(s string) string {
	b, _ := json.Marshal(s) // never fails for a string
	if !strings.ContainsAny(string(b), "`\\") && strconv.CanBackquote(string(b)) {
		return "`" + string(b) + "`"
	}
	return strconv.Quote(string(b))
}
