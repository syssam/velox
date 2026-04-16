package graphql

import (
	"maps"
	"strings"

	"github.com/syssam/velox/schema"
)

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
	// SkipMutations skips all mutations (create, update).
	SkipMutations = SkipMutationCreate | SkipMutationUpdate

	// SkipEverything skips all GraphQL generation including mutations (Velox extension).
	SkipEverything = SkipAll | SkipMutations

	// SkipInputs skips all input types.
	SkipInputs = SkipMutationCreateInput | SkipMutationUpdateInput
)

// MutationType is a bitmask for entity-level mutation control.
type MutationType uint

const (
	mutCreate MutationType = 1 << iota
	mutUpdate
)

// HasCreate reports whether the create mutation is enabled.
func (m MutationType) HasCreate() bool { return m&mutCreate != 0 }

// HasUpdate reports whether the update mutation is enabled.
func (m MutationType) HasUpdate() bool { return m&mutUpdate != 0 }

// MutationOption configures which mutations to generate for an entity.
// Supports .Description() chaining like Ent's entgql.MutationCreate().Description("...").
type MutationOption interface {
	IsCreate() bool
	GetDescription() string
	// Description sets a custom description for the mutation input type.
	Description(string) MutationOption
}

type builtinMutation struct {
	description string
	isCreate    bool
}

func (v builtinMutation) IsCreate() bool         { return v.isCreate }
func (v builtinMutation) GetDescription() string { return v.description }

func (v builtinMutation) Description(desc string) MutationOption {
	v.description = desc
	return v
}

// MutationCreate enables the create mutation for this entity.
// Supports .Description() chaining.
//
// Example:
//
//	graphql.Mutations(graphql.MutationCreate())
//	graphql.Mutations(graphql.MutationCreate().Description("Fields for creating a user"))
func MutationCreate() MutationOption {
	return builtinMutation{isCreate: true}
}

// MutationUpdate enables the update mutation for this entity.
// Supports .Description() chaining.
//
// Example:
//
//	graphql.Mutations(graphql.MutationUpdate())
//	graphql.Mutations(graphql.MutationUpdate().Description("Fields for updating a user"))
func MutationUpdate() MutationOption {
	return builtinMutation{isCreate: false}
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

	// OpHas generates the array-has predicate (e.g., tagsHas: String).
	// Checks if the JSON array contains a single value. Uses sqljson.ValueContains.
	OpHas
	// OpHasSome generates the array-has-some predicate (e.g., tagsHasSome: [String!]).
	// Checks if the JSON array contains ANY of the given values (OR).
	OpHasSome
	// OpHasEvery generates the array-has-every predicate (e.g., tagsHasEvery: [String!]).
	// Checks if the JSON array contains ALL of the given values (AND).
	OpHasEvery
	// OpIsEmpty generates the array-is-empty predicate (e.g., tagsIsEmpty: Boolean).
	// Checks if the JSON array has zero elements.
	OpIsEmpty
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

	// OpsStringBasic includes equality + contains for String fields.
	// This is the secure default — no prefix/suffix/case-fold operators.
	// Opt in to OpsString for fields that genuinely need full text matching.
	OpsStringBasic WhereOp = OpsEquality | OpContains

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

	// OpsJSONArray includes operations for JSON array (slice) fields.
	// Prisma-style naming: has, hasSome, hasEvery, isEmpty.
	// Use with WhereOps annotation on JSON slice fields to opt-in.
	// Nullable ops (IsNil, NotNil) are auto-added for Nillable() fields.
	//
	// Example:
	//
	//	field.JSON("tags", []string{}).
	//	    Annotations(graphql.WhereOps(graphql.OpsJSONArray))
	OpsJSONArray WhereOp = OpHas | OpHasSome | OpHasEvery | OpIsEmpty
)

// Has reports whether the given flag is set.
func (op WhereOp) Has(flag WhereOp) bool { return op&flag != 0 }

// HasEQ reports whether the EQ operator is enabled.
func (op WhereOp) HasEQ() bool { return op&OpEQ != 0 }

// HasNEQ reports whether the NEQ operator is enabled.
func (op WhereOp) HasNEQ() bool { return op&OpNEQ != 0 }

// HasIn reports whether the In operator is enabled.
func (op WhereOp) HasIn() bool { return op&OpIn != 0 }

