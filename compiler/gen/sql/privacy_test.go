package sql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
)

func TestGenPrivacy_BasicEntity(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genPrivacy(helper)
	require.NotNil(t, file)

	code := file.GoString()
	// Re-exported types
	assert.Contains(t, code, "QueryRule")
	assert.Contains(t, code, "MutationRule")
	assert.Contains(t, code, "QueryMutationRule")
	assert.Contains(t, code, "Policy")
	// Decision constants
	assert.Contains(t, code, "Allow")
	assert.Contains(t, code, "Deny")
	assert.Contains(t, code, "Skip")
}

func TestGenPrivacy_EntityRuleTypes(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genPrivacy(helper)
	require.NotNil(t, file)

	code := file.GoString()
	// Per-entity rule types
	assert.Contains(t, code, "UserQueryRuleFunc")
	assert.Contains(t, code, "UserMutationRuleFunc")
	// EvalQuery/EvalMutation methods
	assert.Contains(t, code, "EvalQuery")
	assert.Contains(t, code, "EvalMutation")
}

func TestGenPrivacy_MultipleEntities(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	commentType := createTestType("Comment")
	helper.graph.Nodes = []*gen.Type{userType, postType, commentType}

	file := genPrivacy(helper)
	require.NotNil(t, file)

	code := file.GoString()
	for _, name := range []string{"User", "Post", "Comment"} {
		assert.Contains(t, code, name+"QueryRuleFunc", "missing QueryRuleFunc for %s", name)
		assert.Contains(t, code, name+"MutationRuleFunc", "missing MutationRuleFunc for %s", name)
	}
}

func TestGenPrivacy_FilterTypes(t *testing.T) {
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genPrivacy(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "Filter")
	assert.Contains(t, code, "Filterable")
	assert.Contains(t, code, "FilterFunc")
}

func TestGenPrivacy_HelperFunctions(t *testing.T) {
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genPrivacy(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "Allowf")
	assert.Contains(t, code, "Denyf")
	assert.Contains(t, code, "Skipf")
	assert.Contains(t, code, "AlwaysAllowRule")
	assert.Contains(t, code, "AlwaysDenyRule")
	assert.Contains(t, code, "ContextQueryMutationRule")
	assert.Contains(t, code, "OnMutationOperation")
	assert.Contains(t, code, "DenyMutationOperationRule")
	assert.Contains(t, code, "NewPolicies")
	assert.Contains(t, code, "DecisionContext")
	assert.Contains(t, code, "DecisionFromContext")
}

func TestGenPrivacyEntityTypes_SingleEntity(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("privacy")
	genPrivacyEntityTypes(helper, f, userType, "github.com/test/project/ent")

	code := f.GoString()
	assert.Contains(t, code, "UserQueryRuleFunc")
	assert.Contains(t, code, "UserMutationRuleFunc")
	assert.Contains(t, code, "EvalQuery")
	assert.Contains(t, code, "EvalMutation")
}
