package sql

import (
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genTypes generates the types.go file for the root package with shared types
// that are needed by root-level code but not already in velox.go or errors.go.
//
// Currently only generates Config alias. Error types are in errors.go,
// AggregateFunc/MaskNotFound/Op constants are in velox.go.
func genTypes(h gen.GeneratorHelper) *jen.File {
	f := h.NewFile(h.Pkg())

	// Config alias — wrapper files and entity clients reference this type.
	f.Comment("Config is an alias to runtime.Config.")
	f.Comment("Defined in runtime/ so entity sub-packages can import it without circular deps.")
	f.Type().Id("Config").Op("=").Qual(runtimePkg, "Config")

	return f
}
