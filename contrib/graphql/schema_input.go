package graphql

import (
	"bytes"
	"fmt"

	"github.com/syssam/velox/compiler/gen"
)

// writeConnectionBaseArgs writes the common Relay pagination arguments (after, first, before, last)
// to the given buffer. Used by both edge-level and query-level connection arg generators.
func writeConnectionBaseArgs(buf *bytes.Buffer) {
	buf.WriteString("(\n")
	buf.WriteString(`    """
    Returns the elements in the list that come after the specified cursor.
    """
    after: Cursor

    """
    Returns the first _n_ elements from the list.
    """
    first: Int

    """
    Returns the elements in the list that come before the specified cursor.
    """
    before: Cursor

    """
    Returns the last _n_ elements from the list.
    """
    last: Int

`)
}

// genConnectionArgs generates documented connection arguments.
// wantsWhere indicates whether the where parameter should be included.
func (g *Generator) genConnectionArgs(targetType string, orderByArg string, wantsOrder, wantsWhere bool) string {
	var buf bytes.Buffer
	writeConnectionBaseArgs(&buf)
	if targetType != "" {
		// Ent order: orderBy before where
		if wantsOrder {
			fmt.Fprintf(&buf, `    """
    Ordering options for %s returned from the connection.
    """
    %s

`, pluralize(targetType), orderByArg)
		}
		if wantsWhere {
			fmt.Fprintf(&buf, `    """
    Filtering options for %s returned from the connection.
    """
    where: %sWhereInput
`, pluralize(targetType), targetType)
		}
	}
	buf.WriteString("  )")
	return buf.String()
}

// genEdgeField generates a GraphQL field for an edge.
func (g *Generator) genEdgeField(_ *gen.Type, e *gen.Edge) string {
	name := camel(e.Name)
	targetType := g.graphqlTypeName(e.Type)
	comment := e.Comment()

	var fieldDef string
	if e.Unique {
		// Unique edge: returns single entity
		// Required edges are non-null, optional edges are nullable per Relay spec
		if !e.Optional {
			fieldDef = fmt.Sprintf("%s: %s!", name, targetType)
		} else {
			fieldDef = fmt.Sprintf("%s: %s", name, targetType)
		}
	} else if g.config.RelayConnection && g.hasRelayConnection(e.Type) {
		// Non-unique edge: returns connection or list
		orderByArg := g.orderByArg(e.Type)
		wantsOrder := g.config.Ordering && g.wantsOrderField(e.Type)
		wantsWhere := g.config.WhereInputs && g.hasWhereInput(e)
		args := g.genConnectionArgs(targetType, orderByArg, wantsOrder, wantsWhere)
		fieldDef = fmt.Sprintf("%s%s: %sConnection!", name, args, targetType)
	} else {
		fieldDef = fmt.Sprintf("%s: [%s!]!", name, targetType)
	}

	if comment != "" {
		return fmt.Sprintf("\"\"\"\n  %s\n  \"\"\"\n  %s", comment, fieldDef)
	}
	return fieldDef
}

// genInputsSchema generates all input types.
func (g *Generator) genInputsSchema() string {
	var buf bytes.Buffer
	buf.WriteString("# Input types\n\n")

	// OrderDirection enum with description (like Ent)
	if g.config.Ordering {
		buf.WriteString(`"""
Possible directions in which to order a list of items when provided an ` + "`orderBy`" + ` argument.
"""
enum OrderDirection {
  """
  Specifies an ascending order for a given ` + "`orderBy`" + ` argument.
  """
  ASC
  """
  Specifies a descending order for a given ` + "`orderBy`" + ` argument.
  """
  DESC
}

`)
	}

	// Use filterNodes to get nodes that should be included (like Ent)
	nodes := g.filterNodes(g.graph.Nodes, SkipType)
	for _, t := range nodes {
		// WhereInput - use filterNodes with SkipWhereInput
		if g.config.WhereInputs && g.wantsWhereInput(t) {
			buf.WriteString(g.genWhereInput(t))
			buf.WriteString("\n")
		}

		// OrderBy - only for Relay connection entities (non-Relay use simple list queries)
		if g.config.Ordering && g.wantsOrderField(t) && g.hasRelayConnection(t) {
			buf.WriteString(g.genOrderBy(t))
			buf.WriteString("\n")
		}

		// Generate mutation input types (CreateXXXInput, UpdateXXXInput)
		// These are needed for gqlgen schema validation even though Go structs are bound via gqlgen.yml
		if g.config.Mutations {
			if g.wantsMutationCreate(t) {
				buf.WriteString(g.genCreateInput(t))
				buf.WriteString("\n")
			}
			if g.wantsMutationUpdate(t) {
				buf.WriteString(g.genUpdateInput(t))
				buf.WriteString("\n")
			}
		}
	}

	return buf.String()
}

