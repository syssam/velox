package graphql

import "testing"

// go/types renders the empty interface as "interface {}" with a space, while
// source and the `any` alias render it without. Missing the spaced form let a
// `field.JSON("x", []any{})` reach the scalar generator and produce
// `func Unmarshalinterface {}(...)`, which is not valid Go.
func TestIsGenericGoType(t *testing.T) {
	generic := []string{
		"any", "interface{}", "interface {}", " interface {} ",
		"[]string", "[]any", "[]interface {}", "[]*schema.Address",
		"map[string]any", "map[string]interface {}",
	}
	for _, id := range generic {
		if !isGenericGoType(id) {
			t.Errorf("isGenericGoType(%q) = false, want true", id)
		}
	}

	named := []string{"schema.Address", "*schema.Address", "time.Time", "string", "int64"}
	for _, id := range named {
		if isGenericGoType(id) {
			t.Errorf("isGenericGoType(%q) = true, want false", id)
		}
	}
}
