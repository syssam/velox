package graphql

// This file contains helper types and functions for GraphQL code generation.
// It includes:
//   - PaginationNames for Relay connection type naming
//   - OrderTerm for sorting field definitions
//   - MutationDescriptor for mutation input generation
//   - Filter functions for nodes, edges, and fields

import (
	"fmt"
	"slices"
	"strings"

	"github.com/syssam/velox/compiler/gen"
)

// Is checks if the mode has the given flag.
func (m SkipMode) Is(flag SkipMode) bool {
	return m&flag != 0
}

// =============================================================================
// GraphQL Field Name Constants
// =============================================================================

// GraphQL introspection and special field names.
const (
	// GQLFieldID is the GraphQL ID field name.
	GQLFieldID = "id"
	// GQLFieldTypeName is the GraphQL __typename field name.
	GQLFieldTypeName = "__typename"
)

// =============================================================================
// Pagination Types
// =============================================================================

// PaginationNames holds the names of the pagination types for a node.
// This matches Ent's PaginationNames struct.
type PaginationNames struct {
	Connection string
	Edge       string
	Node       string
	Order      string
	OrderField string
	WhereInput string
}

// paginationNames generates pagination type names from a node name.
func paginationNames(node string) *PaginationNames {
	return &PaginationNames{
		Connection: fmt.Sprintf("%sConnection", node),
		Edge:       fmt.Sprintf("%sEdge", node),
		Node:       node,
		Order:      fmt.Sprintf("%sOrder", node),
		OrderField: fmt.Sprintf("%sOrderField", node),
		WhereInput: fmt.Sprintf("%sWhereInput", node),
	}
}

// =============================================================================
// Field Collection Types
// =============================================================================

// FieldCollection represents an edge with its GraphQL field mapping.
// This matches Ent's fieldCollection struct.
type FieldCollection struct {
	Edge    *gen.Edge
	Mapping []string
}

// =============================================================================
// Filter Functions
// =============================================================================

// filterNodes filters out nodes that should not be included in the GraphQL schema.
// Equivalent to Ent's filterNodes function - excludes composite ID nodes and checks skip mode.
func (g *Generator) filterNodes(nodes []*gen.Type, skip SkipMode) []*gen.Type {
	filteredNodes := make([]*gen.Type, 0, len(nodes))
	for _, n := range nodes {
		// Skip composite ID nodes (like Ent)
		if n.HasCompositeID() {
			continue
		}
		ann := g.getTypeAnnotation(n)
		annSkip := g.annotationSkipMode(ann)
		if !annSkip.Is(skip) {
			filteredNodes = append(filteredNodes, n)
		}
	}
	return filteredNodes
}

// filterEdges filters out edges that should not be included in the GraphQL schema.
// Equivalent to Ent's filterEdges function - excludes edges to composite ID types and checks skip mode.
func (g *Generator) filterEdges(edges []*gen.Edge, skip SkipMode) []*gen.Edge {
	filteredEdges := make([]*gen.Edge, 0, len(edges))
	for _, e := range edges {
		// Skip edges with nil Type (invalid edge configuration)
		if e.Type == nil {
			continue
		}
		// Skip edges to composite ID types (like Ent)
		if e.Type.HasCompositeID() {
			continue
		}
		antE := g.getEdgeAnnotation(e)
		antT := g.getTypeAnnotation(e.Type)
		skipE := g.annotationSkipMode(antE)
		skipT := g.annotationSkipMode(antT)
		if !skipE.Is(skip) && !skipT.Is(skip) {
			filteredEdges = append(filteredEdges, e)
		}
	}
	return filteredEdges
}

// filterFields filters out fields that should not be included in the GraphQL schema.
// Equivalent to Ent's filterFields function.
func (g *Generator) filterFields(fields []*gen.Field, skip SkipMode) []*gen.Field {
	filteredFields := make([]*gen.Field, 0, len(fields))
	for _, f := range fields {
		ann := g.getFieldAnnotation(f)
		annSkip := g.annotationSkipMode(ann)
		if !annSkip.Is(skip) {
			filteredFields = append(filteredFields, f)
		}
	}
	return filteredFields
}