// genCreateInput generates the CreateXXXInput type for a mutation.
func (g *Generator) genCreateInput(t *gen.Type) string {
	var buf bytes.Buffer
	typeName := g.graphqlTypeName(t)
	inputName := "Create" + typeName + "Input"

	// Add @goModel directive for autobind — CreateInput is defined in entity sub-package.
	// Go struct name uses t.Name (not graphqlTypeName) since genEntityMutationInputImpl uses t.Name.
	if g.config.ORMPackage != "" {
		goStructName := "Create" + t.Name + "Input"
		fmt.Fprintf(&buf, "input %s @goModel(model: \"%s.%s\") {\n", inputName, g.entityPkgPath(t), goStructName)
	} else {
		fmt.Fprintf(&buf, "input %s {\n", inputName)
	}

	// Include fields that should be in create input
	fields := g.filterFields(t.Fields, SkipMutationCreateInput)
	for _, f := range fields {
		if !g.fieldInCreateInput(f) {
			continue
		}
		fieldName := g.graphqlFieldName(f)
		fieldType := g.graphqlInputFieldType(t, f)

		// Required fields (not optional/nillable and no default)
		required := !f.Optional && !f.Nillable && !f.Default
		directive := ""
		if g.isFieldOmittable(f) {
			directive = " @goField(omittable: true)"
		}
		if required {
			fmt.Fprintf(&buf, "  %s: %s!%s\n", fieldName, fieldType, directive)
		} else {
			fmt.Fprintf(&buf, "  %s: %s%s\n", fieldName, fieldType, directive)
		}
	}

	// Edge FK fields with explicit .Field("xxx") are already included via t.Fields.
	// For edges without explicit FK fields, add edge ID fields to match the Go struct.
	for _, edge := range t.Edges {
		if !g.edgeInCreateInput(edge) {
			continue
		}
		if edge.Unique {
			fieldName := camel(edge.Name) + "ID"
			if edge.Optional {
				fmt.Fprintf(&buf, "  %s: ID\n", fieldName)
			} else {
				fmt.Fprintf(&buf, "  %s: ID!\n", fieldName)
			}
		} else {
			// Multi-edge: MutationAdd() returns "AddChildIDs" → struct field "ChildIDs" → camelCase
			structField := edge.MutationAdd()[3:] // strip "Add" prefix
			fieldName := camel(structField)
			fmt.Fprintf(&buf, "  %s: [ID!]\n", fieldName)
		}
	}

	buf.WriteString("}\n")
	return buf.String()
}

