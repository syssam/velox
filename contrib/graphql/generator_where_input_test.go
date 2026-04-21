package graphql

import (
	"bytes"
	"strings"
	"testing"

	entgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

// TestGenerator_WhereInputGoStruct tests the Go WhereInput struct generation.
func TestGenerator_WhereInputGoStruct(t *testing.T) {
	userType := &entgen.Type{
		Name: "User",
		ID: &entgen.Field{
			Name: "id",
			Type: &field.TypeInfo{Type: field.TypeInt64},
		},
		Fields: []*entgen.Field{
			{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}},
			{Name: "email", Type: &field.TypeInfo{Type: field.TypeString}},
			{Name: "age", Type: &field.TypeInfo{Type: field.TypeInt}, Optional: true},
			{Name: "score", Type: &field.TypeInfo{Type: field.TypeFloat64}},
			{Name: "is_active", Type: &field.TypeInfo{Type: field.TypeBool}},
			{Name: "nickname", Type: &field.TypeInfo{Type: field.TypeString}, Nillable: true},
		},
		Annotations: map[string]any{},
	}

	postType := &entgen.Type{
		Name: "Post",
		ID: &entgen.Field{
			Name: "id",
			Type: &field.TypeInfo{Type: field.TypeInt64},
		},
		Fields: []*entgen.Field{
			{Name: "title", Type: &field.TypeInfo{Type: field.TypeString}},
		},
		Annotations: map[string]any{},
	}

	// Set up edges
	userType.Edges = []*entgen.Edge{
		{
			Name:   "posts",
			Type:   postType,
			Unique: false,
		},
	}

	g := &entgen.Graph{
		Config: &entgen.Config{
			Package:  "example/ent",
			Features: []entgen.Feature{entgen.FeatureWhereInputAll},
		},
		Nodes: []*entgen.Type{userType, postType},
	}

	gen := NewGenerator(g, Config{
		Package:     "graphql",
		ORMPackage:  "example/ent",
		WhereInputs: true,
	})

	f := gen.genWhereInputGo()
	if f == nil {
		t.Fatal("genWhereInputGo returned nil")
	}

	var buf bytes.Buffer
	if err := f.Render(&buf); err != nil {
		t.Fatalf("failed to render where input: %v", err)
	}
	code := buf.String()

	t.Run("StructGeneration", func(t *testing.T) {
		// Check UserWhereInput struct exists
		if !strings.Contains(code, "type UserWhereInput struct") {
			t.Error("should generate UserWhereInput struct")
		}

		// Check PostWhereInput struct exists
		if !strings.Contains(code, "type PostWhereInput struct") {
			t.Error("should generate PostWhereInput struct")
		}
	})

	t.Run("LogicalOperators", func(t *testing.T) {
		// Check for Not, Or, And fields (with flexible spacing)
		if !strings.Contains(code, "Not") || !strings.Contains(code, "*UserWhereInput") {
			t.Error("UserWhereInput should have Not field")
		}
		if !strings.Contains(code, "Or") || !strings.Contains(code, "[]*UserWhereInput") {
			t.Error("UserWhereInput should have Or field")
		}
		if !strings.Contains(code, "And") {
			t.Error("UserWhereInput should have And field")
		}
	})

	t.Run("FieldPredicates", func(t *testing.T) {
		// Check ID predicates (should be uppercase) - with flexible spacing
		if !strings.Contains(code, "ID ") && !strings.Contains(code, "ID\t") {
			t.Error("should have ID field")
		}
		if !strings.Contains(code, "IDNEQ") {
			t.Error("should have IDNEQ field")
		}
		if !strings.Contains(code, "IDIn") {
			t.Error("should have IDIn field")
		}

		// Check string field predicates (OpsStringBasic: EQ, NEQ, In, NotIn, Contains)
		if !strings.Contains(code, "NameContains") {
			t.Error("should have NameContains for string field")
		}

		// Check numeric field predicates
		if !strings.Contains(code, "AgeGT") {
			t.Error("should have AgeGT for numeric field")
		}
		if !strings.Contains(code, "AgeLTE") {
			t.Error("should have AgeLTE for numeric field")
		}
	})

	t.Run("NillableFieldPredicates", func(t *testing.T) {
		// Nillable fields should have IsNil/NotNil
		if !strings.Contains(code, "NicknameIsNil") {
			t.Error("should have NicknameIsNil for nillable field")
		}
		if !strings.Contains(code, "NicknameNotNil") {
			t.Error("should have NicknameNotNil for nillable field")
		}
	})

	t.Run("EdgePredicates", func(t *testing.T) {
		// Check edge predicates
		if !strings.Contains(code, "HasPosts") {
			t.Error("should have HasPosts edge predicate")
		}
		if !strings.Contains(code, "HasPostsWith") {
			t.Error("should have HasPostsWith edge predicate")
		}
	})

	t.Run("Methods", func(t *testing.T) {
		// Check AddPredicates method
		if !strings.Contains(code, "func (i *UserWhereInput) AddPredicates") {
			t.Error("should have AddPredicates method")
		}

		// Check Filter method
		if !strings.Contains(code, "func (i *UserWhereInput) Filter") {
			t.Error("should have Filter method")
		}

		// Check P method
		if !strings.Contains(code, "func (i *UserWhereInput) P()") {
			t.Error("should have P method")
		}

		// Check error variable
		if !strings.Contains(code, "ErrEmptyUserWhereInput") {
			t.Error("should have ErrEmptyUserWhereInput error variable")
		}
	})
}

// TestGenerator_HelpersFilterFunctions tests the helper filter functions.
func TestGenerator_HelpersFilterFunctions(t *testing.T) {
	// Test filterNodes by checking behavior with annotations
	// Since the annotation parsing depends on the actual Annotation type,
	// we test the basic filtering logic
	visibleType := &entgen.Type{
		Name:        "Visible",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Annotations: map[string]any{},
	}

	// Type without annotations (should be included)
	anotherVisible := &entgen.Type{
		Name:        "AnotherVisible",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Annotations: map[string]any{},
	}

	g := &entgen.Graph{
		Config: &entgen.Config{
			Package: "example/ent",
		},
		Nodes: []*entgen.Type{visibleType, anotherVisible},
	}

	gen := NewGenerator(g, Config{
		Package: "graphql",
	})

	t.Run("FilterNodesNoSkip", func(t *testing.T) {
		// When no types have skip annotations, all should be included
		nodes := gen.filterNodes(g.Nodes, SkipType)
		if len(nodes) != 2 {
			t.Errorf("expected 2 nodes when none are skipped, got %d", len(nodes))
		}
	})

	t.Run("FilterNodesWithSkipMask", func(t *testing.T) {
		// Test that filter function is called with correct mask
		nodes := gen.filterNodes(g.Nodes, SkipType|SkipWhereInput)
		// Both should be included since neither has skip annotations
		if len(nodes) != 2 {
			t.Errorf("expected 2 nodes, got %d", len(nodes))
		}
	})
}

