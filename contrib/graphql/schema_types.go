package graphql

import (
	"bytes"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/syssam/velox/compiler/gen"
)

// typedJSONScalar holds information about a typed JSON scalar.
type typedJSONScalar struct {
	ScalarName  string // GraphQL scalar name (e.g., "Address")
	GoType      string // Full Go type path for @goModel (e.g., "example.com/myapp/velox.Address")
	OrigPkgPath string // Original package path (e.g., "example.com/myapp/schema")
	TypeName    string // Type name without package (e.g., "Address")
}

// collectTypedJSONScalars collects all unique typed JSON fields that need custom scalars.
// Returns a map from Go type identifier to scalar info.
func (g *Generator) collectTypedJSONScalars() map[string]typedJSONScalar {
	scalars := make(map[string]typedJSONScalar)

	for _, t := range g.graph.Nodes {
		for _, f := range t.Fields {
			if !f.IsJSON() || !f.HasGoType() {
				continue
			}
			if f.Type == nil || f.Type.Ident == "" {
				continue
			}

			// Skip fields with SkipAll annotation - they're not exposed in GraphQL
			ann := g.getFieldAnnotation(f)
			if ann.Skip&SkipAll != 0 {
				continue
			}

			// Skip fields with explicit graphql.Type() annotation - user controls the type
			// e.g., graphql.Type("[Permission!]") means Permission is defined elsewhere
			if ann.Type != "" {
				continue
			}

			ident := f.Type.Ident

			// Handle slice types - extract element type for custom scalar registration
			// e.g., "[]schema.Address" or "[]*schema.Address" -> "schema.Address"
			if elemType, ok := strings.CutPrefix(ident, "[]"); ok {
				elemType, _ = strings.CutPrefix(elemType, "*")

				// Skip primitives - they don't need scalar registration
				if g.isPrimitiveGoType(elemType) {
					continue
				}

				// Skip nested slices, maps, and generic types
				if strings.HasPrefix(elemType, "[]") ||
					strings.HasPrefix(elemType, "map[") ||
					elemType == "interface{}" ||
					elemType == "any" {
					continue
				}

				// Use element type for scalar registration
				ident = elemType
			}

			// Skip maps and generic types
			if strings.HasPrefix(ident, "map[") ||
				ident == "interface{}" ||
				ident == "any" {
				continue
			}

			// Create scalar name from type name
			// e.g., "schema.Address" → "Address", "*schema.Address" → "Address"
			scalarName := ident
			scalarName = strings.TrimPrefix(scalarName, "*")
			if idx := strings.LastIndex(scalarName, "."); idx >= 0 {
				scalarName = scalarName[idx+1:]
			}

			// Extract original package path and type name
			origPkgPath := f.Type.PkgPath
			typeName := strings.TrimPrefix(f.Type.Ident, "*")
			if idx := strings.LastIndex(typeName, "."); idx >= 0 {
				typeName = typeName[idx+1:]
			}

			// The @goModel directive must point to the ORM package where we generate the type alias
			// and marshalers, not the original type's package
			// e.g., "example.com/myapp/velox.Address" instead of "example.com/myapp/schema.Address"
			goType := g.config.ORMPackage + "." + scalarName

			// Use original type path as key to handle same-named types from different packages
			key := origPkgPath + "." + typeName
			if _, exists := scalars[key]; !exists {
				scalars[key] = typedJSONScalar{
					ScalarName:  scalarName,
					GoType:      goType,
					OrigPkgPath: origPkgPath,
					TypeName:    typeName,
				}
			}
		}
	}

	return scalars
}

