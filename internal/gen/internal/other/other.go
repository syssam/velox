// Package other declares a schema in a second package, for the tests of
// package gen: an edge to it is an edge out of the schema package.
package other

import "github.com/syssam/velox2/schema"

// Thing is a schema of a package other than the one under test.
type Thing struct{ schema.Schema }
