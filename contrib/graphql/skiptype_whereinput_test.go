package graphql

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	entgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

// TestSkipType_PreservesWhereInputGo pins that when an entity has
// graphql.Skip(graphql.SkipType) but does NOT have SkipWhereInput,
// its WhereInput Go struct is still generated. This is the load-bearing
// behavior for the PII/projection-type pattern (hide Customer output,
// keep CustomerWhereInput for cross-entity filtering via hasCustomerWith).
func TestSkipType_PreservesWhereInputGo(t *testing.T) {
	// Customer: hidden from output, but has filterable fields.
	customer := &entgen.Type{
		Name: "Customer",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{
			{Name: "country", Type: &field.TypeInfo{Type: field.TypeString}},
			{Name: "tier", Type: &field.TypeInfo{Type: field.TypeString}},
		},
		Annotations: map[string]any{
			AnnotationName: Annotation{
				Skip:                 SkipType,
				WhereInputFieldNames: []string{"country", "tier"},
			},
		},
	}
	// Order: visible, with edge to Customer.
	order := &entgen.Type{
		Name: "Order",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{
			{Name: "total", Type: &field.TypeInfo{Type: field.TypeFloat64}},
		},
		Annotations: map[string]any{},
	}
	customerEdge := &entgen.Edge{
		Name:   "customer",
		Type:   customer,
		Unique: true,
		Annotations: map[string]any{
			AnnotationName: Annotation{
				Skip:              SkipType,
				WhereInputEnabled: true,
			},
		},
	}
	order.Edges = []*entgen.Edge{customerEdge}

	g := newTestGeneratorWithConfig(Config{
		Package:     "graphql",
		ORMPackage:  "example/ent",
		WhereInputs: true,
	}, customer, order)

	file := g.genWhereInputGo()
	require.NotNil(t, file, "WhereInput Go file must be generated")

	var buf bytes.Buffer
	require.NoError(t, file.Render(&buf), "rendering Jen file must succeed")
	rendered := buf.String()

	require.Contains(t, rendered, "type CustomerWhereInput",
		"CustomerWhereInput must be generated even when Customer has Skip(SkipType)")
	require.Contains(t, rendered, "type OrderWhereInput",
		"OrderWhereInput must be generated")
	require.Contains(t, rendered, "HasCustomer",
		"OrderWhereInput must include HasCustomer predicate field")
	require.Contains(t, rendered, "HasCustomerWith",
		"OrderWhereInput must include HasCustomerWith predicate field")
	require.True(t, strings.Contains(rendered, "[]*CustomerWhereInput"),
		"OrderWhereInput.HasCustomerWith must reference []*CustomerWhereInput")
}

// TestSkipType_PreservesWhereInputSDL pins that when an entity has
// graphql.Skip(graphql.SkipType), its WhereInput SDL block is still
// emitted even though the output type SDL block is suppressed.
// This complements TestSkipType_PreservesWhereInputGo at the SDL layer:
// input types and output types are independent — the output type is hidden,
// but the WhereInput block must still be emitted so other entities'
// WhereInput can reference [CustomerWhereInput!] in hasCustomerWith.
func TestSkipType_PreservesWhereInputSDL(t *testing.T) {
	customer := &entgen.Type{
		Name: "Customer",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{
			{Name: "country", Type: &field.TypeInfo{Type: field.TypeString}},
			{Name: "tier", Type: &field.TypeInfo{Type: field.TypeString}},
		},
		Annotations: map[string]any{
			AnnotationName: Annotation{
				Skip:                 SkipType,
				WhereInputFieldNames: []string{"country", "tier"},
			},
		},
	}

	g := newTestGeneratorWithConfig(Config{
		Package:     "graphql",
		ORMPackage:  "example/ent",
		WhereInputs: true,
	}, customer)

	sdl := g.genInputsSchema()

	require.NotContains(t, sdl, "type Customer ",
		"Customer output type must NOT appear in SDL when Skip(SkipType) is set")
	require.Contains(t, sdl, "input CustomerWhereInput",
		"CustomerWhereInput SDL must be emitted even when Skip(SkipType) is set")
	require.Contains(t, sdl, "country",
		"CustomerWhereInput must contain whitelisted field 'country'")
}