// getTypedJSONScalarName returns the custom scalar name for a typed JSON field,
// or empty string if it's a generic JSON field.
func (g *Generator) getTypedJSONScalarName(f *gen.Field) string {
	if !f.IsJSON() || !f.HasGoType() {
		return ""
	}
	if f.Type == nil || f.Type.Ident == "" {
		return ""
	}

	// Skip generic JSON types
	ident := f.Type.Ident
	if strings.HasPrefix(ident, "map[") ||
		strings.HasPrefix(ident, "[]") ||
		ident == "interface{}" ||
		ident == "any" {
		return ""
	}

	// Get scalar name
	scalarName := ident
	scalarName = strings.TrimPrefix(scalarName, "*")
	if idx := strings.LastIndex(scalarName, "."); idx >= 0 {
		scalarName = scalarName[idx+1:]
	}

	return scalarName
}

// inferGraphQLSliceType returns the GraphQL list type for a JSON slice field.
// Returns empty string if the type cannot be inferred (maps, nested slices, etc).
// Examples:
//
//	[]string     -> [String!]
//	[]int        -> [Int!]
//	[]*Address   -> [Address!]
//	map[string]any -> "" (no inference)
func (g *Generator) inferGraphQLSliceType(f *gen.Field) string {
	if f.Type == nil || f.Type.Ident == "" {
		return ""
	}

	ident := f.Type.Ident

	// Check both string prefix and RType.Kind for robust slice detection (like Ent)
	isSlice := strings.HasPrefix(ident, "[]")
	if f.Type.RType != nil {
		isSlice = isSlice || f.Type.RType.Kind == reflect.Slice || f.Type.RType.Kind == reflect.Array
	}
	if !isSlice {
		return ""
	}

	// Extract element type: "[]T" or "[]*T" -> "T"
	elemType := strings.TrimPrefix(ident, "[]")
	elemType = strings.TrimPrefix(elemType, "*")

	// Skip nested slices, maps, and generic types
	if strings.HasPrefix(elemType, "[]") ||
		strings.HasPrefix(elemType, "map[") ||
		elemType == "interface{}" ||
		elemType == "any" {
		return ""
	}

	// Map Go type to GraphQL type
	gqlType := g.goTypeToGraphQL(elemType)
	if gqlType == "" {
		return ""
	}

	return "[" + gqlType + "!]"
}

// goTypeToGraphQL maps a Go element type to GraphQL scalar type.
func (g *Generator) goTypeToGraphQL(goType string) string {
	switch goType {
	case "string":
		return ScalarString
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return ScalarInt
	case "float32", "float64":
		return ScalarFloat
	case "bool":
		return ScalarBoolean
	case "time.Time":
		return ScalarTime
	}

	// UUID types
	if goType == "uuid.UUID" || strings.HasSuffix(goType, ".UUID") {
		return ScalarUUID
	}

	// Custom struct types: "pkg.Type" -> "Type"
	if strings.Contains(goType, ".") {
		parts := strings.Split(goType, ".")
		return parts[len(parts)-1]
	}

	// Simple custom type (PascalCase = likely a struct)
	if goType != "" && goType[0] >= 'A' && goType[0] <= 'Z' {
		return goType
	}

	return ""
}

// isPrimitiveGoType returns true if the Go type is a primitive that doesn't need
// custom scalar registration.
func (g *Generator) isPrimitiveGoType(t string) bool {
	switch t {
	case "string", "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64", "bool", "time.Time":
		return true
	}
	if t == "uuid.UUID" || strings.HasSuffix(t, ".UUID") {
		return true
	}
	return false
}

