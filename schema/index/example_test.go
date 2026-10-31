package index_test

import (
	"fmt"

	"github.com/syssam/velox/schema/index"
)

// Where makes the index partial, on PostgreSQL and SQLite: here an email is unique
// among the rows that are not deleted.
func ExampleFields() {
	ix := index.Fields("org_id", "email").Unique().Where("deleted_at IS NULL").Descriptor()
	fmt.Println(ix.Fields, ix.Unique, ix.Where)
	// Output:
	// [org_id email] true deleted_at IS NULL
}