// annotationSkipMode returns the SkipMode from an Annotation.
func (g *Generator) annotationSkipMode(ann Annotation) SkipMode {
	return ann.Skip
}

// hasWhereInput returns true if neither the edge nor its node type has
// the SkipWhereInput annotation.
// Equivalent to Ent's hasWhereInput function.
func (g *Generator) hasWhereInput(e *gen.Edge) bool {
	antEdge := g.getEdgeAnnotation(e)
	antType := g.getTypeAnnotation(e.Type)
	skipEdge := g.annotationSkipMode(antEdge)
	skipType := g.annotationSkipMode(antType)
	return !skipEdge.Is(SkipWhereInput) && !skipType.Is(SkipWhereInput)
}

// hasOrderField returns true if neither the edge nor its node type has
// the SkipOrderField annotation.
func (g *Generator) hasOrderField(e *gen.Edge) bool {
	antEdge := g.getEdgeAnnotation(e)
	antType := g.getTypeAnnotation(e.Type)
	skipEdge := g.annotationSkipMode(antEdge)
	skipType := g.annotationSkipMode(antType)
	return !skipEdge.Is(SkipOrderField) && !skipType.Is(SkipOrderField)
}

// nodeImplementors returns the interfaces that a node implements.
// By default, all nodes implement "Node" unless explicitly skipped or RelaySpec is disabled.
// Additional interfaces can be specified via the Implements annotation.
// Equivalent to Ent's nodeImplementors function.
func (g *Generator) nodeImplementors(t *gen.Type) []string {
	ann := g.getTypeAnnotation(t)
	annSkip := g.annotationSkipMode(ann)

	var ifaces []string
	// Add Node interface if RelaySpec is enabled and not skipped
	if g.config.RelaySpec && !annSkip.Is(SkipType) && !slices.Contains(ann.GetImplements(), "Node") {
		ifaces = append(ifaces, "Node")
	}
	// Add user-defined interfaces
	ifaces = append(ifaces, ann.GetImplements()...)
	return ifaces
}

// =============================================================================
// Order Term Types
// =============================================================================

// OrderTerm represents a single GraphQL order term.
// This matches Ent's OrderTerm struct.
type OrderTerm struct {
	// The type that owns the order field.
	Owner *gen.Type
	// The GraphQL name of the field.
	GQL string
	// The type that owns the field. For type fields, equals Owner.
	// For edge fields, equals the underlying edge's type.
	Type *gen.Type
	// Not nil if it is a type/edge field.
	Field *gen.Field
	// Not nil if it is an edge field or count.
	Edge *gen.Edge
	// True if it is a count field.
	Count bool
}

// IsFieldTerm returns true if the order term is a type field term.
func (o *OrderTerm) IsFieldTerm() bool {
	return o.Field != nil && o.Edge == nil
}

// IsEdgeFieldTerm returns true if the order term is an edge field term.
func (o *OrderTerm) IsEdgeFieldTerm() bool {
	return o.Field != nil && o.Edge != nil
}

// IsEdgeCountTerm returns true if the order term is an edge count term.
func (o *OrderTerm) IsEdgeCountTerm() bool {
	return o.Field == nil && o.Edge != nil && o.Count
}

// VarName returns the name of the variable holding the order term.
func (o *OrderTerm) VarName() string {
	prefix := paginationNames(o.Owner.Name).OrderField
	switch {
	case o.IsFieldTerm():
		return prefix + pascal(o.Field.Name)
	case o.IsEdgeFieldTerm():
		return prefix + pascal(o.Edge.Name) + pascal(o.Field.Name)
	case o.IsEdgeCountTerm():
		return prefix + pascal(o.Edge.Name) + "Count"
	default:
		return prefix
	}
}