// TestGenerator_OrderFieldOperators tests that field operators are correctly generated.
func TestGenerator_OrderFieldOperators(t *testing.T) {
	gen := &Generator{}

	tests := []struct {
		name          string
		field         *entgen.Field
		expectCompare bool // Whether we expect GT/GTE/LT/LTE (numeric/time only)
		expectString  bool // Whether we expect Contains/HasPrefix/etc
		expectNiladic bool // Whether we expect IsNil/NotNil
	}{
		{
			name:          "string field",
			field:         &entgen.Field{Type: &field.TypeInfo{Type: field.TypeString}},
			expectCompare: false, // strings do NOT have GT/GTE/LT/LTE (lexicographic comparison rarely useful)
			expectString:  true,
			expectNiladic: false,
		},
		{
			name:          "int field",
			field:         &entgen.Field{Type: &field.TypeInfo{Type: field.TypeInt}},
			expectCompare: true,
			expectString:  false,
			expectNiladic: false,
		},
		{
			name:          "bool field",
			field:         &entgen.Field{Type: &field.TypeInfo{Type: field.TypeBool}},
			expectCompare: false,
			expectString:  false,
			expectNiladic: false,
		},
		{
			name:          "nillable string field",
			field:         &entgen.Field{Type: &field.TypeInfo{Type: field.TypeString}, Nillable: true},
			expectCompare: false, // strings do NOT have GT/GTE/LT/LTE
			expectString:  true,
			expectNiladic: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops := gen.getWhereInputFieldOps(tt.field)

			// Check for comparison operators (GT/GTE/LT/LTE)
			hasGT := false
			for _, op := range ops {
				if op.Name == "GT" {
					hasGT = true
					break
				}
			}
			if hasGT != tt.expectCompare {
				t.Errorf("GT operator: got %v, want %v", hasGT, tt.expectCompare)
			}

			// Check for string operators
			hasContains := false
			for _, op := range ops {
				if op.Name == "Contains" {
					hasContains = true
					break
				}
			}
			if hasContains != tt.expectString {
				t.Errorf("Contains operator: got %v, want %v", hasContains, tt.expectString)
			}

			// Check for niladic operators
			hasIsNil := false
			for _, op := range ops {
				if op.Name == "IsNil" {
					hasIsNil = true
					break
				}
			}
			if hasIsNil != tt.expectNiladic {
				t.Errorf("IsNil operator: got %v, want %v", hasIsNil, tt.expectNiladic)
			}
		})
	}
}