// genScalarsSchema generates custom scalar definitions.
func (g *Generator) genScalarsSchema() string {
	var buf bytes.Buffer
	buf.WriteString("directive @goField(forceResolver: Boolean, name: String, omittable: Boolean) on FIELD_DEFINITION | INPUT_FIELD_DEFINITION\n")
	buf.WriteString("directive @goModel(model: String, models: [String!], forceGenerate: Boolean) on OBJECT | INPUT_OBJECT | SCALAR | ENUM | INTERFACE | UNION\n")
	buf.WriteString("\n")
	// Custom scalars with descriptions (like Ent)
	buf.WriteString(`"""
Define a Relay Cursor type:
https://relay.dev/graphql/connections.htm#sec-Cursor
"""
`)
	if g.config.ORMPackage != "" {
		fmt.Fprintf(&buf, "scalar Cursor @goModel(model: \"%s.Cursor\")\n", gqlrelayPkg)
	} else {
		buf.WriteString("scalar Cursor\n")
	}
	buf.WriteString(`"""
The builtin Time type
"""
scalar Time
`)
	// Emit scalar Bytes if any entity uses a Bytes field.
	if g.hasBytesField() {
		buf.WriteString(`"""
The builtin Bytes type (base64-encoded)
"""
scalar Bytes
`)
	}
	// Generate custom scalars for typed JSON fields (sorted for deterministic output)
	scalars := g.getTypedJSONScalarsCache()
	scalarKeys := make([]string, 0, len(scalars))
	for k := range scalars {
		scalarKeys = append(scalarKeys, k)
	}
	slices.Sort(scalarKeys)
	for _, k := range scalarKeys {
		scalar := scalars[k]
		buf.WriteString(`"""
Custom JSON type.
"""
`)
		fmt.Fprintf(&buf, "scalar %s @goModel(model: \"%s\")\n", scalar.ScalarName, scalar.GoType)
	}

	return buf.String()
}

// genNodeInterface generates the Relay Node interface.
func (g *Generator) genNodeInterface() string {
	var buf bytes.Buffer
	buf.WriteString(`"""
An object with an ID.
Follows the [Relay Global Object Identification Specification](https://relay.dev/graphql/objectidentification.htm)
"""
`)
	if g.config.ORMPackage != "" {
		fmt.Fprintf(&buf, `interface Node @goModel(model: "%s.Noder") {`, g.config.ORMPackage)
	} else {
		buf.WriteString("interface Node {")
	}
	buf.WriteString(`
  """
  The id of the object.
  """
  id: ID!
}
`)
	return buf.String()
}

// genTypesSchema generates GraphQL types for all entities.
func (g *Generator) genTypesSchema() string {
	var buf bytes.Buffer

	// Generate enum types first
	enumTypes := g.genEnumTypes()
	if enumTypes != "" {
		buf.WriteString(enumTypes)
		buf.WriteString("\n")
	}

	buf.WriteString("# Entity types\n\n")

	// Use filterNodes to get nodes that should be included (like Ent)
	nodes := g.filterNodes(g.graph.Nodes, SkipType)
	for _, t := range nodes {
		buf.WriteString(g.genEntityType(t))
		buf.WriteString("\n")
	}

	return buf.String()
}

// genEntityEnumTypes generates enum type definitions for a single entity's fields.
// Shared enums (same name across entities with identical values) are emitted only
// by the first entity and placed in the root schema file in per-entity mode.
func (g *Generator) genEntityEnumTypes(t *gen.Type) string {
	var buf bytes.Buffer
	fields := g.filterFields(t.Fields, SkipEnumField)
	for _, f := range fields {
		if !f.IsEnum() {
			continue
		}
		enumName := t.Name + pascal(f.Name)
		ann := g.getFieldAnnotation(f)
		if customType := ann.GetType(); customType != "" {
			enumName = customType
		}
		// Skip shared enums entirely — they go in the root file.
		if _, shared := g.sharedEnums[enumName]; shared {
			continue
		}
		buf.WriteString(g.genEnumType(t, f))
		buf.WriteString("\n")
	}
	return buf.String()
}

// genEnumTypes generates GraphQL enum type definitions for all enum fields.
func (g *Generator) genEnumTypes() string {
	var buf bytes.Buffer
	generatedEnums := make(map[string]bool)

	// Use filterNodes helper (like Ent)
	nodes := g.filterNodes(g.graph.Nodes, SkipType)
	for _, t := range nodes {
		// Use filterFields to exclude skipped fields
		fields := g.filterFields(t.Fields, SkipEnumField)
		for _, f := range fields {
			if !f.IsEnum() {
				continue
			}
			// Check for custom GraphQL type name (for shared enums like schematype.ItemType)
			ann := g.getFieldAnnotation(f)
			enumName := t.Name + pascal(f.Name)
			if customType := ann.GetType(); customType != "" {
				enumName = customType
			}
			if generatedEnums[enumName] {
				continue
			}
			generatedEnums[enumName] = true
			buf.WriteString(g.genEnumType(t, f))
			buf.WriteString("\n")
		}
	}

	return buf.String()
}