// HasNotIn reports whether the NotIn operator is enabled.
func (op WhereOp) HasNotIn() bool { return op&OpNotIn != 0 }

// HasGT reports whether the GT operator is enabled.
func (op WhereOp) HasGT() bool { return op&OpGT != 0 }

// HasGTE reports whether the GTE operator is enabled.
func (op WhereOp) HasGTE() bool { return op&OpGTE != 0 }

// HasLT reports whether the LT operator is enabled.
func (op WhereOp) HasLT() bool { return op&OpLT != 0 }

// HasLTE reports whether the LTE operator is enabled.
func (op WhereOp) HasLTE() bool { return op&OpLTE != 0 }

// HasContains reports whether the Contains operator is enabled.
func (op WhereOp) HasContains() bool { return op&OpContains != 0 }

// HasHasPrefix reports whether the HasPrefix operator is enabled.
func (op WhereOp) HasHasPrefix() bool { return op&OpHasPrefix != 0 }

// HasHasSuffix reports whether the HasSuffix operator is enabled.
func (op WhereOp) HasHasSuffix() bool { return op&OpHasSuffix != 0 }

// HasEqualFold reports whether the EqualFold operator is enabled.
func (op WhereOp) HasEqualFold() bool { return op&OpEqualFold != 0 }

// HasContainsFold reports whether the ContainsFold operator is enabled.
func (op WhereOp) HasContainsFold() bool { return op&OpContainsFold != 0 }

// HasIsNil reports whether the IsNil operator is enabled.
func (op WhereOp) HasIsNil() bool { return op&OpIsNil != 0 }

// HasNotNil reports whether the NotNil operator is enabled.
func (op WhereOp) HasNotNil() bool { return op&OpNotNil != 0 }

// HasHas reports whether the Has operator is enabled.
func (op WhereOp) HasHas() bool { return op&OpHas != 0 }

// HasHasSome reports whether the HasSome operator is enabled.
func (op WhereOp) HasHasSome() bool { return op&OpHasSome != 0 }

// HasHasEvery reports whether the HasEvery operator is enabled.
func (op WhereOp) HasHasEvery() bool { return op&OpHasEvery != 0 }

// HasIsEmpty reports whether the IsEmpty operator is enabled.
func (op WhereOp) HasIsEmpty() bool { return op&OpIsEmpty != 0 }

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

// InCreate reports whether the field is included in create inputs.
func (op MutationOp) InCreate() bool { return op&IncludeCreate != 0 }

// InUpdate reports whether the field is included in update inputs.
func (op MutationOp) InUpdate() bool { return op&IncludeUpdate != 0 }

// ResolverMapping defines a custom resolver field for an entity.
type ResolverMapping struct {
	// FieldName is the GraphQL field name, optionally including inline arguments.
	//
	// Simple form:
	//   "glAccount"
	//
	// With inline arguments (SDL syntax):
	//   "priceListItem(priceListId: ID!)"
	//   "search(query: String!, limit: Int)"
	FieldName string
	// ReturnType is the full GraphQL return type including nullability.
	// Include "!" for non-null types. Omit "!" for nullable types.
	//
	//   "PublicGlAccount!"  — non-null
	//   "PublicUser"        — nullable
	//   "[Post!]!"          — non-null list of non-null items
	ReturnType string
	// Comment is the GraphQL description for this field.
	// Emitted as a triple-quoted string (""") above the field definition.
	Comment string
}

// resolverBaseName extracts the field name without inline arguments.
// "priceListItem(priceListId: ID!)" → "priceListItem"
// "glAccount" → "glAccount"
func resolverBaseName(fieldName string) string {
	if before, _, ok := strings.Cut(fieldName, "("); ok {
		return before
	}
	return fieldName
}

// WithComment adds a GraphQL description to the resolver mapping.
//
// Example:
//
//	graphql.Map("glAccount", "PublicGlAccount!").WithComment("The associated GL account")
func (rm ResolverMapping) WithComment(comment string) ResolverMapping {
	rm.Comment = comment
	return rm
}

// SubscriptionConfig holds metadata for a subscription field.
type SubscriptionConfig struct {
	Name        string // Subscription field name (e.g., "onUserCreated")
	ReturnType  string // GraphQL return type (e.g., "User!")
	Description string // Optional description
	Args        string // Optional argument string (e.g., "id: ID!")
}

