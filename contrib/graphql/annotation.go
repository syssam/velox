package graphql

import "github.com/syssam/velox/schema"

// AnnotationName is the name used for GraphQL annotations.
const AnnotationName = "graphql"

// SkipMode defines what to skip in GraphQL generation.
// Values match Ent's entgql.SkipMode exactly for compatibility.
type SkipMode uint

const (
	// SkipType skips the entire type from GraphQL schema.
	SkipType SkipMode = 1 << iota
	// SkipEnumField skips generating enum values for this field.
	SkipEnumField
	// SkipOrderField skips generating OrderField enum for this entity.
	SkipOrderField
	// SkipWhereInput skips generating WhereInput for this entity.
	SkipWhereInput
	// SkipMutationCreateInput skips generating CreateInput type.
	SkipMutationCreateInput
	// SkipMutationUpdateInput skips generating UpdateInput type.
	SkipMutationUpdateInput

	// SkipAll skips all GraphQL generation (matches Ent's SkipAll = 63).
	SkipAll = SkipType | SkipEnumField | SkipOrderField | SkipWhereInput | SkipMutationCreateInput | SkipMutationUpdateInput

	// SkipMutationCreate skips the create mutation (Velox extension).
	SkipMutationCreate SkipMode = 1 << 6
	// SkipMutationUpdate skips the update mutation (Velox extension).
	SkipMutationUpdate SkipMode = 1 << 7
	// SkipMutationDelete skips the delete mutation (Velox extension).
	SkipMutationDelete SkipMode = 1 << 8

	// SkipMutations skips all mutations (create, update, delete).
	SkipMutations = SkipMutationCreate | SkipMutationUpdate | SkipMutationDelete

	// SkipInputs skips all input types.
	SkipInputs = SkipMutationCreateInput | SkipMutationUpdateInput
)

// MutationType is a bitmask for entity-level mutation control.
type MutationType uint

const (
	mutCreate MutationType = 1 << iota
	mutUpdate
	mutDelete
)

// MutationType checks.
func (m MutationType) HasCreate() bool { return m&mutCreate != 0 }
func (m MutationType) HasUpdate() bool { return m&mutUpdate != 0 }
func (m MutationType) HasDelete() bool { return m&mutDelete != 0 }

// MutationOption configures which mutations to generate for an entity.
type MutationOption func(*MutationType)

// MutationCreate enables the create mutation for this entity.
//
// Example:
//
//	graphql.Mutations(graphql.MutationCreate())
func MutationCreate() MutationOption {
	return func(m *MutationType) { *m |= mutCreate }
}

// MutationUpdate enables the update mutation for this entity.
//
// Example:
//
//	graphql.Mutations(graphql.MutationUpdate())
func MutationUpdate() MutationOption {
	return func(m *MutationType) { *m |= mutUpdate }
}

// MutationDelete enables the delete mutation for this entity.
// NOTE: This is a Velox extension - Ent does not have MutationDelete().
// In Ent, delete mutations are written manually by the user.
//
// Example:
//
//	graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate(), graphql.MutationDelete())
func MutationDelete() MutationOption {
	return func(m *MutationType) { *m |= mutDelete }
}

// Directive represents a custom GraphQL directive to apply.
type Directive struct {
	Name string
	Args map[string]any
}

// MutationConfig holds metadata for mutation input types.
// This matches Ent's MutationConfig struct.
type MutationConfig struct {
	// IsCreate indicates this is a create mutation input.
	IsCreate bool `json:"IsCreate,omitempty"`
	// Description is the description of the mutation input.
	Description string `json:"Description,omitempty"`
}

// FilterOp defines which filter operators are available for a field.
// Deprecated: Use WhereOp for fine-grained control over filter operators.
type FilterOp uint

const (
	// OpEquality includes: field, field_not, field_in, field_notIn
	OpEquality FilterOp = 1 << iota
	// OpComparison includes: field_gt, field_gte, field_lt, field_lte (for numbers, dates)
	OpComparison
	// OpString includes: field_contains, field_startsWith, field_endsWith
	OpString
	// OpNull includes: field_isNull (for nullable fields)
	OpNull
	// OpJSON includes: field_path (Prisma-style path-based JSON filtering)
	OpJSON

	// OpNone disables all filter operators
	OpNone FilterOp = 0
	// OpAll enables all standard filter operators (excludes JSON)
	OpAll = OpEquality | OpComparison | OpString | OpNull
)

// FilterOp checks.
func (op FilterOp) HasEquality() bool   { return op&OpEquality != 0 }
func (op FilterOp) HasComparison() bool { return op&OpComparison != 0 }
func (op FilterOp) HasString() bool     { return op&OpString != 0 }
func (op FilterOp) HasNull() bool       { return op&OpNull != 0 }
func (op FilterOp) HasJSON() bool       { return op&OpJSON != 0 }

// WhereOp defines fine-grained filter operations for WhereInput generation.
// Use bitwise OR to combine multiple operations.
//
// Example:
//
//	graphql.WhereOps(graphql.OpEQ | graphql.OpNEQ | graphql.OpIn | graphql.OpNotIn)
type WhereOp uint32

