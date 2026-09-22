package gen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strings"
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

// TestInertFeaturesAreDeprecatedAndAccepted pins the flags that are kept only
// for compatibility. Each is still accepted by name (the CLI's --feature and
// existing gen.Config values must not start failing), and each says so in a
// "Deprecated:" doc comment and in its Description — so the next
// dead-identifier audit reads it as deliberately inert rather than as a
// feature that lost its reader.
//
//   - FeatureValidator: validators are always generated (Ent parity).
func TestInertFeaturesAreDeprecatedAndAccepted(t *testing.T) {
	inert := map[string]Feature{
		"FeatureValidator": FeatureValidator,
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "feature.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	docs := map[string]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok || vs.Doc == nil {
			return true
		}
		for _, name := range vs.Names {
			docs[name.Name] = vs.Doc.Text()
		}
		return true
	})

	for ident, f := range inert {
		t.Run(ident, func(t *testing.T) {
			got, ok := featureByName(f.Name)
			if !ok || got.Name != f.Name {
				t.Errorf("%s (%q) is no longer accepted by name", ident, f.Name)
			}
			if !slices.ContainsFunc(AllFeatures, func(a Feature) bool { return a.Name == f.Name }) {
				t.Errorf("%s dropped from AllFeatures; the CLI would reject --feature %s", ident, f.Name)
			}
			if !strings.Contains(docs[ident], "Deprecated:") {
				t.Errorf("%s doc comment lacks a Deprecated: paragraph:\n%s", ident, docs[ident])
			}
			if !strings.Contains(docs[ident], "no effect") || !strings.Contains(f.Description, "no effect") {
				t.Errorf("%s must say it has no effect in both its doc comment and Description", ident)
			}
		})
	}
}
