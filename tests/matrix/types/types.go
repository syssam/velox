// Package types holds the custom Go types the matrix schema binds to fields
// with GoType(). It is a sibling of the schema package on purpose: generated
// code must import a GoType's package by its own path, and a type declared
// inside the schema package would hide a wrong import.
package types

// Tier is a custom enum type. field.Enum(...).GoType requires Values().
type Tier string

// Tier values.
const (
	TierFree Tier = "free"
	TierPro  Tier = "pro"
)

// Values implements field.EnumValues.
func (Tier) Values() []string { return []string{string(TierFree), string(TierPro)} }

// Label is a custom string type.
type Label string

// Weight is a custom int type.
type Weight int

// Address is stored as a JSON object.
type Address struct {
	Street string `json:"street"`
	City   string `json:"city"`
	Zip    int    `json:"zip,omitempty"`
}