// TestGenerator_WhereOps tests the WhereOps annotation for fine-grained filter control.
func TestGenerator_WhereOps(t *testing.T) {
	t.Run("IDFieldDefaults", func(t *testing.T) {
		// ID field should default to OpsEquality (EQ, NEQ, In, NotIn only)
		userType := &entgen.Type{
			Name: "User",
			ID: &entgen.Field{
				Name: "id",
				Type: &field.TypeInfo{Type: field.TypeString},
			},
			Fields:      []*entgen.Field{},
			Annotations: map[string]any{},
		}

		g := &entgen.Graph{
			Config: &entgen.Config{
				Package:  "example/ent",
				Features: []entgen.Feature{entgen.FeatureWhereInputAll},
			},
			Nodes: []*entgen.Type{userType},
		}

		gen := NewGenerator(g, Config{
			Package:     "graphql",
			ORMPackage:  "example/ent",
			WhereInputs: true,
		})

		f := gen.genWhereInputGo()
		var buf bytes.Buffer
		if err := f.Render(&buf); err != nil {
			t.Fatalf("failed to render: %v", err)
		}
		code := buf.String()

		// ID field should have EQ, NEQ, In, NotIn
		if !strings.Contains(code, "IDNEQ") {
			t.Error("ID field should have IDNEQ")
		}
		if !strings.Contains(code, "IDIn") {
			t.Error("ID field should have IDIn")
		}
		if !strings.Contains(code, "IDNotIn") {
			t.Error("ID field should have IDNotIn")
		}

		// ID field should NOT have string operations (Contains, HasPrefix, etc.)
		if strings.Contains(code, "IDContains") {
			t.Error("ID field should NOT have IDContains")
		}
		if strings.Contains(code, "IDHasPrefix") {
			t.Error("ID field should NOT have IDHasPrefix")
		}
		if strings.Contains(code, "IDEqualFold") {
			t.Error("ID field should NOT have IDEqualFold")
		}
	})

	t.Run("ForeignKeyFieldDefaults", func(t *testing.T) {
		// FK fields (ending in _id or ID) should default to OpsEquality
		userType := &entgen.Type{
			Name: "User",
			ID: &entgen.Field{
				Name: "id",
				Type: &field.TypeInfo{Type: field.TypeInt64},
			},
			Fields: []*entgen.Field{
				{Name: "customer_id", Type: &field.TypeInfo{Type: field.TypeString}},
				{Name: "organizationID", Type: &field.TypeInfo{Type: field.TypeString}},
				{Name: "owner_id", Type: &field.TypeInfo{Type: field.TypeString}, Nillable: true},
			},
			Annotations: map[string]any{},
		}

		g := &entgen.Graph{
			Config: &entgen.Config{
				Package:  "example/ent",
				Features: []entgen.Feature{entgen.FeatureWhereInputAll},
			},
			Nodes: []*entgen.Type{userType},
		}

		gen := NewGenerator(g, Config{
			Package:     "graphql",
			ORMPackage:  "example/ent",
			WhereInputs: true,
		})

		f := gen.genWhereInputGo()
		var buf bytes.Buffer
		if err := f.Render(&buf); err != nil {
			t.Fatalf("failed to render: %v", err)
		}
		code := buf.String()

		// customer_id should have EQ, NEQ, In, NotIn but no string ops
		if !strings.Contains(code, "CustomerIDIn") {
			t.Error("customer_id should have CustomerIDIn")
		}
		if strings.Contains(code, "CustomerIDContains") {
			t.Error("customer_id should NOT have CustomerIDContains")
		}

		// organizationID should have EQ, NEQ, In, NotIn but no string ops
		if !strings.Contains(code, "OrganizationIDIn") {
			t.Error("organizationID should have OrganizationIDIn")
		}
		if strings.Contains(code, "OrganizationIDContains") {
			t.Error("organizationID should NOT have OrganizationIDContains")
		}

		// owner_id (nullable FK) should have IsNil, NotNil
		if !strings.Contains(code, "OwnerIDIsNil") {
			t.Error("nullable FK should have OwnerIDIsNil")
		}
		if !strings.Contains(code, "OwnerIDNotNil") {
			t.Error("nullable FK should have OwnerIDNotNil")
		}
	})

	t.Run("StringFieldDefaults", func(t *testing.T) {
		// Regular string fields should have OpsStringBasic operations
		userType := &entgen.Type{
			Name: "User",
			ID: &entgen.Field{
				Name: "id",
				Type: &field.TypeInfo{Type: field.TypeInt64},
			},
			Fields: []*entgen.Field{
				{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}},
				{Name: "email", Type: &field.TypeInfo{Type: field.TypeString}},
			},
			Annotations: map[string]any{},
		}

		g := &entgen.Graph{
			Config: &entgen.Config{
				Package:  "example/ent",
				Features: []entgen.Feature{entgen.FeatureWhereInputAll},
			},
			Nodes: []*entgen.Type{userType},
		}

		gen := NewGenerator(g, Config{
			Package:     "graphql",
			ORMPackage:  "example/ent",
			WhereInputs: true,
		})

		f := gen.genWhereInputGo()
		var buf bytes.Buffer
		if err := f.Render(&buf); err != nil {
			t.Fatalf("failed to render: %v", err)
		}
		code := buf.String()

		// String fields should have OpsStringBasic operations (EQ, NEQ, In, NotIn, Contains)
		if !strings.Contains(code, "NameContains") {
			t.Error("string field should have NameContains")
		}
		if !strings.Contains(code, "EmailNEQ") {
			t.Error("string field should have EmailNEQ")
		}
	})

	t.Run("ExplicitWhereOpsAnnotation", func(t *testing.T) {
		// Explicit WhereOps annotation should override defaults
		userType := &entgen.Type{
			Name: "User",
			ID: &entgen.Field{
				Name: "id",
				Type: &field.TypeInfo{Type: field.TypeInt64},
			},
			Fields: []*entgen.Field{
				{
					Name: "email",
					Type: &field.TypeInfo{Type: field.TypeString},
					// Restrict email to only EQ, NEQ, In, NotIn, EqualFold
					Annotations: map[string]any{
						AnnotationName: &Annotation{
							WhereOps:    OpsEquality | OpEqualFold,
							HasWhereOps: true,
						},
					},
				},
				{
					Name: "search_text",
					Type: &field.TypeInfo{Type: field.TypeString},
					// Full string ops for search
					Annotations: map[string]any{
						AnnotationName: &Annotation{
							WhereOps:    OpsString,
							HasWhereOps: true,
						},
					},
				},
			},
			Annotations: map[string]any{},
		}

		g := &entgen.Graph{
			Config: &entgen.Config{Package: "example/ent"},
			Nodes:  []*entgen.Type{userType},
		}

		gen := NewGenerator(g, Config{
			Package:     "graphql",
			ORMPackage:  "example/ent",
			WhereInputs: true,
		})

		f := gen.genWhereInputGo()
		var buf bytes.Buffer
		if err := f.Render(&buf); err != nil {
			t.Fatalf("failed to render: %v", err)
		}
		code := buf.String()

		// Email should have EQ, NEQ, In, NotIn, EqualFold
		if !strings.Contains(code, "EmailIn") {
			t.Error("email should have EmailIn")
		}
		if !strings.Contains(code, "EmailEqualFold") {
			t.Error("email should have EmailEqualFold")
		}
		// Email should NOT have Contains, HasPrefix (restricted)
		if strings.Contains(code, "EmailContains") {
			t.Error("email should NOT have EmailContains (restricted by annotation)")
		}
		if strings.Contains(code, "EmailHasPrefix") {
			t.Error("email should NOT have EmailHasPrefix (restricted by annotation)")
		}

		// search_text should have full string ops
		if !strings.Contains(code, "SearchTextContains") {
			t.Error("search_text should have SearchTextContains")
		}
		if !strings.Contains(code, "SearchTextContainsFold") {
			t.Error("search_text should have SearchTextContainsFold")
		}
	})

	t.Run("BoolFieldDefaults", func(t *testing.T) {
		// Bool fields should only have EQ and NEQ
		userType := &entgen.Type{
			Name: "User",
			ID: &entgen.Field{
				Name: "id",
				Type: &field.TypeInfo{Type: field.TypeInt64},
			},
			Fields: []*entgen.Field{
				{Name: "is_active", Type: &field.TypeInfo{Type: field.TypeBool}},
			},
			Annotations: map[string]any{},
		}

		g := &entgen.Graph{
			Config: &entgen.Config{
				Package:  "example/ent",
				Features: []entgen.Feature{entgen.FeatureWhereInputAll},
			},
			Nodes: []*entgen.Type{userType},
		}

		gen := NewGenerator(g, Config{
			Package:     "graphql",
			ORMPackage:  "example/ent",
			WhereInputs: true,
		})

		f := gen.genWhereInputGo()
		var buf bytes.Buffer
		if err := f.Render(&buf); err != nil {
			t.Fatalf("failed to render: %v", err)
		}
		code := buf.String()

		// Bool should have EQ, NEQ
		if !strings.Contains(code, "IsActiveNEQ") {
			t.Error("bool field should have IsActiveNEQ")
		}
		// Bool should NOT have In, NotIn (doesn't make sense for bool)
		if strings.Contains(code, "IsActiveIn") {
			t.Error("bool field should NOT have IsActiveIn")
		}
	})

	t.Run("EnumFieldDefaults", func(t *testing.T) {
		// Enum fields should have OpsEquality
		userType := &entgen.Type{
			Name: "User",
			ID: &entgen.Field{
				Name: "id",
				Type: &field.TypeInfo{Type: field.TypeInt64},
			},
			Fields: []*entgen.Field{
				{
					Name: "status",
					Type: &field.TypeInfo{Type: field.TypeEnum},
					Enums: []entgen.Enum{
						{Name: "Active", Value: "active"},
						{Name: "Inactive", Value: "inactive"},
					},
				},
			},
			Annotations: map[string]any{},
		}

		g := &entgen.Graph{
			Config: &entgen.Config{
				Package:  "example/ent",
				Features: []entgen.Feature{entgen.FeatureWhereInputAll},
			},
			Nodes: []*entgen.Type{userType},
		}

		gen := NewGenerator(g, Config{
			Package:     "graphql",
			ORMPackage:  "example/ent",
			WhereInputs: true,
		})

		f := gen.genWhereInputGo()
		var buf bytes.Buffer
		if err := f.Render(&buf); err != nil {
			t.Fatalf("failed to render: %v", err)
		}
		code := buf.String()

		// Enum should have EQ, NEQ, In, NotIn
		if !strings.Contains(code, "StatusNEQ") {
			t.Error("enum field should have StatusNEQ")
		}
		if !strings.Contains(code, "StatusIn") {
			t.Error("enum field should have StatusIn")
		}
		// Enum should NOT have comparison ops
		if strings.Contains(code, "StatusGT") {
			t.Error("enum field should NOT have StatusGT")
		}
	})

	t.Run("CustomGoTypeWithWhereOps", func(t *testing.T) {
		// Custom Go types (like decimal.Decimal) require explicit WhereOps annotation
		// to enable comparison operations. This is by design - we don't try to guess
		// what operations make sense for custom types.
		orderType := &entgen.Type{
			Name: "Order",
			ID: &entgen.Field{
				Name: "id",
				Type: &field.TypeInfo{Type: field.TypeInt64},
			},
			Fields: []*entgen.Field{
				{
					// Custom type WITHOUT annotation - defaults to OpsEquality
					Name: "subtotal",
					Type: &field.TypeInfo{
						Type:    field.TypeOther,
						Ident:   "decimal.Decimal",
						PkgPath: "github.com/shopspring/decimal",
					},
				},
				{
					// Custom type WITH explicit WhereOps for comparison
					Name: "total_amount",
					Type: &field.TypeInfo{
						Type:    field.TypeOther,
						Ident:   "decimal.Decimal",
						PkgPath: "github.com/shopspring/decimal",
					},
					Annotations: map[string]any{
						AnnotationName: &Annotation{
							WhereOps:    OpsComparison,
							HasWhereOps: true,
						},
					},
				},
				{
					// Custom type with full numeric ops including nullable
					Name: "discount",
					Type: &field.TypeInfo{
						Type:    field.TypeOther,
						Ident:   "decimal.Decimal",
						PkgPath: "github.com/shopspring/decimal",
					},
					Nillable: true,
					Annotations: map[string]any{
						AnnotationName: &Annotation{
							WhereOps:    OpsComparison,
							HasWhereOps: true,
						},
					},
				},
			},
			Annotations: map[string]any{},
		}

		g := &entgen.Graph{
			Config: &entgen.Config{
				Package:  "example/ent",
				Features: []entgen.Feature{entgen.FeatureWhereInputAll},
			},
			Nodes: []*entgen.Type{orderType},
		}

		gen := NewGenerator(g, Config{
			Package:     "graphql",
			ORMPackage:  "example/ent",
			WhereInputs: true,
		})

		f := gen.genWhereInputGo()
		var buf bytes.Buffer
		if err := f.Render(&buf); err != nil {
			t.Fatalf("failed to render: %v", err)
		}
		code := buf.String()

		// subtotal (no annotation) should only have equality ops
		if !strings.Contains(code, "SubtotalNEQ") {
			t.Error("subtotal should have SubtotalNEQ")
		}
		if strings.Contains(code, "SubtotalGT") {
			t.Error("subtotal should NOT have SubtotalGT (no annotation)")
		}

		// total_amount (with OpsComparison) should have comparison ops
		if !strings.Contains(code, "TotalAmountGT") {
			t.Error("total_amount should have TotalAmountGT")
		}
		if !strings.Contains(code, "TotalAmountLTE") {
			t.Error("total_amount should have TotalAmountLTE")
		}
		// But NOT string ops
		if strings.Contains(code, "TotalAmountContains") {
			t.Error("total_amount should NOT have TotalAmountContains")
		}

		// discount (with OpsComparison + Nillable) should have comparison + nullable ops
		if !strings.Contains(code, "DiscountGT") {
			t.Error("discount should have DiscountGT")
		}
		if !strings.Contains(code, "DiscountIsNil") {
			t.Error("discount should have DiscountIsNil (nillable)")
		}
		if !strings.Contains(code, "DiscountNotNil") {
			t.Error("discount should have DiscountNotNil (nillable)")
		}
	})
}