// genUpdateInput generates the UpdateXXXInput type for a mutation.
func (g *Generator) genUpdateInput(t *gen.Type) string {
	var buf bytes.Buffer
	typeName := g.graphqlTypeName(t)
	inputName := "Update" + typeName + "Input"

	// Add @goModel directive for autobind — UpdateInput is defined in entity sub-package.
	// Go struct name uses t.Name (not graphqlTypeName) since genEntityMutationInputImpl uses t.Name.
	if g.config.ORMPackage != "" {
		goStructName := "Update" + t.Name + "Input"
		fmt.Fprintf(&buf, "input %s @goModel(model: \"%s.%s\") {\n", inputName, g.entityPkgPath(t), goStructName)
	} else {
		fmt.Fprintf(&buf, "input %s {\n", inputName)
	}

	// Track if we've added any fields
	hasFields := false

	// All fields are optional in update input
	fields := g.filterFields(t.Fields, SkipMutationUpdateInput)
	for _, f := range fields {
		// Skip immutable fields (like Go struct generation does)
		if f.Immutable || !g.fieldInUpdateInput(f) {
			continue
		}
		fieldName := g.graphqlFieldName(f)
		fieldType := g.graphqlInputFieldType(t, f)

		// Nillable fields get a clear option
		directive := ""
		omittable := g.isFieldOmittable(f)
		if omittable {
			directive = " @goField(omittable: true)"
		}
		if f.Nillable && !omittable {
			// Nillable fields get a clear option (unless omittable — clearing is via Value() == nil)
			fmt.Fprintf(&buf, "  %s: %s%s\n", fieldName, fieldType, directive)
			fmt.Fprintf(&buf, "  clear%s: Boolean\n", pascal(f.Name))
		} else {
			fmt.Fprintf(&buf, "  %s: %s%s\n", fieldName, fieldType, directive)
		}
		hasFields = true
	}

	// Edge FK fields with explicit .Field("xxx") are already included via t.Fields.
	// For edges without explicit FK fields, add edge ID fields to match the Go struct.
	for _, edge := range t.Edges {
		if !g.edgeInUpdateInput(edge) {
			continue
		}
		if edgeClearable(edge) {
			fmt.Fprintf(&buf, "  clear%s: Boolean\n", pascal(edge.Name))
		}
		if edge.Unique {
			fieldName := camel(edge.Name) + "ID"
			fmt.Fprintf(&buf, "  %s: ID\n", fieldName)
		} else {
			// Multi-edge: add and remove fields
			addField := camel(edge.MutationAdd())
			removeField := camel(edge.MutationRemove())
			fmt.Fprintf(&buf, "  %s: [ID!]\n", addField)
			fmt.Fprintf(&buf, "  %s: [ID!]\n", removeField)
		}
		hasFields = true
	}

	// If no fields were added, add a placeholder to avoid invalid empty input type
	// Empty input types are not valid in GraphQL
	if !hasFields {
		buf.WriteString("  # This entity has no updatable fields\n")
		buf.WriteString("  _placeholder: Boolean\n")
	}

	buf.WriteString("}\n")
	return buf.String()
}

// genWhereInput generates WhereInput for an entity.
func (g *Generator) genWhereInput(t *gen.Type) string {
	var buf bytes.Buffer
	typeName := g.graphqlTypeName(t)
	inputName := typeName + "WhereInput"

	// WhereInput type description (like Ent)
	fmt.Fprintf(&buf, `"""%s is used for filtering %s objects.
Input was generated by velox.
"""
`, inputName, typeName)

	// Add @goModel directive for autobind — WhereInput is defined in gqlfilter sub-package.
	if g.config.ORMPackage != "" {
		fmt.Fprintf(&buf, "input %s @goModel(model: \"%s/gqlfilter.%s\") {\n", inputName, g.config.ORMPackage, inputName)
	} else {
		fmt.Fprintf(&buf, "input %s {\n", inputName)
	}

	// Logical operators (Ent order: not, and, or)
	fmt.Fprintf(&buf, "  not: %sWhereInput\n", typeName)
	fmt.Fprintf(&buf, "  and: [%sWhereInput!]\n", typeName)
	fmt.Fprintf(&buf, "  or: [%sWhereInput!]\n", typeName)

	// ID filters - use WhereOps for consistent behavior with Go code generation
	if t.ID != nil && !g.skipFieldInWhereInput(t, t.ID) {
		buf.WriteString(g.genFieldFiltersSDL(t.ID, "ID"))
	}

	// Field filters - use skipFieldInWhereInput for whitelist support
	for _, f := range t.Fields {
		if g.skipFieldInWhereInput(t, f) {
			continue
		}
		buf.WriteString(g.genFieldFilters(t, f))
	}

	// Edge filters - use skipEdgeInWhereInput for whitelist support
	for _, e := range t.Edges {
		if e.Type == nil || e.Type.HasCompositeID() {
			continue
		}
		if g.skipEdgeInWhereInput(t, e) {
			continue
		}
		targetType := g.graphqlTypeName(e.Type)
		edgeName := pascal(e.Name)
		// Edge predicate comment (Ent style, using GraphQL field name)
		fmt.Fprintf(&buf, `  """
  %s edge predicates
  """
`, camel(e.Name))
		fmt.Fprintf(&buf, "  has%s: Boolean\n", edgeName)
		fmt.Fprintf(&buf, "  has%sWith: [%sWhereInput!]\n", edgeName, targetType)
	}

	buf.WriteString("}\n")
	return buf.String()
}