// Individual filter operations.
const (
	// OpEQ generates the equality predicate (e.g., id: ID).
	OpEQ WhereOp = 1 << iota
	// OpNEQ generates the not-equal predicate (e.g., idNEQ: ID).
	OpNEQ
	// OpIn generates the in predicate (e.g., idIn: [ID!]).
	OpIn
	// OpNotIn generates the not-in predicate (e.g., idNotIn: [ID!]).
	OpNotIn
	// OpGT generates the greater-than predicate (e.g., ageGT: Int).
	OpGT
	// OpGTE generates the greater-than-or-equal predicate (e.g., ageGTE: Int).
	OpGTE
	// OpLT generates the less-than predicate (e.g., ageLT: Int).
	OpLT
	// OpLTE generates the less-than-or-equal predicate (e.g., ageLTE: Int).
	OpLTE
	// OpContains generates the contains predicate (e.g., nameContains: String).
	OpContains
	// OpHasPrefix generates the has-prefix predicate (e.g., nameHasPrefix: String).
	OpHasPrefix
	// OpHasSuffix generates the has-suffix predicate (e.g., nameHasSuffix: String).
	OpHasSuffix
	// OpEqualFold generates the case-insensitive equality predicate (e.g., nameEqualFold: String).
	OpEqualFold
	// OpContainsFold generates the case-insensitive contains predicate (e.g., nameContainsFold: String).
	OpContainsFold
	// OpIsNil generates the is-null predicate (e.g., ownerIDIsNil: Boolean).
	OpIsNil
	// OpNotNil generates the is-not-null predicate (e.g., ownerIDNotNil: Boolean).
	OpNotNil
)

// Common operation sets for convenience.
const (
	// OpsNone disables all filter operations.
	OpsNone WhereOp = 0

	// OpsEquality includes basic equality operations: EQ, NEQ, In, NotIn.
	// This is the default for ID, foreign key, enum, and boolean fields.
	OpsEquality WhereOp = OpEQ | OpNEQ | OpIn | OpNotIn

	// OpsNullable includes null-checking operations: IsNil, NotNil.
	// Automatically added for Nillable() fields.
	OpsNullable WhereOp = OpIsNil | OpNotNil

	// OpsComparison includes equality plus ordering operations.
	// This is the default for numeric (Int, Float) and Time fields.
	OpsComparison WhereOp = OpsEquality | OpGT | OpGTE | OpLT | OpLTE

	// OpsSubstring includes text search operations: Contains, HasPrefix, HasSuffix.
	OpsSubstring WhereOp = OpContains | OpHasPrefix | OpHasSuffix

	// OpsCaseFold includes case-insensitive operations: EqualFold, ContainsFold.
	OpsCaseFold WhereOp = OpEqualFold | OpContainsFold

	// OpsString includes equality + text search operations for String fields.
	// This is the default for String fields (except ID/FK fields).
	// Does NOT include GT/GTE/LT/LTE as lexicographic comparison is rarely useful.
	// Total: 9 ops (EQ, NEQ, In, NotIn, Contains, ContainsFold, EqualFold, HasPrefix, HasSuffix)
	OpsString WhereOp = OpsEquality | OpsSubstring | OpsCaseFold

	// OpsAll includes all operations (string + nullable).
	OpsAll WhereOp = OpsString | OpsNullable
)

// WhereOp check methods.
func (op WhereOp) Has(flag WhereOp) bool { return op&flag != 0 }
func (op WhereOp) HasEQ() bool           { return op&OpEQ != 0 }
func (op WhereOp) HasNEQ() bool          { return op&OpNEQ != 0 }
func (op WhereOp) HasIn() bool           { return op&OpIn != 0 }
func (op WhereOp) HasNotIn() bool        { return op&OpNotIn != 0 }
func (op WhereOp) HasGT() bool           { return op&OpGT != 0 }
func (op WhereOp) HasGTE() bool          { return op&OpGTE != 0 }
func (op WhereOp) HasLT() bool           { return op&OpLT != 0 }
func (op WhereOp) HasLTE() bool          { return op&OpLTE != 0 }
func (op WhereOp) HasContains() bool     { return op&OpContains != 0 }
func (op WhereOp) HasHasPrefix() bool    { return op&OpHasPrefix != 0 }
func (op WhereOp) HasHasSuffix() bool    { return op&OpHasSuffix != 0 }
func (op WhereOp) HasEqualFold() bool    { return op&OpEqualFold != 0 }
func (op WhereOp) HasContainsFold() bool { return op&OpContainsFold != 0 }
func (op WhereOp) HasIsNil() bool        { return op&OpIsNil != 0 }
func (op WhereOp) HasNotNil() bool       { return op&OpNotNil != 0 }

// MutationOp defines which mutation inputs a field appears in.
type MutationOp uint