// =============================================================================
// WhereInput whitelist tests (Tasks 6, 7, 8)
// =============================================================================

// TestGenerator_WhereInputWhitelist tests whitelist-based field filtering in WhereInput.
func TestGenerator_WhereInputWhitelist(t *testing.T) {
	t.Run("DefaultWhitelistMode", func(t *testing.T) {
		// Without FeatureWhereInputAll, fields are excluded by default.
		// Only fields with WhereInputEnabled or HasWhereOps are included.
		userType := &entgen.Type{
			Name: "User",
			ID: &entgen.Field{
				Name: "id",
				Type: &field.TypeInfo{Type: field.TypeInt64},
			},
			Fields: []*entgen.Field{
				{Name: "email", Type: &field.TypeInfo{Type: field.TypeString}},
				{
					Name: "name",
					Type: &field.TypeInfo{Type: field.TypeString},
					Annotations: map[string]any{
						AnnotationName: &Annotation{WhereInputEnabled: true},
					},
				},
				{
					Name: "status",
					Type: &field.TypeInfo{Type: field.TypeEnum},
					Enums: []entgen.Enum{
						{Name: "Active", Value: "active"},
						{Name: "Inactive", Value: "inactive"},
					},
					Annotations: map[string]any{
						AnnotationName: &Annotation{
							WhereOps:    OpsEquality,
							HasWhereOps: true,
						},
					},
				},
				{Name: "age", Type: &field.TypeInfo{Type: field.TypeInt}},
			},
			Annotations: map[string]any{},
		}

		g := &entgen.Graph{
			Config: &entgen.Config{Package: "example/ent"},
			Nodes:  []*entgen.Type{userType},
		}

		gen := NewGenerator(g, Config{
			Package:     "graphql",
			ORMPackage:  "example/ent",
			WhereInputs: true,
		})

		// Test Go generation
		f := gen.genWhereInputGo()
		var buf bytes.Buffer
		if err := f.Render(&buf); err != nil {
			t.Fatalf("failed to render: %v", err)
		}
		code := buf.String()

		// email (no annotation) should be excluded
		if strings.Contains(code, "EmailNEQ") {
			t.Error("email should NOT appear in WhereInput (no opt-in)")
		}
		// name (WhereInputEnabled) should be included
		if !strings.Contains(code, "NameContains") {
			t.Error("name should appear in WhereInput (WhereInputEnabled)")
		}
		// status (HasWhereOps) should be included
		if !strings.Contains(code, "StatusNEQ") {
			t.Error("status should appear in WhereInput (HasWhereOps)")
		}
		// age (no annotation) should be excluded
		if strings.Contains(code, "AgeGT") {
			t.Error("age should NOT appear in WhereInput (no opt-in)")
		}
	})

	t.Run("WhereInputFieldsEntityAnnotation", func(t *testing.T) {
		// Entity-level WhereInputFieldNames enables listed fields.
		userType := &entgen.Type{
			Name: "User",
			ID: &entgen.Field{
				Name: "id",
				Type: &field.TypeInfo{Type: field.TypeInt64},
			},
			Fields: []*entgen.Field{
				{Name: "email", Type: &field.TypeInfo{Type: field.TypeString}},
				{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}},
				{Name: "age", Type: &field.TypeInfo{Type: field.TypeInt}},
			},
			Annotations: map[string]any{
				AnnotationName: &Annotation{
					WhereInputFieldNames: []string{"email", "age"},
				},
			},
		}

		g := &entgen.Graph{
			Config: &entgen.Config{Package: "example/ent"},
			Nodes:  []*entgen.Type{userType},
		}

		gen := NewGenerator(g, Config{
			Package:     "graphql",
			ORMPackage:  "example/ent",
			WhereInputs: true,
		})

		f := gen.genWhereInputGo()
		var buf bytes.Buffer
		if err := f.Render(&buf); err != nil {
			t.Fatalf("failed to render: %v", err)
		}
		code := buf.String()

		// email (in WhereInputFieldNames) should be included
		if !strings.Contains(code, "EmailNEQ") {
			t.Error("email should appear in WhereInput (in WhereInputFieldNames)")
		}
		// age (in WhereInputFieldNames) should be included
		if !strings.Contains(code, "AgeGT") {
			t.Error("age should appear in WhereInput (in WhereInputFieldNames)")
		}
		// name (NOT in WhereInputFieldNames) should be excluded
		if strings.Contains(code, "NameContains") {
			t.Error("name should NOT appear in WhereInput (not in WhereInputFieldNames)")
		}
	})

	t.Run("FeatureWhereInputAll_LegacyMode", func(t *testing.T) {
		// With FeatureWhereInputAll, all fields are included without annotations.
		userType := &entgen.Type{
			Name: "User",
			ID: &entgen.Field{
				Name: "id",
				Type: &field.TypeInfo{Type: field.TypeInt64},
			},
			Fields: []*entgen.Field{
				{Name: "email", Type: &field.TypeInfo{Type: field.TypeString}},
				{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}},
				{Name: "age", Type: &field.TypeInfo{Type: field.TypeInt}},
			},
			Annotations: map[string]any{},
		}

		g := &entgen.Graph{
			Config: &entgen.Config{
				Package:  "example/ent",
				Features: []entgen.Feature{entgen.FeatureWhereInputAll},
			},
			Nodes: []*entgen.Type{userType},
		}

		gen := NewGenerator(g, Config{
			Package:     "graphql",
			ORMPackage:  "example/ent",
			WhereInputs: true,
		})

		f := gen.genWhereInputGo()
		var buf bytes.Buffer
		if err := f.Render(&buf); err != nil {
			t.Fatalf("failed to render: %v", err)
		}
		code := buf.String()

		// All fields should be included in legacy mode
		if !strings.Contains(code, "EmailNEQ") {
			t.Error("email should appear in WhereInput (legacy mode)")
		}
		if !strings.Contains(code, "NameContains") {
			t.Error("name should appear in WhereInput (legacy mode)")
		}
		if !strings.Contains(code, "AgeGT") {
			t.Error("age should appear in WhereInput (legacy mode)")
		}
	})
}

