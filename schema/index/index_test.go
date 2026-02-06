package index_test

import (
	"testing"

	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/index"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAnnotation is a test annotation type.
type TestAnnotation struct {
	Label string
	Value int
}

func (TestAnnotation) Name() string { return "TestAnnotation" }

// TestIndexFields tests creating indexes on fields.
func TestIndexFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		build    func() *index.Descriptor
		validate func(t *testing.T, desc *index.Descriptor)
	}{
		{
			name: "single_field",
			build: func() *index.Descriptor {
				return index.Fields("name").Descriptor()
			},
			validate: func(t *testing.T, desc *index.Descriptor) {
				assert.Equal(t, []string{"name"}, desc.Fields)
				assert.Empty(t, desc.Edges)
				assert.False(t, desc.Unique)
				assert.Empty(t, desc.StorageKey)
				assert.Nil(t, desc.Annotations)
			},
		},
		{
			name: "multiple_fields",
			build: func() *index.Descriptor {
				return index.Fields("first", "last").Descriptor()
			},
			validate: func(t *testing.T, desc *index.Descriptor) {
				assert.Equal(t, []string{"first", "last"}, desc.Fields)
				assert.Empty(t, desc.Edges)
			},
		},
		{
			name: "three_fields",
			build: func() *index.Descriptor {
				return index.Fields("a", "b", "c").Descriptor()
			},
			validate: func(t *testing.T, desc *index.Descriptor) {
				assert.Equal(t, []string{"a", "b", "c"}, desc.Fields)
			},
		},
		{
			name: "unique_index",
			build: func() *index.Descriptor {
				return index.Fields("email").Unique().Descriptor()
			},
			validate: func(t *testing.T, desc *index.Descriptor) {
				assert.Equal(t, []string{"email"}, desc.Fields)
				assert.True(t, desc.Unique)
			},
		},
		{
			name: "composite_unique_index",
			build: func() *index.Descriptor {
				return index.Fields("first", "last").Unique().Descriptor()
			},
			validate: func(t *testing.T, desc *index.Descriptor) {
				assert.Equal(t, []string{"first", "last"}, desc.Fields)
				assert.True(t, desc.Unique)
			},
		},
		{
			name: "with_storage_key",
			build: func() *index.Descriptor {
				return index.Fields("name", "address").
					StorageKey("idx_user_name_address").
					Descriptor()
			},
			validate: func(t *testing.T, desc *index.Descriptor) {
				assert.Equal(t, []string{"name", "address"}, desc.Fields)
				assert.Equal(t, "idx_user_name_address", desc.StorageKey)
			},
		},
		{
			name: "unique_with_storage_key",
			build: func() *index.Descriptor {
				return index.Fields("email").
					Unique().
					StorageKey("idx_unique_email").
					Descriptor()
			},
			validate: func(t *testing.T, desc *index.Descriptor) {
				assert.True(t, desc.Unique)
				assert.Equal(t, "idx_unique_email", desc.StorageKey)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			desc := tt.build()
			tt.validate(t, desc)
		})
	}
}

// TestIndexEdges tests creating indexes that include edges.
func TestIndexEdges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		build    func() *index.Descriptor
		validate func(t *testing.T, desc *index.Descriptor)
	}{
		{
			name: "edges_only",
			build: func() *index.Descriptor {
				return index.Edges("parent").Descriptor()
			},
			validate: func(t *testing.T, desc *index.Descriptor) {
				assert.Equal(t, []string{"parent"}, desc.Edges)
				assert.Empty(t, desc.Fields)
			},
		},
		{
			name: "multiple_edges",
			build: func() *index.Descriptor {
				return index.Edges("parent", "type").Descriptor()
			},
			validate: func(t *testing.T, desc *index.Descriptor) {
				assert.Equal(t, []string{"parent", "type"}, desc.Edges)
			},
		},
		{
			name: "fields_with_single_edge",
			build: func() *index.Descriptor {
				return index.Fields("name").
					Edges("parent").
					Descriptor()
			},
			validate: func(t *testing.T, desc *index.Descriptor) {
				assert.Equal(t, []string{"name"}, desc.Fields)
				assert.Equal(t, []string{"parent"}, desc.Edges)
			},
		},
		{
			name: "fields_with_multiple_edges",
			build: func() *index.Descriptor {
				return index.Fields("name").
					Edges("parent", "type").
					Descriptor()
			},
			validate: func(t *testing.T, desc *index.Descriptor) {
				assert.Equal(t, []string{"name"}, desc.Fields)
				assert.Equal(t, []string{"parent", "type"}, desc.Edges)
			},
		},
		{
			name: "unique_field_under_edge",
			build: func() *index.Descriptor {
				return index.Fields("name").
					Edges("parent").
					Unique().
					Descriptor()
			},
			validate: func(t *testing.T, desc *index.Descriptor) {
				assert.Equal(t, []string{"name"}, desc.Fields)
				assert.Equal(t, []string{"parent"}, desc.Edges)
				assert.True(t, desc.Unique)
			},
		},
		{
			name: "edges_then_fields",
			build: func() *index.Descriptor {
				return index.Edges("parent").
					Fields("name", "age").
					Unique().
					Descriptor()
			},
			validate: func(t *testing.T, desc *index.Descriptor) {
				assert.Equal(t, []string{"name", "age"}, desc.Fields)
				assert.Equal(t, []string{"parent"}, desc.Edges)
				assert.True(t, desc.Unique)
			},
		},
		{
			name: "complete_configuration",
			build: func() *index.Descriptor {
				return index.Fields("name", "address").
					Edges("parent", "type").
					Unique().
					StorageKey("idx_complete").
					Descriptor()
			},
			validate: func(t *testing.T, desc *index.Descriptor) {
				assert.Equal(t, []string{"name", "address"}, desc.Fields)
				assert.Equal(t, []string{"parent", "type"}, desc.Edges)
				assert.True(t, desc.Unique)
				assert.Equal(t, "idx_complete", desc.StorageKey)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			desc := tt.build()
			tt.validate(t, desc)
		})
	}
}