// TestSkipType_OnEdge_PreservesHasXxxWith pins that when an edge has
// graphql.Skip(graphql.SkipType) AND graphql.WhereInput(), the
// hasCustomer/hasCustomerWith predicates are emitted on the parent's
// WhereInput even though the `customer` output field is suppressed.
//
// This is the second half of the PII/projection pattern: hiding the edge
// output but keeping cross-entity filtering. The target Customer entity
// does NOT need Skip(SkipType) for this to work — the predicate looks up
// the edge target by name, not by output visibility.
//
// Pin: skipEdgeInWhereInput must NOT consult edge.Skip&SkipType.
func TestSkipType_OnEdge_PreservesHasXxxWith(t *testing.T) {
	customer := &entgen.Type{
		Name: "Customer",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{
			{Name: "country", Type: &field.TypeInfo{Type: field.TypeString}},
		},
		Annotations: map[string]any{
			AnnotationName: Annotation{
				WhereInputFieldNames: []string{"country"},
			},
		},
	}
	order := &entgen.Type{
		Name: "Order",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{
			{Name: "total", Type: &field.TypeInfo{Type: field.TypeFloat64}},
		},
		Annotations: map[string]any{},
	}
	customerEdge := &entgen.Edge{
		Name:   "customer",
		Type:   customer,
		Unique: true, // M2O — Order has one Customer
		Annotations: map[string]any{
			AnnotationName: Annotation{
				Skip:              SkipType, // hide from Order output
				WhereInputEnabled: true,     // keep in WhereInput
			},
		},
	}
	order.Edges = []*entgen.Edge{customerEdge}

	g := newTestGeneratorWithConfig(Config{
		Package:     "graphql",
		ORMPackage:  "example/ent",
		WhereInputs: true,
	}, customer, order)

	file := g.genWhereInputGo()
	require.NotNil(t, file, "WhereInput Go file must be generated")

	var buf bytes.Buffer
	require.NoError(t, file.Render(&buf), "rendering Jen file must succeed")
	rendered := buf.String()

	require.Contains(t, rendered, "HasCustomer",
		"hasCustomer predicate must survive Skip(SkipType) on the edge")
	require.Contains(t, rendered, "HasCustomerWith",
		"hasCustomerWith predicate must survive Skip(SkipType) on the edge")
}

// TestSkipAll_StillSuppressesWhereInput is the inverse pin of
// TestSkipType_PreservesWhereInputGo. It guards against a future "fix"
// that decouples SkipType too aggressively and accidentally lets SkipAll
// (which explicitly includes SkipWhereInput) leak a WhereInput struct.
//
// Contract:
//
//	Skip()                       = SkipAll → no Customer output, no CustomerWhereInput
//	Skip(SkipType)               → no Customer output, but CustomerWhereInput remains
//	Skip(SkipType, SkipWhereInput) → no output, no WhereInput (explicit dual skip)
func TestSkipAll_StillSuppressesWhereInput(t *testing.T) {
	customer := &entgen.Type{
		Name: "Customer",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{
			{Name: "country", Type: &field.TypeInfo{Type: field.TypeString}},
		},
		Annotations: map[string]any{
			// Skip() with no args = SkipAll (= SkipType | SkipWhereInput | ...)
			AnnotationName: Skip().Merge(Annotation{
				WhereInputFieldNames: []string{"country"},
			}),
		},
	}
	g := newTestGeneratorWithConfig(Config{
		Package:     "graphql",
		ORMPackage:  "example/ent",
		WhereInputs: true,
	}, customer)

	file := g.genWhereInputGo()
	if file == nil {
		// Acceptable: no WhereInput at all means the contract holds.
		return
	}
	var buf bytes.Buffer
	require.NoError(t, file.Render(&buf))
	rendered := buf.String()

	require.NotContains(t, rendered, "type CustomerWhereInput",
		"Skip()/SkipAll must continue to suppress WhereInput; only Skip(SkipType) alone preserves it")
}

// TestSkipTypeAndWhereInput_BothFlags pins that the explicit dual-skip
// composition Skip(SkipType, SkipWhereInput) suppresses BOTH surfaces.
// This guards against a future change that drops SkipWhereInput's effect
// when SkipType is also set.
func TestSkipTypeAndWhereInput_BothFlags(t *testing.T) {
	customer := &entgen.Type{
		Name: "Customer",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*entgen.Field{
			{Name: "country", Type: &field.TypeInfo{Type: field.TypeString}},
		},
		Annotations: map[string]any{
			AnnotationName: Skip(SkipType, SkipWhereInput).Merge(Annotation{
				WhereInputFieldNames: []string{"country"},
			}),
		},
	}
	g := newTestGeneratorWithConfig(Config{
		Package:     "graphql",
		ORMPackage:  "example/ent",
		WhereInputs: true,
	}, customer)

	file := g.genWhereInputGo()
	if file == nil {
		return
	}
	var buf bytes.Buffer
	require.NoError(t, file.Render(&buf))
	rendered := buf.String()

	require.NotContains(t, rendered, "type CustomerWhereInput",
		"Skip(SkipType, SkipWhereInput) must suppress both output and WhereInput")
}
