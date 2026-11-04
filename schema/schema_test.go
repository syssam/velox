package schema_test

import (
	"encoding/json"
	"testing"

	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/mixin"
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

type audited struct {
	mixin.Schema
	By string
}

type other struct{ audited }

type withMixins struct{ schema.Schema }

func (withMixins) Mixin() []schema.Mixin {
	return []schema.Mixin{audited{By: "a"}, &audited{By: "b"}, other{}}
}

// TestMixinAt: the mixin at an index, a value copied or a pointer returned.
func TestMixinAt(t *testing.T) {
	if m := schema.MixinAt[audited](withMixins{}, 0); m.By != "a" {
		t.Errorf("value: %+v", m)
	}
	if m := schema.MixinAt[audited](withMixins{}, 1); m.By != "b" {
		t.Errorf("pointer: %+v", m)
	}
}

// TestMixinAtStalePanics: a mixin of another type, or an index past the
// list, as code generated before the schema changed reads, panics with
// "run velox-gen".
func TestMixinAtStalePanics(t *testing.T) {
	for _, c := range []struct {
		i    int
		want string
	}{
		{2, "schema: schema_test.withMixins: Mixin()[2] is schema_test.other, not schema_test.audited: the generated code is older than the schema; run velox-gen"},
		{3, "schema: schema_test.withMixins: Mixin()[3] is nothing, not schema_test.audited: the generated code is older than the schema; run velox-gen"},
	} {
		func() {
			defer func() {
				if r := recover(); r != c.want {
					t.Errorf("index %d: got %v, want %s", c.i, r, c.want)
				}
			}()
			schema.MixinAt[audited](withMixins{}, c.i)
		}()
	}
}