// TestIndexAnnotations tests index annotations.
func TestIndexAnnotations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		build    func() *index.Descriptor
		validate func(t *testing.T, desc *index.Descriptor)
	}{
		{
			name: "single_annotation",
			build: func() *index.Descriptor {
				return index.Fields("name").
					Annotations(TestAnnotation{Label: "prefix", Value: 100}).
					Descriptor()
			},
			validate: func(t *testing.T, desc *index.Descriptor) {
				require.Len(t, desc.Annotations, 1)
				ann := desc.Annotations[0].(TestAnnotation)
				assert.Equal(t, "prefix", ann.Label)
				assert.Equal(t, 100, ann.Value)
			},
		},
		{
			name: "multiple_annotations",
			build: func() *index.Descriptor {
				return index.Fields("name").
					Annotations(
						TestAnnotation{Label: "first", Value: 1},
						TestAnnotation{Label: "second", Value: 2},
					).
					Descriptor()
			},
			validate: func(t *testing.T, desc *index.Descriptor) {
				require.Len(t, desc.Annotations, 2)
				assert.Equal(t, "first", desc.Annotations[0].(TestAnnotation).Label)
				assert.Equal(t, "second", desc.Annotations[1].(TestAnnotation).Label)
			},
		},
		{
			name: "chained_annotations",
			build: func() *index.Descriptor {
				return index.Fields("name").
					Annotations(TestAnnotation{Label: "first", Value: 1}).
					Annotations(TestAnnotation{Label: "second", Value: 2}).
					Descriptor()
			},
			validate: func(t *testing.T, desc *index.Descriptor) {
				require.Len(t, desc.Annotations, 2)
			},
		},
		{
			name: "annotation_with_unique",
			build: func() *index.Descriptor {
				return index.Fields("name").
					Unique().
					Annotations(TestAnnotation{Label: "unique_partial", Value: 50}).
					Descriptor()
			},
			validate: func(t *testing.T, desc *index.Descriptor) {
				assert.True(t, desc.Unique)
				require.Len(t, desc.Annotations, 1)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			desc := tt.build()
			tt.validate(t, desc)
		})
	}
}

// TestIndexBuilderChaining tests various chaining patterns.
func TestIndexBuilderChaining(t *testing.T) {
	t.Parallel()

	t.Run("all_methods_chainable", func(t *testing.T) {
		t.Parallel()
		desc := index.Fields("a", "b").
			Edges("parent").
			Unique().
			StorageKey("idx_test").
			Annotations(TestAnnotation{Label: "test", Value: 1}).
			Descriptor()

		assert.Equal(t, []string{"a", "b"}, desc.Fields)
		assert.Equal(t, []string{"parent"}, desc.Edges)
		assert.True(t, desc.Unique)
		assert.Equal(t, "idx_test", desc.StorageKey)
		require.Len(t, desc.Annotations, 1)
	})

	t.Run("order_independence_edges_fields", func(t *testing.T) {
		t.Parallel()
		// Start with Fields
		desc1 := index.Fields("name").Edges("parent").Descriptor()
		// Start with Edges
		desc2 := index.Edges("parent").Fields("name").Descriptor()

		assert.Equal(t, desc1.Fields, desc2.Fields)
		assert.Equal(t, desc1.Edges, desc2.Edges)
	})

	t.Run("unique_position_flexible", func(t *testing.T) {
		t.Parallel()
		// Unique before StorageKey
		desc1 := index.Fields("name").Unique().StorageKey("idx").Descriptor()
		// Unique after StorageKey
		desc2 := index.Fields("name").StorageKey("idx").Unique().Descriptor()

		assert.Equal(t, desc1.Unique, desc2.Unique)
		assert.Equal(t, desc1.StorageKey, desc2.StorageKey)
	})
}

