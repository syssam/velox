package schema_test

import (
	"strings"
	"testing"

	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Doc struct{ schema.Schema }

func (Doc) Mixin() []schema.Mixin { return []schema.Mixin{mixin.Time{}} }

func (Doc) Fields() []schema.Field {
	return []schema.Field{field.String("token").DefaultFunc(func() string { return "tok" })}
}

// Holey lists a nil mixin and a nil field, which velox-gen refuses and
// FieldDescriptor skips.
type Holey struct{ schema.Schema }

func (Holey) Mixin() []schema.Mixin { return []schema.Mixin{nil} }

func (Holey) Fields() []schema.Field { return []schema.Field{nil, field.String("name")} }

// FieldDescriptor finds a field by its name, the schema's own or a mixin's,
// and panics, naming the schema and the field, when there is none.
func TestFieldDescriptor(t *testing.T) {
	if d := schema.FieldDescriptor(&Doc{}, "token"); d.DefaultFunc.(func() string)() != "tok" {
		t.Errorf("token: %+v", d)
	}
	if d := schema.FieldDescriptor(Doc{}, "updated_at"); d.UpdateDefault == nil {
		t.Errorf("updated_at, from the mixin, has no UpdateDefault: %+v", d)
	}
	if d := schema.FieldDescriptor(Holey{}, "name"); d.Name != "name" {
		t.Errorf("name: %+v", d)
	}
	defer func() {
		msg, _ := recover().(string)
		if !strings.Contains(msg, `schema_test.Doc has no field "slug"`) || !strings.Contains(msg, "run velox-gen") {
			t.Errorf("panic %q", msg)
		}
	}()
	schema.FieldDescriptor(&Doc{}, "slug")
}