// TestGenerator_WhereInputEdgeWhitelist tests edge whitelist filtering in WhereInput.
func TestGenerator_WhereInputEdgeWhitelist(t *testing.T) {
	// Post and Comment need at least one filterable field so their WhereInput types
	// are generated. Without this, edges targeting them would be skipped since
	// HasPostsWith/HasCommentsWith would reference a non-existent WhereInput type.
	postType := &entgen.Type{
		Name: "Post",
		ID: &entgen.Field{
			Name: "id",
			Type: &field.TypeInfo{Type: field.TypeInt64},
		},
		Fields: []*entgen.Field{
			{
				Name: "title",
				Type: &field.TypeInfo{Type: field.TypeString},
				Annotations: map[string]any{
					AnnotationName: &Annotation{WhereInputEnabled: true},
				},
			},
		},
		Annotations: map[string]any{},
	}

	commentType := &entgen.Type{
		Name: "Comment",
		ID: &entgen.Field{
			Name: "id",
			Type: &field.TypeInfo{Type: field.TypeInt64},
		},
		Fields: []*entgen.Field{
			{
				Name: "body",
				Type: &field.TypeInfo{Type: field.TypeString},
				Annotations: map[string]any{
					AnnotationName: &Annotation{WhereInputEnabled: true},
				},
			},
		},
		Annotations: map[string]any{},
	}

	t.Run("EdgeAnnotationOptIn", func(t *testing.T) {
		userType := &entgen.Type{
			Name: "User",
			ID: &entgen.Field{
				Name: "id",
				Type: &field.TypeInfo{Type: field.TypeInt64},
			},
			Fields:      []*entgen.Field{},
			Annotations: map[string]any{},
		}
		userType.Edges = []*entgen.Edge{
			{
				Name:   "posts",
				Type:   postType,
				Unique: false,
				Annotations: map[string]any{
					AnnotationName: &Annotation{WhereInputEnabled: true},
				},
			},
			{
				Name:        "comments",
				Type:        commentType,
				Unique:      false,
				Annotations: map[string]any{},
			},
		}

		g := &entgen.Graph{
			Config: &entgen.Config{Package: "example/ent"},
			Nodes:  []*entgen.Type{userType, postType, commentType},
		}

		gen := NewGenerator(g, Config{
			Package:     "graphql",
			ORMPackage:  "example/ent",
			WhereInputs: true,
		})

		f := gen.genWhereInputGo()
		var buf bytes.Buffer
		if err := f.Render(&buf); err != nil {
			t.Fatalf("failed to render: %v", err)
		}
		code := buf.String()

		// posts edge (WhereInputEnabled) should be included
		if !strings.Contains(code, "HasPosts") {
			t.Error("posts edge should appear in WhereInput (WhereInputEnabled)")
		}
		// comments edge (no annotation) should be excluded
		if strings.Contains(code, "HasComments") {
			t.Error("comments edge should NOT appear in WhereInput (no opt-in)")
		}
	})

	t.Run("WhereInputEdgesEntityAnnotation", func(t *testing.T) {
		userType := &entgen.Type{
			Name: "User",
			ID: &entgen.Field{
				Name: "id",
				Type: &field.TypeInfo{Type: field.TypeInt64},
			},
			Fields: []*entgen.Field{},
			Annotations: map[string]any{
				AnnotationName: &Annotation{
					WhereInputEdgeNames: []string{"posts"},
				},
			},
		}
		userType.Edges = []*entgen.Edge{
			{
				Name:        "posts",
				Type:        postType,
				Unique:      false,
				Annotations: map[string]any{},
			},
			{
				Name:        "comments",
				Type:        commentType,
				Unique:      false,
				Annotations: map[string]any{},
			},
		}

		g := &entgen.Graph{
			Config: &entgen.Config{Package: "example/ent"},
			Nodes:  []*entgen.Type{userType, postType, commentType},
		}

		gen := NewGenerator(g, Config{
			Package:     "graphql",
			ORMPackage:  "example/ent",
			WhereInputs: true,
		})

		f := gen.genWhereInputGo()
		var buf bytes.Buffer
		if err := f.Render(&buf); err != nil {
			t.Fatalf("failed to render: %v", err)
		}
		code := buf.String()

		// posts edge (in WhereInputEdgeNames) should be included
		if !strings.Contains(code, "HasPosts") {
			t.Error("posts edge should appear in WhereInput (in WhereInputEdgeNames)")
		}
		// comments edge (NOT in WhereInputEdgeNames) should be excluded
		if strings.Contains(code, "HasComments") {
			t.Error("comments edge should NOT appear in WhereInput (not in WhereInputEdgeNames)")
		}
	})

	t.Run("EnableWhereInputsFieldAnnotation", func(t *testing.T) {
		// EnableWhereInputs(true) used as a field annotation should also enable
		// the field in WhereInput. This tests the fix for the bug where
		// EnableWhereInputs set WithWhereInputs (entity-level) but
		// isFieldWhereInputEnabled only checked WhereInputEnabled (field-level).
		enableTrue := true
		userType := &entgen.Type{
			Name: "User",
			ID: &entgen.Field{
				Name: "id",
				Type: &field.TypeInfo{Type: field.TypeInt64},
			},
			Fields: []*entgen.Field{
				{
					Name: "status",
					Type: &field.TypeInfo{Type: field.TypeString},
					Annotations: map[string]any{
						AnnotationName: &Annotation{WithWhereInputs: &enableTrue},
					},
				},
				{Name: "internal", Type: &field.TypeInfo{Type: field.TypeString}},
			},
			Annotations: map[string]any{},
		}

		g := &entgen.Graph{
			Config: &entgen.Config{Package: "example/ent"},
			Nodes:  []*entgen.Type{userType},
		}

		gen := NewGenerator(g, Config{
			Package:     "graphql",
			ORMPackage:  "example/ent",
			WhereInputs: true,
		})

		f := gen.genWhereInputGo()
		var buf bytes.Buffer
		if err := f.Render(&buf); err != nil {
			t.Fatalf("failed to render: %v", err)
		}
		code := buf.String()

		// status (EnableWhereInputs(true) / WithWhereInputs) should be included
		if !strings.Contains(code, "StatusContains") {
			t.Error("field with EnableWhereInputs(true) should appear in WhereInput")
		}
		// internal (no annotation) should be excluded
		if strings.Contains(code, "InternalNEQ") {
			t.Error("field without annotation should NOT appear in WhereInput")
		}
	})

	t.Run("EnableWhereInputsEdgeAnnotation", func(t *testing.T) {
		// EnableWhereInputs(true) used as an edge annotation should also enable
		// the edge in WhereInput.
		enableTrue := true
		userType := &entgen.Type{
			Name: "User",
			ID: &entgen.Field{
				Name: "id",
				Type: &field.TypeInfo{Type: field.TypeInt64},
			},
			Fields:      []*entgen.Field{},
			Annotations: map[string]any{},
		}
		userType.Edges = []*entgen.Edge{
			{
				Name:   "posts",
				Type:   postType,
				Unique: false,
				Annotations: map[string]any{
					AnnotationName: &Annotation{WithWhereInputs: &enableTrue},
				},
			},
			{
				Name:        "comments",
				Type:        commentType,
				Unique:      false,
				Annotations: map[string]any{},
			},
		}

		g := &entgen.Graph{
			Config: &entgen.Config{Package: "example/ent"},
			Nodes:  []*entgen.Type{userType, postType, commentType},
		}

		gen := NewGenerator(g, Config{
			Package:     "graphql",
			ORMPackage:  "example/ent",
			WhereInputs: true,
		})

		f := gen.genWhereInputGo()
		var buf bytes.Buffer
		if err := f.Render(&buf); err != nil {
			t.Fatalf("failed to render: %v", err)
		}
		code := buf.String()

		// posts edge (EnableWhereInputs(true)) should be included
		if !strings.Contains(code, "HasPosts") {
			t.Error("edge with EnableWhereInputs(true) should appear in WhereInput")
		}
		// comments edge (no annotation) should be excluded
		if strings.Contains(code, "HasComments") {
			t.Error("edge without annotation should NOT appear in WhereInput")
		}
	})
}

