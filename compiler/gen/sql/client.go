package sql

import (
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genClient generates the client.go file.
func genClient(h gen.GeneratorHelper) *jen.File {
	f := h.NewFile(h.Pkg())
	graph := h.Graph()

	// Generate hooks and inters structs
	genHooksStruct(h, f)
	genIntersStruct(h, f)

	// Generate Client struct
	genClientStruct(h, f)

	// Generate Option type and options
	genOptions(h, f)

	// For compile-time check, reference graph
	_ = graph

	return f
}

// genHooksStruct generates the hooks struct.
func genHooksStruct(h gen.GeneratorHelper, f *jen.File) {
	f.Comment("hooks holds the hooks for all entity types.")
	f.Type().Id("hooks").StructFunc(func(group *jen.Group) {
		for _, t := range h.Graph().Nodes {
			group.Id(t.Name).Index().Id("Hook")
		}
	})
}

// genIntersStruct generates the interceptors struct.
func genIntersStruct(h gen.GeneratorHelper, f *jen.File) {
	f.Comment("inters holds the interceptors for all entity types.")
	f.Type().Id("inters").StructFunc(func(group *jen.Group) {
		for _, t := range h.Graph().Nodes {
			group.Id(t.Name).Index().Id("Interceptor")
		}
	})
}

// genClientStruct generates the Client struct and methods.
func genClientStruct(h gen.GeneratorHelper, f *jen.File) {
	graph := h.Graph()

	// migrate package path
	migratePkg := graph.Config.Package + "/migrate"

	// Client struct
	f.Comment("Client is the client that holds all entity clients.")
	f.Type().Id("Client").StructFunc(func(group *jen.Group) {
		group.Id("config")
		group.Id("debug").Bool()
		group.Id("log").Func().Params(jen.Op("...").Any())
		group.Id("hooks").Op("*").Id("hooks")
		group.Id("inters").Op("*").Id("inters")
		// tables is used for universal-id support in GraphQL Node interface.
		// Always include since gql_node.go references c.tables.nodeType
		// for Relay Node interface resolution.
		group.Id("tables").Id("tables")
		// Schema provides access to schema migration
		group.Comment("// Schema is the client for creating, migrating and dropping schema.")
		group.Id("Schema").Op("*").Qual(migratePkg, "Schema")

		// Entity clients
		for _, t := range graph.Nodes {
			group.Id(t.Name).Op("*").Id(t.ClientName())
		}
	})

	// config struct
	f.Comment("config holds the configuration of the client.")
	f.Type().Id("config").Struct(
		jen.Id("driver").Qual(dialectPkg(), "Driver"),
		jen.Id("debug").Bool(),
		jen.Id("log").Func().Params(jen.Op("...").Any()),
		jen.Id("hooks").Op("*").Id("hooks"),
		jen.Id("inters").Op("*").Id("inters"),
	)

	// Feature: sql/execquery - ExecContext/QueryContext methods on config
	if h.FeatureEnabled("sql/execquery") {
		genConfigExecQueryMethods(h, f)
	}

	// NewClient constructor
	f.Comment("NewClient creates a new client configured with the given options.")
	f.Func().Id("NewClient").Params(
		jen.Id("opts").Op("...").Id("Option"),
	).Op("*").Id("Client").Block(
		jen.Id("c").Op(":=").Id("config").Values(jen.Dict{
			jen.Id("log"):    jen.Qual("log", "Println"),
			jen.Id("hooks"):  jen.Op("&").Id("hooks").Values(),
			jen.Id("inters"): jen.Op("&").Id("inters").Values(),
		}),
		jen.For(jen.List(jen.Id("_"), jen.Id("opt")).Op(":=").Range().Id("opts")).Block(
			jen.Id("opt").Call(jen.Op("&").Id("c")),
		),
		jen.Id("client").Op(":=").Op("&").Id("Client").ValuesFunc(func(vals *jen.Group) {
			vals.Id("config").Op(":").Id("c")
			vals.Id("debug").Op(":").Id("c").Dot("debug")
			vals.Id("log").Op(":").Id("c").Dot("log")
			vals.Id("hooks").Op(":").Id("c").Dot("hooks")
			vals.Id("inters").Op(":").Id("c").Dot("inters")
			for _, t := range graph.Nodes {
				vals.Id(t.Name).Op(":").Id("New" + t.ClientName()).Call(jen.Id("c"))
			}
		}),
		jen.Id("client").Dot("Schema").Op("=").Qual(migratePkg, "NewSchema").Call(jen.Id("c").Dot("driver")),
		jen.Return(jen.Id("client")),
	)

	// Open opens a connection to the database
	f.Comment("Open opens a database connection and returns the client.")
	f.Func().Id("Open").Params(
		jen.Id("driverName").String(),
		jen.Id("dataSourceName").String(),
		jen.Id("opts").Op("...").Id("Option"),
	).Params(jen.Op("*").Id("Client"), jen.Error()).Block(
		jen.Switch(jen.Id("driverName")).BlockFunc(func(grp *jen.Group) {
			grp.Case(jen.Qual(dialectPkg(), "SQLite"), jen.Qual(dialectPkg(), "MySQL"), jen.Qual(dialectPkg(), "Postgres")).Block(
				jen.List(jen.Id("drv"), jen.Id("err")).Op(":=").Qual(h.SQLPkg(), "Open").Call(
					jen.Id("driverName"),
					jen.Id("dataSourceName"),
				),
				jen.If(jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Id("err")),
				),
				jen.Return(jen.Id("NewClient").Call(
					jen.Append(jen.Id("opts"), jen.Id("Driver").Call(jen.Id("drv"))).Op("..."),
				), jen.Nil()),
			)
			grp.Default().Block(
				jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(
					jen.Lit("unsupported driver: %q"),
					jen.Id("driverName"),
				)),
			)
		}),
	)

	// Tx starts a transaction
	f.Comment("Tx returns a new transactional client.")
	f.Func().Params(jen.Id("c").Op("*").Id("Client")).Id("Tx").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Op("*").Id("Tx"), jen.Error()).Block(
		jen.If(
			jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id("c").Dot("config").Dot("driver").Op(".").Parens(jen.Op("*").Id("txDriver")),
			jen.Id("ok"),
		).Block(
			jen.Return(jen.Nil(), jen.Qual("errors", "New").Call(jen.Lit("cannot start a transaction within a transaction"))),
		),
		jen.List(jen.Id("tx"), jen.Id("err")).Op(":=").Id("newTx").Call(jen.Id("ctx"), jen.Id("c").Dot("config")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("starting a transaction: %w"), jen.Id("err"))),
		),
		jen.Return(jen.Id("tx"), jen.Nil()),
	)

	// BeginTx starts a transaction with options
	f.Comment("BeginTx returns a transactional client with specified options.")
	f.Func().Params(jen.Id("c").Op("*").Id("Client")).Id("BeginTx").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("opts").Op("*").Qual("database/sql", "TxOptions"),
	).Params(jen.Op("*").Id("Tx"), jen.Error()).Block(
		jen.If(
			jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id("c").Dot("config").Dot("driver").Op(".").Parens(jen.Op("*").Id("txDriver")),
			jen.Id("ok"),
		).Block(
			jen.Return(jen.Nil(), jen.Qual("errors", "New").Call(jen.Lit("cannot start a transaction within a transaction"))),
		),
		jen.List(jen.Id("tx"), jen.Id("err")).Op(":=").Id("newTxWithOptions").Call(
			jen.Id("ctx"),
			jen.Id("c").Dot("config"),
			jen.Id("opts"),
		),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("starting a transaction: %w"), jen.Id("err"))),
		),
		jen.Return(jen.Id("tx"), jen.Nil()),
	)

	// Debug returns a debug client
	f.Comment("Debug returns a new debug-client.")
	f.Func().Params(jen.Id("c").Op("*").Id("Client")).Id("Debug").Params().Op("*").Id("Client").Block(
		jen.If(jen.Id("c").Dot("debug")).Block(
			jen.Return(jen.Id("c")),
		),
		jen.Id("cfg").Op(":=").Id("c").Dot("config"),
		jen.Id("cfg").Dot("driver").Op("=").Qual(dialectPkg(), "Debug").Call(
			jen.Id("c").Dot("driver"),
			jen.Id("c").Dot("log"),
		),
		jen.Id("client").Op(":=").Op("&").Id("Client").Values(jen.Dict{
			jen.Id("config"): jen.Id("cfg"),
			// Preserve the tables cache from the parent client to avoid redundant DB queries
			jen.Id("tables"): jen.Id("c").Dot("tables"),
		}),
		jen.Id("client").Dot("init").Call(),
		jen.Id("client").Dot("Schema").Op("=").Qual(graph.Config.Package+"/migrate", "NewSchema").Call(jen.Id("cfg").Dot("driver")),
		jen.Return(jen.Id("client")),
	)

	// Close closes the database connection
	f.Comment("Close closes the database connection and prevents new queries from starting.")
	f.Func().Params(jen.Id("c").Op("*").Id("Client")).Id("Close").Params().Error().Block(
		jen.Return(jen.Id("c").Dot("config").Dot("driver").Dot("Close").Call()),
	)

	// Use adds hooks
	f.Comment("Use adds the mutation hooks to all the entity clients.")
	f.Func().Params(jen.Id("c").Op("*").Id("Client")).Id("Use").Params(
		jen.Id("hooks").Op("...").Id("Hook"),
	).BlockFunc(func(grp *jen.Group) {
		for _, t := range graph.Nodes {
			grp.Id("c").Dot(t.Name).Dot("Use").Call(jen.Id("hooks").Op("..."))
		}
	})

	// Intercept adds interceptors
	f.Comment("Intercept adds the query interceptors to all the entity clients.")
	f.Func().Params(jen.Id("c").Op("*").Id("Client")).Id("Intercept").Params(
		jen.Id("interceptors").Op("...").Id("Interceptor"),
	).BlockFunc(func(grp *jen.Group) {
		for _, t := range graph.Nodes {
			grp.Id("c").Dot(t.Name).Dot("Intercept").Call(jen.Id("interceptors").Op("..."))
		}
	})

	// init initializes all entity clients
	f.Func().Params(jen.Id("c").Op("*").Id("Client")).Id("init").Params().BlockFunc(func(grp *jen.Group) {
		for _, t := range graph.Nodes {
			grp.Id("c").Dot(t.Name).Op("=").Id("New" + t.ClientName()).Call(
				jen.Id("c").Dot("config"),
			)
		}
	})
}

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
