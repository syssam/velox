// Package reflectutil provides shared reflection helpers for the compiler packages.
// It exists to avoid duplicating small utility functions across compiler/compiler.go
// and compiler/load/schema.go, which cannot share code directly due to import cycles
// (internal imports load, so load cannot import internal).
package reflectutil

import "reflect"

// Indirect returns the type at the end of indirection,
// dereferencing all pointer layers.
func Indirect(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}