// genEnumType generates a GraphQL enum type definition.
func (g *Generator) genEnumType(t *gen.Type, f *gen.Field) string {
	var buf bytes.Buffer

	// Get field annotation for custom type name and enum value mapping
	ann := g.getFieldAnnotation(f)
	enumMapping := ann.GetEnumValues()

	// Check for custom GraphQL type name (for shared enums like schematype.ItemType)
	enumName := t.Name + pascal(f.Name)
	if customType := ann.GetType(); customType != "" {
		enumName = customType
	}

	// Add @goModel directive to bind to ORM enum type
	// For shared enums with GoType, use the GoType package path
	// For entity-specific enums, use the entity package
	goModel := ""
	if g.config.ORMPackage != "" {
		if f.HasGoType() && f.Type != nil && f.Type.PkgPath != "" {
			// Shared enum with GoType - extract the type name from the full ident
			typeName := f.Type.Ident
			if idx := strings.LastIndex(typeName, "."); idx >= 0 {
				typeName = typeName[idx+1:]
			}
			goModel = fmt.Sprintf(` @goModel(model: "%s.%s")`, f.Type.PkgPath, typeName)
		} else {
			// Entity-specific enum
			entityPkg := strings.ToLower(t.Name)
			goModel = fmt.Sprintf(` @goModel(model: "%s/%s.%s")`, g.config.ORMPackage, entityPkg, pascal(f.Name))
		}
	}

	// Enum type description (like Ent)
	fmt.Fprintf(&buf, `"""%s is enum for the field %s
"""
`, enumName, f.Name)
	fmt.Fprintf(&buf, "enum %s%s {\n", enumName, goModel)
	for _, v := range f.EnumValues() {
		// Use custom mapping if provided, otherwise auto-uppercase
		gqlValue := g.graphqlEnumValue(v, enumMapping)
		fmt.Fprintf(&buf, "  %s\n", gqlValue)
	}
	buf.WriteString("}\n")

	return buf.String()
}

// graphqlEnumValue returns the GraphQL enum value for a database value.
// If a custom mapping exists, it uses the mapped value.
// Otherwise, it automatically converts to SCREAMING_SNAKE_CASE (uppercase).
func (g *Generator) graphqlEnumValue(dbValue string, mapping map[string]string) string {
	if mapping != nil {
		if gqlValue, ok := mapping[dbValue]; ok {
			return gqlValue
		}
	}
	// Default: convert to uppercase (GraphQL enum convention)
	return strings.ToUpper(dbValue)
}

// genEntityType generates a GraphQL type for an entity.
func (g *Generator) genEntityType(t *gen.Type) string {
	var buf bytes.Buffer

	typeName := g.graphqlTypeName(t)

	// Build implements clause using nodeImplementors helper (like Ent)
	implementsList := g.nodeImplementors(t)
	implements := ""
	if len(implementsList) > 0 {
		implements = " implements " + strings.Join(implementsList, " & ")
	}

	// Type directives (user-defined + @goModel for autobind)
	// Entity structs live in the entity/ subpackage, so @goModel must point there.
	directives := g.typeDirectives(t)
	if g.config.ORMPackage != "" {
		directives = fmt.Sprintf(" @goModel(model: \"%s/entity.%s\")%s", g.config.ORMPackage, t.Name, directives)
	}

	fmt.Fprintf(&buf, "type %s%s%s {\n", typeName, implements, directives)

	// ID field
	buf.WriteString("  id: ID!\n")

	// Regular fields - use filterFields helper (like Ent)
	fields := g.filterFields(t.Fields, SkipType)
	for _, f := range fields {
		// Check if this field is an edge FK field that should be skipped
		if g.isEdgeFKField(t, f) && g.shouldSkipEdgeFKField(t, f) {
			continue
		}
		fmt.Fprintf(&buf, "  %s\n", g.genField(t, f))
	}

	// Edge fields - use filterEdges helper (like Ent)
	edges := g.filterEdges(t.Edges, SkipType)
	for _, e := range edges {
		fmt.Fprintf(&buf, "  %s\n", g.genEdgeField(t, e))
	}

	// Resolver mapping fields (add new fields with @goField(forceResolver: true))
	entityAnn := g.getTypeAnnotation(t)
	for _, rm := range entityAnn.ResolverMappings {
		if rm.Comment != "" {
			fmt.Fprintf(&buf, "  \"\"\"\n  %s\n  \"\"\"\n", rm.Comment)
		}
		fmt.Fprintf(&buf, "  %s: %s @goField(forceResolver: true)\n",
			rm.FieldName, rm.ReturnType)
	}

	buf.WriteString("}\n")
	return buf.String()
}

