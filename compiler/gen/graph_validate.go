package gen

import (
	"errors"
	"fmt"
	"go/token"
)

// Validate performs comprehensive validation of the graph and returns all
// errors found, rather than failing on the first error like NewGraph does.
// This is useful for reporting all schema issues at once to the user.
func (g *Graph) Validate() error {
	var errs []error

	// Validate edge references exist (Type should be resolved to non-nil).
	for _, t := range g.Nodes {
		for _, e := range t.Edges {
			if e.Type == nil {
				typeName := e.Name
				if e.def != nil {
					typeName = e.def.Type
				}
				errs = append(errs, NewEdgeError(t.Name, typeName, e.Name,
					fmt.Sprintf("edge %q references unknown type %q", e.Name, typeName), nil))
			}
		}
	}

	// Validate edge names are valid Go identifiers. Edge names become Go
	// method names (QueryPosts) and SQL identifiers (join-table column
	// names); rejecting non-identifiers here closes the same attack surface
	// the field-name check closes in type.checkField.
	for _, t := range g.Nodes {
		for _, e := range t.Edges {
			if e.Name == "" {
				errs = append(errs, NewEdgeError(t.Name, "", e.Name,
					"edge name cannot be empty", nil))
				continue
			}
			if !token.IsIdentifier(e.Name) {
				edgeTypeName := ""
				if e.Type != nil {
					edgeTypeName = e.Type.Name
				}
				errs = append(errs, NewEdgeError(t.Name, edgeTypeName, e.Name,
					fmt.Sprintf("edge name %q is not a valid Go identifier", e.Name), nil))
			}
		}
	}

	// Validate field names don't collide with edge names.
	for _, t := range g.Nodes {
		fieldNames := make(map[string]bool, len(t.Fields))
		for _, f := range t.Fields {
			fieldNames[f.Name] = true
		}
		for _, e := range t.Edges {
			if fieldNames[e.Name] {
				edgeTypeName := ""
				if e.Type != nil {
					edgeTypeName = e.Type.Name
				}
				errs = append(errs, NewEdgeError(t.Name, edgeTypeName, e.Name,
					fmt.Sprintf("edge %q conflicts with field of same name", e.Name), nil))
			}
		}
	}

	// Validate duplicate edge names within a type.
	for _, t := range g.Nodes {
		seen := make(map[string]bool, len(t.Edges))
		for _, e := range t.Edges {
			if seen[e.Name] {
				edgeTypeName := ""
				if e.Type != nil {
					edgeTypeName = e.Type.Name
				}
				errs = append(errs, NewEdgeError(t.Name, edgeTypeName, e.Name,
					fmt.Sprintf("duplicate edge name %q", e.Name), nil))
			}
			seen[e.Name] = true
		}
	}

	// Validate Optional() on non-standard types requires Default() or Nillable().
	// Standard types (string, int, bool, float, time, bytes, uuid, json) have
	// well-defined zero values, so Optional() alone is meaningful. For enums
	// and other custom types, the zero value is ambiguous and must be spelled
	// out explicitly via Default() or by making the field nullable via Nillable().
	for _, t := range g.Nodes {
		for _, f := range t.Fields {
			if !f.Optional || f.Nillable || f.Default {
				continue
			}
			if f.Type == nil || f.Type.Type.IsStandardType() {
				continue
			}
			errs = append(errs, &SchemaValidationError{
				Type:  t.Name,
				Field: f.Name,
				Message: fmt.Sprintf(
					"Optional() on type %s requires Default() or Nillable(); "+
						"Optional() alone uses zero-value semantics, only meaningful for standard types (string/int/bool/float/time)",
					f.Type,
				),
			})
		}
	}

	// Validate indexes reference existing fields/columns.
	for _, t := range g.Nodes {
		validCols := make(map[string]bool, len(t.Fields)+len(t.Edges))
		for _, f := range t.Fields {
			validCols[f.Name] = true
			// Also accept the storage key (e.g., "user_posts" for edge FK columns).
			if sk := f.StorageKey(); sk != f.Name {
				validCols[sk] = true
			}
		}
		// Edge FK columns are valid index targets.
		for _, e := range t.Edges {
			for _, col := range e.Rel.Columns {
				validCols[col] = true
			}
		}
		for _, idx := range t.Indexes {
			for _, col := range idx.Columns {
				if !validCols[col] {
					errs = append(errs, &SchemaValidationError{
						Type:    t.Name,
						Field:   col,
						Message: fmt.Sprintf("index references unknown field %q", col),
					})
				}
			}
		}
	}

	return errors.Join(errs...)
}
