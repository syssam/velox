package graphql

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/dave/jennifer/jen"
	"github.com/go-openapi/inflect"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

// =============================================================================
// GraphQL type/field name helpers
// =============================================================================

func (g *Generator) graphqlTypeName(t *gen.Type) string {
	if t == nil {
		return ""
	}
	ann := g.getTypeAnnotation(t)
	if name := ann.GetType(); name != "" {
		return name
	}
	return t.Name
}

func (g *Generator) graphqlFieldName(f *gen.Field) string {
	ann := g.getFieldAnnotation(f)
	if name := ann.GetFieldName(); name != "" {
		return name
	}
	return camel(f.Name)
}

// graphqlFieldType returns the GraphQL type for a field.
// If t is provided and the field is an edge FK field, returns ID type.
func (g *Generator) graphqlFieldType(t *gen.Type, f *gen.Field) string {
	ann := g.getFieldAnnotation(f)
	if customType := ann.GetType(); customType != "" {
		return customType
	}

	// Check custom scalar mapping function
	if g.config.MapScalarFunc != nil {
		if scalar := g.config.MapScalarFunc(t, f); scalar != "" {
			return scalar
		}
	}

	// If this is an edge FK field, return ID type (like Ent does)
	if t != nil && g.isEdgeFKField(t, f) {
		return ScalarID
	}

	if f.Type == nil {
		return ScalarString
	}

	// Check field type constant directly for special types
	if f.IsTime() {
		return ScalarTime
	}
	if f.IsUUID() {
		return ScalarUUID
	}
	if f.IsJSON() {
		// Try to infer GraphQL list type for slice types (e.g., []string -> [String!])
		if inferredType := g.inferGraphQLSliceType(f); inferredType != "" {
			return inferredType
		}
		// Use custom scalar for typed JSON fields (structs), generic JSON for untyped
		if scalarName := g.getTypedJSONScalarName(f); scalarName != "" {
			return scalarName
		}
		return ScalarJSON
	}
	if f.IsEnum() {
		// Enum type name is EntityFieldName format (e.g., CategoryStatus, TodoStatus)
		if t != nil {
			return t.Name + pascal(f.Name)
		}
		return pascal(f.Name)
	}
	// Use field.Type enum constants for robust type matching
	switch f.Type.Type {
	case field.TypeString:
		return ScalarString
	case field.TypeInt, field.TypeInt8, field.TypeInt16, field.TypeInt32, field.TypeInt64,
		field.TypeUint, field.TypeUint8, field.TypeUint16, field.TypeUint32, field.TypeUint64:
		return ScalarInt
	case field.TypeFloat32, field.TypeFloat64:
		return ScalarFloat
	case field.TypeBool:
		return ScalarBoolean
	case field.TypeBytes:
		return ScalarBytes
	default:
		return ScalarString
	}
}

// graphqlInputFieldType returns the GraphQL type for a field in input contexts.
// For input types, custom object types must fall back to JSON scalar since GraphQL
// only allows scalars, enums, and input objects in input types.
func (g *Generator) graphqlInputFieldType(t *gen.Type, f *gen.Field) string {
	ann := g.getFieldAnnotation(f)
	if customType := ann.GetType(); customType != "" {
		// Check if this custom type is valid in input context
		// Known valid scalars and standard types
		if g.isValidInputType(customType) {
			return customType
		}
		// Custom type references an object type — fall back to JSON for input.
		// This typically means the user annotated a field with graphql.Type("SomeObjectType")
		// which can't be used in input types. Use graphql.Type("SomeScalar") instead.
		slog.Warn("graphql: custom type not valid in input context, falling back to JSON",
			"type", customType, "field", f.Name)
		return ScalarJSON
	}
	// No custom type - use standard field type (which handles scalars correctly)
	return g.graphqlFieldType(t, f)
}

