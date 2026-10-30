package field_test

import (
	"fmt"

	"github.com/syssam/velox2/schema/field"
)

// MaxLen counts bytes and MaxRuneLen characters. Both set the column's size.
func ExampleStringBuilder_MaxRuneLen() {
	title := field.String("title").NotEmpty().MaxRuneLen(100).Descriptor()
	fmt.Println(title.Name, title.Type, title.Size)
	// Output:
	// title string 100
}

// A nillable field is a NULL-able column and a pointer in the entity. An optional
// one stays NOT NULL, and needs no value on create.
func ExampleTime() {
	deleted := field.Time("deleted_at").Optional().Nillable().Descriptor()
	fmt.Println(deleted.Optional, deleted.Nillable)
	// Output:
	// true true
}

func ExampleEnum() {
	role := field.Enum("role").Values("viewer", "admin").Default("viewer").Descriptor()
	fmt.Println(role.Name, role.Type, role.Default)
	// Output:
	// role enum viewer
}
