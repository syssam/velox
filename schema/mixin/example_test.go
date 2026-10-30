package mixin_test

import (
	"fmt"

	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/mixin"
)

// Time adds created_at and updated_at to every schema that lists it.
func ExampleTime() {
	var m schema.Mixin = mixin.Time{}
	for _, f := range m.Fields() {
		fmt.Println(f.Descriptor().Name)
	}
	// Output:
	// created_at
	// updated_at
}
