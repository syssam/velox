package sql

import (
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

// =============================================================================
// genMutation — Real-World Scenarios
// =============================================================================

// TestGenMutation_InterfaceAssertion verifies the generated code includes
// a compile-time interface check: var _ velox.Mutation = (*UserMutation)(nil).
func TestGenMutation_InterfaceAssertion(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genMutation(helper, userType)
	code := file.GoString()

	assert.Contains(t, code, "velox.Mutation")
	assert.Contains(t, code, "(*UserMutation)(nil)")
}

// TestGenMutation_Constructor verifies the newXxxMutation constructor
// with functional option pattern.
func TestGenMutation_Constructor(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genMutation(helper, userType)
	code := file.GoString()

	// Constructor function — exported so root package builders can call it
	assert.Contains(t, code, "func NewUserMutation(")
	assert.Contains(t, code, "runtime.Op(op)")
	assert.NotContains(t, code, "runtime.NewMutationBase")

	// Functional option type (uses MutationOptionName — "userOption" for User)
	assert.Contains(t, code, "type userOption func(*UserMutation)")
}

// TestGenMutation_FieldSetGetReset verifies Set/Get/Reset are generated
// for every field.
func TestGenMutation_FieldSetGetReset(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
		createTestField("age", field.TypeInt),
		createTestField("active", field.TypeBool),
	})
	helper.graph.Nodes = []*gen.Type{userType}

	file := genMutation(helper, userType)
	code := file.GoString()

	for _, f := range []string{"Name", "Age", "Active"} {
		assert.Contains(t, code, "Set"+f, "missing Set%s", f)
		assert.Contains(t, code, "Reset"+f, "missing Reset%s", f)
	}

	// Getter methods (non-conflicting names)
	assert.Contains(t, code, "func (m *UserMutation) Name()")
	assert.Contains(t, code, "func (m *UserMutation) Age()")
	assert.Contains(t, code, "func (m *UserMutation) Active()")
}

// TestGenMutation_TypeOpConflictResolution verifies that fields named "type"
// or "op" use GetXxx instead of Xxx to avoid shadowing the generated
// Type()/Op() mutation methods.
func TestGenMutation_TypeOpConflictResolution(t *testing.T) {
	helper := newMockHelper()
	eventType := createTestTypeWithFields("Event", []*gen.Field{
		createTestField("type", field.TypeString),
		createTestField("op", field.TypeString),
		createTestField("name", field.TypeString),
	})
	helper.graph.Nodes = []*gen.Type{eventType}

	file := genMutation(helper, eventType)
	code := file.GoString()

	// "type" and "op" fields should use GetXxx to avoid conflicting with the
	// generated Type()/Op() interface methods.
	assert.Contains(t, code, "GetType")
	assert.Contains(t, code, "GetOp")

	// Regular field uses plain getter
	assert.Contains(t, code, "func (m *EventMutation) Name()")
}

// TestGenMutation_NumericFieldAdd verifies AddXxx and AddedXxx for numeric fields.
func TestGenMutation_NumericFieldAdd(t *testing.T) {
	helper := newMockHelper()
	accountType := createTestTypeWithFields("Account", []*gen.Field{
		createTestField("balance", field.TypeInt64),
		createTestField("login_count", field.TypeInt),
		createTestField("name", field.TypeString),
		createTestField("active", field.TypeBool),
	})
	helper.graph.Nodes = []*gen.Type{accountType}

	file := genMutation(helper, accountType)
	code := file.GoString()

	// Numeric fields get Add/Added
	assert.Contains(t, code, "AddBalance")
	assert.Contains(t, code, "AddedBalance")
	assert.Contains(t, code, "AddLoginCount")
	assert.Contains(t, code, "AddedLoginCount")

	// Non-numeric fields do NOT get Add/Added
	assert.NotContains(t, code, "AddName")
	assert.NotContains(t, code, "AddActive")
}

// TestGenMutation_NillableFieldClear verifies ClearXxx for nillable fields.
func TestGenMutation_NillableFieldClear(t *testing.T) {
	helper := newMockHelper()
	profileType := createTestTypeWithFields("Profile", []*gen.Field{
		createTestField("email", field.TypeString),
		createNillableField("phone", field.TypeString),
		createNillableField("avatar", field.TypeString),
	})
	helper.graph.Nodes = []*gen.Type{profileType}

	file := genMutation(helper, profileType)
	code := file.GoString()

	// Nillable fields get Clear/Cleared
	assert.Contains(t, code, "ClearPhone")
	assert.Contains(t, code, "PhoneCleared")
	assert.Contains(t, code, "ClearAvatar")
	assert.Contains(t, code, "AvatarCleared")

	// Non-nillable required field does NOT get Clear
	assert.NotContains(t, code, "ClearEmail")
}

