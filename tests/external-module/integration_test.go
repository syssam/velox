package main

import (
	"testing"

	commentclient "example.com/integration-test/velox/client/comment"
	postclient "example.com/integration-test/velox/client/post"
	profileclient "example.com/integration-test/velox/client/profile"
	tagclient "example.com/integration-test/velox/client/tag"
	userclient "example.com/integration-test/velox/client/user"
	"example.com/integration-test/velox/comment"
	"example.com/integration-test/velox/entity"
	"example.com/integration-test/velox/post"
	"example.com/integration-test/velox/profile"
	"example.com/integration-test/velox/tag"
	"example.com/integration-test/velox/user"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syssam/velox/runtime"
)

// TestEntityLayerTypes verifies entity/ has pure data types with edge detection.
func TestEntityLayerTypes(t *testing.T) {
	u := &entity.User{ID: 1, Name: "Alice", Email: "alice@example.com", Active: true}
	assert.Equal(t, 1, u.ID)
	assert.Equal(t, "Alice", u.Name)

	// O2M: not loaded → error
	_, err := u.Edges.PostsOrErr()
	assert.Error(t, err)
	assert.True(t, runtime.IsNotLoaded(err))

	// After marking loaded
	u.Edges.MarkPostsLoaded()
	posts, err := u.Edges.PostsOrErr()
	require.NoError(t, err)
	assert.Empty(t, posts)

	// O2O: not loaded → error
	_, err = u.Edges.ProfileOrErr()
	assert.Error(t, err)

	u.Edges.MarkProfileLoaded()
	prof, err := u.Edges.ProfileOrErr()
	require.NoError(t, err)
	assert.Nil(t, prof) // loaded but nil

	// M2O: Comment edges
	c := &entity.Comment{}
	_, err = c.Edges.AuthorOrErr()
	assert.True(t, runtime.IsNotLoaded(err))
	_, err = c.Edges.PostOrErr()
	assert.True(t, runtime.IsNotLoaded(err))

	// M2M: Post → Tags
	p := &entity.Post{}
	_, err = p.Edges.TagsOrErr()
	assert.True(t, runtime.IsNotLoaded(err))
}

// TestSubPackageQueries verifies entity sub-package query builders.
func TestSubPackageQueries(t *testing.T) {
	cfg := runtime.Config{}

	// User query with typed methods + chaining
	uq := userclient.NewUserClient(cfg).Query()
	require.NotNil(t, uq)
	uq = uq.Limit(10).Offset(0)
	assert.NotNil(t, uq)

	// WithXxx eager loading (O2M, O2O)
	uq = userclient.NewUserClient(cfg).Query().WithPosts().WithComments().WithProfile()
	assert.NotNil(t, uq)

	// Post query with M2O + M2M edges
	pq := postclient.NewPostClient(cfg).Query().WithAuthor().WithComments().WithTags()
	assert.NotNil(t, pq)

	// Tag query with M2M inverse
	tq := tagclient.NewTagClient(cfg).Query().WithPosts()
	assert.NotNil(t, tq)

	// Comment query with M2O edges
	cq := commentclient.NewCommentClient(cfg).Query().WithAuthor().WithPost()
	assert.NotNil(t, cq)

	// Profile query with O2O inverse
	prq := profileclient.NewProfileClient(cfg).Query().WithUser()
	assert.NotNil(t, prq)
}

// TestEdgeConstants verifies edge name constants in sub-packages.
func TestEdgeConstants(t *testing.T) {
	// O2M
	assert.Equal(t, "posts", user.EdgePosts)
	assert.Equal(t, "comments", user.EdgeComments)
	assert.Equal(t, "comments", post.EdgeComments)

	// O2O
	assert.Equal(t, "profile", user.EdgeProfile)

	// M2O
	assert.Equal(t, "author", post.EdgeAuthor)
	assert.Equal(t, "author", comment.EdgeAuthor)
	assert.Equal(t, "post", comment.EdgePost)

	// M2M
	assert.Equal(t, "tags", post.EdgeTags)

	// M2M inverse
	assert.Equal(t, "posts", tag.EdgePosts)

	// O2O inverse
	assert.Equal(t, "user", profile.EdgeUser)
}

// TestCreateBuilders verifies create builders chain correctly.
func TestCreateBuilders(t *testing.T) {
	cfg := runtime.Config{}

	uc := userclient.NewUserClient(cfg).Create().SetName("Test").SetEmail("test@example.com").SetActive(true)
	assert.NotNil(t, uc)

	pc := postclient.NewPostClient(cfg).Create().SetTitle("Test Post")
	assert.NotNil(t, pc)

	tc := tagclient.NewTagClient(cfg).Create().SetName("golang")
	assert.NotNil(t, tc)
}

// TestUpdateViaEntity verifies Update() shorthand on entity types.
func TestUpdateViaEntity(t *testing.T) {
	cfg := runtime.Config{}

	// Entity-level update via client
	uu := userclient.NewUserClient(cfg).UpdateOneID(1)
	require.NotNil(t, uu)

	pu := postclient.NewPostClient(cfg).UpdateOneID(1)
	require.NotNil(t, pu)
}

// TestDeleteBuilders verifies delete builders exist.
func TestDeleteBuilders(t *testing.T) {
	cfg := runtime.Config{}

	ud := userclient.NewUserClient(cfg).Delete()
	assert.NotNil(t, ud)

	pd := postclient.NewPostClient(cfg).Delete()
	assert.NotNil(t, pd)
}

// TestEdgePredicates verifies typed edge predicate functions.
func TestEdgePredicateTypes(t *testing.T) {
	cfg := runtime.Config{}

	// HasXxx and HasXxxWith predicates
	_ = userclient.NewUserClient(cfg).Query().Where(user.HasPosts())
	_ = userclient.NewUserClient(cfg).Query().Where(user.HasComments())
	_ = userclient.NewUserClient(cfg).Query().Where(user.HasProfile())
	_ = postclient.NewPostClient(cfg).Query().Where(post.HasAuthor())
	_ = postclient.NewPostClient(cfg).Query().Where(post.HasTags())
	_ = tagclient.NewTagClient(cfg).Query().Where(tag.HasPosts())
	_ = commentclient.NewCommentClient(cfg).Query().Where(comment.HasAuthor())
	_ = commentclient.NewCommentClient(cfg).Query().Where(comment.HasPost())
}

// TestUnwrap pins the Ent-parity contract: Unwrap() panics on a
// non-transactional entity. Any "silent no-op on non-tx" behavior
// would reintroduce the original bug class where committed *txDriver
// references silently survive post-commit and fail later in unrelated
// code paths.
func TestUnwrap(t *testing.T) {
	u := &entity.User{ID: 1}
	assert.Panics(t, func() { u.Unwrap() })
}
