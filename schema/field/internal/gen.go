// gen is a codegen cmd for generating numeric build types from template.
package main

import (
	"bytes"
	"go/format"
	"log"
	"os"
	"strings"
	"text/template"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/syssam/velox/schema/field"
)

func main() {
	buf, err := os.ReadFile("internal/numeric.tmpl")
	if err != nil {
		log.Fatal("reading template file:", err)
	}
	titleCaser := cases.Title(language.English)
	intTmpl := template.Must(template.New("numeric").
		Funcs(template.FuncMap{"title": titleCaser.String, "hasPrefix": strings.HasPrefix, "toUpper": strings.ToUpper}).
		Parse(string(buf)))
	b := &bytes.Buffer{}
	if err = intTmpl.Execute(b, struct {
		Ints, Floats []field.Type
	}{
		Ints: []field.Type{
			field.TypeInt,
			field.TypeUint,
			field.TypeInt8,
			field.TypeInt16,
			field.TypeInt32,
			field.TypeInt64,
			field.TypeUint8,
			field.TypeUint16,
			field.TypeUint32,
			field.TypeUint64,
		},
		Floats: []field.Type{
			field.TypeFloat64,
			field.TypeFloat32,
		},
	}); err != nil {
		log.Fatal("executing template:", err)
	}
	if buf, err = format.Source(b.Bytes()); err != nil {
		log.Fatal("formatting output:", err)
	}
	if err = os.WriteFile("numeric.go", buf, 0o644); err != nil {
		log.Fatal("writing go file:", err)
	}
}
