package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/syssam/velox/dialect/sql"
)

func TestLoadOption_Limit(t *testing.T) {
	cfg := &LoadConfig{}
	Limit(10)(cfg)
	assert.NotNil(t, cfg.Limit)
	assert.Equal(t, 10, *cfg.Limit)
}

func TestLoadOption_Select(t *testing.T) {
	cfg := &LoadConfig{}
	Select("name", "email")(cfg)
	assert.Equal(t, []string{"name", "email"}, cfg.Fields)
	// Append more
	Select("age")(cfg)
	assert.Equal(t, []string{"name", "email", "age"}, cfg.Fields)
}

func TestLoadOption_OrderBy(t *testing.T) {
	cfg := &LoadConfig{}
	OrderBy(func(s *sql.Selector) {})(cfg)
	assert.Len(t, cfg.Orders, 1)
}

func TestLoadOption_WithEdge(t *testing.T) {
	cfg := &LoadConfig{}
	assert.Nil(t, cfg.Edges)
	WithEdge("comments", Limit(5))(cfg)
	assert.NotNil(t, cfg.Edges)
	assert.Len(t, cfg.Edges["comments"], 1)
	// Add another nested edge
	WithEdge("author")(cfg)
	assert.Len(t, cfg.Edges, 2)
	assert.Len(t, cfg.Edges["author"], 0)
}

// TestLoadConfig_NestedEdges verifies that WithEdge can be nested multiple levels deep,
// e.g. WithEdge("posts", WithEdge("author", WithEdge("profile"))).
func TestLoadConfig_NestedEdges(t *testing.T) {
	cfg := &LoadConfig{}
	// Three levels deep: posts -> author -> profile
	WithEdge("posts", WithEdge("author", WithEdge("profile")))(cfg)

	// Top level should have "posts"
	assert.NotNil(t, cfg.Edges)
	postsOpts, ok := cfg.Edges["posts"]
	assert.True(t, ok, "expected 'posts' edge to be registered")
	assert.Len(t, postsOpts, 1, "expected one option (WithEdge author) for posts")

	// Apply the posts options to a nested config to verify the second level
	authorCfg := &LoadConfig{}
	for _, opt := range postsOpts {
		opt(authorCfg)
	}
	assert.NotNil(t, authorCfg.Edges)
	authorOpts, ok := authorCfg.Edges["author"]
	assert.True(t, ok, "expected 'author' edge to be registered inside posts")
	assert.Len(t, authorOpts, 1, "expected one option (WithEdge profile) for author")

	// Apply the author options to a nested config to verify the third level
	profileCfg := &LoadConfig{}
	for _, opt := range authorOpts {
		opt(profileCfg)
	}
	assert.NotNil(t, profileCfg.Edges)
	_, ok = profileCfg.Edges["profile"]
	assert.True(t, ok, "expected 'profile' edge to be registered inside author")
}

// TestLoadConfig_ZeroValue verifies that a zero-value LoadConfig can be used
// without panics — applying options to it must not crash.
func TestLoadConfig_ZeroValue(t *testing.T) {
	var cfg LoadConfig
	// All fields should be nil/zero — accessing them must not panic.
	assert.Nil(t, cfg.Predicates)
	assert.Nil(t, cfg.Limit)
	assert.Nil(t, cfg.Orders)
	assert.Nil(t, cfg.Fields)
	assert.Nil(t, cfg.Edges)

	// Applying options to a zero-value struct must not panic.
	assert.NotPanics(t, func() {
		Select("id")(&cfg)
		Limit(5)(&cfg)
		OrderBy(func(s *sql.Selector) {})(&cfg)
		WithEdge("posts")(&cfg)
	})

	// After applying, values should be populated.
	assert.Equal(t, []string{"id"}, cfg.Fields)
	assert.NotNil(t, cfg.Limit)
	assert.Len(t, cfg.Orders, 1)
	assert.Contains(t, cfg.Edges, "posts")
}

// TestLoadConfig_MultipleSelectCalls verifies that calling Select multiple times
// accumulates all specified fields rather than overwriting.
func TestLoadConfig_MultipleSelectCalls(t *testing.T) {
	cfg := &LoadConfig{}
	Select("name")(cfg)
	Select("email")(cfg)
	assert.Equal(t, []string{"name", "email"}, cfg.Fields)
}