// genFieldFilters generates filter fields for a field.
func (g *Generator) genFieldFilters(t *gen.Type, f *gen.Field) string {
	gqlType := g.graphqlFieldType(t, f)
	return g.genFieldFiltersSDL(f, gqlType)
}

// genFieldFiltersSDL generates GraphQL SDL filter fields for a field using the WhereOp system.
// This ensures consistent behavior between SDL generation and Go code generation.
func (g *Generator) genFieldFiltersSDL(f *gen.Field, gqlType string) string {
	// JSON fields: only generate ops when explicitly annotated with JSON array ops
	if f.IsJSON() {
		ann := g.getFieldAnnotation(f)
		if !ann.HasWhereOpsSet() {
			return ""
		}
		ops := ann.GetWhereOps()
		if ops.HasHas() || ops.HasHasSome() || ops.HasHasEvery() || ops.HasIsEmpty() {
			return g.genJSONSliceFiltersSDL(f)
		}
		return ""
	}

	var buf bytes.Buffer
	name := camel(f.Name)

	// Get effective WhereOps (same logic as Go code generation)
	whereOps := g.getEffectiveWhereOps(f)

	// Bool fields only support EQ and NEQ — strip In/NotIn even if explicitly set.
	if f.IsBool() {
		whereOps &= OpEQ | OpNEQ | OpsNullable
	}

	// OpsNone: skip entirely (no comment, no operators)
	if whereOps == OpsNone {
		return ""
	}

	// Field predicate comment (Ent style, using GraphQL field name)
	fmt.Fprintf(&buf, `  """
  %s field predicates
  """
`, name)

	// EQ, NEQ
	if whereOps.HasEQ() {
		fmt.Fprintf(&buf, "  %s: %s\n", name, gqlType)
	}
	if whereOps.HasNEQ() {
		fmt.Fprintf(&buf, "  %sNEQ: %s\n", name, gqlType)
	}

	// In, NotIn (variadic)
	if whereOps.HasIn() {
		fmt.Fprintf(&buf, "  %sIn: [%s!]\n", name, gqlType)
	}
	if whereOps.HasNotIn() {
		fmt.Fprintf(&buf, "  %sNotIn: [%s!]\n", name, gqlType)
	}

	// GT, GTE, LT, LTE (ordering)
	if whereOps.HasGT() {
		fmt.Fprintf(&buf, "  %sGT: %s\n", name, gqlType)
	}
	if whereOps.HasGTE() {
		fmt.Fprintf(&buf, "  %sGTE: %s\n", name, gqlType)
	}
	if whereOps.HasLT() {
		fmt.Fprintf(&buf, "  %sLT: %s\n", name, gqlType)
	}
	if whereOps.HasLTE() {
		fmt.Fprintf(&buf, "  %sLTE: %s\n", name, gqlType)
	}

	// String operations (substring matching)
	if whereOps.HasContains() {
		fmt.Fprintf(&buf, "  %sContains: %s\n", name, gqlType)
	}
	if whereOps.HasHasPrefix() {
		fmt.Fprintf(&buf, "  %sHasPrefix: %s\n", name, gqlType)
	}
	if whereOps.HasHasSuffix() {
		fmt.Fprintf(&buf, "  %sHasSuffix: %s\n", name, gqlType)
	}

	// Case-insensitive operations
	if whereOps.HasEqualFold() {
		fmt.Fprintf(&buf, "  %sEqualFold: %s\n", name, gqlType)
	}
	if whereOps.HasContainsFold() {
		fmt.Fprintf(&buf, "  %sContainsFold: %s\n", name, gqlType)
	}

	// Nullable operations
	if whereOps.HasIsNil() {
		fmt.Fprintf(&buf, "  %sIsNil: Boolean\n", name)
	}
	if whereOps.HasNotNil() {
		fmt.Fprintf(&buf, "  %sNotNil: Boolean\n", name)
	}

	return buf.String()
}

