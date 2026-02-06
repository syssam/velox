package schema_test

import (
	"testing"

	"github.com/syssam/velox/schema"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCommentAnnotation tests the CommentAnnotation type.
func TestCommentAnnotation(t *testing.T) {
	t.Run("Name", func(t *testing.T) {
		ann := &schema.CommentAnnotation{Text: "test comment"}
		assert.Equal(t, "Comment", ann.Name())
	})

	t.Run("Comment_constructor", func(t *testing.T) {
		ann := schema.Comment("User represents a user in the system.")
		require.NotNil(t, ann)
		assert.Equal(t, "User represents a user in the system.", ann.Text)
		assert.Equal(t, "Comment", ann.Name())
	})

	t.Run("implements_Annotation", func(_ *testing.T) {
		var _ schema.Annotation = (*schema.CommentAnnotation)(nil)
	})
}

// mockAnnotation is a test implementation of Annotation.
type mockAnnotation struct {
	name  string
	value string
}

func (m *mockAnnotation) Name() string {
	return m.name
}

// mockMerger implements both Annotation and Merger.
type mockMerger struct {
	name   string
	values []string
}

func (m *mockMerger) Name() string {
	return m.name
}

func (m *mockMerger) Merge(other schema.Annotation) schema.Annotation {
	if o, ok := other.(*mockMerger); ok {
		return &mockMerger{
			name:   m.name,
			values: append(m.values, o.values...),
		}
	}
	return m
}

// TestAnnotationInterface tests that types implement the Annotation interface correctly.
func TestAnnotationInterface(t *testing.T) {
	t.Run("custom_annotation", func(t *testing.T) {
		ann := &mockAnnotation{name: "MyAnnotation", value: "test"}
		var _ schema.Annotation = ann
		assert.Equal(t, "MyAnnotation", ann.Name())
	})

	t.Run("unique_names", func(t *testing.T) {
		ann1 := &mockAnnotation{name: "Ann1", value: "val1"}
		ann2 := &mockAnnotation{name: "Ann2", value: "val2"}

		assert.NotEqual(t, ann1.Name(), ann2.Name())
	})
}

// TestMergerInterface tests the Merger interface.
func TestMergerInterface(t *testing.T) {
	t.Run("merge_same_type", func(t *testing.T) {
		m1 := &mockMerger{name: "Test", values: []string{"a", "b"}}
		m2 := &mockMerger{name: "Test", values: []string{"c", "d"}}

		merged := m1.Merge(m2)
		require.NotNil(t, merged)

		mm, ok := merged.(*mockMerger)
		require.True(t, ok)
		assert.Equal(t, []string{"a", "b", "c", "d"}, mm.values)
	})

	t.Run("merge_different_type", func(t *testing.T) {
		m1 := &mockMerger{name: "Test", values: []string{"a", "b"}}
		other := &mockAnnotation{name: "Other", value: "x"}

		merged := m1.Merge(other)
		assert.Equal(t, m1, merged) // Should return self when types don't match
	})

	t.Run("implements_both_interfaces", func(_ *testing.T) {
		var merger mockMerger

		var _ schema.Annotation = &merger
		var _ schema.Merger = &merger
	})
}

// BenchmarkAnnotation benchmarks annotation operations.
func BenchmarkAnnotation(b *testing.B) {
	b.Run("Comment_constructor", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = schema.Comment("User entity representing a user in the system")
		}
	})

	b.Run("Name", func(b *testing.B) {
		ann := schema.Comment("test")
		for i := 0; i < b.N; i++ {
			_ = ann.Name()
		}
	})

	b.Run("Merge", func(b *testing.B) {
		m1 := &mockMerger{name: "Test", values: []string{"a", "b"}}
		m2 := &mockMerger{name: "Test", values: []string{"c", "d"}}
		for i := 0; i < b.N; i++ {
			_ = m1.Merge(m2)
		}
	})
}