// SubscriptionFieldBuilder builds a SubscriptionConfig with chaining.
type SubscriptionFieldBuilder struct {
	config SubscriptionConfig
}

// SubscriptionField creates a subscription field definition.
//
// Example:
//
//	graphql.Subscription(
//	    graphql.SubscriptionField("onUserCreated", "User!"),
//	    graphql.SubscriptionField("onPostPublished", "Post!").WithDescription("Fires when published"),
//	)
func SubscriptionField(name, returnType string) SubscriptionFieldBuilder {
	return SubscriptionFieldBuilder{
		config: SubscriptionConfig{Name: name, ReturnType: returnType},
	}
}

// WithDescription sets a description for the subscription field.
func (b SubscriptionFieldBuilder) WithDescription(desc string) SubscriptionFieldBuilder {
	b.config.Description = desc
	return b
}

// WithArgs sets arguments for the subscription field (e.g., "id: ID!").
func (b SubscriptionFieldBuilder) WithArgs(args string) SubscriptionFieldBuilder {
	b.config.Args = args
	return b
}

// Subscription defines subscription fields for an entity.
// These are collected into a `type Subscription` block in the generated schema.
// gqlgen handles the runtime subscription implementation.
func Subscription(fields ...SubscriptionFieldBuilder) Annotation {
	configs := make([]SubscriptionConfig, len(fields))
	for i, f := range fields {
		configs[i] = f.config
	}
	return Annotation{Subscriptions: configs}
}

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

	// QueryFieldConfig holds optional configuration for the query field
	// (custom name, description, directives). Set by QueryField() constructor.
	QueryFieldConfig *QueryFieldSettings `json:"QueryFieldConfig,omitempty"`

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

	// Subscriptions defines subscription fields contributed by this entity.
	// Collected into a `type Subscription` block in the generated schema.
	Subscriptions []SubscriptionConfig

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

	// WhereOps sets which filter operations are available for this field in WhereInput.
	// By default, operations are determined by field type:
	//   - ID/FK fields: OpsEquality (EQ, NEQ, In, NotIn)
	//   - String fields: OpsStringBasic (EQ, NEQ, In, NotIn, Contains)
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

	// --- WhereInput whitelist settings (Velox extension) ---

	// WhereInputEnabled marks this field/edge as filterable in WhereInput.
	// In whitelist mode (default), only fields/edges with this flag or HasWhereOps
	// are included in WhereInput generation.
	WhereInputEnabled bool

	// WhereInputFieldNames lists field names (schema names, e.g. "customer_id")
	// to enable for WhereInput filtering. Entity-level annotation.
	// Listed fields get smart type-based default operators.
	// PascalCase Go names (e.g. "CustomerID") are also accepted.
	WhereInputFieldNames []string

	// WhereInputEdgeNames lists edge names to enable for WhereInput filtering.
	// Entity-level annotation. Listed edges get HasXxx/HasXxxWith predicates.
	WhereInputEdgeNames []string

	// --- Resolver mappings (Velox extension) ---

	// ResolverMappings defines custom resolver fields for this entity.
	// Map entries add new fields with @goField(forceResolver: true) and a custom return type.
	// Resolve entries add @goField(forceResolver: true) to existing fields.
	ResolverMappings []ResolverMapping `json:"resolver_mappings,omitempty"`

	// Omittable wraps this field with graphql.Omittable[T] for PATCH semantics.
	// Changes both SDL (@goField(omittable: true)) and Go mutation input struct
	// (*T -> graphql.Omittable[*T]).
	Omittable bool `json:"omittable,omitempty"`
}

// Name implements velox.Annotation.
func (a Annotation) Name() string {
	return AnnotationName
}

// Ensure Annotation implements schema.Annotation.
var _ schema.Annotation = (*Annotation)(nil)

// --- Functional Constructors ---