// genJSONSliceFiltersSDL generates GraphQL SDL filter fields for JSON slice fields.
// Uses Prisma-style naming: has, hasSome, hasEvery, isEmpty.
func (g *Generator) genJSONSliceFiltersSDL(f *gen.Field) string {
	elemGQLType := g.jsonSliceElementGQLType(f)
	if elemGQLType == "" {
		return ""
	}

	var buf bytes.Buffer
	name := camel(f.Name)
	ann := g.getFieldAnnotation(f)
	ops := ann.GetWhereOps()

	fmt.Fprintf(&buf, "  \"\"\"\n  %s field predicates\n  \"\"\"\n", name)
	if ops.HasHas() {
		fmt.Fprintf(&buf, "  %sHas: %s\n", name, elemGQLType)
	}
	if ops.HasHasSome() {
		fmt.Fprintf(&buf, "  %sHasSome: [%s!]\n", name, elemGQLType)
	}
	if ops.HasHasEvery() {
		fmt.Fprintf(&buf, "  %sHasEvery: [%s!]\n", name, elemGQLType)
	}
	if ops.HasIsEmpty() {
		fmt.Fprintf(&buf, "  %sIsEmpty: Boolean\n", name)
	}
	if ops.HasIsNil() {
		fmt.Fprintf(&buf, "  %sIsNil: Boolean\n", name)
	}
	if ops.HasNotNil() {
		fmt.Fprintf(&buf, "  %sNotNil: Boolean\n", name)
	}
	return buf.String()
}

// writeConnectionEdgeTypes writes the Connection and Edge type SDL for a single entity type
// to the given buffer. Used by both genConnectionsSchema (all entities) and
// genEntityConnectionSchema (single entity).
func (g *Generator) writeConnectionEdgeTypes(buf *bytes.Buffer, typeName string) {
	connName := typeName + "Connection"
	edgeName := typeName + "Edge"

	// Connection type with non-null edge items per Relay spec.
	// Note: edges list items are [Edge!] (non-null) since a connection
	// edge is always a valid cursor+node pair; null items would break
	// cursor-based pagination. The list itself is nullable.
	// Add @goModel directive for autobind — Connection types are in the model/ sub-package.
	buf.WriteString(`"""
A connection to a list of items.
"""
`)
	if g.config.ORMPackage != "" {
		fmt.Fprintf(buf, "type %s @goModel(model: \"%s.%s\") {\n", connName, g.modelPkg(), connName)
	} else {
		fmt.Fprintf(buf, "type %s {\n", connName)
	}
	fmt.Fprintf(buf, `  """
  A list of edges.
  """
  edges: [%s!]
  """
  Information to aid in pagination.
  """
  pageInfo: PageInfo!
  """
  Identifies the total count of items in the connection.
  """
  totalCount: Int!
}
`, edgeName)

	// Edge type with documentation (nullable node like Ent)
	// Add @goModel directive for autobind — Edge types are in the model/ sub-package.
	buf.WriteString(`"""
An edge in a connection.
"""
`)
	if g.config.ORMPackage != "" {
		fmt.Fprintf(buf, "type %s @goModel(model: \"%s.%s\") {\n", edgeName, g.modelPkg(), edgeName)
	} else {
		fmt.Fprintf(buf, "type %s {\n", edgeName)
	}
	fmt.Fprintf(buf, `  """
  The item at the end of the edge.
  """
  node: %s
  """
  A cursor for use in pagination.
  """
  cursor: Cursor!
}
`, typeName)
}

