package graphqlgen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dave/jennifer/jen"

	entgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/contrib/graphql"
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
				graphql.AnnotationName: &graphql.Annotation{WhereInputEnabled: true},
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

// TestNillableBytesResolvesThroughAccessor pins that a Nillable Bytes
// field (typed *[]byte) is resolved through a generated []byte accessor.
// gqlgen dereferences a *[]byte without a nil check, so reading NULL
// through the struct field panicked ("internal system error").
func TestNillableBytesResolvesThroughAccessor(t *testing.T) {
	typ := &entgen.Type{
		Name: "Doc",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{
			{Name: "thumb", Type: &field.TypeInfo{Type: field.TypeBytes}, Optional: true, Nillable: true},
			{Name: "blob", Type: &field.TypeInfo{Type: field.TypeBytes}},
		},
		Annotations: map[string]any{},
	}
	g := newTestGeneratorWithConfig(Config{ORMPackage: "example.com/app/velox", Package: "velox"}, typ)
	sdl := g.genEntityType(typ)
	if !strings.Contains(sdl, `thumb: Bytes @goField(name: "ThumbOrNil")`) {
		t.Errorf("Nillable Bytes field must resolve through ThumbOrNil:\n%s", sdl)
	}
	if !strings.Contains(sdl, "blob: Bytes!\n") {
		t.Errorf("a non-nillable Bytes field needs no accessor:\n%s", sdl)
	}
	f := g.genEntityEdge(typ)
	if f == nil {
		t.Fatal("a type with a Nillable Bytes field must get its accessor file")
	}
	code := f.GoString()
	if !strings.Contains(code, "func (m *Doc) ThumbOrNil() []byte {") || strings.Contains(code, "BlobOrNil") {
		t.Errorf("want only the ThumbOrNil accessor:\n%s", code)
	}
}