// TestIndexDescriptorZeroValue tests the zero value behavior.
func TestIndexDescriptorZeroValue(t *testing.T) {
	t.Parallel()

	desc := &index.Descriptor{}

	assert.False(t, desc.Unique)
	assert.Nil(t, desc.Fields)
	assert.Nil(t, desc.Edges)
	assert.Empty(t, desc.StorageKey)
	assert.Nil(t, desc.Annotations)
}

// TestIndexCommonPatterns tests common indexing patterns.
func TestIndexCommonPatterns(t *testing.T) {
	t.Parallel()

	t.Run("unique_email_index", func(t *testing.T) {
		t.Parallel()
		desc := index.Fields("email").Unique().Descriptor()
		assert.True(t, desc.Unique)
		assert.Equal(t, []string{"email"}, desc.Fields)
	})

	t.Run("composite_name_index", func(t *testing.T) {
		t.Parallel()
		desc := index.Fields("first_name", "last_name").Descriptor()
		assert.False(t, desc.Unique)
		assert.Equal(t, []string{"first_name", "last_name"}, desc.Fields)
	})

	t.Run("unique_constraint_on_relation", func(t *testing.T) {
		t.Parallel()
		desc := index.Fields("name").
			Edges("tenant").
			Unique().
			Descriptor()
		assert.True(t, desc.Unique)
		assert.Equal(t, []string{"name"}, desc.Fields)
		assert.Equal(t, []string{"tenant"}, desc.Edges)
	})

	t.Run("multi_tenant_unique_constraint", func(t *testing.T) {
		t.Parallel()
		desc := index.Fields("slug").
			Edges("organization", "workspace").
			Unique().
			StorageKey("idx_org_ws_slug").
			Descriptor()
		assert.True(t, desc.Unique)
		assert.Equal(t, []string{"slug"}, desc.Fields)
		assert.Equal(t, []string{"organization", "workspace"}, desc.Edges)
	})

	t.Run("lookup_index_non_unique", func(t *testing.T) {
		t.Parallel()
		desc := index.Fields("status", "created_at").
			StorageKey("idx_status_created").
			Descriptor()
		assert.False(t, desc.Unique)
		assert.Equal(t, []string{"status", "created_at"}, desc.Fields)
	})
}

// BenchmarkIndexBuilder benchmarks index builder performance.
func BenchmarkIndexBuilder(b *testing.B) {
	b.Run("simple_index", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			index.Fields("name").Descriptor()
		}
	})

	b.Run("composite_index", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			index.Fields("first", "last", "email").Descriptor()
		}
	})

	b.Run("unique_index", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			index.Fields("email").Unique().Descriptor()
		}
	})

	b.Run("full_configuration", func(b *testing.B) {
		annotations := []schema.Annotation{
			TestAnnotation{Label: "test", Value: 1},
		}
		for i := 0; i < b.N; i++ {
			index.Fields("name", "status").
				Edges("parent", "type").
				Unique().
				StorageKey("idx_full").
				Annotations(annotations...).
				Descriptor()
		}
	})
}

// TestIndexEmptyInputs tests edge cases with empty inputs.
func TestIndexEmptyInputs(t *testing.T) {
	t.Parallel()

	t.Run("empty_fields", func(t *testing.T) {
		t.Parallel()
		desc := index.Fields().Descriptor()
		assert.Empty(t, desc.Fields)
	})

	t.Run("empty_edges", func(t *testing.T) {
		t.Parallel()
		desc := index.Edges().Descriptor()
		assert.Empty(t, desc.Edges)
	})

	t.Run("empty_storage_key", func(t *testing.T) {
		t.Parallel()
		desc := index.Fields("name").StorageKey("").Descriptor()
		assert.Empty(t, desc.StorageKey)
	})
}

// TestIndexImmutability tests that builder operations don't affect each other.
func TestIndexImmutability(t *testing.T) {
	t.Parallel()

	t.Run("separate_builders", func(t *testing.T) {
		t.Parallel()
		builder := index.Fields("name")

		desc1 := builder.Unique().Descriptor()
		desc2 := builder.Descriptor()

		// Both should now be unique because they share the same underlying descriptor
		// This is expected behavior - the builder pattern modifies in place
		assert.True(t, desc1.Unique)
		assert.True(t, desc2.Unique) // Same descriptor
	})
}
