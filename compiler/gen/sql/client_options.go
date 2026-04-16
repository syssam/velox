package sql

import (
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genOptions generates Option type and option functions.
func genOptions(_ gen.GeneratorHelper, f *jen.File) {
	// Option type
	f.Comment("Option function to configure the client.")
	f.Type().Id("Option").Func().Params(jen.Op("*").Id("config"))

	// Driver option
	f.Comment("Driver sets the driver for the client.")
	f.Func().Id("Driver").Params(
		jen.Id("driver").Qual(dialectPkg(), "Driver"),
	).Id("Option").Block(
		jen.Return(jen.Func().Params(jen.Id("c").Op("*").Id("config")).Block(
			jen.Id("c").Dot("driver").Op("=").Id("driver"),
		)),
	)

	// Debug option
	f.Comment("Debug enables debug logging on the client.")
	f.Func().Id("Debug").Params().Id("Option").Block(
		jen.Return(jen.Func().Params(jen.Id("c").Op("*").Id("config")).Block(
			jen.Id("c").Dot("debug").Op("=").True(),
		)),
	)

	// Log option
	f.Comment("Log sets the logging function for debug mode.")
	f.Func().Id("Log").Params(
		jen.Id("fn").Func().Params(jen.Op("...").Any()),
	).Id("Option").Block(
		jen.Return(jen.Func().Params(jen.Id("c").Op("*").Id("config")).Block(
			jen.Id("c").Dot("log").Op("=").Id("fn"),
		)),
	)
}

// genConfigExecQueryMethods generates ExecContext/QueryContext methods on config.
// This is part of the sql/execquery feature.
func genConfigExecQueryMethods(_ gen.GeneratorHelper, f *jen.File) {
	stdsqlPkg := "database/sql"

	// ExecContext method
	f.Comment("ExecContext allows calling the underlying ExecContext method of the driver if it is supported by it.")
	f.Comment("See, database/sql#DB.ExecContext for more information.")
	f.Func().Params(jen.Id("c").Op("*").Id("config")).Id("ExecContext").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("query").String(),
		jen.Id("args").Op("...").Any(),
	).Params(jen.Qual(stdsqlPkg, "Result"), jen.Error()).Block(
		jen.List(jen.Id("ex"), jen.Id("ok")).Op(":=").Id("c").Dot("driver").Op(".").Parens(
			jen.Interface(
				jen.Id("ExecContext").Params(jen.Qual("context", "Context"), jen.String(), jen.Op("...").Any()).Params(jen.Qual(stdsqlPkg, "Result"), jen.Error()),
			),
		),
		jen.If(jen.Op("!").Id("ok")).Block(
			jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("Driver.ExecContext is not supported"))),
		),
		jen.Return(jen.Id("ex").Dot("ExecContext").Call(jen.Id("ctx"), jen.Id("query"), jen.Id("args").Op("..."))),
	)

	// QueryContext method
	f.Comment("QueryContext allows calling the underlying QueryContext method of the driver if it is supported by it.")
	f.Comment("See, database/sql#DB.QueryContext for more information.")
	f.Func().Params(jen.Id("c").Op("*").Id("config")).Id("QueryContext").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("query").String(),
		jen.Id("args").Op("...").Any(),
	).Params(jen.Op("*").Qual(stdsqlPkg, "Rows"), jen.Error()).Block(
		jen.List(jen.Id("q"), jen.Id("ok")).Op(":=").Id("c").Dot("driver").Op(".").Parens(
			jen.Interface(
				jen.Id("QueryContext").Params(jen.Qual("context", "Context"), jen.String(), jen.Op("...").Any()).Params(jen.Op("*").Qual(stdsqlPkg, "Rows"), jen.Error()),
			),
		),
		jen.If(jen.Op("!").Id("ok")).Block(
			jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("Driver.QueryContext is not supported"))),
		),
		jen.Return(jen.Id("q").Dot("QueryContext").Call(jen.Id("ctx"), jen.Id("query"), jen.Id("args").Op("..."))),
	)
}
