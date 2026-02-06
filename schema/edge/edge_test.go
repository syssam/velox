package edge_test

import (
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test schema types for edge testing.
type (
	User       struct{ velox.Schema }
	Post       struct{ velox.Schema }
	Comment    struct{ velox.Schema }
	Group      struct{ velox.Schema }
	Friendship struct{ velox.Schema }
)

// TestEdgeTo tests the edge.To builder with various configurations.
func TestEdgeTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		build    func() *edge.Descriptor
		validate func(t *testing.T, desc *edge.Descriptor)
	}{
		{
			name: "basic_edge",
			build: func() *edge.Descriptor {
				return edge.To("posts", Post.Type).Descriptor()
			},
			validate: func(t *testing.T, desc *edge.Descriptor) {
				assert.Equal(t, "posts", desc.Name)
				assert.Equal(t, "Post", desc.Type)
				assert.False(t, desc.Inverse)
				assert.False(t, desc.Unique)
				assert.False(t, desc.Required)
				assert.False(t, desc.Immutable)
				assert.Empty(t, desc.Comment)
				assert.Nil(t, desc.StorageKey)
			},
		},
		{
			name: "unique_edge",
			build: func() *edge.Descriptor {
				return edge.To("profile", User.Type).Unique().Descriptor()
			},
			validate: func(t *testing.T, desc *edge.Descriptor) {
				assert.Equal(t, "profile", desc.Name)
				assert.True(t, desc.Unique)
				assert.False(t, desc.Required)
			},
		},
		{
			name: "required_edge",
			build: func() *edge.Descriptor {
				return edge.To("owner", User.Type).Required().Descriptor()
			},
			validate: func(t *testing.T, desc *edge.Descriptor) {
				assert.Equal(t, "owner", desc.Name)
				assert.True(t, desc.Required)
				assert.False(t, desc.Unique)
			},
		},
		{
			name: "immutable_edge",
			build: func() *edge.Descriptor {
				return edge.To("creator", User.Type).Immutable().Descriptor()
			},
			validate: func(t *testing.T, desc *edge.Descriptor) {
				assert.Equal(t, "creator", desc.Name)
				assert.True(t, desc.Immutable)
			},
		},
		{
			name: "edge_with_comment",
			build: func() *edge.Descriptor {
				return edge.To("friends", User.Type).Comment("user friends").Descriptor()
			},
			validate: func(t *testing.T, desc *edge.Descriptor) {
				assert.Equal(t, "user friends", desc.Comment)
			},
		},
		{
			name: "edge_with_struct_tag",
			build: func() *edge.Descriptor {
				return edge.To("user", User.Type).StructTag(`json:"user,omitempty"`).Descriptor()
			},
			validate: func(t *testing.T, desc *edge.Descriptor) {
				assert.Equal(t, `json:"user,omitempty"`, desc.Tag)
			},
		},
		{
			name: "edge_with_field_binding",
			build: func() *edge.Descriptor {
				return edge.To("owner", User.Type).Field("owner_id").Unique().Descriptor()
			},
			validate: func(t *testing.T, desc *edge.Descriptor) {
				assert.Equal(t, "owner_id", desc.Field)
				assert.True(t, desc.Unique)
			},
		},
		{
			name: "edge_with_through",
			build: func() *edge.Descriptor {
				return edge.To("friends", User.Type).Through("friendships", Friendship.Type).Descriptor()
			},
			validate: func(t *testing.T, desc *edge.Descriptor) {
				require.NotNil(t, desc.Through)
				assert.Equal(t, "friendships", desc.Through.N)
				assert.Equal(t, "Friendship", desc.Through.T)
			},
		},
		{
			name: "edge_with_all_options",
			build: func() *edge.Descriptor {
				return edge.To("parent", User.Type).
					Unique().
					Required().
					Immutable().
					Field("parent_id").
					Comment("parent user").
					StructTag(`json:"parent"`).
					Descriptor()
			},
			validate: func(t *testing.T, desc *edge.Descriptor) {
				assert.Equal(t, "parent", desc.Name)
				assert.Equal(t, "User", desc.Type)
				assert.True(t, desc.Unique)
				assert.True(t, desc.Required)
				assert.True(t, desc.Immutable)
				assert.Equal(t, "parent_id", desc.Field)
				assert.Equal(t, "parent user", desc.Comment)
				assert.Equal(t, `json:"parent"`, desc.Tag)
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

// TestEdgeFrom tests the edge.From builder for inverse edges.
func TestEdgeFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		build    func() *edge.Descriptor
		validate func(t *testing.T, desc *edge.Descriptor)
	}{
		{
			name: "basic_inverse_edge",
			build: func() *edge.Descriptor {
				return edge.From("author", User.Type).Ref("posts").Descriptor()
			},
			validate: func(t *testing.T, desc *edge.Descriptor) {
				assert.Equal(t, "author", desc.Name)
				assert.Equal(t, "User", desc.Type)
				assert.True(t, desc.Inverse)
				assert.Equal(t, "posts", desc.RefName)
			},
		},
		{
			name: "inverse_unique_edge",
			build: func() *edge.Descriptor {
				return edge.From("owner", User.Type).Ref("pets").Unique().Descriptor()
			},
			validate: func(t *testing.T, desc *edge.Descriptor) {
				assert.True(t, desc.Inverse)
				assert.True(t, desc.Unique)
			},
		},
		{
			name: "inverse_required_edge",
			build: func() *edge.Descriptor {
				return edge.From("author", User.Type).Ref("posts").Required().Descriptor()
			},
			validate: func(t *testing.T, desc *edge.Descriptor) {
				assert.True(t, desc.Required)
			},
		},
		{
			name: "inverse_immutable_edge",
			build: func() *edge.Descriptor {
				return edge.From("creator", User.Type).Ref("items").Immutable().Descriptor()
			},
			validate: func(t *testing.T, desc *edge.Descriptor) {
				assert.True(t, desc.Immutable)
			},
		},
		{
			name: "inverse_with_field",
			build: func() *edge.Descriptor {
				return edge.From("owner", User.Type).Ref("pets").Field("owner_id").Unique().Descriptor()
			},
			validate: func(t *testing.T, desc *edge.Descriptor) {
				assert.Equal(t, "owner_id", desc.Field)
			},
		},
		{
			name: "inverse_with_through",
			build: func() *edge.Descriptor {
				return edge.From("liked_users", User.Type).
					Ref("liked_posts").
					Through("likes", Friendship.Type).
					Descriptor()
			},
			validate: func(t *testing.T, desc *edge.Descriptor) {
				require.NotNil(t, desc.Through)
				assert.Equal(t, "likes", desc.Through.N)
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

// TestBidirectionalEdges tests edges created using the From chain method.
func TestBidirectionalEdges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		build    func() *edge.Descriptor
		validate func(t *testing.T, desc *edge.Descriptor)
	}{
		{
			name: "m2m_same_type",
			build: func() *edge.Descriptor {
				return edge.To("following", User.Type).
					From("followers").
					Descriptor()
			},
			validate: func(t *testing.T, desc *edge.Descriptor) {
				assert.True(t, desc.Inverse)
				assert.Equal(t, "followers", desc.Name)
				assert.False(t, desc.Unique)
				require.NotNil(t, desc.Ref)
				assert.Equal(t, "following", desc.Ref.Name)
				assert.False(t, desc.Ref.Unique)
			},
		},
		{
			name: "o2m_same_type",
			build: func() *edge.Descriptor {
				return edge.To("following", User.Type).
					Unique().
					From("followers").
					Descriptor()
			},
			validate: func(t *testing.T, desc *edge.Descriptor) {
				assert.False(t, desc.Unique, "inverse side should not be unique")
				assert.True(t, desc.Ref.Unique, "assoc side should be unique")
			},
		},
		{
			name: "m2o_same_type",
			build: func() *edge.Descriptor {
				return edge.To("following", User.Type).
					From("followers").
					Unique().
					Descriptor()
			},
			validate: func(t *testing.T, desc *edge.Descriptor) {
				assert.True(t, desc.Unique, "inverse side should be unique")
				assert.False(t, desc.Ref.Unique, "assoc side should not be unique")
			},
		},
		{
			name: "o2o_same_type",
			build: func() *edge.Descriptor {
				return edge.To("following", User.Type).
					Unique().
					From("followers").
					Unique().
					Descriptor()
			},
			validate: func(t *testing.T, desc *edge.Descriptor) {
				assert.True(t, desc.Unique)
				assert.True(t, desc.Ref.Unique)
			},
		},
		{
			name: "bidi_with_struct_tags",
			build: func() *edge.Descriptor {
				return edge.To("following", User.Type).
					StructTag("following").
					From("followers").
					StructTag("followers").
					Descriptor()
			},
			validate: func(t *testing.T, desc *edge.Descriptor) {
				assert.Equal(t, "followers", desc.Tag)
				assert.Equal(t, "following", desc.Ref.Tag)
			},
		},
		{
			name: "bidi_with_field_binding",
			build: func() *edge.Descriptor {
				return edge.To("children", User.Type).
					From("parent").
					Unique().
					Field("parent_id").
					Comment("parent reference").
					Descriptor()
			},
			validate: func(t *testing.T, desc *edge.Descriptor) {
				assert.Equal(t, "parent_id", desc.Field)
				assert.Equal(t, "parent reference", desc.Comment)
				assert.Empty(t, desc.Ref.Field)
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

// TestStorageKey tests edge storage key configuration.
func TestStorageKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		build    func() *edge.Descriptor
		validate func(t *testing.T, key *edge.StorageKey)
	}{
		{
			name: "table_only",
			build: func() *edge.Descriptor {
				return edge.To("groups", Group.Type).
					StorageKey(edge.Table("user_groups")).
					Descriptor()
			},
			validate: func(t *testing.T, key *edge.StorageKey) {
				assert.Equal(t, "user_groups", key.Table)
				assert.Empty(t, key.Columns)
				assert.Empty(t, key.Symbols)
			},
		},
		{
			name: "table_with_columns",
			build: func() *edge.Descriptor {
				return edge.To("groups", Group.Type).
					StorageKey(
						edge.Table("user_groups"),
						edge.Columns("user_id", "group_id"),
					).
					Descriptor()
			},
			validate: func(t *testing.T, key *edge.StorageKey) {
				assert.Equal(t, "user_groups", key.Table)
				assert.Equal(t, []string{"user_id", "group_id"}, key.Columns)
			},
		},
		{
			name: "single_column",
			build: func() *edge.Descriptor {
				return edge.To("owner", User.Type).
					Unique().
					StorageKey(edge.Column("owner_id")).
					Descriptor()
			},
			validate: func(t *testing.T, key *edge.StorageKey) {
				assert.Equal(t, []string{"owner_id"}, key.Columns)
			},
		},
		{
			name: "single_symbol",
			build: func() *edge.Descriptor {
				return edge.To("owner", User.Type).
					Unique().
					StorageKey(edge.Symbol("fk_post_owner")).
					Descriptor()
			},
			validate: func(t *testing.T, key *edge.StorageKey) {
				assert.Equal(t, []string{"fk_post_owner"}, key.Symbols)
			},
		},
		{
			name: "m2m_symbols",
			build: func() *edge.Descriptor {
				return edge.To("groups", Group.Type).
					StorageKey(
						edge.Table("user_groups"),
						edge.Symbols("fk_user", "fk_group"),
					).
					Descriptor()
			},
			validate: func(t *testing.T, key *edge.StorageKey) {
				assert.Equal(t, []string{"fk_user", "fk_group"}, key.Symbols)
			},
		},
		{
			name: "full_storage_config",
			build: func() *edge.Descriptor {
				return edge.To("groups", Group.Type).
					StorageKey(
						edge.Table("user_groups"),
						edge.Columns("user_id", "group_id"),
						edge.Symbol("fk_users_groups"),
					).
					Descriptor()
			},
			validate: func(t *testing.T, key *edge.StorageKey) {
				assert.Equal(t, "user_groups", key.Table)
				assert.Equal(t, []string{"user_id", "group_id"}, key.Columns)
				assert.Equal(t, []string{"fk_users_groups"}, key.Symbols)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			desc := tt.build()
			require.NotNil(t, desc.StorageKey, "StorageKey should not be nil")
			tt.validate(t, desc.StorageKey)
		})
	}
}

// GQL is a test annotation type.
type GQL struct {
	Field string
}

func (GQL) Name() string { return "GQL" }

// TestAnnotations tests edge annotations.
func TestAnnotations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		build    func() *edge.Descriptor
		validate func(t *testing.T, desc *edge.Descriptor)
	}{
		{
			name: "single_annotation_to",
			build: func() *edge.Descriptor {
				return edge.To("user", User.Type).
					Annotations(GQL{Field: "user_edge"}).
					Descriptor()
			},
			validate: func(t *testing.T, desc *edge.Descriptor) {
				require.Len(t, desc.Annotations, 1)
				assert.Equal(t, GQL{Field: "user_edge"}, desc.Annotations[0])
			},
		},
		{
			name: "single_annotation_from",
			build: func() *edge.Descriptor {
				return edge.From("user", User.Type).
					Annotations(GQL{Field: "from_edge"}).
					Descriptor()
			},
			validate: func(t *testing.T, desc *edge.Descriptor) {
				require.Len(t, desc.Annotations, 1)
				assert.Equal(t, GQL{Field: "from_edge"}, desc.Annotations[0])
			},
		},
		{
			name: "multiple_annotations",
			build: func() *edge.Descriptor {
				return edge.To("posts", Post.Type).
					Annotations(
						GQL{Field: "first"},
						GQL{Field: "second"},
					).
					Descriptor()
			},
			validate: func(t *testing.T, desc *edge.Descriptor) {
				require.Len(t, desc.Annotations, 2)
				assert.Equal(t, "first", desc.Annotations[0].(GQL).Field)
				assert.Equal(t, "second", desc.Annotations[1].(GQL).Field)
			},
		},
		{
			name: "bidi_annotations",
			build: func() *edge.Descriptor {
				return edge.To("following", User.Type).
					Annotations(GQL{Field: "to_annotation"}).
					From("followers").
					Annotations(GQL{Field: "from_annotation"}).
					Descriptor()
			},
			validate: func(t *testing.T, desc *edge.Descriptor) {
				require.Len(t, desc.Annotations, 1)
				assert.Equal(t, GQL{Field: "from_annotation"}, desc.Annotations[0])
				require.Len(t, desc.Ref.Annotations, 1)
				assert.Equal(t, GQL{Field: "to_annotation"}, desc.Ref.Annotations[0])
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

// TestEdgeRelationshipTypes tests different relationship patterns.
func TestEdgeRelationshipTypes(t *testing.T) {
	t.Parallel()

	t.Run("O2O", func(t *testing.T) {
		t.Parallel()
		// One-to-one: both sides unique
		desc := edge.To("spouse", User.Type).
			Unique().
			From("spouse").
			Unique().
			Descriptor()
		assert.True(t, desc.Unique)
		assert.True(t, desc.Ref.Unique)
	})

	t.Run("O2M", func(t *testing.T) {
		t.Parallel()
		// One-to-many: assoc side unique, inverse side not unique
		desc := edge.To("pets", Post.Type).
			From("owner").
			Unique().
			Descriptor()
		assert.True(t, desc.Unique, "owner (inverse) should be unique")
		assert.False(t, desc.Ref.Unique, "pets (assoc) should not be unique")
	})

	t.Run("M2O", func(t *testing.T) {
		t.Parallel()
		// Many-to-one: assoc side unique, inverse not unique
		desc := edge.To("owner", User.Type).
			Unique().
			From("pets").
			Descriptor()
		assert.False(t, desc.Unique, "pets (inverse) should not be unique")
		assert.True(t, desc.Ref.Unique, "owner (assoc) should be unique")
	})

	t.Run("M2M", func(t *testing.T) {
		t.Parallel()
		// Many-to-many: neither side unique
		desc := edge.To("friends", User.Type).
			From("friends").
			Descriptor()
		assert.False(t, desc.Unique)
		assert.False(t, desc.Ref.Unique)
	})
}

// BenchmarkEdgeBuilder benchmarks edge builder performance.
func BenchmarkEdgeBuilder(b *testing.B) {
	b.Run("simple_edge", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			edge.To("posts", Post.Type).Descriptor()
		}
	})

	b.Run("complex_edge", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			edge.To("following", User.Type).
				Unique().
				Required().
				Immutable().
				Comment("following users").
				StructTag(`json:"following"`).
				StorageKey(
					edge.Table("user_followers"),
					edge.Columns("user_id", "follower_id"),
				).
				From("followers").
				Unique().
				StructTag(`json:"followers"`).
				Descriptor()
		}
	})

	b.Run("with_annotations", func(b *testing.B) {
		annotations := []schema.Annotation{
			GQL{Field: "test1"},
			GQL{Field: "test2"},
		}
		for i := 0; i < b.N; i++ {
			edge.To("posts", Post.Type).
				Annotations(annotations...).
				Descriptor()
		}
	})
}