// isValidInputType checks if a GraphQL type name is valid in input context.
// GraphQL input types only allow scalars, enums, and other input objects — not
// object types. This function uses a priority-ordered fallback chain:
//
//  1. Known scalars (ID, String, Int, Float, Boolean, Time, UUID, JSON, etc.)
//  2. Explicit input types (names ending with "Input")
//  3. Generated enum types (cached in g.enumNames from buildEnumNamesCache)
//  4. All-uppercase names (convention for user-defined enums not in the cache)
//
// If none match, the type is assumed to be an object type (invalid for input).
// The caller falls back to the generic "JSON" scalar and logs a warning.
func (g *Generator) isValidInputType(typeName string) bool {
	// Strip list markers for inner type check
	innerType := strings.TrimPrefix(typeName, "[")
	innerType = strings.TrimSuffix(innerType, "!")
	innerType = strings.TrimSuffix(innerType, "]")
	innerType = strings.TrimSuffix(innerType, "!")

	if knownGraphQLScalars[innerType] {
		return true
	}

	// Check if it ends with "Input" (explicit input type)
	if strings.HasSuffix(innerType, "Input") {
		return true
	}

	// Check if it's all uppercase (likely an enum)
	if strings.ToUpper(innerType) == innerType && innerType != "" {
		return true
	}

	// Check if it's a generated enum type (cached O(1) lookup)
	// These are valid for input since enums are both input and output types
	if g.enumNames[innerType] {
		return true
	}

	// Unknown type - likely an object type, not valid for input
	return false
}

// =============================================================================
// Annotation extraction
// =============================================================================

// extractGraphQLAnnotation extracts an Annotation from an annotations map.
// It handles direct value, pointer, and JSON-marshaled interface types.
func extractGraphQLAnnotation(annotations map[string]any) Annotation {
	if annotations == nil {
		return Annotation{}
	}
	ann, ok := annotations[AnnotationName]
	if !ok {
		return Annotation{}
	}
	// Direct type assertion
	if a, ok := ann.(Annotation); ok {
		return a
	}
	// Pointer type assertion
	if a, ok := ann.(*Annotation); ok && a != nil {
		return *a
	}
	// JSON marshal/unmarshal fallback for interface types (e.g., schema.Annotation wrappers)
	data, err := json.Marshal(ann)
	if err != nil {
		slog.Warn("graphql: failed to marshal annotation for extraction",
			"type", fmt.Sprintf("%T", ann), "error", err)
		return Annotation{}
	}
	var a Annotation
	if err := json.Unmarshal(data, &a); err != nil {
		slog.Warn("graphql: failed to unmarshal annotation",
			"type", fmt.Sprintf("%T", ann), "error", err)
		return Annotation{}
	}
	return a
}

var graphqlFieldNameRe = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)

// GraphQL scalar type name constants. Use these instead of string literals
// to prevent typos and enable refactoring.
const (
	ScalarID      = "ID"
	ScalarString  = "String"
	ScalarInt     = "Int"
	ScalarFloat   = "Float"
	ScalarBoolean = "Boolean"
	ScalarTime    = "Time"
	ScalarUUID    = "UUID"
	ScalarJSON    = "JSON"
	ScalarBytes   = "Bytes"
	ScalarDecimal = "Decimal"
	ScalarCursor  = "Cursor"
	ScalarMap     = "Map"
	ScalarAny     = "Any"
)

// knownGraphQLScalars is the set of built-in GraphQL scalar types recognized in input validation.
var knownGraphQLScalars = map[string]bool{
	ScalarID: true, ScalarString: true, ScalarInt: true, ScalarFloat: true, ScalarBoolean: true,
	ScalarTime: true, ScalarUUID: true, ScalarJSON: true, ScalarBytes: true, ScalarDecimal: true,
	ScalarCursor: true, ScalarMap: true, ScalarAny: true,
}

// =============================================================================
// Validation helpers
// =============================================================================

// validateResolverMappings validates resolver mappings for an entity type.
func (g *Generator) validateResolverMappings(t *gen.Type) error {
	ann := g.getTypeAnnotation(t)
	if len(ann.ResolverMappings) == 0 {
		return nil
	}

	// Build set of existing GraphQL field names.
	existingFields := make(map[string]bool)
	for _, f := range t.Fields {
		existingFields[camel(f.Name)] = true
	}

	seen := make(map[string]bool)
	for _, rm := range ann.ResolverMappings {
		baseName := resolverBaseName(rm.FieldName)
		if baseName == "" {
			return fmt.Errorf("graphql: %s: resolver mapping has empty FieldName", t.Name)
		}
		if seen[baseName] {
			return fmt.Errorf("graphql: %s: duplicate resolver mapping FieldName %q", t.Name, baseName)
		}
		seen[baseName] = true

		if rm.ReturnType == "" {
			return fmt.Errorf("graphql: %s: Map(%q) has empty ReturnType", t.Name, baseName)
		}
		if !graphqlFieldNameRe.MatchString(baseName) {
			return fmt.Errorf("graphql: %s: Map(%q) FieldName is not a valid GraphQL identifier (must match ^[a-z][a-zA-Z0-9]*$)", t.Name, baseName)
		}
		// Check conflict with existing field (edge override is OK).
		if existingFields[baseName] {
			isEdge := false
			for _, e := range t.Edges {
				if camel(e.Name) == baseName {
					isEdge = true
					break
				}
			}
			if !isEdge {
				return fmt.Errorf("graphql: %s: Map(%q) conflicts with existing field", t.Name, baseName)
			}
		}
	}
	return nil
}

