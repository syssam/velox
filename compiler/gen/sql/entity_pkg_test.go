package sql

import (
	"strings"
	"testing"

	"github.com/dave/jennifer/jen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

// =============================================================================
// genEntityPkgSelectorInterface Tests
// =============================================================================

// TestGenEntityPkgSelectorInterface verifies the Selector interface is generated
// with Aggregate (returning self), Scan, ScanX, and all scalar accessor methods.
func TestGenEntityPkgSelectorInterface(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project"

	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := genEntityPkgFileWithRegistry(helper, userType, helper.graph.Nodes, nil)
	require.NotNil(t, f)

	code := f.GoString()

	// Interface exists
	assert.Contains(t, code, "type UserSelector interface")

	// Aggregate returns self interface
	assert.Contains(t, code, "Aggregate(fns ...runtime.AggregateFunc) UserSelector")

	// Scan / ScanX
	assert.Contains(t, code, "Scan(ctx context.Context, v any) error")
	assert.Contains(t, code, "ScanX(ctx context.Context, v any)")

	// String accessors
	assert.Contains(t, code, "Strings(ctx context.Context) ([]string, error)")
	assert.Contains(t, code, "StringsX(ctx context.Context) []string")
	assert.Contains(t, code, "String(ctx context.Context) (string, error)")
	assert.Contains(t, code, "StringX(ctx context.Context) string")

	// Int accessors
	assert.Contains(t, code, "Ints(ctx context.Context) ([]int, error)")
	assert.Contains(t, code, "IntsX(ctx context.Context) []int")
	assert.Contains(t, code, "Int(ctx context.Context) (int, error)")
	assert.Contains(t, code, "IntX(ctx context.Context) int")

	// Float64 accessors
	assert.Contains(t, code, "Float64s(ctx context.Context) ([]float64, error)")
	assert.Contains(t, code, "Float64sX(ctx context.Context) []float64")
	assert.Contains(t, code, "Float64(ctx context.Context) (float64, error)")
	assert.Contains(t, code, "Float64X(ctx context.Context) float64")

	// Bool accessors
	assert.Contains(t, code, "Bools(ctx context.Context) ([]bool, error)")
	assert.Contains(t, code, "BoolsX(ctx context.Context) []bool")
	assert.Contains(t, code, "Bool(ctx context.Context) (bool, error)")
	assert.Contains(t, code, "BoolX(ctx context.Context) bool")

	assertValidGo(t, f, "UserSelector")
}

// =============================================================================
// genEntityPkgGroupByerInterface Tests
// =============================================================================

// TestGenEntityPkgGroupByerInterface verifies the GroupByer interface is generated
// with Aggregate (returning self), Scan, ScanX, and all scalar accessor methods.
func TestGenEntityPkgGroupByerInterface(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project"

	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := genEntityPkgFileWithRegistry(helper, userType, helper.graph.Nodes, nil)
	require.NotNil(t, f)

	code := f.GoString()

	// Interface exists
	assert.Contains(t, code, "type UserGroupByer interface")

	// Aggregate returns self interface (GroupByer, not Selector)
	assert.Contains(t, code, "Aggregate(fns ...runtime.AggregateFunc) UserGroupByer")

	// Scan / ScanX
	assert.Contains(t, code, "Scan(ctx context.Context, v any) error")
	assert.Contains(t, code, "ScanX(ctx context.Context, v any)")

	// String accessors
	assert.Contains(t, code, "Strings(ctx context.Context) ([]string, error)")
	assert.Contains(t, code, "StringsX(ctx context.Context) []string")
	assert.Contains(t, code, "String(ctx context.Context) (string, error)")
	assert.Contains(t, code, "StringX(ctx context.Context) string")

	// Int accessors
	assert.Contains(t, code, "Ints(ctx context.Context) ([]int, error)")
	assert.Contains(t, code, "IntsX(ctx context.Context) []int")
	assert.Contains(t, code, "Int(ctx context.Context) (int, error)")
	assert.Contains(t, code, "IntX(ctx context.Context) int")

	// Float64 accessors
	assert.Contains(t, code, "Float64s(ctx context.Context) ([]float64, error)")
	assert.Contains(t, code, "Float64sX(ctx context.Context) []float64")
	assert.Contains(t, code, "Float64(ctx context.Context) (float64, error)")
	assert.Contains(t, code, "Float64X(ctx context.Context) float64")

	// Bool accessors
	assert.Contains(t, code, "Bools(ctx context.Context) ([]bool, error)")
	assert.Contains(t, code, "BoolsX(ctx context.Context) []bool")
	assert.Contains(t, code, "Bool(ctx context.Context) (bool, error)")
	assert.Contains(t, code, "BoolX(ctx context.Context) bool")

	assertValidGo(t, f, "UserGroupByer")
}

// =============================================================================
// genEntityPkgNamedEdgeMethods Tests
// =============================================================================

func TestGenEntityPkgNamedEdgeMethods(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project"

	userType := createTestType("User")
	postType := createTestType("Post")

	postsEdge := createO2MEdge("posts", postType, "posts", "user_id")
	userType.Edges = []*gen.Edge{postsEdge}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("entity")
	genEntityPkgNamedEdgeMethods(f, userType, postsEdge)

	code := f.GoString()
	assert.Contains(t, code, "NamedPosts")
	assert.Contains(t, code, "AppendNamedPosts")
	assert.Contains(t, code, "namedPosts")
}

// =============================================================================
// genEntityPkgEnumType Tests
// =============================================================================

func TestGenEntityPkgEnumType(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project"

	userType := createTestType("User")
	roleField := createEnumField("role", []string{"admin", "user", "moderator"})
	userType.Fields = append(userType.Fields, roleField)
	helper.graph.Nodes = []*gen.Type{userType}

	reg := buildEntityPkgEnumRegistry(helper.graph.Nodes)
	f := helper.NewFile("entity")
	genEntityPkgEnumType(f, userType, roleField, reg)

	code := f.GoString()
	// Enum type definition (with only one entity "User", the enum name is just "Role" not "UserRole")
	assert.Contains(t, code, "type Role string")
	// Enum constants
	assert.Contains(t, code, "RoleAdmin")
	assert.Contains(t, code, "RoleUser")
	assert.Contains(t, code, "RoleModerator")
	// String method
	assert.Contains(t, code, "func (e Role) String()")
	// IsValid method
	assert.Contains(t, code, "func (e Role) IsValid()")
	// Values function
	assert.Contains(t, code, "RoleValues")
	// Scan method
	assert.Contains(t, code, "func (e *Role) Scan(")
	// Value method (driver.Valuer)
	assert.Contains(t, code, "func (e Role) Value()")
	// MarshalGQL
	assert.Contains(t, code, "MarshalGQL")
}

// =============================================================================
// genEntityPkgFKValueMethod Tests
// =============================================================================

func TestGenEntityPkgFKValueMethod_NoFKs(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()

	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("entity")
	genEntityPkgFKValueMethod(f, userType)

	code := f.GoString()
	// No FKs, so no FKValue method should be generated
	assert.NotContains(t, code, "FKValue")
}

// =============================================================================
// genEntityPkgAssignValues Tests
// =============================================================================

// TestGenEntityPkgAssignValues_DefaultCaseStoresSelectValues pins that the
// generated AssignValues switch has a default case that writes unknown columns
// into e.selectValues. Without this, columns selected via Modify() (aggregate
// expressions, aliased computations, ORDER BY expressions) are scanned with
// sql.UnknownType but their values are silently discarded — (*Entity).Value(name)
// then returns "value was not selected" for every caller. Matches Ent's
// (*T).assignValues default-case contract in .references/ent/entc/integration/
// ent/user.go:330-332.
func TestGenEntityPkgAssignValues_DefaultCaseStoresSelectValues(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project"

	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := genEntityPkgFileWithRegistry(helper, userType, helper.graph.Nodes, nil)
	require.NotNil(t, f)
	code := f.GoString()

	start := strings.Index(code, "func (e *User) AssignValues(")
	require.NotEqual(t, -1, start, "AssignValues method must be emitted")
	end := strings.Index(code[start:], "return nil")
	require.NotEqual(t, -1, end, "AssignValues body must end with return nil")
	body := code[start : start+end]

	assert.Contains(t, body, "default:",
		"AssignValues switch must have a default case so unknown columns from Modify()/ORDER BY aren't dropped")
	assert.Contains(t, body, "e.selectValues.Set(columns[i], values[i])",
		"default case must write into selectValues — without it Value(name) always reports 'not selected'")
}

// =============================================================================
// genEntityPkgFieldAssignment Tests
// =============================================================================

func TestGenEntityPkgFieldAssignment_RegularField(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()

	userType := createTestType("User")
	nameField := createTestField("name", field.TypeString)
	helper.graph.Nodes = []*gen.Type{userType}

	reg := buildEntityPkgEnumRegistry(helper.graph.Nodes)
	f := helper.NewFile("entity")

	// Generate a wrapper function to test field assignment
	f.Func().Id("test").Params().Block(
		jen.BlockFunc(func(grp *jen.Group) {
			genEntityPkgFieldAssignment(helper, grp, userType, nameField, "i", "e", "Name", reg)
		}),
	)

	code := f.GoString()
	assert.Contains(t, code, "values")
}

// =============================================================================
// genEntityPkgQuerierInterface Tests
// =============================================================================

// TestGenEntityPkgQuerierInterface verifies the Querier interface includes all
// terminal, chainable, edge, selection, aggregation, scan, and clone methods.
func TestGenEntityPkgQuerierInterface(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project"

	userType := createTestType("User")
	postType := createTestType("Post")

	postsEdge := &gen.Edge{
		Name:   "posts",
		Type:   postType,
		Unique: false,
		Rel:    gen.Relation{Type: gen.O2M},
	}
	userType.Edges = []*gen.Edge{postsEdge}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := genEntityPkgFileWithRegistry(helper, userType, helper.graph.Nodes, nil)
	require.NotNil(t, f)

	code := f.GoString()

	// Interface exists
	assert.Contains(t, code, "type UserQuerier interface")

	// --- Terminal methods (sanity) ---
	assert.Contains(t, code, "All(ctx context.Context) ([]*User, error)")
	assert.Contains(t, code, "First(ctx context.Context) (*User, error)")
	assert.Contains(t, code, "Only(ctx context.Context) (*User, error)")
	assert.Contains(t, code, "Count(ctx context.Context) (int, error)")
	assert.Contains(t, code, "Exist(ctx context.Context) (bool, error)")
	assert.Contains(t, code, "ExistX(context.Context) bool")

	// --- ID methods (sanity) ---
	assert.Contains(t, code, "IDs(ctx context.Context)")

	// --- Chainable methods (sanity) ---
	assert.Contains(t, code, "Where(ps ...predicate.User) UserQuerier")
	assert.Contains(t, code, "Limit(n int) UserQuerier")
	assert.Contains(t, code, "Offset(n int) UserQuerier")
	assert.Contains(t, code, "Clone() UserQuerier")

	// --- Edge eager loading (sanity) ---
	assert.Contains(t, code, "WithPosts(opts ...func(PostQuerier)) UserQuerier")

	// --- New methods: Select / GroupBy / Aggregate / Modify ---
	assert.Contains(t, code, "Select(fields ...string) UserSelector")
	assert.Contains(t, code, "Modify(modifiers ...func(*sql.Selector)) UserQuerier")
	assert.Contains(t, code, "GroupBy(field string, fields ...string) UserGroupByer")
	assert.Contains(t, code, "Aggregate(fns ...runtime.AggregateFunc) UserSelector")

	// --- New methods: Scan ---
	assert.Contains(t, code, "Scan(context.Context, any) error")
	assert.Contains(t, code, "ScanX(context.Context, any)")

	assertValidGo(t, f, "UserQuerier")
}

// TestGenEntityPkgEdgeQueryMethods_SetInterStore verifies that the PRODUCTION
// generator (genEntityPkgEdgeQueryMethods) correctly wires SetInterStore on
// queries created via runtime.NewEntityQuery. Without this, terminal methods
// (All, First, Only, Count) panic with nil pointer dereference and privacy
// policies are bypassed.
func TestGenEntityPkgEdgeQueryMethods_SetInterStore(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project"

	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createO2MEdge("posts", postType, "posts", "user_id")
	userType.Edges = []*gen.Edge{edge}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := genEntityPkgFileWithRegistry(helper, userType, helper.graph.Nodes, nil)
	code := f.GoString()

	// Entity-level QueryPosts must wire SetInterStore
	assert.Contains(t, code, "SetInterStore", "production entity edge query must wire SetInterStore")
}
