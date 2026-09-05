//go:build tools

// This file exists so `go mod tidy` keeps the code-generation dependencies.
// generate.go carries a `//go:build ignore` tag, so tidy never sees its
// imports and would otherwise strip velox's compiler packages from go.sum,
// breaking `go run generate.go`.
package main

import (
	_ "github.com/syssam/velox/compiler"
	_ "github.com/syssam/velox/compiler/gen"
)
