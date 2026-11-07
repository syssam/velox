// Package engine is what velox's generic API runs on: statements, conditions, entity
// metadata, rendering, scoping, relations' loading, routing, transactions, and DB, the
// pipeline every statement goes through. Its types are public through aliases in the
// root package, which documents them; the root's DB wraps this one. Package dberr
// reads the drivers' errors. The plain data types are in package veloxtype, and
// veloxtype.go aliases them for the rest of this package.
package engine