const (
	// IncludeCreate includes the field in CreateXXXInput.
	IncludeCreate MutationOp = 1 << iota
	// IncludeUpdate includes the field in UpdateXXXInput.
	IncludeUpdate

	// IncludeNone excludes the field from all mutation inputs.
	IncludeNone MutationOp = 0
	// IncludeBoth includes the field in all mutation inputs (default).
	IncludeBoth = IncludeCreate | IncludeUpdate
)

// MutationOp checks.
func (op MutationOp) InCreate() bool { return op&IncludeCreate != 0 }
func (op MutationOp) InUpdate() bool { return op&IncludeUpdate != 0 }

// Annotation holds GraphQL-specific settings for entities and fields.
// Works at both entity-level and field-level (like Ent's entgql).
//
// Can be used with functional constructors or struct literals:
//
//	// Functional style
//	graphql.RelayConnection()
//	graphql.Type("Member")
//
//	// Struct literal style (like Ent's entgql)
//	graphql.Annotation{RelayConnection: true, Type: "Member"}
type Annotation struct {
	// --- Entity-level settings ---

	// Skip defines what to skip in GraphQL generation.
	// Use SkipMode constants: SkipType, SkipWhereInput, SkipMutationCreate, etc.
	Skip SkipMode

	// RelayConnection enables Relay-style cursor connections.
	RelayConnection bool

	// QueryField explicitly includes this entity in the Query type.
	QueryField bool

	// Mutations is a bitmask of enabled mutations (mutCreate, mutUpdate).
	// Use the Mutations() functional constructor for cleaner API.
	Mutations MutationType

	// HasMutationsSet tracks whether Mutations was explicitly set.
	HasMutationsSet bool

	// MutationInputs specifies the mutation input types to generate.
	// This matches Ent's MutationInputs field for full compatibility.
	// If not set, defaults to both create and update inputs based on Skip flags.
	MutationInputs []MutationConfig

	// MultiOrder enables multi-column ordering for this entity.
	MultiOrder bool

	// Directives adds custom GraphQL directives to the type definition.
	Directives []Directive

	// Implements specifies additional GraphQL interfaces this type implements.
	// The "Node" interface is automatically included when RelaySpec is enabled.
	// Use this for custom interfaces like "Auditable", "Timestamped", etc.
	Implements []string

	// WithWhereInputs explicitly enables or disables WhereInput generation.
	// Takes precedence over Skip flags.
	WithWhereInputs *bool

	// WithOrderField explicitly enables or disables OrderField enum generation.
	// Takes precedence over Skip flags.
	WithOrderField *bool

	// --- Shared settings (entity or field level) ---

	// Type sets a custom GraphQL type name.
	// Entity-level: renames the GraphQL type (e.g., User -> Member)
	// Field-level: sets the scalar type (e.g., String -> ID)
	Type string

	// --- Field-level settings ---

	// SkipField excludes this field from GraphQL schema.
	SkipField bool

	// FieldName sets a custom GraphQL field name.
	FieldName string

	// OrderField sets the name for this field in the OrderBy enum.
	// Example: "EMAIL" for an email field.
	OrderField string

	// Operators sets which filter operators are available for this field.
	// Deprecated: Use WhereOps for fine-grained control.
	Operators FilterOp

	// HasOperators tracks whether Operators was explicitly set.
	// Deprecated: Use HasWhereOps instead.
	HasOperators bool

	// WhereOps sets which filter operations are available for this field in WhereInput.
	// By default, operations are determined by field type:
	//   - ID/FK fields: OpsEquality (EQ, NEQ, In, NotIn)
	//   - String fields: OpsString (all string operations)
	//   - Numeric/Time: OpsComparison (equality + ordering)
	//   - Enum fields: OpsEquality
	//   - Bool fields: OpEQ | OpNEQ
	// Nullable fields automatically get OpsNullable (IsNil, NotNil) added.
	WhereOps WhereOp

	// HasWhereOps tracks whether WhereOps was explicitly set.
	HasWhereOps bool

	// FieldMutationOps controls which mutation inputs this field appears in.
	FieldMutationOps MutationOp

	// HasFieldMutationOps tracks whether FieldMutationOps was explicitly set.
	HasFieldMutationOps bool

	// CreateInputValidateTag sets a go-playground/validator struct tag for CreateXXXInput.
	// This tag is applied to the field in the generated CreateXXXInput struct.
	CreateInputValidateTag string

	// UpdateInputValidateTag sets a go-playground/validator struct tag for UpdateXXXInput.
	// This tag is applied to the field in the generated UpdateXXXInput struct.
	UpdateInputValidateTag string

	// EnumValues maps database enum values to custom GraphQL enum values.
	// Key is the database value (from NamedValues), value is the GraphQL enum name.
	// If not specified, database values are used as-is in GraphQL.
	EnumValues map[string]string

	// --- Edge-level settings (like entgql) ---

	// Unbind unbinds the edge from the GraphQL field name.
	// When true, the edge won't be automatically mapped to a GraphQL field.
	Unbind bool

	// Mapping specifies custom GraphQL field name mappings for this edge.
	// Used with Unbind for field collection (eager loading).
	Mapping []string

	// CollectedFor specifies which GraphQL fields should trigger collection
	// of this field. Used for eager loading optimization.
	CollectedFor []string
}