// VarField returns the field name inside the variable holding the order term.
func (o *OrderTerm) VarField() string {
	switch {
	case o.IsFieldTerm():
		return fmt.Sprintf("%s.%s", strings.ToLower(o.Type.Name), "Field"+pascal(o.Field.Name))
	case o.IsEdgeFieldTerm(), o.IsEdgeCountTerm():
		return fmt.Sprintf("%q", strings.ToLower(o.GQL))
	default:
		return ""
	}
}

// =============================================================================
// Mutation Descriptor Types
// =============================================================================

// MutationDescriptor holds information about a GraphQL mutation input.
// This matches Ent's MutationDescriptor struct.
type MutationDescriptor struct {
	*gen.Type
	IsCreate bool
}

// Input returns the input's name (e.g., CreateUserInput, UpdateUserInput).
func (m *MutationDescriptor) Input(g *Generator) string {
	gqlType := g.graphqlTypeName(m.Type)
	if m.IsCreate {
		return fmt.Sprintf("Create%sInput", gqlType)
	}
	return fmt.Sprintf("Update%sInput", gqlType)
}

// Builders returns the builder names to apply the input.
func (m *MutationDescriptor) Builders() []string {
	if m.IsCreate {
		return []string{m.Type.Name + "Create"}
	}
	return []string{m.Type.Name + "Update", m.Type.Name + "UpdateOne"}
}

// InputFieldDescriptor holds information about a field in the input type.
type InputFieldDescriptor struct {
	*gen.Field
	// AppendOp indicates if the field has the Append operator
	AppendOp bool
	// ClearOp indicates if the field has the Clear operator
	ClearOp bool
	// Nullable indicates if the field is nullable.
	Nullable bool
}

// IsPointer returns true if the Go type should be a pointer.
func (f *InputFieldDescriptor) IsPointer() bool {
	if f.Nillable || (f.Type != nil && f.Type.RType != nil && strings.HasPrefix(f.Type.RType.String(), "*")) {
		return false
	}
	return f.Nullable
}

// InputFields returns the list of fields in the input type.
func (m *MutationDescriptor) InputFields(g *Generator) []*InputFieldDescriptor {
	fields := make([]*InputFieldDescriptor, 0, len(m.Type.Fields))
	for _, f := range m.Type.Fields {
		ann := g.getFieldAnnotation(f)
		annSkip := g.annotationSkipMode(ann)

		// Skip edge fields (FK fields managed by edges)
		if f.IsEdgeField() {
			continue
		}
		// Skip based on mutation type
		if m.IsCreate && annSkip.Is(SkipMutationCreateInput) {
			continue
		}
		if !m.IsCreate && (f.Immutable || annSkip.Is(SkipMutationUpdateInput)) {
			continue
		}

		fields = append(fields, &InputFieldDescriptor{
			Field:    f,
			AppendOp: !m.IsCreate && f.SupportsMutationAppend(),
			// ClearOp: Only Nillable fields can be cleared (set to NULL in DB)
			// Optional fields have NOT NULL constraint, they can't be set to NULL
			ClearOp:  !m.IsCreate && f.Nillable,
			Nullable: !m.IsCreate || f.Optional || f.Default,
		})
	}
	return fields
}

// InputEdges returns the list of edges in the input type.
func (m *MutationDescriptor) InputEdges(g *Generator) []*gen.Edge {
	edges := make([]*gen.Edge, 0, len(m.Type.Edges))
	for _, e := range m.Type.Edges {
		ann := g.getEdgeAnnotation(e)
		annSkip := g.annotationSkipMode(ann)

		// Skip based on mutation type
		if m.IsCreate && annSkip.Is(SkipMutationCreateInput) {
			continue
		}
		if !m.IsCreate && (e.Immutable || annSkip.Is(SkipMutationUpdateInput)) {
			continue
		}
		edges = append(edges, e)
	}
	return edges
}

// =============================================================================
// ID Type
// =============================================================================

// IDType represents the scalar (Go) type of the GraphQL ID.
// This matches Ent's idType struct.
type IDType struct {
	Type  string
	Mixed bool
}
