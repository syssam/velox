//go:build ignore

package schema

// Visibility is an enum type of the application, which Post and Group
// share: velox.go declares its one validator.
type Visibility string

// Values lists Visibility's values, in their order.
func (Visibility) Values() []string { return []string{"public", "private"} }