// Name implements velox.Annotation.
func (a Annotation) Name() string {
	return AnnotationName
}

// Ensure Annotation implements schema.Annotation.
var _ schema.Annotation = (*Annotation)(nil)

// --- Functional Constructors ---

// Skip returns an annotation that skips the specified modes.
//
// Example:
//
//	graphql.Skip(graphql.SkipMutationCreate, graphql.SkipWhereInput)
func Skip(modes ...SkipMode) Annotation {
	var skip SkipMode
	for _, m := range modes {
		skip |= m
	}
	return Annotation{Skip: skip}
}

// RelayConnection enables Relay-style cursor connections for this entity.
//
// Example:
//
//	graphql.RelayConnection()
func RelayConnection() Annotation {
	return Annotation{RelayConnection: true}
}

// QueryField includes this entity in the Query type.
// By default entities are included, use this to explicitly enable after Skip.
//
// Example:
//
//	graphql.QueryField()
func QueryField() Annotation {
	return Annotation{QueryField: true}
}

// Type sets a custom GraphQL type name.
// Works at both entity and field levels (like Ent's entgql.Type).
//
// Entity-level example:
//
//	graphql.Type("Member") // User entity becomes Member in GraphQL
//
// Field-level example:
//
//	field.String("user_id").Annotations(graphql.Type("ID"))
func Type(name string) Annotation {
	return Annotation{Type: name}
}

// Mutations enables specific mutations for this entity.
// Following Ent's opt-in style, mutations are NOT generated by default.
// Use this to explicitly specify which mutations should be available.
//
// If called with no arguments, defaults to both create and update (Ent-compatible).
//
// Example:
//
//	// Both create and update (default when no args)
//	graphql.Mutations()
//
//	// Only create mutation (immutable entity)
//	graphql.Mutations(graphql.MutationCreate())
//
//	// Create and update (explicit)
//	graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate())
func Mutations(opts ...MutationOption) Annotation {
	// Ent-compatible: default to both create and update if no options specified
	if len(opts) == 0 {
		opts = []MutationOption{MutationCreate(), MutationUpdate()}
	}
	var m MutationType
	for _, opt := range opts {
		opt(&m)
	}
	return Annotation{Mutations: m, HasMutationsSet: true}
}

// MultiOrder enables multi-column ordering for this entity.
// When enabled, the orderBy argument accepts an array of order specifications.
//
// Example:
//
//	graphql.MultiOrder()
func MultiOrder() Annotation {
	return Annotation{MultiOrder: true}
}

// Directives adds custom GraphQL directives to this entity's type definition.
//
// Example:
//
//	graphql.Directives(
//	    graphql.Directive{Name: "cacheControl", Args: map[string]any{"maxAge": 300}},
//	    graphql.Directive{Name: "deprecated", Args: map[string]any{"reason": "Use NewUser"}},
//	)
func Directives(dirs ...Directive) Annotation {
	return Annotation{Directives: dirs}
}

// Implements specifies additional GraphQL interfaces this type implements.
// The "Node" interface is automatically included when RelaySpec is enabled.
// Use this for custom interfaces like "Auditable", "Timestamped", etc.
//
// Example:
//
//	graphql.Implements("Auditable", "Timestamped")
//
// This generates:
//
//	type User implements Node & Auditable & Timestamped { ... }
func Implements(interfaces ...string) Annotation {
	return Annotation{Implements: interfaces}
}

// EnableWhereInputs explicitly enables or disables WhereInput generation.
// This takes precedence over SkipWhereInput.
//
// Example:
//
//	graphql.EnableWhereInputs(true)   // Enable WhereInput
//	graphql.EnableWhereInputs(false)  // Disable WhereInput
func EnableWhereInputs(enable bool) Annotation {
	return Annotation{WithWhereInputs: &enable}
}

// EnableOrderField explicitly enables or disables OrderField enum generation.
// This takes precedence over SkipOrderField.
//
// Example:
//
//	graphql.EnableOrderField(true)   // Enable OrderField
//	graphql.EnableOrderField(false)  // Disable OrderField
func EnableOrderField(enable bool) Annotation {
	return Annotation{WithOrderField: &enable}
}

// --- Field-Level Constructors ---

// SkipField excludes this field from GraphQL schema.
//
// Example:
//
//	field.String("internal_id").Annotations(graphql.SkipField())
func SkipField() Annotation {
	return Annotation{SkipField: true}
}

// FieldName sets a custom GraphQL field name.
//
// Example:
//
//	field.String("user_id").Annotations(graphql.FieldName("userId"))
func FieldName(name string) Annotation {
	return Annotation{FieldName: name}
}

// OrderField sets the name for this field in the OrderBy enum.
// Equivalent to entgql.OrderField("EMAIL").
//
// Example:
//
//	field.String("email").Annotations(graphql.OrderField("EMAIL"))
func OrderField(name string) Annotation {
	return Annotation{OrderField: name}
}

