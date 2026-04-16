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

// TestIDMixin tests the ID mixin.
func TestIDMixin(t *testing.T) {
	m := mixin.ID{}
	fields := m.Fields()

	t.Run("has_one_field", func(t *testing.T) {
		require.Len(t, fields, 1)
	})

	t.Run("field_name", func(t *testing.T) {
		desc := fields[0].Descriptor()
		assert.Equal(t, "id", desc.Name)
	})

	t.Run("field_is_immutable", func(t *testing.T) {
		desc := fields[0].Descriptor()
		assert.True(t, desc.Immutable)
	})

	t.Run("has_default", func(t *testing.T) {
		desc := fields[0].Descriptor()
		assert.NotNil(t, desc.Default)
	})
}

// TestTenantIDMixin tests the TenantID mixin.
func TestTenantIDMixin(t *testing.T) {
	m := mixin.TenantID{}
	fields := m.Fields()

	t.Run("has_one_field", func(t *testing.T) {
		require.Len(t, fields, 1)
	})

	t.Run("field_name", func(t *testing.T) {
		desc := fields[0].Descriptor()
		assert.Equal(t, "tenant_id", desc.Name)
	})

	t.Run("field_is_immutable", func(t *testing.T) {
		desc := fields[0].Descriptor()
		assert.True(t, desc.Immutable)
	})

	t.Run("has_validator", func(t *testing.T) {
		desc := fields[0].Descriptor()
		assert.NotEmpty(t, desc.Validators, "tenant_id should have NotEmpty validator")
	})
}

// TestTimeMixin tests the Time mixin (created_at + updated_at).
func TestTimeMixin(t *testing.T) {
	m := mixin.Time{}
	fields := m.Fields()

	t.Run("has_two_fields", func(t *testing.T) {
		require.Len(t, fields, 2)
	})

	t.Run("created_at_field", func(t *testing.T) {
		desc := fields[0].Descriptor()
		assert.Equal(t, "created_at", desc.Name)
		assert.True(t, desc.Immutable, "created_at should be immutable")
		assert.NotNil(t, desc.Default, "created_at should have a default")
		assert.NotEmpty(t, desc.Comment, "created_at should have a comment")
	})

	t.Run("updated_at_field", func(t *testing.T) {
		desc := fields[1].Descriptor()
		assert.Equal(t, "updated_at", desc.Name)
		assert.False(t, desc.Immutable, "updated_at should not be immutable")
		assert.NotNil(t, desc.Default, "updated_at should have a default")
		assert.NotNil(t, desc.UpdateDefault, "updated_at should have an update default")
		assert.NotEmpty(t, desc.Comment, "updated_at should have a comment")
	})

	t.Run("implements_mixin_interface", func(t *testing.T) {
		var _ velox.Mixin = mixin.Time{}
	})
}

// TestCreateTimeMixin tests the CreateTime mixin (created_at only).
func TestCreateTimeMixin(t *testing.T) {
	m := mixin.CreateTime{}
	fields := m.Fields()

	t.Run("has_one_field", func(t *testing.T) {
		require.Len(t, fields, 1)
	})

	t.Run("created_at_field", func(t *testing.T) {
		desc := fields[0].Descriptor()
		assert.Equal(t, "created_at", desc.Name)
		assert.True(t, desc.Immutable, "created_at should be immutable")
		assert.NotNil(t, desc.Default, "created_at should have a default")
		assert.NotEmpty(t, desc.Comment)
	})

	t.Run("implements_mixin_interface", func(t *testing.T) {
		var _ velox.Mixin = mixin.CreateTime{}
	})
}

// TestUpdateTimeMixin tests the UpdateTime mixin (updated_at only).
func TestUpdateTimeMixin(t *testing.T) {
	m := mixin.UpdateTime{}
	fields := m.Fields()

	t.Run("has_one_field", func(t *testing.T) {
		require.Len(t, fields, 1)
	})

	t.Run("updated_at_field", func(t *testing.T) {
		desc := fields[0].Descriptor()
		assert.Equal(t, "updated_at", desc.Name)
		assert.False(t, desc.Immutable, "updated_at should not be immutable")
		assert.NotNil(t, desc.Default, "updated_at should have a default")
		assert.NotNil(t, desc.UpdateDefault, "updated_at should have an update default")
		assert.NotEmpty(t, desc.Comment)
	})

	t.Run("implements_mixin_interface", func(t *testing.T) {
		var _ velox.Mixin = mixin.UpdateTime{}
	})
}