// Skip returns an annotation that skips the specified modes.
// When called with no arguments, defaults to SkipAll (like Ent).
//
// Example:
//
//	graphql.Skip()                                            // skip all
//	graphql.Skip(graphql.SkipMutationCreate, graphql.SkipWhereInput) // skip specific
func Skip(modes ...SkipMode) Annotation {
	if len(modes) == 0 {
		return Annotation{Skip: SkipAll}
	}
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

// QueryFieldSettings holds optional configuration for a query field.
// Matches Ent's FieldConfig struct for QueryField annotations.
type QueryFieldSettings struct {
	// Name overrides the default query field name.
	Name string `json:"Name,omitempty"`
	// Description sets the GraphQL description for the field.
	Description string `json:"Description,omitempty"`
	// Directives to add on the query field.
	Directives []Directive `json:"Directives,omitempty"`
}

// QueryFieldAnnotation is a builder for QueryField annotations.
// Supports chaining: QueryField("users").Description("All users").Directives(...)
type QueryFieldAnnotation struct {
	Annotation
}

// Description sets the GraphQL description for the query field.
func (a QueryFieldAnnotation) Description(text string) QueryFieldAnnotation {
	a.QueryFieldConfig.Description = text
	return a
}

// Directives adds GraphQL directives to the query field.
func (a QueryFieldAnnotation) Directives(directives ...Directive) QueryFieldAnnotation {
	a.QueryFieldConfig.Directives = directives
	return a
}

// QueryField includes this entity in the Query type.
// Optionally accepts a custom field name. Supports chaining with
// .Description() and .Directives() like Ent's entgql.QueryField.
//
// Example:
//
//	graphql.QueryField()
//	graphql.QueryField("allUsers").Description("List all users")
func QueryField(name ...string) QueryFieldAnnotation {
	cfg := &QueryFieldSettings{}
	if len(name) > 0 {
		cfg.Name = name[0]
	}
	return QueryFieldAnnotation{Annotation: Annotation{QueryField: true, QueryFieldConfig: cfg}}
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
	var inputs []MutationConfig
	for _, opt := range opts {
		if opt.IsCreate() {
			m |= mutCreate
		} else {
			m |= mutUpdate
		}
		inputs = append(inputs, MutationConfig{
			IsCreate:    opt.IsCreate(),
			Description: opt.GetDescription(),
		})
	}
	return Annotation{Mutations: m, HasMutationsSet: true, MutationInputs: inputs}
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

// NewDirective creates a Directive with the given name and arguments.
// This is a convenience constructor equivalent to Ent's entgql.NewDirective.
//
// Example:
//
//	graphql.Directives(
//	    graphql.NewDirective("cacheControl", map[string]any{"maxAge": 300}),
//	)
func NewDirective(name string, args map[string]any) Directive {
	return Directive{Name: name, Args: args}
}

// Deprecated creates a @deprecated directive with the given reason.
// This is a convenience constructor equivalent to Ent's entgql.Deprecated.
//
// Example:
//
//	graphql.Directives(graphql.Deprecated("Use Member instead"))
func Deprecated(reason string) Directive {
	if reason == "" {
		return Directive{Name: "deprecated"}
	}
	return Directive{Name: "deprecated", Args: map[string]any{"reason": reason}}
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

// --- Apollo Federation v2 Constructors ---

// FederationKey marks an entity as a federation entity with the given key fields.
// The fields argument uses the Apollo Federation FieldSet syntax.
//
// Example:
//
//	func (User) Annotations() []velox.Annotation {
//	    return []velox.Annotation{
//	        graphql.FederationKey("id"),
//	    }
//	}
//
// This renders as: type User @key(fields: "id") { ... }
func FederationKey(fields string) Annotation {
	return Annotation{
		Directives: []Directive{{
			Name: "key",
			Args: map[string]any{"fields": fields},
		}},
	}
}

// FederationKeyResolvable marks an entity as a federation entity with a resolvable flag.
// Set resolvable to false for stub types that reference entities owned by other subgraphs.
//
// Example:
//
//	func (Product) Annotations() []velox.Annotation {
//	    return []velox.Annotation{
//	        graphql.FederationKeyResolvable("id", false),
//	    }
//	}
//
// This renders as: type Product @key(fields: "id", resolvable: false) { ... }
func FederationKeyResolvable(fields string, resolvable bool) Annotation {
	return Annotation{
		Directives: []Directive{{
			Name: "key",
			Args: map[string]any{
				"fields":     fields,
				"resolvable": resolvable,
			},
		}},
	}
}

// FederationExternal marks a field as owned by another subgraph.
// External fields are not resolved by this subgraph but can be used
// in @requires directives to compute other fields.
//
// Example:
//
//	field.Float("price").Annotations(graphql.FederationExternal())
//
// This renders as: price: Float @external
func FederationExternal() Annotation {
	return Annotation{
		Directives: []Directive{{Name: "external"}},
	}
}

// FederationRequires specifies fields that must be fetched from other subgraphs
// before this field's resolver is called. The fields argument uses FieldSet syntax.
//
// Example:
//
//	field.Float("inEuros").Annotations(
//	    graphql.FederationRequires("price currency"),
//	)
//
// This renders as: inEuros: Float @requires(fields: "price currency")
func FederationRequires(fields string) Annotation {
	return Annotation{
		Directives: []Directive{{
			Name: "requires",
			Args: map[string]any{"fields": fields},
		}},
	}
}

// FederationProvides specifies fields on a returned type that this subgraph
// can resolve, even though the type is owned by another subgraph.
//
// Example:
//
//	edge.To("product", Product.Type).Annotations(
//	    graphql.FederationProvides("name price"),
//	)
//
// This renders as: product: Product @provides(fields: "name price")
func FederationProvides(fields string) Annotation {
	return Annotation{
		Directives: []Directive{{
			Name: "provides",
			Args: map[string]any{"fields": fields},
		}},
	}
}

// FederationShareable marks a type or field as resolvable by multiple subgraphs.
// Use this when the same field can be resolved by more than one subgraph.
//
// Example:
//
//	field.String("name").Annotations(graphql.FederationShareable())
//
// This renders as: name: String @shareable
func FederationShareable() Annotation {
	return Annotation{
		Directives: []Directive{{Name: "shareable"}},
	}
}

// FederationInaccessible marks a type or field as inaccessible from the supergraph.
// Inaccessible elements are hidden from the public API but can still be used
// internally by other subgraphs via @requires.
//
// Example:
//
//	field.String("internalCode").Annotations(graphql.FederationInaccessible())
//
// This renders as: internalCode: String @inaccessible
func FederationInaccessible() Annotation {
	return Annotation{
		Directives: []Directive{{Name: "inaccessible"}},
	}
}

// FederationOverride transfers ownership of a field from one subgraph to another.
// The from argument specifies the name of the subgraph that previously owned the field.
//
// Example:
//
//	field.String("name").Annotations(graphql.FederationOverride("products"))
//
// This renders as: name: String @override(from: "products")
func FederationOverride(from string) Annotation {
	return Annotation{
		Directives: []Directive{{
			Name: "override",
			Args: map[string]any{"from": from},
		}},
	}
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

// WhereOps configures which filter operations are generated for a field in WhereInput.
//
// By default, operations are determined by field type:
//   - ID/FK fields (detected by name ending in "ID" or "_id"): OpsEquality
//   - String fields: OpsStringBasic (EQ, NEQ, In, NotIn, Contains)
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

// --- WhereInput Whitelist Constructors (Velox extension) ---

// WhereInput marks a field or edge as filterable in WhereInput using smart
// type-based default operators. Without FeatureWhereInputAll, only fields
// with WhereInput() or WhereOps() annotations are filterable.
//
// For fields, operator defaults are determined by type:
//   - ID/FK fields: OpsEquality (EQ, NEQ, In, NotIn)
//   - String fields: OpsStringBasic (EQ, NEQ, In, NotIn, Contains)
//   - Numeric/Time: OpsComparison (EQ, NEQ, In, NotIn, GT, GTE, LT, LTE)
//   - Enum fields: OpsEquality
//   - Bool fields: OpEQ | OpNEQ
//
// To customize operators, use WhereOps() instead (which also implies opt-in).
//
// Example:
//
//	field.String("status").Annotations(graphql.WhereInput())
//	edge.To("posts", Post.Type).Annotations(graphql.WhereInput())
func WhereInput() Annotation {
	return Annotation{WhereInputEnabled: true}
}

// WhereInputFields marks specific fields as filterable in WhereInput.
// Names should match the schema definition name (e.g., "customer_id", not "CustomerID").
// PascalCase Go names are also accepted for flexibility.
// Each listed field gets smart type-based default operators.
//
// Example:
//
//	func (Invoice) Annotations() []velox.Annotation {
//	    return []velox.Annotation{
//	        graphql.WhereInputFields("status", "customer_id", "created_at"),
//	    }
//	}
func WhereInputFields(fields ...string) Annotation {
	return Annotation{WhereInputFieldNames: fields}
}

// WhereInputEdges marks specific edges as filterable in WhereInput.
// Listed edges get HasXxx and HasXxxWith predicates generated.
//
// Example:
//
//	func (Invoice) Annotations() []velox.Annotation {
//	    return []velox.Annotation{
//	        graphql.WhereInputEdges("items", "payments"),
//	    }
//	}
func WhereInputEdges(edges ...string) Annotation {
	return Annotation{WhereInputEdgeNames: edges}
}

// --- Resolver Constructors (Velox extension) ---

// Resolvers defines custom resolver fields for this entity.
// All resolver configuration is centralized here.
//
// Example:
//
//	func (Invoice) Annotations() []schema.Annotation {
//	    return []schema.Annotation{
//	        graphql.Resolvers(
//	            graphql.Map("glAccount", "PublicGlAccount!"),
//	            graphql.Map("approver", "PublicUser"),
//	            graphql.Map("priceListItem(priceListId: ID!)", "PriceListItem!"),
//	        ),
//	    }
//	}
func Resolvers(mappings ...ResolverMapping) Annotation {
	return Annotation{ResolverMappings: mappings}
}

// Map creates a resolver mapping that adds a new field to the entity type
// with @goField(forceResolver: true). The user implements the resolver.
//
// returnType is the full GraphQL type including nullability:
//
//	graphql.Map("glAccount", "PublicGlAccount!")     // non-null
//	graphql.Map("approver", "PublicUser")            // nullable
//	graphql.Map("posts", "[Post!]!")                 // non-null list
//
// fieldName can include inline arguments in SDL syntax:
//
//	graphql.Map("priceListItem(priceListId: ID!)", "PriceListItem!")
//	graphql.Map("search(query: String!, limit: Int)", "[Result!]!")
func Map(fieldName, returnType string) ResolverMapping {
	return ResolverMapping{FieldName: fieldName, ReturnType: returnType}
}

// Omittable marks this field for three-state input handling in mutations.
// gqlgen wraps the field with graphql.Omittable[T], enabling PATCH semantics:
//   - field not sent:       IsSet() = false
//   - field sent as null:   IsSet() = true, Value() = nil
//   - field sent with value: IsSet() = true, Value() = &value
//
// Example:
//
//	field.String("memo").Optional().Nillable().
//	    Annotations(graphql.Omittable())
func Omittable() Annotation {
	return Annotation{Omittable: true}
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

// MapsTo returns a mapping annotation that also unbinds the edge.
// This is equivalent to Ent's entgql.MapsTo — it sets both Mapping and Unbind
// because mapped edges cannot use default name-based binding.
//
// Example:
//
//	edge.To("children", Todo.Type).Annotations(
//	    graphql.MapsTo("subTasks", "assignedTasks"),
//	)
func MapsTo(names ...string) Annotation {
	return Annotation{
		Mapping: names,
		Unbind:  true,
	}
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
// SkipType implies skipping all GraphQL surfaces including mutation inputs.
func (a Annotation) IsSkipMutationCreateInput() bool {
	return a.Skip&(SkipMutationCreateInput|SkipType) != 0
}

// IsSkipMutationUpdateInput returns true if UpdateInput should be skipped.
// SkipType implies skipping all GraphQL surfaces including mutation inputs.
func (a Annotation) IsSkipMutationUpdateInput() bool {
	return a.Skip&(SkipMutationUpdateInput|SkipType) != 0
}

// IsSkipMutationCreate returns true if create mutation should be skipped.
func (a Annotation) IsSkipMutationCreate() bool { return a.Skip&SkipMutationCreate != 0 }

// IsSkipMutationUpdate returns true if update mutation should be skipped.
func (a Annotation) IsSkipMutationUpdate() bool { return a.Skip&SkipMutationUpdate != 0 }

// HasRelayConnection returns true if Relay connections are explicitly enabled.
func (a Annotation) HasRelayConnection() bool { return a.RelayConnection }

// HasQueryField returns true if query field is explicitly enabled.
func (a Annotation) HasQueryField() bool { return a.QueryField }

// GetTypeName returns the custom GraphQL type name, or empty string for default.
//
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

// GetWhereOps returns the filter operations configured for this field in WhereInput.
func (a Annotation) GetWhereOps() WhereOp {
	return a.WhereOps
}

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

// --- Resolver Getters ---

// GetResolverMappings returns the resolver mappings for this entity.
func (a Annotation) GetResolverMappings() []ResolverMapping { return a.ResolverMappings }

// IsOmittable returns true if this field uses Omittable for PATCH semantics.
func (a Annotation) IsOmittable() bool { return a.Omittable }

// --- Merge ---

// Merge implements schema.Merger interface for combining annotations.
// This is called when multiple annotations are added to the same schema element.
func (a Annotation) Merge(other schema.Annotation) schema.Annotation {
	switch o := other.(type) {
	case Annotation:
		return mergeAnnotations(a, o)
	case *Annotation:
		if o != nil {
			return mergeAnnotations(a, *o)
		}
		return a
	case QueryFieldAnnotation:
		return mergeAnnotations(a, o.Annotation)
	case *QueryFieldAnnotation:
		if o != nil {
			return mergeAnnotations(a, o.Annotation)
		}
		return a
	default:
		return a
	}
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
	if o.QueryFieldConfig != nil {
		if result.QueryFieldConfig == nil {
			result.QueryFieldConfig = &QueryFieldSettings{}
		}
		if o.QueryFieldConfig.Name != "" {
			result.QueryFieldConfig.Name = o.QueryFieldConfig.Name
		}
		if o.QueryFieldConfig.Description != "" {
			result.QueryFieldConfig.Description = o.QueryFieldConfig.Description
		}
		if len(o.QueryFieldConfig.Directives) > 0 {
			result.QueryFieldConfig.Directives = append(result.QueryFieldConfig.Directives, o.QueryFieldConfig.Directives...)
		}
	}
	if o.HasMutationsSet {
		result.Mutations |= o.Mutations
		result.HasMutationsSet = true
	}
	if len(o.MutationInputs) > 0 {
		result.MutationInputs = append(result.MutationInputs, o.MutationInputs...)
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
	if len(o.Subscriptions) > 0 {
		result.Subscriptions = append(result.Subscriptions, o.Subscriptions...)
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
	if o.HasWhereOps {
		result.WhereOps |= o.WhereOps
		result.HasWhereOps = true
	}
	if o.HasFieldMutationOps {
		// Last-value semantics (not OR-merge) so IncludeNone can override IncludeBoth.
		result.FieldMutationOps = o.FieldMutationOps
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
		maps.Copy(result.EnumValues, o.EnumValues)
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

	// WhereInput whitelist
	if o.WhereInputEnabled {
		result.WhereInputEnabled = true
	}
	if len(o.WhereInputFieldNames) > 0 {
		result.WhereInputFieldNames = appendUnique(result.WhereInputFieldNames, o.WhereInputFieldNames...)
	}
	if len(o.WhereInputEdgeNames) > 0 {
		result.WhereInputEdgeNames = appendUnique(result.WhereInputEdgeNames, o.WhereInputEdgeNames...)
	}

	// Resolver mappings
	if len(o.ResolverMappings) > 0 {
		result.ResolverMappings = mergeResolverMappings(result.ResolverMappings, o.ResolverMappings)
	}

	// Omittable
	if o.Omittable {
		result.Omittable = true
	}

	return result
}

// mergeResolverMappings combines two slices, deduplicating by base field name
// (without inline args). When both slices contain a mapping with the same base
// name, the entry from the second slice wins (last-non-zero-value convention).
func mergeResolverMappings(a, o []ResolverMapping) []ResolverMapping {
	seen := make(map[string]int)
	result := make([]ResolverMapping, 0, len(a)+len(o))
	for _, m := range a {
		seen[resolverBaseName(m.FieldName)] = len(result)
		result = append(result, m)
	}
	for _, m := range o {
		key := resolverBaseName(m.FieldName)
		if idx, ok := seen[key]; ok {
			result[idx] = m
		} else {
			seen[key] = len(result)
			result = append(result, m)
		}
	}
	return result
}

// MergeAnnotations combines multiple GraphQL annotations into one.
// Skip flags are OR'd together, other settings use last non-zero value.
// Delegates to Annotation.Merge() for each annotation.
func MergeAnnotations(annotations ...Annotation) Annotation {
	result := Annotation{}
	for _, a := range annotations {
		result = mergeAnnotations(result, a)
	}
	return result
}
