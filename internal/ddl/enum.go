package ddl

import (
	"strings"

	"github.com/syssam/velox2"
)

// EnumType is the column type of the PostgreSQL enum type name in the
// database schema schema: "schema"."name", or "name" when schema is empty.
func EnumType(schema, name string) string {
	if schema != "" {
		return Ident(velox.Postgres, schema) + "." + Ident(velox.Postgres, name)
	}
	return Ident(velox.Postgres, name)
}

// EnumCheck is the check that holds column col to values: "col" IN ('a', 'b').
func EnumCheck(d velox.Dialect, col string, values []string) string {
	vs := make([]string, len(values))
	for i, v := range values {
		vs[i] = Literal(d, v)
	}
	return Ident(d, col) + " IN (" + strings.Join(vs, ", ") + ")"
}

// ParseEnumCheck is the column and values of a check of EnumCheck's form;
// false when expr is not one. Space around the literals does not matter, and
// an unquoted column is read up to " IN (".
func ParseEnumCheck(d velox.Dialect, expr string) (col string, values []string, ok bool) {
	q := `"`
	if d == velox.MySQL {
		q = "`"
	}
	var list string
	if strings.HasPrefix(expr, q) {
		j := quoteEnd(d, expr, 0)
		if j == len(expr) {
			return "", nil, false
		}
		col = strings.ReplaceAll(expr[1:j-1], q+q, q)
		if list, ok = strings.CutPrefix(expr[j:], " IN ("); !ok {
			return "", nil, false
		}
	} else if col, list, ok = strings.Cut(expr, " IN ("); !ok {
		return "", nil, false
	}
	list, ok = strings.CutSuffix(list, ")")
	if !ok {
		return "", nil, false
	}
	if values, ok = literalList(d, list); !ok {
		return "", nil, false
	}
	return col, values, true
}

// MySQLEnum is MySQL's column type enum('a','b').
func MySQLEnum(values []string) string {
	vs := make([]string, len(values))
	for i, v := range values {
		vs[i] = Literal(velox.MySQL, v)
	}
	return "enum(" + strings.Join(vs, ",") + ")"
}

// ParseMySQLEnum is the values of a type of MySQLEnum's form; false when t is
// not one. Space around the literals does not matter.
func ParseMySQLEnum(t string) ([]string, bool) {
	list, ok := strings.CutPrefix(t, "enum(")
	if !ok {
		return nil, false
	}
	if list, ok = strings.CutSuffix(list, ")"); !ok {
		return nil, false
	}
	return literalList(velox.MySQL, list)
}

// literalList reads one or more string literals separated by commas.
func literalList(d velox.Dialect, s string) ([]string, bool) {
	var out []string
	for i := 0; ; {
		for i < len(s) && s[i] == ' ' {
			i++
		}
		if i >= len(s) || s[i] != '\'' {
			return nil, false
		}
		j := quoteEnd(d, s, i)
		v, ok := Unquote(d, s[i:j])
		if !ok {
			return nil, false
		}
		out = append(out, v)
		for j < len(s) && s[j] == ' ' {
			j++
		}
		if j == len(s) {
			return out, true
		}
		if s[j] != ',' {
			return nil, false
		}
		i = j + 1
	}
}
