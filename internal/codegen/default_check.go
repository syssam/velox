package codegen

import (
	"math/big"
	"slices"
	"strconv"
	"strings"

	"github.com/syssam/velox"
)

// DefaultWarnings returns a warning for each field that is not Nillable
// and whose SchemaDefault (its own, or a sqlschema default lowered into it)
// is, on some dialect, a literal of a non-zero value of its Go type: true,
// a number other than 0 (a decimal's too), a non-empty string. An insert leaves the Go zero
// value of such a column to the database (ADR 0093), which stores the
// default, so no Insert stores the zero, a setter's included (D10). An
// enum is left alone, as its zero, "", is none of its values; so is a
// zero literal, which stores the same zero, and an expression, such as
// now() or gen_random_uuid(), whose value velox-gen cannot read: a time's,
// a UUID's, bytes' or JSON's default is not read at all. A Go Default
// does not help: it fills only a zero the struct holds before the setters,
// and a setter's zero is still left to the database. Each warning is
// "<pos>: schema X: field "f": reason", as an Error prints.
func DefaultWarnings(g *Graph) []string {
	var out []string
	for _, e := range g.Entities {
		for _, f := range e.Fields[1:] {
			if f.Nillable || !nonZeroDefault(f) {
				continue
			}
			out = append(out, (&Error{Pos: f.Pos, Schema: e.Name, Where: fieldAt(f.Name),
				Reason: "its database default is not the zero value: an insert leaves a zero to the database, which stores the default, so the zero value cannot be stored through Insert; make it Nillable to store it"}).Error())
		}
	}
	return out
}

// nonZeroDefault reports whether f's SchemaDefault is, on some dialect, a
// literal of a non-zero bool, number or string (DefaultWarnings). An Other
// field in a decimal column (decimalField) is a number: its handle is a
// NumberCol, and its default a numeric literal.
func nonZeroDefault(f *Field) bool {
	typ := f.Type
	if typ == "other" && decimalField(f) {
		typ = "decimal"
	}
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
		if s := f.SchemaDefault[d.String()]; s != "" && nonZeroLiteral(typ, s) {
			return true
		}
	}
	return false
}

// nonZeroLiteral reports whether s, the SQL text of a default of a field of
// type typ ("decimal" for an Other field in a decimal column), is a literal
// of a value other than the type's zero. Parentheses
// around it (MySQL's form for some types) and a PostgreSQL cast after a
// quoted literal ('x'::text) are read through; anything else that is not a
// literal is an expression, and reports false.
func nonZeroLiteral(typ, s string) bool {
	s = strings.TrimSpace(s)
	for len(s) >= 2 && s[0] == '(' && s[len(s)-1] == ')' {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	if strings.HasPrefix(s, "'") {
		if i := strings.LastIndex(s, "'::"); i > 0 {
			s = s[:i+1]
		}
	}
	quoted, inner := sqlString(s)
	switch {
	case typ == "bool":
		v := strings.ToLower(s)
		if quoted {
			v = strings.ToLower(inner)
		}
		return slices.Contains([]string{"true", "1", "t", "y", "yes", "on"}, v)
	case numeric[typ]:
		if quoted {
			s = inner
		}
		n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		return err == nil && n != 0
	case typ == "decimal":
		if quoted {
			s = inner
		}
		r, ok := new(big.Rat).SetString(strings.TrimSpace(s))
		return ok && r.Sign() != 0
	case typ == "string":
		return quoted && inner != ""
	}
	return false
}

// sqlString reports whether s is one SQL string literal, '...' with each
// quote inside doubled, and returns its value.
func sqlString(s string) (bool, string) {
	if len(s) < 2 || s[0] != '\'' || s[len(s)-1] != '\'' {
		return false, ""
	}
	body := s[1 : len(s)-1]
	if strings.Count(strings.ReplaceAll(body, "''", ""), "'") > 0 {
		return false, "" // more than one literal, as in 'a' || 'b'
	}
	return true, strings.ReplaceAll(body, "''", "'")
}
