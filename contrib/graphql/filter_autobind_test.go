package graphql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAutobindUsesFilterPackage asserts that InjectVeloxBindings adds
// "<ormPackage>/filter" (not gqlfilter) to the autobind list.
func TestAutobindUsesFilterPackage(t *testing.T) {
	cfg := &GQLGenConfig{
		Models:   make(map[string]TypeMapEntry),
		Autobind: []string{},
	}
	cfg.InjectVeloxBindings("example.com/app/velox", "")

	require.NotEmpty(t, cfg.Autobind,
		"InjectVeloxBindings should add at least one autobind entry")

	var found bool
	for _, pkg := range cfg.Autobind {
		if pkg == "example.com/app/velox/filter" {
			found = true
		}
		assert.NotEqual(t, "example.com/app/velox/gqlfilter", pkg,
			"legacy gqlfilter path must not appear in autobind")
	}
	assert.True(t, found, "filter/ autobind path missing; got: %v", cfg.Autobind)
}
