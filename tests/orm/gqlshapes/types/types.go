// Package types holds the Go types the schema binds fields to. It is beside
// the schema package and not in it: a type declared there would make every
// package that names an entity depend on the schema.
package types

// Specs is what an asset's "specs" column holds, as JSON.
type Specs struct {
	Weight float64  `json:"weight_kg"`
	Tags   []string `json:"tags,omitempty"`
}