// Operators sets which filter operators are available for this field.
// By default, operators are determined by field type.
//
// Deprecated: Use WhereOps for fine-grained control over filter operations.
//
// Example:
//
//	field.String("email").Annotations(graphql.Operators(graphql.OpEquality))
//	field.Int64("price").Annotations(graphql.Operators(graphql.OpEquality | graphql.OpComparison))
func Operators(ops FilterOp) Annotation {
	return Annotation{Operators: ops, HasOperators: true}
}

// WhereOps configures which filter operations are generated for a field in WhereInput.
//
// By default, operations are determined by field type:
//   - ID/FK fields (detected by name ending in "ID" or "_id"): OpsEquality
//   - String fields: OpsString (all string operations)
//   - Numeric/Time fields: OpsComparison (equality + ordering)
//   - Enum fields: OpsEquality
//   - Bool fields: OpEQ | OpNEQ
//
// Nullable fields automatically get OpsNullable (IsNil, NotNil) added.
//
// Example:
//
//	// ID field - uses default OpsEquality (no annotation needed)
//	field.String("id")
//
//	// Add cursor pagination support to ID field
//	field.String("id").Annotations(
//	    graphql.WhereOps(graphql.OpsEquality | graphql.OpGT | graphql.OpLT),
//	)
//
//	// Restrict email to exact match + case-insensitive
//	field.String("email").Annotations(
//	    graphql.WhereOps(graphql.OpsEquality | graphql.OpEqualFold),
//	)
//
//	// Full text search on description
//	field.String("description").Annotations(
//	    graphql.WhereOps(graphql.OpsString),
//	)
func WhereOps(ops WhereOp) Annotation {
	return Annotation{WhereOps: ops, HasWhereOps: true}
}

// FieldMutationOps sets which mutation inputs this field appears in.
// By default, fields appear in both Create and Update inputs.
//
// Example:
//
//	// Exclude "role" from UpdateUserInput (can only set on create)
//	field.String("role").Annotations(graphql.FieldMutationOps(graphql.IncludeCreate))
//
//	// Exclude "internal_id" from both inputs
//	field.String("internal_id").Annotations(graphql.FieldMutationOps(graphql.IncludeNone))
//
//	// Only in UpdateUserInput (e.g., password change)
//	field.String("new_password").Annotations(graphql.FieldMutationOps(graphql.IncludeUpdate))
func FieldMutationOps(ops MutationOp) Annotation {
	return Annotation{FieldMutationOps: ops, HasFieldMutationOps: true}
}

// --- Validation Tag Constructors ---

// CreateInputValidate sets a go-playground/validator struct tag for CreateXXXInput.
// The tag is used to validate input when creating entities via GraphQL mutations.
//
// Example:
//
//	field.String("email").Annotations(
//	    graphql.CreateInputValidate("required,email"),
//	)
//
// This generates:
//
//	type CreateUserInput struct {
//	    Email string `json:"email" validate:"required,email"`
//	}
func CreateInputValidate(tag string) Annotation {
	return Annotation{CreateInputValidateTag: tag}
}

// UpdateInputValidate sets a go-playground/validator struct tag for UpdateXXXInput.
// The tag is used to validate input when updating entities via GraphQL mutations.
//
// Example:
//
//	field.String("email").Annotations(
//	    graphql.UpdateInputValidate("omitempty,email"),
//	)
//
// This generates:
//
//	type UpdateUserInput struct {
//	    Email *string `json:"email,omitempty" validate:"omitempty,email"`
//	}
func UpdateInputValidate(tag string) Annotation {
	return Annotation{UpdateInputValidateTag: tag}
}

// MutationInputValidate sets go-playground/validator struct tags for both Create and Update inputs.
// This is a convenience function when the same validation applies to both mutations.
//
// Example:
//
//	field.String("email").Annotations(
//	    graphql.MutationInputValidate("required,email", "omitempty,email"),
//	)
func MutationInputValidate(createTag, updateTag string) Annotation {
	return Annotation{
		CreateInputValidateTag: createTag,
		UpdateInputValidateTag: updateTag,
	}
}

// --- Enum Constructors ---

// EnumValues maps database enum values to custom GraphQL enum values.
// Use this when you want different names in GraphQL than what's stored in the database.
//
// Example:
//
//	field.Enum("status").
//	    NamedValues(
//	        "InProgress", "IN_PROGRESS",
//	        "Completed", "COMPLETED",
//	    ).
//	    Annotations(
//	        graphql.EnumValues(map[string]string{
//	            "IN_PROGRESS": "inProgress",  // DB value → GraphQL value
//	            "COMPLETED":   "completed",
//	        }),
//	    )
//
// This generates GraphQL enum:
//
//	enum Status {
//	    inProgress
//	    completed
//	}
func EnumValues(mapping map[string]string) Annotation {
	return Annotation{EnumValues: mapping}
}

