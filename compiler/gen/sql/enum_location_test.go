package sql

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/syssam/velox/compiler/gen"
)

// TestEnum_LivesInEntitySubPackage pins the target state of the cycle-break
// refactor: enum type declarations MUST be emitted into the per-entity leaf
// sub-package (e.g., user/) as real types, NOT into the shared entity/ package.
//
// Current (FAILING) state:
//   - entity/ holds the real "type UserStatus string" + const block + methods.
//   - user/ holds only a type alias "type Status = entity.UserStatus" and var aliases.
//
// Target state (Tasks 2-4 will make these pass):
//   - user/ holds the real "type Status string" + const block + methods directly.
//   - entity/ holds NO "type UserStatus string" and NO "UserStatusActive" consts.
func TestEnum_LivesInEntitySubPackage(t *testing.T) {
	t.Parallel()

	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project"

	userType := createTestType("User")
	userType.Fields = append(userType.Fields, createEnumField("status", []string{"active", "inactive"}))
	helper.graph.Nodes = []*gen.Type{userType}

	enumReg := buildEntityPkgEnumRegistry(helper.graph.Nodes)

	// Leaf package output (user/ sub-package).
	leafFile := genPackage(helper, userType, enumReg)
	leafCode := leafFile.GoString()

	// entity/ package output (shared package for this entity).
	entityFile := genEntityPkgFileWithRegistry(helper, userType, helper.graph.Nodes, enumReg)
	entityCode := entityFile.GoString()

	// --- Leaf package MUST have a REAL type declaration, not an alias ---

	// Target: "type Status string" — a real type with its own method set.
	assert.Contains(t, leafCode, "type Status string",
		"leaf package must declare the real enum type, not an alias")

	// Target: real const block with typed constant values, e.g. "StatusActive Status = \"active\""
	assert.Contains(t, leafCode, `StatusActive`,
		"leaf package must have the StatusActive constant")

	// Target: method defined directly on the leaf type, not delegated.
	assert.Contains(t, leafCode, "func (e Status) IsValid()",
		"leaf package must have IsValid() method defined directly on Status")

	// --- Leaf package MUST NOT alias back to entity/ ---

	// Current (wrong) state: "type Status = entity.UserStatus"
	assert.NotContains(t, leafCode, "type Status = entity.",
		"leaf package must not alias the enum type from entity/")

	// Current (wrong) state: "StatusActive = entity.UserStatusActive"
	assert.NotContains(t, leafCode, "StatusActive = entity.",
		"leaf package must not use var aliases pointing at entity/")

	// --- entity/ package MUST NOT contain the enum type ---

	// Current (wrong) state: "type UserStatus string"
	assert.NotContains(t, entityCode, "type UserStatus string",
		"entity/ must not hold the real enum type after cycle-break refactor")

	// Current (wrong) state: const block "UserStatusActive UserStatus = \"active\""
	assert.NotContains(t, entityCode, "UserStatusActive",
		"entity/ must not hold enum constants after cycle-break refactor")
}