// TestGenMutation_OptionalFieldClear verifies ClearXxx is NOT generated for
// optional-only (non-nillable) fields — NOT NULL columns cannot be set to NULL.
func TestGenMutation_OptionalFieldClear(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		createOptionalField("bio", field.TypeString),
	})
	helper.graph.Nodes = []*gen.Type{userType}

	file := genMutation(helper, userType)
	code := file.GoString()

	assert.NotContains(t, code, "ClearBio", "Optional non-nillable field must not have ClearXxx")
	assert.NotContains(t, code, "BioCleared", "Optional non-nillable field must not have XxxCleared")
}

// TestGenMutation_UniqueEdgeMethods verifies unique (O2O/M2O) edges get
// Set/Clear/Cleared instead of Add/Remove.
func TestGenMutation_UniqueEdgeMethods(t *testing.T) {
	helper := newMockHelper()
	postType := createTestType("Post")
	userType := createTestType("User")

	postType.Edges = []*gen.Edge{
		createM2OEdge("author", userType, "posts", "user_id"),
	}
	helper.graph.Nodes = []*gen.Type{postType, userType}

	file := genMutation(helper, postType)
	code := file.GoString()

	// Unique edge: Set/Clear/Cleared (not Add/Remove)
	assert.Contains(t, code, "SetAuthorID")
	assert.Contains(t, code, "ClearAuthor")
	assert.Contains(t, code, "AuthorCleared")
	assert.NotContains(t, code, "AddAuthorIDs")
	assert.NotContains(t, code, "RemoveAuthorIDs")
}

// TestGenMutation_NonUniqueEdgeMethods verifies non-unique (O2M/M2M) edges
// get Add/Remove/Clear/Reset.
func TestGenMutation_NonUniqueEdgeMethods(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	groupType := createTestType("Group")

	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_id"),
		createM2MEdge("groups", groupType, "user_groups", []string{"user_id", "group_id"}),
	}
	helper.graph.Nodes = []*gen.Type{userType, postType, groupType}

	file := genMutation(helper, userType)
	code := file.GoString()

	// O2M: Add/Remove/Clear/Reset
	assert.Contains(t, code, "AddPostIDs")
	assert.Contains(t, code, "RemovePostIDs")
	assert.Contains(t, code, "ClearPosts")
	assert.Contains(t, code, "ResetPosts")

	// M2M: Add/Remove/Clear/Reset
	assert.Contains(t, code, "AddGroupIDs")
	assert.Contains(t, code, "RemoveGroupIDs")
	assert.Contains(t, code, "ClearGroups")
	assert.Contains(t, code, "ResetGroups")
}

// TestGenMutation_FieldAccessors verifies the generic field accessors
// needed by the velox.Mutation interface (for hooks/interceptors).
func TestGenMutation_FieldAccessors(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genMutation(helper, userType)
	code := file.GoString()

	// Generic field accessors
	accessors := []string{
		"func (m *UserMutation) Fields()",
		"func (m *UserMutation) Field(",
		"func (m *UserMutation) SetField(",
		"func (m *UserMutation) AddedFields()",
		"func (m *UserMutation) AddedField(",
		"func (m *UserMutation) AddField(",
		"func (m *UserMutation) ClearedFields()",
		"func (m *UserMutation) FieldCleared(",
		"func (m *UserMutation) ClearField(",
		"func (m *UserMutation) ResetField(",
	}
	for _, a := range accessors {
		assert.Contains(t, code, a, "missing field accessor: %s", a)
	}
}

// TestGenMutation_EdgeAccessors verifies generic edge accessors.
func TestGenMutation_EdgeAccessors(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genMutation(helper, userType)
	code := file.GoString()

	edgeAccessors := []string{
		"func (m *UserMutation) AddedEdges()",
		"func (m *UserMutation) AddedIDs(",
		"func (m *UserMutation) RemovedEdges()",
		"func (m *UserMutation) RemovedIDs(",
		"func (m *UserMutation) ClearedEdges()",
		"func (m *UserMutation) EdgeCleared(",
		"func (m *UserMutation) ClearEdge(",
		"func (m *UserMutation) ResetEdge(",
	}
	for _, a := range edgeAccessors {
		assert.Contains(t, code, a, "missing edge accessor: %s", a)
	}
}