// =============================================================================
// Annotation accessors
// =============================================================================

func (g *Generator) getTypeAnnotation(t *gen.Type) Annotation {
	return extractGraphQLAnnotation(t.Annotations)
}

func (g *Generator) getFieldAnnotation(f *gen.Field) Annotation {
	return extractGraphQLAnnotation(f.Annotations)
}

func (g *Generator) getEdgeAnnotation(e *gen.Edge) Annotation {
	return extractGraphQLAnnotation(e.Annotations)
}

// =============================================================================
// Feature detection helpers
// =============================================================================

func (g *Generator) hasRelayConnection(t *gen.Type) bool {
	if !g.config.RelayConnection {
		return false
	}
	ann := g.getTypeAnnotation(t)
	// If RelayConnection is explicitly enabled, use it
	if ann.HasRelayConnection() {
		return true
	}
	// If QueryField is explicitly set but RelayConnection is not, use simple list
	if ann.HasQueryField() {
		return false
	}
	// Default: use connection if not skipped
	return !ann.IsSkipType()
}

func (g *Generator) wantsWhereInput(t *gen.Type) bool {
	ann := g.getTypeAnnotation(t)
	if !ann.WantsWhereInputs() {
		return false
	}
	// Skip generating WhereInput if the entity has no filterable fields or edges.
	return g.hasFilterableContent(t)
}

func (g *Generator) wantsOrderField(t *gen.Type) bool {
	ann := g.getTypeAnnotation(t)
	return ann.WantsOrderField()
}

// hasMultiOrder returns true if the entity supports multi-column ordering.
func (g *Generator) hasMultiOrder(t *gen.Type) bool {
	ann := g.getTypeAnnotation(t)
	return ann.HasMultiOrder()
}

// orderByArg returns the orderBy argument string for a type (array or single).
func (g *Generator) orderByArg(t *gen.Type) string {
	typeName := g.graphqlTypeName(t)
	if g.hasMultiOrder(t) {
		return fmt.Sprintf("orderBy: [%sOrder!]", typeName)
	}
	return fmt.Sprintf("orderBy: %sOrder", typeName)
}

func (g *Generator) wantsMutationCreate(t *gen.Type) bool {
	ann := g.getTypeAnnotation(t)
	return ann.WantsMutationCreate() && !ann.IsSkipMutationCreate()
}

func (g *Generator) wantsMutationUpdate(t *gen.Type) bool {
	ann := g.getTypeAnnotation(t)
	return ann.WantsMutationUpdate() && !ann.IsSkipMutationUpdate()
}

func (g *Generator) wantsMutationDelete(_ *gen.Type) bool {
	// Delete mutations are not yet implemented.
	return false
}

// modelPkg returns the import path for entity model types (@goModel directives).
// Connection, Edge, Order types live in the entity/ sub-package.
func (g *Generator) modelPkg() string {
	return g.config.ORMPackage + "/entity"
}

// =============================================================================
// Mutation input helpers
// =============================================================================

func (g *Generator) fieldInCreateInput(f *gen.Field) bool {
	// Check annotation first
	ann := g.getFieldAnnotation(f)
	if ann.HasFieldMutationOpsSet() {
		return ann.InCreateInput()
	}
	// Auto-exclude system-managed fields
	if g.isSystemManagedField(f) {
		return false
	}
	return ann.InCreateInput()
}

func (g *Generator) fieldInUpdateInput(f *gen.Field) bool {
	// Check annotation first
	ann := g.getFieldAnnotation(f)
	if ann.HasFieldMutationOpsSet() {
		return ann.InUpdateInput()
	}
	// Auto-exclude system-managed fields
	if g.isSystemManagedField(f) {
		return false
	}
	// Also exclude fields with UpdateDefault (auto-updated timestamps)
	if f.UpdateDefault {
		return false
	}
	return ann.InUpdateInput()
}

// isSystemManagedField returns true if this field is typically managed by the system
// and should not appear in mutation inputs (unless explicitly configured via annotations).
// Detection is metadata-based: time fields with Default or UpdateDefault are system-managed
// (e.g., created_at with Default(time.Now), updated_at with UpdateDefault(time.Now)).
func (g *Generator) isSystemManagedField(f *gen.Field) bool {
	// Time fields with a default (typically time.Now for created_at) are system-managed.
	if f.IsTime() && f.Default {
		return true
	}
	// Fields with UpdateDefault (typically time.Now for updated_at) are system-managed.
	if f.UpdateDefault {
		return true
	}
	return false
}