// EnumValue is a convenience function for mapping a single enum value.
// Use EnumValues() for multiple mappings.
//
// Example:
//
//	field.Enum("status").
//	    Values("pending", "active").
//	    Annotations(
//	        graphql.EnumValue("pending", "PENDING"),
//	        graphql.EnumValue("active", "ACTIVE"),
//	    )
func EnumValue(dbValue, graphqlValue string) Annotation {
	return Annotation{EnumValues: map[string]string{dbValue: graphqlValue}}
}

// --- Edge-Level Constructors (like entgql) ---

// Unbind unbinds the edge from automatic GraphQL field mapping.
// Use with Mapping() to specify custom field collection mappings.
//
// Example:
//
//	edge.To("posts", Post.Type).Annotations(graphql.Unbind())
func Unbind() Annotation {
	return Annotation{Unbind: true}
}

// Mapping sets custom GraphQL field name mappings for edge collection.
// Used with Unbind() for advanced field collection (eager loading) scenarios.
//
// Example:
//
//	edge.To("posts", Post.Type).Annotations(
//	    graphql.Unbind(),
//	    graphql.Mapping("authorPosts", "publishedPosts"),
//	)
func Mapping(fields ...string) Annotation {
	return Annotation{Mapping: fields}
}

// CollectedFor specifies which GraphQL fields should trigger collection of this field.
// Used for eager loading optimization.
//
// Example:
//
//	field.String("computed_name").Annotations(
//	    graphql.CollectedFor("fullName", "displayName"),
//	)
func CollectedFor(fields ...string) Annotation {
	return Annotation{CollectedFor: fields}
}

// --- Entity-Level Getters ---

// IsSkipType returns true if the entire type should be skipped.
func (a Annotation) IsSkipType() bool { return a.Skip&SkipType != 0 }

// IsSkipWhereInput returns true if WhereInput should be skipped.
func (a Annotation) IsSkipWhereInput() bool { return a.Skip&SkipWhereInput != 0 }

// IsSkipOrderField returns true if OrderField should be skipped.
func (a Annotation) IsSkipOrderField() bool { return a.Skip&SkipOrderField != 0 }

// IsSkipMutationCreateInput returns true if CreateInput should be skipped.
func (a Annotation) IsSkipMutationCreateInput() bool { return a.Skip&SkipMutationCreateInput != 0 }

// IsSkipMutationUpdateInput returns true if UpdateInput should be skipped.
func (a Annotation) IsSkipMutationUpdateInput() bool { return a.Skip&SkipMutationUpdateInput != 0 }

// IsSkipMutationCreate returns true if create mutation should be skipped.
func (a Annotation) IsSkipMutationCreate() bool { return a.Skip&SkipMutationCreate != 0 }

// IsSkipMutationUpdate returns true if update mutation should be skipped.
func (a Annotation) IsSkipMutationUpdate() bool { return a.Skip&SkipMutationUpdate != 0 }

// IsSkipMutationDelete returns true if delete mutation should be skipped.
func (a Annotation) IsSkipMutationDelete() bool { return a.Skip&SkipMutationDelete != 0 }

// HasRelayConnection returns true if Relay connections are explicitly enabled.
func (a Annotation) HasRelayConnection() bool { return a.RelayConnection }

// HasQueryField returns true if query field is explicitly enabled.
func (a Annotation) HasQueryField() bool { return a.QueryField }

// GetTypeName returns the custom GraphQL type name, or empty string for default.
// Deprecated: Use GetType() instead.
func (a Annotation) GetTypeName() string { return a.Type }

// GetType returns the custom GraphQL type name, or empty string for default.
func (a Annotation) GetType() string { return a.Type }

// HasMutations returns true if Mutations was explicitly set.
func (a Annotation) HasMutations() bool { return a.HasMutationsSet }

// EnabledMutations returns the bitmask of enabled mutations.
func (a Annotation) EnabledMutations() MutationType { return a.Mutations }

// WantsMutationCreate returns true if create mutation should be generated.
// Following Ent's opt-in style: mutations are only generated when explicitly enabled
// via graphql.Mutations(graphql.MutationCreate()).
func (a Annotation) WantsMutationCreate() bool {
	if a.HasMutationsSet {
		return a.Mutations&mutCreate != 0
	}
	// Opt-in style: no mutations unless explicitly enabled
	return false
}

// WantsMutationUpdate returns true if update mutation should be generated.
// Following Ent's opt-in style: mutations are only generated when explicitly enabled
// via graphql.Mutations(graphql.MutationUpdate()).
func (a Annotation) WantsMutationUpdate() bool {
	if a.HasMutationsSet {
		return a.Mutations&mutUpdate != 0
	}
	// Opt-in style: no mutations unless explicitly enabled
	return false
}

// WantsMutationDelete returns true if delete mutation should be generated.
// Following opt-in style: mutations are only generated when explicitly enabled
// via graphql.Mutations(graphql.MutationDelete()).
func (a Annotation) WantsMutationDelete() bool {
	if a.HasMutationsSet {
		return a.Mutations&mutDelete != 0
	}
	// Opt-in style: no mutations unless explicitly enabled
	return false
}

