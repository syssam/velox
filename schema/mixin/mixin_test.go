package mixin_test

import (
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSchemaBaseMixin tests the base Schema mixin.
func TestSchemaBaseMixin(t *testing.T) {
	m := mixin.Schema{}

	t.Run("returns_nil_fields", func(t *testing.T) {
		assert.Nil(t, m.Fields())
	})

	t.Run("returns_nil_edges", func(t *testing.T) {
		assert.Nil(t, m.Edges())
	})

	t.Run("returns_nil_indexes", func(t *testing.T) {
		assert.Nil(t, m.Indexes())
	})

	t.Run("returns_nil_hooks", func(t *testing.T) {
		assert.Nil(t, m.Hooks())
	})

	t.Run("returns_nil_interceptors", func(t *testing.T) {
		assert.Nil(t, m.Interceptors())
	})

	t.Run("returns_nil_policy", func(t *testing.T) {
		assert.Nil(t, m.Policy())
	})

	t.Run("returns_nil_annotations", func(t *testing.T) {
		assert.Nil(t, m.Annotations())
	})
}

// TestMixinImplementsInterface tests that Schema implements velox.Mixin.
func TestMixinImplementsInterface(t *testing.T) {
	var _ velox.Mixin = mixin.Schema{}
	var _ velox.Mixin = &mixin.Schema{}
}

// TestAnnotation is a test annotation type.
type TestAnnotation string

func (TestAnnotation) Name() string { return "TestAnnotation" }

// TestCustomMixin is a custom mixin for testing.
type TestCustomMixin struct {
	mixin.Schema
}

func (TestCustomMixin) Fields() []velox.Field {
	return []velox.Field{
		field.String("field1"),
		field.String("field2"),
	}
}

// TestSchema is a test schema with edges.
type TestSchema struct {
	velox.Schema
}

func (TestSchema) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("one", TestSchema.Type),
		edge.From("two", TestSchema.Type).
			Ref("one"),
	}
}

