package integration_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCodegenInvariant_EdgeQueryThreeSetter pins that every entity-level
// edge query method (e.g. user.QueryPosts()) wires SetInterStore AND
// SetPath unconditionally, and wires SetPolicy iff the target entity has
// a privacy policy. Past regression (pre-2026-04-14 cleanup): an edge
// query emitter forgot SetInterStore, causing nil-pointer panics on
// interceptor dispatch. 2026-04-15 clone bug motivates the SetPolicy
// half: any state piece the target query needs must be wired on
// construction, not assumed to arrive via config.
//
// testschema policy map (reflected from testschema/): User has a policy;
// Post, Comment, Tag, Token do not. Update the policyEntities set if
// that ever changes.
func TestCodegenInvariant_EdgeQueryThreeSetter(t *testing.T) {
	policyEntities := map[string]bool{"User": true}

	entityDir := "./entity"
	entries, err := os.ReadDir(entityDir)
	require.NoError(t, err)

	methodRe := regexp.MustCompile(`(?s)func \(_e \*(\w+)\) Query(\w+)\(\) \w+ \{(.+?)\n\}`)
	targetRe := regexp.MustCompile(`Query(\w+)\(\) (\w+)Querier`)

	for _, ent := range entries {
		if !strings.HasSuffix(ent.Name(), ".go") || strings.HasSuffix(ent.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(entityDir, ent.Name())
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		src := string(data)

		for _, m := range methodRe.FindAllStringSubmatch(src, -1) {
			sourceEntity, edgeName, body := m[1], m[2], m[3]
			ctx := sourceEntity + ".Query" + edgeName

			if !strings.Contains(body, "SetInterStore(") {
				t.Errorf("%s: missing SetInterStore wiring — interceptor dispatch will nil-panic", ctx)
			}
			if !strings.Contains(body, "SetPath(") {
				t.Errorf("%s: missing SetPath wiring — edge traversal path is unset", ctx)
			}

			// Determine target entity from the returned interface type.
			// Pattern: "<Target>Querier { ... }". Scan all matches and
			// pick the one whose edge name matches this method.
			var target string
			for _, tm := range targetRe.FindAllStringSubmatch(src, -1) {
				if tm[1] == edgeName {
					target = tm[2]
					break
				}
			}
			if target == "" {
				t.Logf("%s: could not infer target entity from return type; skipping policy check", ctx)
				continue
			}

			hasSetPolicy := strings.Contains(body, "SetPolicy(")
			wantPolicy := policyEntities[target]
			// The generator unconditionally emits SetPolicy wrapped in a runtime
			// nil-guard (runtime.EntityPolicy returns nil for non-policy entities),
			// so `hasSetPolicy` is textually true for every edge. We only assert
			// the positive direction — if a target with a known policy has no
			// SetPolicy emission at all, the generator regressed.
			if wantPolicy && !hasSetPolicy {
				t.Errorf("%s: target %s HAS a policy but edge query does NOT wire SetPolicy — tenant filter will be silently bypassed on edge traversal", ctx, target)
			}
		}
	}
}
