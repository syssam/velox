package index_test

import (
	"fmt"

	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/schema/index"
)

// sqlschema.IndexWhere makes the index partial, on PostgreSQL and SQLite:
// here an email is unique among the rows that are not deleted.
func ExampleFields() {
	ix := index.Fields("org_id", "email").Unique().Annotations(sqlschema.IndexWhere("deleted_at IS NULL")).Descriptor()
	fmt.Println(ix.Fields, ix.Unique, ix.Annotations[0].(*sqlschema.IndexAnnotation).Where)
	// Output:
	// [org_id email] true deleted_at IS NULL
}
