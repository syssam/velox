package gen

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFeatureStages(t *testing.T) {
	// Promoted features should be Alpha.
	assert.Equal(t, Alpha, FeatureUpsert.Stage, "FeatureUpsert should be Alpha")
	assert.Equal(t, Alpha, FeatureNamedEdges.Stage, "FeatureNamedEdges should be Alpha")
	assert.Equal(t, Alpha, FeatureLock.Stage, "FeatureLock should be Alpha")
	assert.Equal(t, Alpha, FeatureModifier.Stage, "FeatureModifier should be Alpha")

	// Existing stable features remain stable.
	assert.Equal(t, Stable, FeatureSchemaConfig.Stage)
	assert.Equal(t, Stable, FeatureValidator.Stage)
	assert.Equal(t, Stable, FeatureEntPredicates.Stage)
	assert.Equal(t, Stable, FeatureWhereInputAll.Stage)
}

func TestAllFeatures_NoDuplicateNames(t *testing.T) {
	seen := make(map[string]bool, len(AllFeatures))
	for _, f := range AllFeatures {
		assert.False(t, seen[f.Name], "duplicate feature name: %s", f.Name)
		seen[f.Name] = true
	}
}

func TestFeatureByName(t *testing.T) {
	f, ok := featureByName("privacy")
	assert.True(t, ok)
	assert.Equal(t, "privacy", f.Name)

	_, ok = featureByName("nonexistent")
	assert.False(t, ok)
}
