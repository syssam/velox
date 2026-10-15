package schema_test

import (
	"encoding/json"
	"testing"

	"github.com/syssam/velox2/schema"
)

type User struct{ schema.Schema }

func TestSchemaDefaults(t *testing.T) {
	var s schema.Interface = User{}
	if s.Fields() != nil || s.Edges() != nil || s.Indexes() != nil || s.Mixin() != nil || s.Annotations() != nil {
		t.Fatal("an empty schema declares nothing")
	}
	_ = User.Type // the method expression edges use
}

func TestTableAnnotations(t *testing.T) {
	for _, c := range []struct {
		a    schema.Annotation
		name string
		js   string
	}{
		{schema.Table("users"), "Table", `{"Schema":"","Table":"users"}`},
		{schema.TableIn("billing", "invoices"), "Table", `{"Schema":"billing","Table":"invoices"}`},
		{schema.Comment("a user"), "Comment", `{"Text":"a user"}`},
	} {
		b, err := json.Marshal(c.a)
		if err != nil || string(b) != c.js || c.a.Name() != c.name {
			t.Errorf("%T: name %q json %s (%v), want %q %s", c.a, c.a.Name(), b, err, c.name, c.js)
		}
	}
}