func (g *Generator) getOrderFieldName(f *gen.Field) string {
	ann := g.getFieldAnnotation(f)
	if name := ann.GetOrderField(); name != "" {
		return name
	}
	return strings.ToUpper(toSnakeCase(f.Name))
}

// =============================================================================
// Directive helpers
// =============================================================================

func (g *Generator) typeDirectives(t *gen.Type) string {
	ann := g.getTypeAnnotation(t)
	return renderDirectives(ann.GetDirectives())
}

// fieldDirectives returns user-defined directives (e.g., @deprecated) for a field.
// These are separate from @goField directives which are handled by buildGoFieldDirective.
func (g *Generator) fieldDirectives(f *gen.Field) string {
	ann := g.getFieldAnnotation(f)
	return renderDirectives(ann.GetDirectives())
}

// renderDirectives renders a list of Directive values as SDL directive strings.
// Returns empty string if no directives, otherwise " @dir1 @dir2(arg: val)".
func renderDirectives(dirs []Directive) string {
	if len(dirs) == 0 {
		return ""
	}

	var parts []string
	for _, d := range dirs {
		if len(d.Args) == 0 {
			parts = append(parts, "@"+d.Name)
		} else {
			// Sort keys for deterministic output
			keys := make([]string, 0, len(d.Args))
			for k := range d.Args {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			var args []string
			for _, k := range keys {
				args = append(args, fmt.Sprintf("%s: %s", k, formatDirectiveArg(d.Args[k])))
			}
			parts = append(parts, fmt.Sprintf("@%s(%s)", d.Name, strings.Join(args, ", ")))
		}
	}
	return " " + strings.Join(parts, " ")
}

// formatDirectiveArg formats a directive argument value for SDL output.
// Strings are quoted, booleans and numbers use their literal form.
// Unsupported types (maps, slices, structs) are quoted as strings
// to prevent invalid SDL from schema annotations.
func formatDirectiveArg(v any) string {
	switch val := v.(type) {
	case string:
		return fmt.Sprintf("%q", val)
	case bool:
		return fmt.Sprintf("%t", val)
	case int:
		return fmt.Sprintf("%d", val)
	case int8:
		return fmt.Sprintf("%d", val)
	case int16:
		return fmt.Sprintf("%d", val)
	case int32:
		return fmt.Sprintf("%d", val)
	case int64:
		return fmt.Sprintf("%d", val)
	case uint:
		return fmt.Sprintf("%d", val)
	case uint8:
		return fmt.Sprintf("%d", val)
	case uint16:
		return fmt.Sprintf("%d", val)
	case uint32:
		return fmt.Sprintf("%d", val)
	case uint64:
		return fmt.Sprintf("%d", val)
	case float32:
		return fmt.Sprintf("%g", val)
	case float64:
		return fmt.Sprintf("%g", val)
	default:
		// Unknown types are quoted as strings to prevent SDL injection.
		// Maps, slices, and structs are not valid GraphQL directive arguments.
		slog.Warn("graphql: unsupported directive argument type, using string representation",
			"type", fmt.Sprintf("%T", v), "value", v)
		return fmt.Sprintf("%q", fmt.Sprintf("%v", v))
	}
}

// =============================================================================
// ORM type reference helpers
// =============================================================================

// ormTypePtr returns a pointer to an ORM type.
func (g *Generator) ormTypePtr(typeName string) jen.Code {
	if g.samePackage {
		return jen.Op("*").Id(typeName)
	}
	return jen.Op("*").Qual(g.config.ORMPackage, typeName)
}

// =============================================================================
// String utilities
// =============================================================================

// toSnakeCase converts a PascalCase string to snake_case.
func toSnakeCase(s string) string {
	var result strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				result.WriteByte('_')
			}
			result.WriteRune(unicode.ToLower(r))
		} else {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// pascal delegates to gen.Pascal so that Go type names in the GraphQL package
// use the same acronym/initialism rules as the ORM generator. Users can call
// gen.AddAcronym("GL") etc. to register domain-specific initialisms that apply
// to both the ORM and GraphQL output.
func pascal(s string) string {
	return gen.Pascal(s)
}

func camel(s string) string {
	// Split by underscore first
	parts := strings.Split(s, "_")
	var words []string
	for _, part := range parts {
		// Split PascalCase into words
		words = append(words, splitPascal(part)...)
	}
	if len(words) == 0 {
		return ""
	}
	var result strings.Builder
	for i, w := range words {
		if w == "" {
			continue
		}
		lower := strings.ToLower(w)
		if i == 0 {
			// First word: always lowercase (even acronyms like ID → id)
			result.WriteString(lower)
		} else {
			// Subsequent words: title case (e.g., URL → Url, ID → Id)
			// GraphQL convention is camelCase, not Go initialisms
			result.WriteString(strings.ToUpper(lower[:1]) + lower[1:])
		}
	}
	return result.String()
}

// splitPascal splits a PascalCase string into words.
// e.g., "UserName" → ["User", "Name"], "HTTPServer" → ["HTTP", "Server"]
func splitPascal(s string) []string {
	if s == "" {
		return nil
	}
	runes := []rune(s)
	var words []string
	var word strings.Builder
	for i, r := range runes {
		if i > 0 && r >= 'A' && r <= 'Z' {
			// Check if this is part of an acronym (consecutive uppercase)
			if word.Len() > 0 {
				prev := runes[i-1]
				if prev >= 'A' && prev <= 'Z' {
					// Previous was also uppercase - might be end of acronym
					// Look ahead to see if next is lowercase
					if i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z' {
						// End of acronym, start new word
						words = append(words, word.String())
						word.Reset()
					}
				} else {
					// Previous was lowercase, this starts a new word
					words = append(words, word.String())
					word.Reset()
				}
			}
		}
		word.WriteRune(r)
	}
	if word.Len() > 0 {
		words = append(words, word.String())
	}
	return words
}

