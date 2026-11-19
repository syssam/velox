package gqlgraph

import "strings"

// IsOrderField reports whether s is the value of one of c's order fields.
// For generated code: an XOrderField's IsValid.
func (c *Type) IsOrderField(s string) bool { return c.find(s) != nil }

// UnmarshalOrderField sets *o from v, a value of c's GraphQL enum of order
// fields, XOrderField. For generated code: the enum's UnmarshalGQL. Any
// other v fails with an input error that lists the enum's values.
func UnmarshalOrderField[T ~string](o *T, v any, c *Type) error {
	if s, ok := v.(string); ok && c.find(s) != nil {
		*o = T(s)
		return nil
	}
	names := make([]string, len(c.Order))
	for i, col := range c.Order {
		names[i] = col.Name
	}
	return InputErrorf("%sOrderField must be %s, not %v", c.Name, orList(names), v)
}

// orList is vs as a sentence lists them: "A, B or C".
func orList(vs []string) string {
	if len(vs) < 2 {
		return strings.Join(vs, "")
	}
	return strings.Join(vs[:len(vs)-1], ", ") + " or " + vs[len(vs)-1]
}