// WantsWhereInputs returns true if WhereInput should be generated.
func (a Annotation) WantsWhereInputs() bool {
	if a.WithWhereInputs != nil {
		return *a.WithWhereInputs
	}
	return a.Skip&SkipWhereInput == 0
}

// WantsOrderField returns true if OrderField should be generated.
func (a Annotation) WantsOrderField() bool {
	if a.WithOrderField != nil {
		return *a.WithOrderField
	}
	return a.Skip&SkipOrderField == 0
}

// HasMultiOrder returns true if multi-column ordering is enabled.
func (a Annotation) HasMultiOrder() bool { return a.MultiOrder }

// GetDirectives returns the custom directives for this entity.
func (a Annotation) GetDirectives() []Directive { return a.Directives }

// GetImplements returns the additional GraphQL interfaces this type implements.
// The "Node" interface is automatically included when RelaySpec is enabled.
func (a Annotation) GetImplements() []string { return a.Implements }

// GetMutationInputs returns the mutation input configurations.
// If not explicitly set, returns default configs based on Skip flags.
func (a Annotation) GetMutationInputs() []MutationConfig {
	if len(a.MutationInputs) > 0 {
		return a.MutationInputs
	}
	// Default: generate both create and update unless skipped
	var inputs []MutationConfig
	if a.Skip&SkipMutationCreateInput == 0 {
		inputs = append(inputs, MutationConfig{IsCreate: true})
	}
	if a.Skip&SkipMutationUpdateInput == 0 {
		inputs = append(inputs, MutationConfig{IsCreate: false})
	}
	return inputs
}

// --- Field-Level Getters ---

// IsSkipField returns true if this field should be excluded from GraphQL schema.
func (a Annotation) IsSkipField() bool { return a.SkipField }

// GetFieldName returns the custom GraphQL field name, or empty string for default.
func (a Annotation) GetFieldName() string { return a.FieldName }

// GetOrderField returns the order field enum name (e.g., "EMAIL"), or empty for default.
func (a Annotation) GetOrderField() string { return a.OrderField }

// GetOperators returns the filter operators configured for this field.
// Deprecated: Use GetWhereOps instead.
func (a Annotation) GetOperators() FilterOp { return a.Operators }

// HasOperatorsSet returns true if filter operators were explicitly configured.
// Deprecated: Use HasWhereOpsSet instead.
func (a Annotation) HasOperatorsSet() bool { return a.HasOperators }

// GetWhereOps returns the filter operations configured for this field in WhereInput.
func (a Annotation) GetWhereOps() WhereOp { return a.WhereOps }

// HasWhereOpsSet returns true if WhereOps was explicitly configured.
func (a Annotation) HasWhereOpsSet() bool { return a.HasWhereOps }

// GetFieldMutationOps returns the mutation input configuration for this field.
func (a Annotation) GetFieldMutationOps() MutationOp { return a.FieldMutationOps }

// HasFieldMutationOpsSet returns true if field mutation ops were explicitly configured.
func (a Annotation) HasFieldMutationOpsSet() bool { return a.HasFieldMutationOps }

// InCreateInput returns true if this field should appear in CreateXXXInput.
func (a Annotation) InCreateInput() bool {
	return !a.HasFieldMutationOps || a.FieldMutationOps.InCreate()
}

// InUpdateInput returns true if this field should appear in UpdateXXXInput.
func (a Annotation) InUpdateInput() bool {
	return !a.HasFieldMutationOps || a.FieldMutationOps.InUpdate()
}

// GetCreateInputValidateTag returns the validation tag for CreateXXXInput fields.
func (a Annotation) GetCreateInputValidateTag() string { return a.CreateInputValidateTag }

// GetUpdateInputValidateTag returns the validation tag for UpdateXXXInput fields.
func (a Annotation) GetUpdateInputValidateTag() string { return a.UpdateInputValidateTag }

// GetEnumValues returns the custom GraphQL enum value mappings.
// Key is database value, value is GraphQL enum name.
func (a Annotation) GetEnumValues() map[string]string { return a.EnumValues }

// GetGraphQLEnumValue returns the GraphQL enum value for a database value.
// If no custom mapping exists, returns the original database value.
func (a Annotation) GetGraphQLEnumValue(dbValue string) string {
	if a.EnumValues != nil {
		if gqlValue, ok := a.EnumValues[dbValue]; ok {
			return gqlValue
		}
	}
	return dbValue
}

// --- Edge-Level Getters ---

// IsUnbound returns true if the edge is unbound from GraphQL field name.
func (a Annotation) IsUnbound() bool { return a.Unbind }

// GetMapping returns the custom GraphQL field name mappings for this edge.
func (a Annotation) GetMapping() []string { return a.Mapping }

// GetCollectedFor returns the GraphQL fields that trigger collection of this field.
func (a Annotation) GetCollectedFor() []string { return a.CollectedFor }

// --- Merge ---

// Merge implements schema.Merger interface for combining annotations.
// This is called when multiple annotations are added to the same schema element.
func (a Annotation) Merge(other schema.Annotation) schema.Annotation {
	o, ok := other.(Annotation)
	if !ok {
		return a
	}
	return mergeAnnotations(a, o)
}

