// A codegen cmd for generating builder types from template.
package main

import (
	"bytes"
	"go/format"
	"log"
	"os"
	"text/template"
	"unicode"

	"github.com/syssam/velox/schema/field"
)

func main() {
	buf, err := os.ReadFile("internal/types.tmpl")
	if err != nil {
		log.Fatal("reading template file:", err)
	}
	tmpl := template.Must(template.New("types").
		Funcs(template.FuncMap{
			"ops":      ops,
			"title":    titleCase,
			"ident":    ident,
			"type":     typ,
			"hasIn":    hasIn,
			"isString": isString,
		}).
		Parse(string(buf)))
	b := &bytes.Buffer{}
	if err = tmpl.Execute(b, struct {
		Types []field.Type
	}{
		Types: []field.Type{
			field.TypeBool,
			field.TypeBytes,
			field.TypeTime,
			field.TypeUint,
			field.TypeUint8,
			field.TypeUint16,
			field.TypeUint32,
			field.TypeUint64,
			field.TypeInt,
			field.TypeInt8,
			field.TypeInt16,
			field.TypeInt32,
			field.TypeInt64,
			field.TypeFloat32,
			field.TypeFloat64,
			field.TypeString,
			field.TypeUUID,
			field.TypeOther,
		},
	}); err != nil {
		log.Fatal("executing template:", err)
	}
	if buf, err = format.Source(b.Bytes()); err != nil {
		log.Fatal("formatting output:", err)
	}
	if err = os.WriteFile("types.go", buf, 0o644); err != nil {
		log.Fatal("writing go file:", err)
	}
}

func ops(t field.Type) []string {
	switch t {
	case field.TypeBool, field.TypeBytes, field.TypeUUID, field.TypeOther:
		return []string{"EQ", "NEQ"}
	default:
		return []string{"EQ", "NEQ", "LT", "LTE", "GT", "GTE"}
	}
}

// hasIn reports whether the type supports In/NotIn operations.
func hasIn(t field.Type) bool {
	switch t {
	case field.TypeBool, field.TypeBytes:
		return false
	default:
		return true
	}
}

// isString reports whether the type is a string type.
func isString(t field.Type) bool {
	return t == field.TypeString
}

func ident(t field.Type) string {
	switch t {
	case field.TypeBytes:
		return "bytes"
	case field.TypeTime:
		return "time"
	case field.TypeUUID:
		return "value"
	case field.TypeOther:
		return "other"
	default:
		return t.String()
	}
}

func typ(t field.Type) string {
	if t == field.TypeUUID || t == field.TypeOther {
		return "driver.Valuer"
	}
	return t.String()
}

// titleCase uppercases the first rune of s. Replaces deprecated strings.Title.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	rs := []rune(s)
	rs[0] = unicode.ToUpper(rs[0])
	return string(rs)
}
