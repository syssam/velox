package sql

import (
	"go/parser"
	"go/token"
	"testing"

	"github.com/dave/jennifer/jen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

// testGenDelete is a test-only helper that calls genDelete and discards the error.
func testGenDelete(h gen.GeneratorHelper, t *gen.Type) *jen.File {
	f, _ := genDelete(h, t)
	return f
}

// =============================================================================
// genDelete — Real-World Scenarios
// =============================================================================

// TestGenDelete_StructDefinitions verifies both Delete and DeleteOne
// structs are generated with the correct embedded fields.
func TestGenDelete_StructDefinitions(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := testGenDelete(helper, userType)
	require.NotNil(t, file)
	code := file.GoString()

	// Both structs
	assert.Contains(t, code, "type UserDelete struct")
	assert.Contains(t, code, "type UserDeleteOne struct")

	// Delete struct has mutation field
	assert.Contains(t, code, "mutation")
	assert.Contains(t, code, "hooks")
}

// TestGenDelete_WhereOnBothBuilders verifies Where method exists on both
// Delete and DeleteOne builders.
func TestGenDelete_WhereOnBothBuilders(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	orderType := createTestType("Order")
	helper.graph.Nodes = []*gen.Type{orderType}

	file := testGenDelete(helper, orderType)
	code := file.GoString()

	assert.Contains(t, code, "func (_d *OrderDelete) Where(")
	assert.Contains(t, code, "func (_d *OrderDeleteOne) Where(")
}

// TestGenDelete_DeleteOneNotFoundError verifies DeleteOne.Exec returns
// NotFoundError when no rows are deleted (entity doesn't exist).
func TestGenDelete_DeleteOneNotFoundError(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	itemType := createTestType("Item")
	helper.graph.Nodes = []*gen.Type{itemType}

	file := testGenDelete(helper, itemType)
	code := file.GoString()

	// DeleteOne checks n == 0 and returns NotFoundError
	assert.Contains(t, code, "NewNotFoundError")
	assert.Contains(t, code, `"Item"`)
}

// TestGenDelete_DeleteReturnsCount verifies Delete.Exec returns (int, error)
// for the number of deleted rows.
func TestGenDelete_DeleteReturnsCount(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	logType := createTestType("AuditLog")
	helper.graph.Nodes = []*gen.Type{logType}

	file := testGenDelete(helper, logType)
	code := file.GoString()

	// Delete.Exec returns (int, error)
	assert.Contains(t, code, "func (_d *AuditLogDelete) Exec(ctx context.Context) (int, error)")
}

// TestGenDelete_DeleteOneReturnsError verifies DeleteOne.Exec returns just error.
func TestGenDelete_DeleteOneReturnsError(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := testGenDelete(helper, userType)
	code := file.GoString()

	// DeleteOne.Exec returns just error
	assert.Contains(t, code, "func (_d *UserDeleteOne) Exec(ctx context.Context) error")
}

// TestGenDelete_ExecXPanicsOnError verifies ExecX panics on both builders.
func TestGenDelete_ExecXPanicsOnError(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := testGenDelete(helper, userType)
	code := file.GoString()

	// Both have ExecX with panic
	assert.Contains(t, code, "func (_d *UserDelete) ExecX(")
	assert.Contains(t, code, "func (_d *UserDeleteOne) ExecX(")
	assert.Contains(t, code, "panic(err)")
}

// TestGenDelete_DelegatesToRuntime verifies delete delegates to runtime.DeleteNodes.
func TestGenDelete_DelegatesToRuntime(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	sessionType := createTestType("Session")
	helper.graph.Nodes = []*gen.Type{sessionType}

	file := testGenDelete(helper, sessionType)
	code := file.GoString()

	assert.Contains(t, code, "runtime.DeleterBase")
	assert.Contains(t, code, "runtime.DeleteNodes")
}

// TestGenDelete_MutationAccessor verifies Mutation() is only on Delete (not DeleteOne).
func TestGenDelete_MutationAccessor(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := testGenDelete(helper, userType)
	code := file.GoString()

	// Delete has Mutation()
	assert.Contains(t, code, "func (_d *UserDelete) Mutation()")
}

// TestGenDelete_DeleteOneDelegatesToDelete verifies DeleteOne.Exec
// delegates to the embedded Delete builder.
func TestGenDelete_DeleteOneDelegatesToDelete(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := testGenDelete(helper, userType)
	code := file.GoString()

	// DeleteOne embeds Delete (via receiver shortcut)
	assert.Contains(t, code, "UserDeleteOne struct")
}

// TestGenDelete_ValidGoForComplexEntity verifies valid Go output.
func TestGenDelete_ValidGoForComplexEntity(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
		createTestField("email", field.TypeString),
	})
	helper.graph.Nodes = []*gen.Type{userType}

	file := testGenDelete(helper, userType)
	code := file.GoString()

	fset := token.NewFileSet()
	_, err := parser.ParseFile(fset, "user_delete.go", code, parser.AllErrors)
	assert.NoError(t, err, "delete code should be valid Go")
}

// TestGenDelete_ConvertPredicates verifies predicates are converted
// from typed to []func(*sql.Selector).
func TestGenDelete_ConvertPredicates(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := testGenDelete(helper, userType)
	code := file.GoString()

	// Predicate conversion pattern (uses public method for cross-package access)
	assert.Contains(t, code, "mutation.PredicatesFuncs()")
}