// mergeAnnotations combines two annotations (internal implementation).
func mergeAnnotations(a, o Annotation) Annotation {
	result := a
	// Entity-level
	result.Skip |= o.Skip
	if o.RelayConnection {
		result.RelayConnection = true
	}
	if o.QueryField {
		result.QueryField = true
	}
	if o.HasMutationsSet {
		result.Mutations |= o.Mutations
		result.HasMutationsSet = true
	}
	if len(o.MutationInputs) > 0 {
		result.MutationInputs = o.MutationInputs
	}
	if o.MultiOrder {
		result.MultiOrder = true
	}
	if len(o.Directives) > 0 {
		result.Directives = append(result.Directives, o.Directives...)
	}
	if len(o.Implements) > 0 {
		result.Implements = append(result.Implements, o.Implements...)
	}
	if o.WithWhereInputs != nil {
		result.WithWhereInputs = o.WithWhereInputs
	}
	if o.WithOrderField != nil {
		result.WithOrderField = o.WithOrderField
	}

	// Shared
	if o.Type != "" {
		result.Type = o.Type
	}

	// Field-level
	if o.SkipField {
		result.SkipField = true
	}
	if o.FieldName != "" {
		result.FieldName = o.FieldName
	}
	if o.OrderField != "" {
		result.OrderField = o.OrderField
	}
	if o.HasOperators {
		result.Operators |= o.Operators
		result.HasOperators = true
	}
	if o.HasWhereOps {
		result.WhereOps |= o.WhereOps
		result.HasWhereOps = true
	}
	if o.HasFieldMutationOps {
		result.FieldMutationOps |= o.FieldMutationOps
		result.HasFieldMutationOps = true
	}
	if o.CreateInputValidateTag != "" {
		result.CreateInputValidateTag = o.CreateInputValidateTag
	}
	if o.UpdateInputValidateTag != "" {
		result.UpdateInputValidateTag = o.UpdateInputValidateTag
	}
	if len(o.EnumValues) > 0 {
		if result.EnumValues == nil {
			result.EnumValues = make(map[string]string)
		}
		for k, v := range o.EnumValues {
			result.EnumValues[k] = v
		}
	}

	// Edge-level
	if o.Unbind {
		result.Unbind = true
	}
	if len(o.Mapping) > 0 {
		result.Mapping = o.Mapping
	}
	if len(o.CollectedFor) > 0 {
		result.CollectedFor = o.CollectedFor
	}

	return result
}

// MergeAnnotations combines multiple GraphQL annotations into one.
// Skip flags are OR'd together, other settings use last non-zero value.
func MergeAnnotations(annotations ...Annotation) Annotation {
	result := Annotation{}
	for _, a := range annotations {
		// Entity-level
		result.Skip |= a.Skip
		if a.RelayConnection {
			result.RelayConnection = true
		}
		if a.QueryField {
			result.QueryField = true
		}
		if a.HasMutationsSet {
			result.Mutations |= a.Mutations
			result.HasMutationsSet = true
		}
		if a.MultiOrder {
			result.MultiOrder = true
		}
		if len(a.Directives) > 0 {
			result.Directives = append(result.Directives, a.Directives...)
		}
		if len(a.Implements) > 0 {
			result.Implements = append(result.Implements, a.Implements...)
		}
		if a.WithWhereInputs != nil {
			result.WithWhereInputs = a.WithWhereInputs
		}
		if a.WithOrderField != nil {
			result.WithOrderField = a.WithOrderField
		}

		// Shared
		if a.Type != "" {
			result.Type = a.Type
		}

		// Field-level
		if a.SkipField {
			result.SkipField = true
		}
		if a.FieldName != "" {
			result.FieldName = a.FieldName
		}
		if a.OrderField != "" {
			result.OrderField = a.OrderField
		}
		if a.HasOperators {
			result.Operators = a.Operators
			result.HasOperators = true
		}
		if a.HasWhereOps {
			result.WhereOps = a.WhereOps
			result.HasWhereOps = true
		}
		if a.HasFieldMutationOps {
			result.FieldMutationOps = a.FieldMutationOps
			result.HasFieldMutationOps = true
		}
		if a.CreateInputValidateTag != "" {
			result.CreateInputValidateTag = a.CreateInputValidateTag
		}
		if a.UpdateInputValidateTag != "" {
			result.UpdateInputValidateTag = a.UpdateInputValidateTag
		}
		if len(a.EnumValues) > 0 {
			if result.EnumValues == nil {
				result.EnumValues = make(map[string]string)
			}
			for k, v := range a.EnumValues {
				result.EnumValues[k] = v
			}
		}

		// Edge-level
		if a.Unbind {
			result.Unbind = true
		}
		if len(a.Mapping) > 0 {
			result.Mapping = a.Mapping
		}
		if len(a.CollectedFor) > 0 {
			result.CollectedFor = a.CollectedFor
		}
	}
	return result
}
