// Package schema defines the Product entity for the JSON field example.
//
// This example demonstrates how to store structured data as a single
// JSON/JSONB column. Useful when:
//
//  1. The shape varies per row (custom fields, user-defined attributes)
//  2. The data is always read/written together (no need to query by
//     nested keys at the SQL layer)
//  3. You want type-safe access in Go but flexible storage in the DB
package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/field"
)

// Specs is a typed Go struct stored as JSON on Product.specs.
// Both velox's generated client and the underlying driver serialize
// to/from this shape automatically.
type Specs struct {
	Weight   float64  `json:"weight_kg"`
	Color    string   `json:"color,omitempty"`
	Features []string `json:"features,omitempty"`
}

// Product represents an item with structured specs stored as JSON.
type Product struct {
	velox.Schema
}

func (Product) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").NotEmpty(),

		// --- Typed JSON: Go sees Specs, DB sees JSON column. ---
		// The second argument is a zero value that tells velox what type
		// the JSON column holds. Generated getters/setters use *Specs.
		field.JSON("specs", Specs{}),

		// --- Untyped JSON: flexible bag of data. ---
		// Use map[string]any when the shape varies per row or you want
		// to accept arbitrary user-supplied JSON.
		field.JSON("metadata", map[string]any{}).Optional(),

		// --- JSON slice. ---
		// Stored as a JSON array, exposed in Go as []string.
		field.JSON("tags", []string{}).Optional(),
	}
}