// ruleset is a custom inflect ruleset with additional irregular nouns
// that the default go-openapi/inflect rules miss.
var ruleset *inflect.Ruleset

func init() {
	ruleset = inflect.NewDefaultRuleset()
	ruleset.AddPlural("bus", "buses")
	ruleset.AddIrregular("person", "people")
	ruleset.AddIrregular("child", "children")
	ruleset.AddIrregular("mouse", "mice")
	ruleset.AddIrregular("goose", "geese")
	// PascalCase variants (inflect is case-sensitive)
	ruleset.AddPlural("Bus", "Buses")
	ruleset.AddIrregular("Person", "People")
	ruleset.AddIrregular("Child", "Children")
	ruleset.AddIrregular("Mouse", "Mice")
	ruleset.AddIrregular("Goose", "Geese")
}

func pluralize(s string) string {
	return ruleset.Pluralize(s)
}

// =============================================================================
// Federation helpers
// =============================================================================

// hasFederationEntities reports whether any entity has federation directives (@key).
func (g *Generator) hasFederationEntities() bool {
	if g.config.Federation {
		return true
	}
	for _, t := range g.graph.Nodes {
		ann := g.getTypeAnnotation(t)
		for _, d := range ann.GetDirectives() {
			if d.Name == "key" {
				return true
			}
		}
	}
	return false
}

// federationLinkDirective returns the Federation v2 @link schema extension.
func federationLinkDirective() string {
	return `extend schema @link(url: "https://specs.apollo.dev/federation/v2.0", import: ["@key", "@external", "@requires", "@provides", "@shareable", "@inaccessible", "@override"])`
}

// collectSubscriptions collects all subscription field definitions across all entities.
func (g *Generator) collectSubscriptions() []SubscriptionConfig {
	var subs []SubscriptionConfig
	for _, t := range g.graph.Nodes {
		ann := g.getTypeAnnotation(t)
		subs = append(subs, ann.Subscriptions...)
	}
	return subs
}

// genSubscriptionType generates the `type Subscription { ... }` SDL block.
// Returns empty string if no entities define subscriptions.
func (g *Generator) genSubscriptionType() string {
	subs := g.collectSubscriptions()
	if len(subs) == 0 {
		return ""
	}

	var buf bytes.Buffer
	buf.WriteString("type Subscription {\n")
	for _, sub := range subs {
		if sub.Description != "" {
			fmt.Fprintf(&buf, "  \"\"\"\n  %s\n  \"\"\"\n", sub.Description)
		}
		if sub.Args != "" {
			fmt.Fprintf(&buf, "  %s(%s): %s\n", sub.Name, sub.Args, sub.ReturnType)
		} else {
			fmt.Fprintf(&buf, "  %s: %s\n", sub.Name, sub.ReturnType)
		}
	}
	buf.WriteString("}\n")
	return buf.String()
}