// TestAnnotateFields tests the AnnotateFields function.
func TestAnnotateFields(t *testing.T) {
	tests := []struct {
		name        string
		mixin       velox.Mixin
		annotations []schema.Annotation
		validate    func(t *testing.T, fields []velox.Field)
	}{
		{
			name:        "annotate_custom_mixin",
			mixin:       TestCustomMixin{},
			annotations: []schema.Annotation{TestAnnotation("foo")},
			validate: func(t *testing.T, fields []velox.Field) {
				require.Len(t, fields, 2)
				for _, f := range fields {
					desc := f.Descriptor()
					require.Len(t, desc.Annotations, 1)
					assert.Equal(t, TestAnnotation("foo"), desc.Annotations[0])
				}
			},
		},
		{
			name:  "multiple_annotations",
			mixin: TestCustomMixin{},
			annotations: []schema.Annotation{
				TestAnnotation("foo"),
				TestAnnotation("bar"),
				TestAnnotation("baz"),
			},
			validate: func(t *testing.T, fields []velox.Field) {
				require.Len(t, fields, 2)
				for _, f := range fields {
					desc := f.Descriptor()
					require.Len(t, desc.Annotations, 3)
				}
			},
		},
		{
			name:        "empty_annotations",
			mixin:       TestCustomMixin{},
			annotations: []schema.Annotation{},
			validate: func(t *testing.T, fields []velox.Field) {
				for _, f := range fields {
					assert.Empty(t, f.Descriptor().Annotations)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			annotated := mixin.AnnotateFields(tt.mixin, tt.annotations...)
			fields := annotated.Fields()
			tt.validate(t, fields)
		})
	}
}

// TestAnnotateEdges tests the AnnotateEdges function.
func TestAnnotateEdges(t *testing.T) {
	tests := []struct {
		name        string
		annotations []schema.Annotation
		validate    func(t *testing.T, edges []velox.Edge)
	}{
		{
			name:        "single_annotation",
			annotations: []schema.Annotation{TestAnnotation("edge_ann")},
			validate: func(t *testing.T, edges []velox.Edge) {
				require.Len(t, edges, 2)
				for _, e := range edges {
					desc := e.Descriptor()
					require.Len(t, desc.Annotations, 1)
					assert.Equal(t, TestAnnotation("edge_ann"), desc.Annotations[0])
				}
			},
		},
		{
			name: "multiple_annotations",
			annotations: []schema.Annotation{
				TestAnnotation("foo"),
				TestAnnotation("bar"),
				TestAnnotation("baz"),
			},
			validate: func(t *testing.T, edges []velox.Edge) {
				require.Len(t, edges, 2)
				for _, e := range edges {
					desc := e.Descriptor()
					require.Len(t, desc.Annotations, 3)
				}
			},
		},
		{
			name:        "empty_annotations",
			annotations: []schema.Annotation{},
			validate: func(t *testing.T, edges []velox.Edge) {
				for _, e := range edges {
					assert.Empty(t, e.Descriptor().Annotations)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			annotated := mixin.AnnotateEdges(TestSchema{}, tt.annotations...)
			edges := annotated.Edges()
			tt.validate(t, edges)
		})
	}
}

// TestAnnotateFieldsPreservesOtherMethods tests that AnnotateFields preserves other mixin methods.
func TestAnnotateFieldsPreservesOtherMethods(t *testing.T) {
	original := TestCustomMixin{}
	annotated := mixin.AnnotateFields(original, TestAnnotation("test"))

	// Fields should be annotated
	fields := annotated.Fields()
	require.Len(t, fields, 2)
	for _, f := range fields {
		require.Len(t, f.Descriptor().Annotations, 1)
	}

	// Other methods should be preserved (nil from embedded Schema)
	assert.Nil(t, annotated.Edges())
	assert.Nil(t, annotated.Indexes())
	assert.Nil(t, annotated.Hooks())
	assert.Nil(t, annotated.Policy())
}

// TestAnnotateEdgesPreservesOtherMethods tests that AnnotateEdges preserves other mixin methods.
func TestAnnotateEdgesPreservesOtherMethods(t *testing.T) {
	annotated := mixin.AnnotateEdges(TestSchema{}, TestAnnotation("test"))

	// Edges should be annotated
	edges := annotated.Edges()
	require.Len(t, edges, 2)
	for _, e := range edges {
		require.Len(t, e.Descriptor().Annotations, 1)
	}

	// Other methods should be preserved
	assert.Nil(t, annotated.Fields())
	assert.Nil(t, annotated.Indexes())
	assert.Nil(t, annotated.Hooks())
	assert.Nil(t, annotated.Policy())
}

// TestCustomMixinWithSchema tests creating a custom mixin by embedding Schema.
func TestCustomMixinWithSchema(t *testing.T) {
	t.Run("custom_mixin_embeds_schema", func(t *testing.T) {
		type AuditMixin struct {
			mixin.Schema
		}

		// Verify it implements Mixin interface
		var _ velox.Mixin = (*AuditMixin)(nil)

		// Test that it can define fields
		fields := func(AuditMixin) []velox.Field {
			return []velox.Field{
				field.String("created_by"),
				field.String("updated_by").Optional(),
			}
		}

		f := fields(AuditMixin{})
		require.Len(t, f, 2)
		assert.Equal(t, "created_by", f[0].Descriptor().Name)
		assert.Equal(t, "updated_by", f[1].Descriptor().Name)
	})
}

// BenchmarkMixin benchmarks mixin operations.
func BenchmarkMixin(b *testing.B) {
	b.Run("AnnotateFields", func(b *testing.B) {
		m := TestCustomMixin{}
		annotations := []schema.Annotation{
			TestAnnotation("foo"),
			TestAnnotation("bar"),
		}
		for i := 0; i < b.N; i++ {
			annotated := mixin.AnnotateFields(m, annotations...)
			_ = annotated.Fields()
		}
	})

	b.Run("AnnotateEdges", func(b *testing.B) {
		annotations := []schema.Annotation{
			TestAnnotation("foo"),
			TestAnnotation("bar"),
		}
		for i := 0; i < b.N; i++ {
			annotated := mixin.AnnotateEdges(TestSchema{}, annotations...)
			_ = annotated.Edges()
		}
	})
}
