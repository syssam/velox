package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/syssam/velox/dialect/sql"
)

func TestEdgeLoad_Struct(t *testing.T) {
	el := EdgeLoad{
		Name: "posts",
		Opts: []LoadOption{Limit(10)},
	}
	assert.Equal(t, "posts", el.Name)
	assert.Len(t, el.Opts, 1)
}

func TestLoadOption_Where(t *testing.T) {
	cfg := &LoadConfig{}
	opt := Where(func(s *sql.Selector) {})
	opt(cfg)
	assert.Len(t, cfg.Predicates, 1)
	// Apply another
	opt(cfg)
	assert.Len(t, cfg.Predicates, 2)
}

func TestLoadOption_Limit(t *testing.T) {
	cfg := &LoadConfig{}
	Limit(10)(cfg)
	assert.NotNil(t, cfg.Limit)
	assert.Equal(t, 10, *cfg.Limit)
}

func TestLoadOption_Offset(t *testing.T) {
	cfg := &LoadConfig{}
	Offset(5)(cfg)
	assert.NotNil(t, cfg.Offset)
	assert.Equal(t, 5, *cfg.Offset)
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
	assert.Nil(t, cfg.Offset)
	assert.Nil(t, cfg.Orders)
	assert.Nil(t, cfg.Fields)
	assert.Nil(t, cfg.Edges)

	// Applying options to a zero-value struct must not panic.
	assert.NotPanics(t, func() {
		Where(func(s *sql.Selector) {})(&cfg)
		Select("id")(&cfg)
		Limit(5)(&cfg)
		Offset(0)(&cfg)
		OrderBy(func(s *sql.Selector) {})(&cfg)
		WithEdge("posts")(&cfg)
	})

	// After applying, values should be populated.
	assert.Len(t, cfg.Predicates, 1)
	assert.Equal(t, []string{"id"}, cfg.Fields)
	assert.NotNil(t, cfg.Limit)
	assert.NotNil(t, cfg.Offset)
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

// TestLoadConfig_MultipleWherePredicates verifies that calling Where multiple times
// accumulates all predicates rather than overwriting.
func TestLoadConfig_MultipleWherePredicates(t *testing.T) {
	cfg := &LoadConfig{}
	pred1Called := false
	pred2Called := false
	pred1 := func(s *sql.Selector) { pred1Called = true }
	pred2 := func(s *sql.Selector) { pred2Called = true }

	Where(pred1)(cfg)
	Where(pred2)(cfg)
	assert.Len(t, cfg.Predicates, 2)

	// Execute all predicates to confirm both are present.
	for _, p := range cfg.Predicates {
		p(nil)
	}
	assert.True(t, pred1Called, "first predicate should have been called")
	assert.True(t, pred2Called, "second predicate should have been called")
}
