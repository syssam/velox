package graphql

import "testing"

// A typed-JSON scalar can only be declared for a NAMED type. The guard used
// to be a deny-list of spellings (`any`, `interface{}` with and without the
// space, `[]`, `map[`), and every unnamed shape it did not list — an
// anonymous struct, a fixed-size array, a pointer to a slice — reached the
// scalar generator and produced `func Unmarshalstruct { X int }(...)`,
// failing the whole run at the format step. The guard is now an allow-list:
// an identifier, optionally package-qualified, optionally behind one `*`.
func TestIsGenericGoType(t *testing.T) {
	generic := []string{
		"any", "interface{}", "interface {}", " interface {} ",
		"[]string", "[]any", "[]interface {}", "[]*schema.Address",
		"map[string]any", "map[string]interface {}",
		// Unnamed types that are not slices or maps: reflect spells them
		// out, and a scalar declaration cannot be named after them.
		"struct { X int }", "struct{}", "[3]int", "*[]string", "*map[string]int",
		"func()", "chan int", "**schema.Address",
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