// isEdgeFKField returns true if the field is a foreign key field for an edge.
func (g *Generator) isEdgeFKField(t *gen.Type, f *gen.Field) bool {
	// Direct check if the field is marked as an edge field (has fk info)
	if f.IsEdgeField() {
		return true
	}
	// Check if any edge explicitly references this field by name
	for _, e := range t.Edges {
		if ef := e.Field(); ef != nil && ef.Name == f.Name {
			return true
		}
	}
	// Heuristic fallback: check if field name matches common FK patterns
	// Fields ending with _id that are numeric and have a matching edge
	if f.Type != nil && f.Type.Numeric() && strings.HasSuffix(f.Name, "_id") {
		// Try to find a matching edge by stripping _id suffix
		edgeName := strings.TrimSuffix(f.Name, "_id")
		for _, e := range t.Edges {
			if e.Name == edgeName {
				return true
			}
		}
	}
	return false
}

// shouldSkipEdgeFKField returns true if the edge FK field should be skipped from the GraphQL type.
// Edge FK fields are skipped by default (the edge itself provides the relationship).
// A field is NOT skipped if it has an explicit graphql.Type() or graphql.FieldName() annotation,
// indicating the user wants to expose the FK field directly.
func (g *Generator) shouldSkipEdgeFKField(_ *gen.Type, f *gen.Field) bool {
	ann := g.getFieldAnnotation(f)
	// If the field has an explicit annotation to include it, don't skip
	if ann.GetType() != "" || ann.GetFieldName() != "" {
		return false
	}
	// Default: skip edge FK fields (the edge already exposes the relationship)
	return true
}

// genField generates a GraphQL field definition.
func (g *Generator) genField(t *gen.Type, f *gen.Field) string {
	name := g.graphqlFieldName(f)
	typeName := g.graphqlFieldType(t, f)
	nullable := ""
	if !f.Optional && !f.Nillable {
		nullable = "!"
	}
	directive := g.buildGoFieldDirective(f)
	// Add user-defined directives (e.g., @deprecated) after @goField
	directive += g.fieldDirectives(f)
	fieldDef := fmt.Sprintf("%s: %s%s%s", name, typeName, nullable, directive)

	comment := f.Comment()
	if comment != "" {
		return fmt.Sprintf("\"\"\"\n  %s\n  \"\"\"\n  %s", comment, fieldDef)
	}
	return fieldDef
}

// buildGoFieldDirective consolidates all @goField params for a field.
func (g *Generator) buildGoFieldDirective(f *gen.Field) string {
	ann := g.getFieldAnnotation(f)
	var parts []string

	// Field name mapping: emit @goField(name: "GoFieldName") so gqlgen
	// maps the GraphQL field to the correct Go struct field.
	if ann.GetFieldName() != "" && ann.GetFieldName() != camel(f.Name) {
		parts = append(parts, fmt.Sprintf("name: %q", pascal(ann.GetFieldName())))
	}

	if len(parts) == 0 {
		return ""
	}
	return " @goField(" + strings.Join(parts, ", ") + ")"
}
