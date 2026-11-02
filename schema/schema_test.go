package schema_test

import (
	"encoding/json"
	"testing"

	"github.com/syssam/velox/schema"
)

type User struct{ schema.Schema }

func TestSchemaDefaults(t *testing.T) {
	var s schema.Interface = User{}
	if s.Fields() != nil || s.Edges() != nil || s.Indexes() != nil || s.Mixin() != nil || s.Annotations() != nil {
		t.Fatal("an empty schema declares nothing")
	}
	_ = User.Type // the method expression edges use
}

// TestAnnotationJSON pins the annotations' lower-case JSON keys, which
// velox-gen and plugins read.
func TestAnnotationJSON(t *testing.T) {
	for _, c := range []struct {
		a    schema.Annotation
		name string
		js   string
	}{
		{schema.Table("users"), "Table", `{"table":"users"}`},
		{schema.TableIn("s", "t"), "Table", `{"schema":"s","table":"t"}`},
		{schema.Comment("a user"), "Comment", `{"text":"a user"}`},
		{schema.Scoped("org"), "Scoped", `{"key":"org"}`},
	} {
		b, err := json.Marshal(c.a)
		if err != nil || string(b) != c.js || c.a.Name() != c.name {
			t.Errorf("%T: name %q json %s (%v), want %q %s", c.a, c.a.Name(), b, err, c.name, c.js)
		}
	}
}

func TestCheckMerge(t *testing.T) {
	a := schema.Check("a", "x > 0")
	m, ok := a.Merge(schema.Check("b", "y > 0")).(*schema.CheckAnnotation)
	if !ok || len(m.Checks) != 2 || len(a.Checks) != 1 {
		t.Errorf("Merge = %+v, a = %+v", m, a)
	}
	if got := a.Merge(schema.Table("t")); got != schema.Annotation(a) {
		t.Errorf("Merge of another annotation = %+v", got)
	}
}