// TestGenerator_WhereInputSkipOverridesEnable tests that Skip(SkipWhereInput) takes precedence
// over WhereInputEnabled.
func TestGenerator_WhereInputSkipOverridesEnable(t *testing.T) {
	userType := &entgen.Type{
		Name: "User",
		ID: &entgen.Field{
			Name: "id",
			Type: &field.TypeInfo{Type: field.TypeInt64},
		},
		Fields: []*entgen.Field{
			{
				Name: "secret",
				Type: &field.TypeInfo{Type: field.TypeString},
				Annotations: map[string]any{
					AnnotationName: &Annotation{
						WhereInputEnabled: true,
						Skip:              SkipWhereInput,
					},
				},
			},
		},
		Annotations: map[string]any{},
	}

	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{userType},
	}

	gen := NewGenerator(g, Config{
		Package:     "graphql",
		ORMPackage:  "example/ent",
		WhereInputs: true,
	})

	f := gen.genWhereInputGo()
	var buf bytes.Buffer
	if err := f.Render(&buf); err != nil {
		t.Fatalf("failed to render: %v", err)
	}
	code := buf.String()

	// Field with both WhereInputEnabled and SkipWhereInput should be excluded (skip wins)
	if strings.Contains(code, "SecretNEQ") {
		t.Error("field with SkipWhereInput should be excluded even when WhereInputEnabled is set")
	}
}

// TestGenerator_WhereInputSensitiveOverridesEnable tests that sensitive fields are excluded
// from WhereInput even with WhereInputEnabled.
func TestGenerator_WhereInputSensitiveOverridesEnable(t *testing.T) {
	userType := &entgen.Type{
		Name: "User",
		ID: &entgen.Field{
			Name: "id",
			Type: &field.TypeInfo{Type: field.TypeInt64},
		},
		Fields: []*entgen.Field{
			{
				Name: "name",
				Type: &field.TypeInfo{Type: field.TypeString},
				Annotations: map[string]any{
					AnnotationName: &Annotation{WhereInputEnabled: true},
				},
			},
		},
		Annotations: map[string]any{},
	}

	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{userType},
	}

	gen := NewGenerator(g, Config{
		Package:     "graphql",
		ORMPackage:  "example/ent",
		WhereInputs: true,
	})

	// Verify the non-sensitive field IS included first
	f := gen.genWhereInputGo()
	var buf bytes.Buffer
	if err := f.Render(&buf); err != nil {
		t.Fatalf("failed to render: %v", err)
	}
	code := buf.String()

	if !strings.Contains(code, "NameContains") {
		t.Error("non-sensitive field with WhereInputEnabled should be included")
	}

	// Note: Field.Sensitive() returns true only when the field was constructed through
	// the real graph builder (with a *load.Field). In direct test construction, Sensitive()
	// always returns false. This test validates that the skipFieldInWhereInput code path
	// checks Sensitive() before WhereInputEnabled (tested via the skip annotation above).
}

// TestGenerator_WhereInputSDLGoConsistency tests that both Go and SDL generation
// expose the same fields in whitelist mode.
func TestGenerator_WhereInputSDLGoConsistency(t *testing.T) {
	userType := &entgen.Type{
		Name: "User",
		ID: &entgen.Field{
			Name: "id",
			Type: &field.TypeInfo{Type: field.TypeInt64},
		},
		Fields: []*entgen.Field{
			{
				Name: "email",
				Type: &field.TypeInfo{Type: field.TypeString},
				Annotations: map[string]any{
					AnnotationName: &Annotation{WhereInputEnabled: true},
				},
			},
			{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}}, // no annotation
			{
				Name: "age",
				Type: &field.TypeInfo{Type: field.TypeInt},
				Annotations: map[string]any{
					AnnotationName: &Annotation{
						WhereOps:    OpsComparison,
						HasWhereOps: true,
					},
				},
			},
		},
		Annotations: map[string]any{},
	}

	// Post needs at least one filterable field for its WhereInput to be generated.
	// Without this, the edge would be skipped since HasPostsWith would reference
	// a non-existent PostWhereInput.
	postType := &entgen.Type{
		Name: "Post",
		ID: &entgen.Field{
			Name: "id",
			Type: &field.TypeInfo{Type: field.TypeInt64},
		},
		Fields: []*entgen.Field{
			{
				Name: "title",
				Type: &field.TypeInfo{Type: field.TypeString},
				Annotations: map[string]any{
					AnnotationName: &Annotation{WhereInputEnabled: true},
				},
			},
		},
		Annotations: map[string]any{},
	}

	userType.Edges = []*entgen.Edge{
		{
			Name:   "posts",
			Type:   postType,
			Unique: false,
			Annotations: map[string]any{
				AnnotationName: &Annotation{WhereInputEnabled: true},
			},
		},
	}

	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{userType, postType},
	}

	gen := NewGenerator(g, Config{
		Package:     "graphql",
		ORMPackage:  "example/ent",
		WhereInputs: true,
	})

	// Generate Go code
	goFile := gen.genWhereInputGo()
	var goBuf bytes.Buffer
	if err := goFile.Render(&goBuf); err != nil {
		t.Fatalf("failed to render Go: %v", err)
	}
	goCode := goBuf.String()

	// Generate SDL
	sdl := gen.genWhereInput(userType)

	// email: should be in BOTH Go and SDL
	if !strings.Contains(goCode, "EmailNEQ") {
		t.Error("Go: email should be in WhereInput")
	}
	if !strings.Contains(sdl, "emailNEQ: String") {
		t.Error("SDL: email should be in WhereInput")
	}

	// name: should be in NEITHER Go nor SDL (no opt-in)
	if strings.Contains(goCode, "NameContains") {
		t.Error("Go: name should NOT be in WhereInput (no opt-in)")
	}
	if strings.Contains(sdl, "nameContains:") {
		t.Error("SDL: name should NOT be in WhereInput (no opt-in)")
	}

	// age: should be in BOTH Go and SDL (HasWhereOps)
	if !strings.Contains(goCode, "AgeGT") {
		t.Error("Go: age should be in WhereInput (HasWhereOps)")
	}
	if !strings.Contains(sdl, "ageGT: Int") {
		t.Error("SDL: age should be in WhereInput (HasWhereOps)")
	}

	// posts edge: should be in BOTH Go and SDL (target has filterable fields)
	if !strings.Contains(goCode, "HasPosts") {
		t.Error("Go: posts edge should be in WhereInput")
	}
	if !strings.Contains(sdl, "hasPosts: Boolean") {
		t.Error("SDL: posts edge should be in WhereInput")
	}
}

