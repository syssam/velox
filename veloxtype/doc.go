// Package veloxtype holds velox's plain data types: dialects, the kinds and parts
// of a statement, what a statement or a transaction did, the errors velox
// reports and the column metadata generated code writes. Each has only
// exported fields, and reaches only the others and the standard library.
//
// The root package re-exports every name here under the same name, as an
// alias, so velox.Result and veloxtype.Result are one type. Application code
// writes velox.Result and does not need to import this package. It is public so
// that the documentation can show these types' fields and methods.
package veloxtype
