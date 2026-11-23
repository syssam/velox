package codegen

import (
	"slices"
	"testing"
)

// TestColumnPartsSchemaName: a ColumnDescriptor says SchemaName only when
// the schema field's name is not its column, as with a StorageKey, so
// the output of a schema without one does not change.
func TestColumnPartsSchemaName(t *testing.T) {
	plain := columnParts(&OutField{GoName: "Title", Column: "title", Name: "title"})
	if want := []string{`Field: "Title"`, `Name: "title"`}; !slices.Equal(plain, want) {
		t.Errorf("plain: %q, want %q", plain, want)
	}
	keyed := columnParts(&OutField{GoName: "AddressCity", Column: "city", Name: "address_city"})
	if want := []string{`Field: "AddressCity"`, `Name: "city"`, `SchemaName: "address_city"`}; !slices.Equal(keyed, want) {
		t.Errorf("storage key: %q, want %q", keyed, want)
	}
}
