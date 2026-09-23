// Package types holds custom Go types that testschema binds to fields with
// GoType(). They exist so the integration prototype compiles and exercises
// the generated code paths a custom type takes: validators declared at the
// basic type and called through a conversion, the enum validator of a type
// the generator did not declare, and String() on a non-string string kind.
package types

// Tier is a custom enum type. field.Enum(...).GoType requires the type to
// implement field.EnumValues.
type Tier string

// Tier values.
const (
	TierFree Tier = "free"
	TierPro  Tier = "pro"
)

// Values implements field.EnumValues.
func (Tier) Values() []string { return []string{string(TierFree), string(TierPro)} }

// Label is a custom string type carrying schema validators.
type Label string

// Weight is a custom int type carrying a schema validator.
type Weight int