// TestGenMutation_OldFieldDispatcher verifies OldField dispatches by name.
func TestGenMutation_OldFieldDispatcher(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
	})
	helper.graph.Nodes = []*gen.Type{userType}

	file := genMutation(helper, userType)
	code := file.GoString()

	assert.Contains(t, code, "func (m *UserMutation) OldField(")
	assert.Contains(t, code, "switch name {")
	assert.NotContains(t, code, "MutationBase")
}

// TestGenMutation_OldXxxMethods verifies per-field OldXxx methods are generated.
func TestGenMutation_OldXxxMethods(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
		createTestField("age", field.TypeInt),
	})
	helper.graph.Nodes = []*gen.Type{userType}

	file := genMutation(helper, userType)
	code := file.GoString()

	assert.Contains(t, code, "func (m *UserMutation) OldName(ctx context.Context)")
	assert.Contains(t, code, "func (m *UserMutation) OldAge(ctx context.Context)")
	assert.Contains(t, code, "m.loadOld(ctx)")
}

// TestGenMutation_WherePredicates verifies Where appends predicates.
func TestGenMutation_WherePredicates(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genMutation(helper, userType)
	code := file.GoString()

	assert.Contains(t, code, "func (m *UserMutation) Where(")
	assert.Contains(t, code, "m.predicates")
}

// TestGenMutation_ValidGoComplexEntity verifies generated mutation code
// is valid Go for a realistic schema.
func TestGenMutation_ValidGoComplexEntity(t *testing.T) {
	helper := newMockHelper()
	invoiceType := createTestTypeWithFields("Invoice", []*gen.Field{
		createTestField("number", field.TypeString),
		createTestField("total_cents", field.TypeInt64),
		createOptionalField("notes", field.TypeString),
		createNillableField("due_date", field.TypeString),
		createEnumField("status", []string{"draft", "sent", "paid", "overdue"}),
	})
	vendorType := createTestType("Vendor")
	lineItemType := createTestType("LineItem")

	invoiceType.Edges = []*gen.Edge{
		createM2OEdge("vendor", vendorType, "invoices", "vendor_id"),
		createO2MEdge("line_items", lineItemType, "line_items", "invoice_id"),
	}
	helper.graph.Nodes = []*gen.Type{invoiceType, vendorType, lineItemType}

	file := genMutation(helper, invoiceType)
	code := file.GoString()

	fset := token.NewFileSet()
	_, err := parser.ParseFile(fset, "invoice_mutation.go", code, parser.AllErrors)
	assert.NoError(t, err, "invoice mutation code should be valid Go")
}

// TestGenMutation_TypedFieldStorage verifies setters and resets read and
// write the typed pointer fields directly (no MutationBase map storage).
func TestGenMutation_TypedFieldStorage(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
	})
	helper.graph.Nodes = []*gen.Type{userType}

	file := genMutation(helper, userType)
	code := file.GoString()

	// Setters write only the typed pointer field — no dual-write.
	assert.Contains(t, code, `m._name = &v`)
	assert.NotContains(t, code, `MutationBase`)
	// Getters read from typed field.
	assert.Contains(t, code, `m._name == nil`)
	assert.Contains(t, code, `*m._name`)
}

// TestGenMutation_EdgeResetClearsAllState verifies ResetEdge clears
// added, removed, and cleared edge state.
func TestGenMutation_EdgeResetClearsAllState(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")

	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_id"),
	}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	file := genMutation(helper, userType)
	code := file.GoString()

	// ResetPosts should clear all edge state maps
	assert.Contains(t, code, "ResetPosts")
	assert.Contains(t, code, "AddedEdges")
	assert.Contains(t, code, "RemovedEdges")
	assert.Contains(t, code, "ClearedEdges")
}

// TestGenMutation_NoFieldsEntity verifies mutation works for entity
// with only an ID (no extra fields).
func TestGenMutation_NoFieldsEntity(t *testing.T) {
	helper := newMockHelper()
	pivotType := createTestTypeWithFields("Pivot", nil)
	helper.graph.Nodes = []*gen.Type{pivotType}

	file := genMutation(helper, pivotType)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "PivotMutation")
	// Still has generic accessors
	assert.Contains(t, code, "func (m *PivotMutation) Fields()")
}
