package mixin_test

import (
	"maps"
	"testing"

	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

var _ schema.Mixin = mixin.Time{}

func TestTime(t *testing.T) {
	fs := mixin.Time{}.Fields()
	if len(fs) != 2 {
		t.Fatalf("%d fields", len(fs))
	}
	c, u := fs[0].Descriptor(), fs[1].Descriptor()
	if c.Name != "created_at" || !c.Immutable || c.DefaultFunc == nil && c.Default == nil || len(c.Errs) != 0 {
		t.Fatalf("created_at %+v", c)
	}
	if u.Name != "updated_at" || u.UpdateDefault == nil || len(u.Errs) != 0 {
		t.Fatalf("updated_at %+v", u)
	}
	if len(mixin.CreateTime{}.Fields()) != 1 || len(mixin.UpdateTime{}.Fields()) != 1 {
		t.Fatal("CreateTime and UpdateTime add one field each")
	}
	if (mixin.Schema{}).Edges() != nil {
		t.Fatal("an empty mixin declares nothing")
	}
}

func TestTimeSchemaDefault(t *testing.T) {
	want := map[string]string{field.Postgres: "now()", field.MySQL: "CURRENT_TIMESTAMP(6)", field.SQLite: "CURRENT_TIMESTAMP"}
	fs := mixin.Time{}.Fields()
	for _, f := range fs {
		if d := f.Descriptor(); !maps.Equal(d.SchemaDefault, want) {
			t.Errorf("%s: %v", d.Name, d.SchemaDefault)
		}
	}
	if len(fs) != 2 {
		t.Fatalf("%d fields", len(fs))
	}
}

// TestSchemaIsEmpty: an embedded Schema contributes nothing until a method overrides it.
func TestSchemaIsEmpty(t *testing.T) {
	var s mixin.Schema
	if s.Fields() != nil || s.Indexes() != nil || s.Annotations() != nil {
		t.Errorf("mixin.Schema: %v %v %v", s.Fields(), s.Indexes(), s.Annotations())
	}
}