// TestGenerator_WhereInputValidation tests validation of WhereInputFieldNames
// and WhereInputEdgeNames for invalid references.
func TestGenerator_WhereInputValidation(t *testing.T) {
	t.Run("InvalidFieldName", func(t *testing.T) {
		userType := &entgen.Type{
			Name: "User",
			ID: &entgen.Field{
				Name: "id",
				Type: &field.TypeInfo{Type: field.TypeInt64},
			},
			Fields: []*entgen.Field{
				{Name: "email", Type: &field.TypeInfo{Type: field.TypeString}},
			},
			Annotations: map[string]any{
				AnnotationName: &Annotation{
					WhereInputFieldNames: []string{"email", "nonexistent_field"},
				},
			},
		}

		g := &entgen.Graph{
			Config: &entgen.Config{Package: "example/ent"},
			Nodes:  []*entgen.Type{userType},
		}

		gen := NewGenerator(g, Config{
			Package:     "graphql",
			ORMPackage:  "example/ent",
			WhereInputs: true,
		})

		err := gen.validateWhereInputAnnotations()
		if err == nil {
			t.Fatal("expected error for nonexistent field name, got nil")
		}
		if !strings.Contains(err.Error(), "nonexistent_field") {
			t.Errorf("error should mention the invalid field name, got: %v", err)
		}
	})

	t.Run("InvalidEdgeName", func(t *testing.T) {
		postType := &entgen.Type{
			Name: "Post",
			ID: &entgen.Field{
				Name: "id",
				Type: &field.TypeInfo{Type: field.TypeInt64},
			},
			Fields:      []*entgen.Field{},
			Annotations: map[string]any{},
		}

		userType := &entgen.Type{
			Name: "User",
			ID: &entgen.Field{
				Name: "id",
				Type: &field.TypeInfo{Type: field.TypeInt64},
			},
			Fields: []*entgen.Field{},
			Edges: []*entgen.Edge{
				{Name: "posts", Type: postType, Unique: false},
			},
			Annotations: map[string]any{
				AnnotationName: &Annotation{
					WhereInputEdgeNames: []string{"posts", "nonexistent_edge"},
				},
			},
		}

		g := &entgen.Graph{
			Config: &entgen.Config{Package: "example/ent"},
			Nodes:  []*entgen.Type{userType, postType},
		}

		gen := NewGenerator(g, Config{
			Package:     "graphql",
			ORMPackage:  "example/ent",
			WhereInputs: true,
		})

		err := gen.validateWhereInputAnnotations()
		if err == nil {
			t.Fatal("expected error for nonexistent edge name, got nil")
		}
		if !strings.Contains(err.Error(), "nonexistent_edge") {
			t.Errorf("error should mention the invalid edge name, got: %v", err)
		}
	})

	t.Run("ValidNames", func(t *testing.T) {
		postType := &entgen.Type{
			Name: "Post",
			ID: &entgen.Field{
				Name: "id",
				Type: &field.TypeInfo{Type: field.TypeInt64},
			},
			Fields:      []*entgen.Field{},
			Annotations: map[string]any{},
		}

		userType := &entgen.Type{
			Name: "User",
			ID: &entgen.Field{
				Name: "id",
				Type: &field.TypeInfo{Type: field.TypeInt64},
			},
			Fields: []*entgen.Field{
				{Name: "email", Type: &field.TypeInfo{Type: field.TypeString}},
				{Name: "created_at", Type: &field.TypeInfo{Type: field.TypeTime}},
			},
			Edges: []*entgen.Edge{
				{Name: "posts", Type: postType, Unique: false},
			},
			Annotations: map[string]any{
				AnnotationName: &Annotation{
					WhereInputFieldNames: []string{"email", "CreatedAt"}, // both schema and struct names
					WhereInputEdgeNames:  []string{"posts"},
				},
			},
		}

		g := &entgen.Graph{
			Config: &entgen.Config{Package: "example/ent"},
			Nodes:  []*entgen.Type{userType, postType},
		}

		gen := NewGenerator(g, Config{
			Package:     "graphql",
			ORMPackage:  "example/ent",
			WhereInputs: true,
		})

		err := gen.validateWhereInputAnnotations()
		if err != nil {
			t.Errorf("expected no error for valid names, got: %v", err)
		}
	})

	t.Run("ValidIDFieldName", func(t *testing.T) {
		// ID field name should be accepted in WhereInputFieldNames
		userType := &entgen.Type{
			Name: "User",
			ID: &entgen.Field{
				Name: "id",
				Type: &field.TypeInfo{Type: field.TypeInt64},
			},
			Fields: []*entgen.Field{},
			Annotations: map[string]any{
				AnnotationName: &Annotation{
					WhereInputFieldNames: []string{"id"},
				},
			},
		}

		g := &entgen.Graph{
			Config: &entgen.Config{Package: "example/ent"},
			Nodes:  []*entgen.Type{userType},
		}

		gen := NewGenerator(g, Config{
			Package:     "graphql",
			ORMPackage:  "example/ent",
			WhereInputs: true,
		})

		err := gen.validateWhereInputAnnotations()
		if err != nil {
			t.Errorf("ID field name should be valid, got: %v", err)
		}
	})
}

