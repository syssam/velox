package sql

import (
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genClient generates the client.go file.
func genClient(h gen.GeneratorHelper) *jen.File {
	f := h.NewFile(h.Pkg())

	// Side-effect import the generated query/ package so its init() runs
	// RegisterQueryFactory for every entity. Without this, the first call
	// to c.User.Query() panics in runtime.NewEntityQuery — query/ is
	// otherwise only pulled in transitively by intercept/, privacy/, or
	// filter/, so projects that use none of those features would hit
	// the panic unless they remembered a manual blank import.
	if len(h.Graph().Nodes) > 0 {
		f.Anon(h.QueryPkg())
	}

	// ErrTxStarted sentinel
	f.Comment("ErrTxStarted is returned when trying to start a new transaction from a transactional client.")
	f.Var().Id("ErrTxStarted").Op("=").Qual("errors", "New").Call(jen.Lit("velox: cannot start a transaction within a transaction"))

	// Hooks and interceptors are stored in generated typed hooks/inters structs
	// with one field per entity. Runtime.Config bridges via callbacks for
	// direct field access — no map lookup, no mutex.

	// Generate Client struct
	genClientStruct(h, f)

	// Generate Option type and options — only in root mode where config is locally defined.
	// In entity mode, options are defined elsewhere.
	genOptions(h, f)

	return f
}

// genClientStruct generates the Client struct and methods.
func genClientStruct(h gen.GeneratorHelper, f *jen.File) {
	graph := h.Graph()

	// migrate package path
	migratePkg := graph.Package + "/migrate"

	// Entity package path for interface types.
	entityPkg := h.SharedEntityPkg()

	// Client struct
	f.Comment("Client is the client that holds all entity clients.")
	f.Type().Id("Client").StructFunc(func(group *jen.Group) {
		group.Id("config")
		// Schema provides access to schema migration
		group.Comment("// Schema is the client for creating, migrating and dropping schema.")
		group.Id("Schema").Op("*").Qual(migratePkg, "Schema")

		// Per-entity client fields: concrete types from client/{entity}/ sub-packages.
		for _, t := range graph.Nodes {
			clientPkg := graph.Package + "/client/" + t.PackageDir()
			f.ImportName(clientPkg, t.PackageDir()+"client")
			group.Id(t.Name).Op("*").Qual(clientPkg, t.ClientName())
		}
	})

	// config struct — uses entity.HookStore and entity.InterceptorStore directly.
	f.Comment("config holds the configuration of the client.")
	f.Type().Id("config").StructFunc(func(group *jen.Group) {
		group.Id("driver").Qual(dialectPkg(), "Driver")
		group.Id("debug").Bool()
		group.Id("log").Func().Params(jen.Op("...").Any())
		group.Id("hooks").Op("*").Qual(entityPkg, "HookStore")
		group.Id("inters").Op("*").Qual(entityPkg, "InterceptorStore")
		if h.FeatureEnabled(gen.FeatureSchemaConfig.Name) {
			group.Id("schemaConfig").Id("SchemaConfig")
		}
	})

	// runtimeConfig returns a runtime.Config for passing to entity sub-packages.
	// HookStore and InterStore are passed as any, type-asserted once by each entity
	// client constructor for direct field access thereafter.
	f.Comment("runtimeConfig returns a runtime.Config derived from the local config.")
	f.Comment("HookStore and InterStore carry pointers to the typed store structs,")
	f.Comment("type-asserted once by each entity client constructor.")
	f.Func().Params(jen.Id("c").Op("*").Id("config")).Id("runtimeConfig").Params().Qual(runtimePkg, "Config").Block(
		jen.Return(jen.Qual(runtimePkg, "Config").Values(jen.Dict{
			jen.Id("Driver"):     jen.Id("c").Dot("driver"),
			jen.Id("Debug"):      jen.Id("c").Dot("debug"),
			jen.Id("Log"):        jen.Id("c").Dot("log"),
			jen.Id("HookStore"):  jen.Id("c").Dot("hooks"),
			jen.Id("InterStore"): jen.Id("c").Dot("inters"),
		})),
	)

	// Public RuntimeConfig method for external use.
	f.Comment("RuntimeConfig returns a runtime.Config for use by entity sub-packages.")
	f.Func().Params(jen.Id("c").Op("*").Id("Client")).Id("RuntimeConfig").Params().Qual(runtimePkg, "Config").Block(
		jen.Return(jen.Id("c").Dot("config").Dot("runtimeConfig").Call()),
	)

	// SchemaConfig type alias + AlternateSchema option (gated on feature)
	if h.FeatureEnabled(gen.FeatureSchemaConfig.Name) {
		f.Comment("SchemaConfig is an alias for the generated internal.SchemaConfig type.")
		f.Type().Id("SchemaConfig").Op("=").Qual(h.InternalPkg(), "SchemaConfig")

		f.Comment("AlternateSchema returns an option to set alternate schema names for all tables.")
		f.Func().Id("AlternateSchema").Params(
			jen.Id("sc").Id("SchemaConfig"),
		).Id("Option").Block(
			jen.Return(jen.Func().Params(jen.Id("c").Op("*").Id("config")).Block(
				jen.Id("c").Dot("schemaConfig").Op("=").Id("sc"),
			)),
		)
	}

	// Feature: sql/execquery - ExecContext/QueryContext methods on config
	if h.FeatureEnabled(gen.FeatureExecQuery.Name) {
		genConfigExecQueryMethods(h, f)
	}

	// NewClient constructor
	f.Comment("NewClient creates a new client configured with the given options.")
	f.Func().Id("NewClient").Params(
		jen.Id("opts").Op("...").Id("Option"),
	).Op("*").Id("Client").BlockFunc(func(grp *jen.Group) {
		grp.Id("c").Op(":=").Id("config").Values(jen.Dict{
			jen.Id("log"):    jen.Func().Params(jen.Id("v").Op("...").Any()).Block(jen.Qual("log/slog", "Info").Call(jen.Qual("fmt", "Sprint").Call(jen.Id("v").Op("...")))),
			jen.Id("hooks"):  jen.Op("&").Qual(entityPkg, "HookStore").Values(),
			jen.Id("inters"): jen.Op("&").Qual(entityPkg, "InterceptorStore").Values(),
		})
		grp.For(jen.List(jen.Id("_"), jen.Id("opt")).Op(":=").Range().Id("opts")).Block(
			jen.Id("opt").Call(jen.Op("&").Id("c")),
		)
		// Issue 5: Auto-wrap driver with debug logging when debug option is set.
		grp.If(jen.Id("c").Dot("debug")).Block(
			jen.Id("c").Dot("driver").Op("=").Qual(dialectPkg(), "Debug").Call(
				jen.Id("c").Dot("driver"),
				jen.Id("c").Dot("log"),
			),
		)
		grp.Id("client").Op(":=").Op("&").Id("Client").Values(jen.Dict{
			jen.Id("config"): jen.Id("c"),
		})
		// Entity mode: populate per-entity interface fields from registry.
		grp.Id("client").Dot("init").Call()
		grp.Id("client").Dot("Schema").Op("=").Qual(migratePkg, "NewSchema").Call(jen.Id("c").Dot("driver"))
		grp.Return(jen.Id("client"))
	})

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
			jen.Return(jen.Nil(), jen.Id("ErrTxStarted")),
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
			jen.Return(jen.Nil(), jen.Id("ErrTxStarted")),
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
	f.Func().Params(jen.Id("c").Op("*").Id("Client")).Id("Debug").Params().Op("*").Id("Client").BlockFunc(func(grp *jen.Group) {
		grp.If(jen.Id("c").Dot("debug")).Block(
			jen.Return(jen.Id("c")),
		)
		grp.Id("cfg").Op(":=").Id("c").Dot("config")
		grp.Id("cfg").Dot("driver").Op("=").Qual(dialectPkg(), "Debug").Call(
			jen.Id("c").Dot("driver"),
			jen.Id("c").Dot("log"),
		)
		grp.Id("cfg").Dot("debug").Op("=").True()
		grp.Id("client").Op(":=").Op("&").Id("Client").Values(jen.Dict{
			jen.Id("config"): jen.Id("cfg"),
		})
		// Re-init per-entity clients with the debug config.
		grp.Id("client").Dot("init").Call()
		grp.Id("client").Dot("Schema").Op("=").Qual(graph.Package+"/migrate", "NewSchema").Call(jen.Id("cfg").Dot("driver"))
		grp.Return(jen.Id("client"))
	})

	// Close closes the database connection
	f.Comment("Close closes the database connection and prevents new queries from starting.")
	f.Func().Params(jen.Id("c").Op("*").Id("Client")).Id("Close").Params().Error().Block(
		jen.Return(jen.Id("c").Dot("config").Dot("driver").Dot("Close").Call()),
	)

	// Use delegates to HookStore.AppendAll — O(1) generated code regardless of entity count.
	f.Comment("Use adds the mutation hooks to all the entity clients.")
	f.Comment("")
	f.Comment("All Use calls must complete before concurrent query or mutation")
	f.Comment("execution begins. Use is intended for application startup (e.g. in")
	f.Comment("main or TestMain), not for runtime registration. No synchronization")
	f.Comment("is provided — violations are caught by go test -race.")
	f.Func().Params(jen.Id("c").Op("*").Id("Client")).Id("Use").Params(
		jen.Id("hooks").Op("...").Id("Hook"),
	).Block(
		jen.Id("c").Dot("config").Dot("hooks").Dot("AppendAll").Call(jen.Id("hooks").Op("...")),
	)

	f.Comment("Intercept adds the query interceptors to all the entity clients.")
	f.Comment("")
	f.Comment("All Intercept calls must complete before concurrent query execution")
	f.Comment("begins. Intercept is intended for application startup (e.g. in")
	f.Comment("main or TestMain), not for runtime registration. No synchronization")
	f.Comment("is provided — violations are caught by go test -race.")
	f.Func().Params(jen.Id("c").Op("*").Id("Client")).Id("Intercept").Params(
		jen.Id("interceptors").Op("...").Id("Interceptor"),
	).Block(
		jen.Id("c").Dot("config").Dot("inters").Dot("AppendAll").Call(jen.Id("interceptors").Op("...")),
	)

	// Mutate dispatches to the correct entity client based on mutation type.
	genClientMutateMethod(h, f, graph)

	// init() constructs per-entity client fields directly from sub-packages.
	f.Comment("init constructs per-entity client fields directly.")
	f.Func().Params(jen.Id("c").Op("*").Id("Client")).Id("init").Params().BlockFunc(func(grp *jen.Group) {
		grp.Id("cfg").Op(":=").Id("c").Dot("config").Dot("runtimeConfig").Call()
		for _, t := range graph.Nodes {
			clientPkg := graph.Package + "/client/" + t.PackageDir()
			f.ImportName(clientPkg, t.PackageDir()+"client")
			grp.Id("c").Dot(t.Name).Op("=").Qual(clientPkg, "New"+t.ClientName()).Call(
				jen.Id("cfg"),
			)
		}
	})
}

// genClientMutateMethod generates the Mutate method on Client that dispatches
// to the correct entity client based on the mutation's concrete type.
func genClientMutateMethod(_ gen.GeneratorHelper, f *jen.File, _ *gen.Graph) {
	// Entity sub-package mode: use runtime.FindMutator registry dispatch.
	f.Comment("Mutate executes the given mutation against the correct entity client.")
	f.Func().Params(jen.Id("c").Op("*").Id("Client")).Id("Mutate").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("m").Id("Mutation"),
	).Params(jen.Id("Value"), jen.Error()).Block(
		jen.Id("fn").Op(":=").Qual(runtimePkg, "FindMutator").Call(jen.Id("m").Dot("Type").Call()),
		jen.If(jen.Id("fn").Op("==").Nil()).Block(
			jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("unknown mutation type %T"), jen.Id("m"))),
		),
		jen.Return(jen.Id("fn").Call(jen.Id("ctx"), jen.Id("c").Dot("config").Dot("runtimeConfig").Call(), jen.Id("m"))),
	)
}
