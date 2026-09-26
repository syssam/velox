package graphql

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dave/jennifer/jen"

	entgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

// TestInputs_CustomUUIDType pins that a UUID field whose Go type is not
// github.com/google/uuid.UUID (a named type, gofrs/uuid) keeps that type in
// the generated inputs. WhereInput and the mutation inputs hard-coded
// uuid.UUID while the predicates and setters (item.RefField.In, SetRef)
// take the schema's type, so the generated packages did not compile.
func TestInputs_CustomUUIDType(t *testing.T) {
	typ := &entgen.Type{
		Name: "Item",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{{
			Name: "ref",
			Type: &field.TypeInfo{Type: field.TypeUUID, Ident: "schematype.MyUUID", PkgPath: "example.com/app/schematype"},
			Annotations: map[string]any{
				AnnotationName: &Annotation{WhereInputEnabled: true},
			},
		}},
		Annotations: map[string]any{},
	}
	g := newTestGeneratorWithConfig(Config{ORMPackage: "example.com/app/velox", Package: "velox", WhereInputs: true}, typ)
	var buf bytes.Buffer
	if err := g.genWhereInputGo().Render(&buf); err != nil {
		t.Fatal(err)
	}
	code := buf.String()
	if !strings.Contains(code, "RefIn ") {
		t.Fatal("fixture must reach the WhereInput field")
	}
	for _, line := range strings.Split(code, "\n") {
		if strings.Contains(line, "RefIn ") {
			t.Log(strings.TrimSpace(line))
			if !strings.Contains(line, "schematype.MyUUID") {
				t.Errorf("RefIn must be typed []schematype.MyUUID to match item.RefField.In; got %q", strings.TrimSpace(line))
			}
		}
	}

	create := jen.NewFile("velox")
	g.genCreateInputStruct(create, typ)
	if got := create.GoString(); !strings.Contains(got, "schematype.MyUUID") || strings.Contains(got, "uuid.UUID") {
		t.Errorf("CreateItemInput.Ref must be typed schematype.MyUUID:\n%s", got)
	}
}
