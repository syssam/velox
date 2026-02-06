package sql

import "github.com/syssam/velox/compiler/gen"

// dialectPkg returns the import path for the dialect package.
func dialectPkg() string {
	return "github.com/syssam/velox/dialect"
}

// schemaPkg returns the import path for the schema field package.
func schemaPkg() string {
	return "github.com/syssam/velox/schema/field"
}

// edgeGoTypeCode returns the Go type for an edge as a string.
func edgeGoTypeCode(e *gen.Edge) string {
	if e.Unique {
		return "*" + e.Type.Name
	}
	return "[]*" + e.Type.Name
}