// TestSoftDeleteMixin tests the SoftDelete mixin (deleted_at).
func TestSoftDeleteMixin(t *testing.T) {
	m := mixin.SoftDelete{}
	fields := m.Fields()

	t.Run("has_one_field", func(t *testing.T) {
		require.Len(t, fields, 1)
	})

	t.Run("deleted_at_field", func(t *testing.T) {
		desc := fields[0].Descriptor()
		assert.Equal(t, "deleted_at", desc.Name)
		assert.True(t, desc.Optional, "deleted_at should be optional")
		assert.True(t, desc.Nillable, "deleted_at should be nillable")
		assert.NotEmpty(t, desc.Comment)
	})

	t.Run("implements_mixin_interface", func(t *testing.T) {
		var _ velox.Mixin = mixin.SoftDelete{}
	})
}

// TestTimeSoftDeleteMixin tests the TimeSoftDelete mixin (created_at + updated_at + deleted_at).
func TestTimeSoftDeleteMixin(t *testing.T) {
	m := mixin.TimeSoftDelete{}
	fields := m.Fields()

	t.Run("has_three_fields", func(t *testing.T) {
		require.Len(t, fields, 3)
	})

	t.Run("contains_time_fields", func(t *testing.T) {
		assert.Equal(t, "created_at", fields[0].Descriptor().Name)
		assert.Equal(t, "updated_at", fields[1].Descriptor().Name)
	})

	t.Run("contains_soft_delete_field", func(t *testing.T) {
		assert.Equal(t, "deleted_at", fields[2].Descriptor().Name)
	})

	t.Run("created_at_is_immutable", func(t *testing.T) {
		assert.True(t, fields[0].Descriptor().Immutable)
	})

	t.Run("deleted_at_is_nillable", func(t *testing.T) {
		desc := fields[2].Descriptor()
		assert.True(t, desc.Optional)
		assert.True(t, desc.Nillable)
	})

	t.Run("implements_mixin_interface", func(t *testing.T) {
		var _ velox.Mixin = mixin.TimeSoftDelete{}
	})
}

// TestAuditMixin tests the Audit mixin (created_at, created_by, updated_at, updated_by).
func TestAuditMixin(t *testing.T) {
	m := mixin.Audit{}
	fields := m.Fields()

	t.Run("has_four_fields", func(t *testing.T) {
		require.Len(t, fields, 4)
	})

	t.Run("field_names", func(t *testing.T) {
		assert.Equal(t, "created_at", fields[0].Descriptor().Name)
		assert.Equal(t, "created_by", fields[1].Descriptor().Name)
		assert.Equal(t, "updated_at", fields[2].Descriptor().Name)
		assert.Equal(t, "updated_by", fields[3].Descriptor().Name)
	})

	t.Run("created_at_is_immutable", func(t *testing.T) {
		desc := fields[0].Descriptor()
		assert.True(t, desc.Immutable, "created_at should be immutable")
		assert.NotNil(t, desc.Default, "created_at should have a default")
	})

	t.Run("created_by_is_optional_and_nillable", func(t *testing.T) {
		desc := fields[1].Descriptor()
		assert.True(t, desc.Optional, "created_by should be optional")
		assert.True(t, desc.Nillable, "created_by should be nillable")
	})

	t.Run("updated_at_has_update_default", func(t *testing.T) {
		desc := fields[2].Descriptor()
		assert.NotNil(t, desc.Default, "updated_at should have a default")
		assert.NotNil(t, desc.UpdateDefault, "updated_at should have an update default")
		assert.False(t, desc.Immutable, "updated_at should not be immutable")
	})

	t.Run("updated_by_is_optional_and_nillable", func(t *testing.T) {
		desc := fields[3].Descriptor()
		assert.True(t, desc.Optional, "updated_by should be optional")
		assert.True(t, desc.Nillable, "updated_by should be nillable")
	})

	t.Run("implements_mixin_interface", func(t *testing.T) {
		var _ velox.Mixin = mixin.Audit{}
	})
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
		for b.Loop() {
			annotated := mixin.AnnotateFields(m, annotations...)
			_ = annotated.Fields()
		}
	})

	b.Run("AnnotateEdges", func(b *testing.B) {
		annotations := []schema.Annotation{
			TestAnnotation("foo"),
			TestAnnotation("bar"),
		}
		for b.Loop() {
			annotated := mixin.AnnotateEdges(TestSchema{}, annotations...)
			_ = annotated.Edges()
		}
	})
}