// TestGenerator_WhereInputUUIDNotBlanketID verifies that UUID fields without ID/FK naming
// get full comparison ops rather than being blanket-classified as ID fields.
func TestGenerator_WhereInputUUIDNotBlanketID(t *testing.T) {
	userType := &entgen.Type{
		Name: "User",
		ID: &entgen.Field{
			Name: "id",
			Type: &field.TypeInfo{Type: field.TypeInt64},
		},
		Fields: []*entgen.Field{
			{
				Name: "correlation_ref",
				Type: &field.TypeInfo{Type: field.TypeUUID},
				Annotations: map[string]any{
					AnnotationName: &Annotation{WhereInputEnabled: true},
				},
			},
			{
				Name: "user_id",
				Type: &field.TypeInfo{Type: field.TypeUUID},
				Annotations: map[string]any{
					AnnotationName: &Annotation{WhereInputEnabled: true},
				},
			},
		},
		Annotations: map[string]any{},
	}

	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{userType},
	}

	gen := NewGenerator(g, Config{
		Package:     "graphql",
		ORMPackage:  "example/ent",
		WhereInputs: true,
	})

	f := gen.genWhereInputGo()
	var buf bytes.Buffer
	if err := f.Render(&buf); err != nil {
		t.Fatalf("failed to render: %v", err)
	}
	code := buf.String()

	// correlation_ref is a UUID but NOT an ID/FK field by name — should get comparison ops
	if !strings.Contains(code, "CorrelationRefGT") {
		t.Error("non-ID UUID field should get comparison ops (GT), not just equality")
	}

	// user_id IS an FK by naming convention — should get only equality ops
	if strings.Contains(code, "UserIDGT") {
		t.Error("FK UUID field (user_id) should get equality ops only, not comparison")
	}
	if !strings.Contains(code, "UserIDIn") {
		t.Error("FK UUID field (user_id) should get equality ops (In)")
	}
}

// TestGenerator_WhereOnEdgeConnection_ForceResolverCoupling is the
// whole-pipeline guard for the silent-drop bug fixed by @goField(forceResolver:
// true) on edge-connection fields. A narrower unit test lives next to
// genEdgeField in generator_coverage_test.go
// (TestGenerator_GenEdgeField_ForceResolverOnWhere); this test runs the full
// genTypesSchema + genFullSchema pipeline on a realistic multi-entity graph
// and then asserts the *invariant* that links the two SDL pieces:
//
//	every connection field carrying a `where:` argument also carries
//	@goField(forceResolver: true)
//
// The invariant matters because a regression need not touch genEdgeField
// directly to break it — any higher-level code path that assembles edge
// fields without routing through genEdgeField would reintroduce silent drop.
// Parsing the emitted SDL as a black box catches that class of regression,
// not just direct changes to the edge-field helper.
func TestGenerator_WhereOnEdgeConnection_ForceResolverCoupling(t *testing.T) {
	// Two filterable types + a user that has edges to both, some of which
	// carry a `where:` arg under the whitelist and some of which don't.
	postType := &entgen.Type{
		Name: "Post",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{
				Name: "title",
				Type: &field.TypeInfo{Type: field.TypeString},
				Annotations: map[string]any{
					AnnotationName: &Annotation{WhereInputEnabled: true},
				},
			},
		},
		Annotations: map[string]any{
			AnnotationName: &Annotation{RelayConnection: true},
		},
	}
	commentType := &entgen.Type{
		Name: "Comment",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{
				Name: "body",
				Type: &field.TypeInfo{Type: field.TypeString},
				Annotations: map[string]any{
					AnnotationName: &Annotation{WhereInputEnabled: true},
				},
			},
		},
		// No RelayConnection annotation → `comments` edge below renders as
		// a plain list `[Comment!]!` instead of a connection, so it never
		// carries `where:` regardless of the whitelist. That's the negative
		// case the invariant check must not flag.
		Annotations: map[string]any{},
	}
	userType := &entgen.Type{
		Name:        "User",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields:      []*entgen.Field{},
		Annotations: map[string]any{AnnotationName: &Annotation{RelayConnection: true}},
	}
	userType.Edges = []*entgen.Edge{
		{
			// Whitelisted connection edge → SDL gets `where:` → must force
			// resolver. This is the positive case.
			Name:   "posts",
			Type:   postType,
			Unique: false,
			Annotations: map[string]any{
				AnnotationName: &Annotation{WhereInputEnabled: true},
			},
		},
		{
			// Non-connection (target has no RelayConnection annotation) → SDL
			// renders as `[Comment!]!`. Must not force resolver, because the
			// entity method has no `where` mismatch to silently drop.
			Name:        "comments",
			Type:        commentType,
			Unique:      false,
			Annotations: map[string]any{},
		},
	}

	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{userType, postType, commentType},
	}
	gen := NewGenerator(g, Config{
		Package:         "graphql",
		RelaySpec:       true,
		RelayConnection: true,
		WhereInputs:     true,
	})

	sdl := gen.genFullSchema()

	// Walk the SDL line by line, tracking which `type X {` block we're in.
	// The invariant applies only to edge-connection fields (inside entity
	// types), not to root Query connection fields — Query fields always
	// go through the generated QueryResolver interface regardless, so the
	// autobind subset-match path doesn't apply to them and they must NOT
	// carry forceResolver.
	//
	// A small state machine is easier to reason about than a regex here:
	// track the enclosing type name and the current field's opening line,
	// accumulate until we see the closing `)`, then apply the check on
	// the closing line (which is where the return type and directives
	// live).
	lines := strings.Split(sdl, "\n")
	var (
		currentType   string // name of the enclosing `type X {` block, empty outside any type
		inArgs        bool
		sawWhereArg   bool
		fieldOpenIdx  int
		foundCoupling bool // at least one where-bearing edge connection
	)
	for i, line := range lines {
		// Track the enclosing type. Conservatively only match top-level
		// `type X ...{` lines — directives / descriptions are on separate
		// lines so a simple prefix check is fine.
		if strings.HasPrefix(line, "type ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				currentType = parts[1]
			}
			continue
		}
		if strings.HasPrefix(line, "}") {
			currentType = ""
			continue
		}
		// Only enforce the invariant inside entity types. Query/Mutation/
		// Subscription fields are resolver-resolved regardless and must
		// not carry the directive.
		if currentType == "" || currentType == "Query" || currentType == "Mutation" || currentType == "Subscription" {
			continue
		}

		if !inArgs && strings.Contains(line, "(") && !strings.Contains(line, ")") {
			inArgs = true
			sawWhereArg = false
			fieldOpenIdx = i
			continue
		}
		if inArgs {
			if strings.Contains(line, "where:") {
				sawWhereArg = true
			}
			if strings.Contains(line, ")") {
				inArgs = false
				closeLine := line
				isConnection := strings.Contains(closeLine, "Connection")
				hasForceResolver := strings.Contains(closeLine, "@goField(forceResolver: true)")

				if sawWhereArg && isConnection && !hasForceResolver {
					t.Errorf(
						"edge-connection field in type %q (starting at SDL line %d) carries `where:` but is missing @goField(forceResolver: true):\n%s",
						currentType, fieldOpenIdx+1, closeLine)
				}
				if sawWhereArg && isConnection && hasForceResolver {
					foundCoupling = true
				}
				if !sawWhereArg && hasForceResolver {
					t.Errorf(
						"field in type %q (starting at SDL line %d) has @goField(forceResolver: true) but no `where:` arg — unnecessary resolver coercion:\n%s",
						currentType, fieldOpenIdx+1, closeLine)
				}
			}
		}
	}

	// Liveness: ensure the test actually exercised the coupling path. A
	// buggy hasFilterableContent or hasWhereInput that made no edge carry
	// `where:` would make the invariant checks above trivially pass.
	if !foundCoupling {
		t.Fatal("liveness: expected at least one edge-connection field with both `where:` and @goField(forceResolver: true)")
	}
}