// genConnectionsSchema generates Relay connection types.
func (g *Generator) genConnectionsSchema() string {
	var buf bytes.Buffer
	buf.WriteString("# Relay connection types\n\n")

	// PageInfo type with documentation and Relay spec link (matching Ent)
	// Add @goModel directive for autobind
	buf.WriteString(`"""
Information about pagination in a connection.
https://relay.dev/graphql/connections.htm#sec-undefined.PageInfo
"""
`)
	if g.config.ORMPackage != "" {
		fmt.Fprintf(&buf, "type PageInfo @goModel(model: \"%s.PageInfo\") {\n", gqlrelayPkg)
	} else {
		buf.WriteString("type PageInfo {\n")
	}
	buf.WriteString(`  """
  When paginating forwards, are there more items?
  """
  hasNextPage: Boolean!
  """
  When paginating backwards, are there more items?
  """
  hasPreviousPage: Boolean!
  """
  When paginating backwards, the cursor to continue.
  """
  startCursor: Cursor
  """
  When paginating forwards, the cursor to continue.
  """
  endCursor: Cursor
}

`)

	// Connection types for each entity - use filterNodes (like Ent)
	nodes := g.filterNodes(g.graph.Nodes, SkipType)
	for _, t := range nodes {
		if !g.hasRelayConnection(t) {
			continue
		}
		g.writeConnectionEdgeTypes(&buf, g.graphqlTypeName(t))
		buf.WriteString("\n")
	}

	return buf.String()
}

// genEntityConnectionSchema generates Connection and Edge types for a single entity.
// Used by per-entity split mode to include these types in velox_{entity}.graphql.
func (g *Generator) genEntityConnectionSchema(t *gen.Type) string {
	var buf bytes.Buffer
	g.writeConnectionEdgeTypes(&buf, g.graphqlTypeName(t))
	return buf.String()
}

// genOrderBy generates OrderBy input for an entity.
func (g *Generator) genOrderBy(t *gen.Type) string {
	var buf bytes.Buffer
	typeName := g.graphqlTypeName(t)
	orderName := typeName + "Order"
	orderFieldName := typeName + "OrderField"

	// Order input with descriptions and default value (matching Ent)
	// Add @goModel directive for autobind
	fmt.Fprintf(&buf, `"""
Ordering options for %s connections
"""
`, typeName)
	if g.config.ORMPackage != "" {
		fmt.Fprintf(&buf, "input %s @goModel(model: \"%s.%s\") {\n", orderName, g.modelPkg(), orderName)
	} else {
		fmt.Fprintf(&buf, "input %s {\n", orderName)
	}
	fmt.Fprintf(&buf, `  """
  The ordering direction.
  """
  direction: OrderDirection! = ASC
  """
  The field by which to order %s.
  """
  field: %s!
}
`, pluralize(typeName), orderFieldName)

	// OrderField enum with description
	// Add @goModel directive for autobind
	fmt.Fprintf(&buf, `"""
Properties by which %s connections can be ordered.
"""
`, typeName)
	if g.config.ORMPackage != "" {
		fmt.Fprintf(&buf, "enum %s @goModel(model: \"%s.%s\") {\n", orderFieldName, g.modelPkg(), orderFieldName)
	} else {
		fmt.Fprintf(&buf, "enum %s {\n", orderFieldName)
	}

	// Filter by SkipOrderField annotation, then exclude non-orderable fields.
	// Text fields (field.Text(), Size >= MaxInt32) are excluded because ordering
	// by unbounded text columns is inefficient and rarely useful in practice.
	// Orderable types: String (non-Text), Int, Int64, Float, Bool, Enum, Time.
	fields := g.filterFields(t.Fields, SkipOrderField)
	for _, f := range fields {
		if !g.isOrderableField(f) {
			continue
		}
		orderName := g.getOrderFieldName(f)
		fmt.Fprintf(&buf, "  %s\n", orderName)
	}

	// Edge count ordering - only when explicitly annotated with OrderField (like Ent)
	for _, e := range t.Edges {
		if !e.Unique {
			ann := g.getEdgeAnnotation(e)
			if orderField := ann.GetOrderField(); orderField != "" {
				fmt.Fprintf(&buf, "  %s\n", orderField)
			}
		}
	}

	buf.WriteString("}\n")
	return buf.String()
}
