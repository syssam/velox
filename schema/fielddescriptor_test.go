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
// FieldDescriptors skips.
type Holey struct{ schema.Schema }

func (Holey) Mixin() []schema.Mixin { return []schema.Mixin{nil} }

func (Holey) Fields() []schema.Field { return []schema.Field{nil, field.String("name")} }

// Counted counts the calls of its Mixin and Fields.
type Counted struct {
	schema.Schema
	mixins, fields *int
}

func (c Counted) Mixin() []schema.Mixin { *c.mixins++; return []schema.Mixin{mixin.Time{}} }

func (c Counted) Fields() []schema.Field {
	*c.fields++
	return []schema.Field{field.String("a"), field.String("b")}
}

// FieldDescriptors finds fields by their names, the schema's own or a
// mixin's, in the order of the names, and panics, naming the schema and the
// field, when there is none.
func TestFieldDescriptors(t *testing.T) {
	ds := schema.FieldDescriptors(&Doc{}, "token", "updated_at")
	if ds[0].DefaultFunc.(func() string)() != "tok" {
		t.Errorf("token: %+v", ds[0])
	}
	if ds[1].UpdateDefault == nil {
		t.Errorf("updated_at, from the mixin, has no UpdateDefault: %+v", ds[1])
	}
	if d := schema.FieldDescriptors(Holey{}, "name")[0]; d.Name != "name" {
		t.Errorf("name: %+v", d)
	}
	defer func() {
		msg, _ := recover().(string)
		if !strings.Contains(msg, `schema_test.Doc has no field "slug"`) || !strings.Contains(msg, "run velox-gen") {
			t.Errorf("panic %q", msg)
		}
	}()
	schema.FieldDescriptors(&Doc{}, "token", "slug")
}

// TestFieldDescriptorsReadOnce: the descriptors of several fields cost one
// call of the schema's Mixin and of its Fields, where a call per field made
// one each per field.
func TestFieldDescriptorsReadOnce(t *testing.T) {
	var m, f int
	ds := schema.FieldDescriptors(Counted{mixins: &m, fields: &f}, "b", "created_at", "a")
	if ds[0].Name != "b" || ds[1].Name != "created_at" || ds[2].Name != "a" {
		t.Errorf("order: %s %s %s", ds[0].Name, ds[1].Name, ds[2].Name)
	}
	if m != 1 || f != 1 {
		t.Errorf("Mixin called %d times, Fields %d; want 1 each", m, f)
	}
}
