package field_test

import (
	"fmt"

	"github.com/syssam/velox/schema/field"
)

// ExampleString demonstrates defining a string field with Unique and NotEmpty constraints.
func ExampleString() {
	f := field.String("email").
		Unique().
		NotEmpty()

	d := f.Descriptor()
	fmt.Println("name:", d.Name)
	fmt.Println("unique:", d.Unique)
	fmt.Println("validators:", len(d.Validators))
	// Output:
	// name: email
	// unique: true
	// validators: 1
}

// ExampleEnum demonstrates defining an enum field with allowed values and a default.
func ExampleEnum() {
	f := field.Enum("status").
		Values("active", "inactive", "pending").
		Default("active")

	d := f.Descriptor()
	fmt.Println("name:", d.Name)
	fmt.Println("values:", len(d.Enums))
	fmt.Println("default:", d.Default)
	// Output:
	// name: status
	// values: 3
	// default: active
}
