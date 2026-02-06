package sql

import (
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genTx generates the tx.go file with transaction support.
func genTx(h gen.GeneratorHelper) *jen.File {
	f := h.NewFile(h.Pkg())
	graph := h.Graph()

	// Committer interface
	f.Comment("Committer is the interface that wraps the Commit method.")
	f.Type().Id("Committer").Interface(
		jen.Id("Commit").Params(jen.Qual("context", "Context"), jen.Op("*").Id("Tx")).Error(),
	)

	// CommitFunc adapter
	f.Comment("CommitFunc is an adapter to allow the use of ordinary function as Committer.")
	f.Type().Id("CommitFunc").Func().Params(jen.Qual("context", "Context"), jen.Op("*").Id("Tx")).Error()

	// Commit method on CommitFunc
	f.Comment("Commit calls f(ctx, tx).")
	f.Func().Params(jen.Id("f").Id("CommitFunc")).Id("Commit").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("tx").Op("*").Id("Tx"),
	).Error().Block(
		jen.Return(jen.Id("f").Call(jen.Id("ctx"), jen.Id("tx"))),
	)

	// CommitHook type
	f.Comment("CommitHook defines the \"commit middleware\". A function that gets a Committer")
	f.Comment("and returns a Committer. For example:")
	f.Comment("")
	f.Comment("  hook := func(next Committer) Committer {")
	f.Comment("      return CommitFunc(func(ctx context.Context, tx *Tx) error {")
	f.Comment("          // Do something before.")
	f.Comment("          if err := next.Commit(ctx, tx); err != nil {")
	f.Comment("              return err")
	f.Comment("          }")
	f.Comment("          // Do something after.")
	f.Comment("          return nil")
	f.Comment("      })")
	f.Comment("  }")
	f.Type().Id("CommitHook").Func().Params(jen.Id("Committer")).Id("Committer")

	// Rollbacker interface
	f.Comment("Rollbacker is the interface that wraps the Rollback method.")
	f.Type().Id("Rollbacker").Interface(
		jen.Id("Rollback").Params(jen.Qual("context", "Context"), jen.Op("*").Id("Tx")).Error(),
	)

	// RollbackFunc adapter
	f.Comment("RollbackFunc is an adapter to allow the use of ordinary function as Rollbacker.")
	f.Type().Id("RollbackFunc").Func().Params(jen.Qual("context", "Context"), jen.Op("*").Id("Tx")).Error()

	// Rollback method on RollbackFunc
	f.Comment("Rollback calls f(ctx, tx).")
	f.Func().Params(jen.Id("f").Id("RollbackFunc")).Id("Rollback").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("tx").Op("*").Id("Tx"),
	).Error().Block(
		jen.Return(jen.Id("f").Call(jen.Id("ctx"), jen.Id("tx"))),
	)

	// RollbackHook type
	f.Comment("RollbackHook defines the \"rollback middleware\". A function that gets a Rollbacker")
	f.Comment("and returns a Rollbacker. For example:")
	f.Comment("")
	f.Comment("  hook := func(next Rollbacker) Rollbacker {")
	f.Comment("      return RollbackFunc(func(ctx context.Context, tx *Tx) error {")
	f.Comment("          // Do something before.")
	f.Comment("          if err := next.Rollback(ctx, tx); err != nil {")
	f.Comment("              return err")
	f.Comment("          }")
	f.Comment("          // Do something after.")
	f.Comment("          return nil")
	f.Comment("      })")
	f.Comment("  }")
	f.Type().Id("RollbackHook").Func().Params(jen.Id("Rollbacker")).Id("Rollbacker")

	// Tx struct
	f.Comment("Tx is a transactional client.")
	f.Type().Id("Tx").StructFunc(func(group *jen.Group) {
		group.Id("config")
		for _, t := range graph.Nodes {
			group.Id(t.Name).Op("*").Id(t.ClientName())
		}
		group.Comment("lazily loaded.")
		group.Id("client").Op("*").Id("Client")
		group.Id("clientOnce").Qual("sync", "Once")
		group.Id("ctx").Qual("context", "Context")
	})

	// newTx
	f.Comment("newTx creates a new transaction.")
	f.Func().Id("newTx").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("c").Id("config"),
	).Params(jen.Op("*").Id("Tx"), jen.Error()).Block(
		jen.Return(jen.Id("newTxWithOptions").Call(jen.Id("ctx"), jen.Id("c"), jen.Nil())),
	)

	// newTxWithOptions
	f.Comment("newTxWithOptions creates a new transaction with the given options.")
	f.Func().Id("newTxWithOptions").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("c").Id("config"),
		jen.Id("opts").Op("*").Qual("database/sql", "TxOptions"),
	).Params(jen.Op("*").Id("Tx"), jen.Error()).Block(
		jen.If(
			jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id("c").Dot("driver").Op(".").Parens(jen.Op("*").Id("txDriver")),
			jen.Id("ok"),
		).Block(
			jen.Return(jen.Nil(), jen.Qual("errors", "New").Call(jen.Lit("already in a transaction"))),
		),
		// Safe type assertion for BeginTx interface
		jen.List(jen.Id("drv"), jen.Id("ok")).Op(":=").Id("c").Dot("driver").Op(".").Parens(
			jen.Interface(
				jen.Id("BeginTx").Params(jen.Qual("context", "Context"), jen.Op("*").Qual("database/sql", "TxOptions")).Params(jen.Qual(dialectPkg(), "Tx"), jen.Error()),
			),
		),
		jen.If(jen.Op("!").Id("ok")).Block(
			jen.Return(jen.Nil(), jen.Qual("errors", "New").Call(jen.Lit("driver does not support transactions"))),
		),
		jen.List(jen.Id("tx"), jen.Id("err")).Op(":=").Id("drv").Dot("BeginTx").Call(jen.Id("ctx"), jen.Id("opts")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("starting transaction: %w"), jen.Id("err"))),
		),
		jen.Id("cfg").Op(":=").Id("c"),
		jen.Id("cfg").Dot("driver").Op("=").Op("&").Id("txDriver").Values(jen.Dict{
			jen.Id("tx"):         jen.Id("tx"),
			jen.Id("drv"):        jen.Id("c").Dot("driver"),
			jen.Id("onCommit"):   jen.Make(jen.Index().Id("CommitHook"), jen.Lit(0)),
			jen.Id("onRollback"): jen.Make(jen.Index().Id("RollbackHook"), jen.Lit(0)),
		}),
		jen.Id("t").Op(":=").Op("&").Id("Tx").Values(jen.Dict{
			jen.Id("config"): jen.Id("cfg"),
			jen.Id("ctx"):    jen.Id("ctx"),
		}),
		jen.Id("t").Dot("init").Call(),
		jen.Return(jen.Id("t"), jen.Nil()),
	)

	// init
	f.Comment("init initializes the entity clients.")
	f.Func().Params(jen.Id("tx").Op("*").Id("Tx")).Id("init").Params().BlockFunc(func(grp *jen.Group) {
		for _, t := range graph.Nodes {
			grp.Id("tx").Dot(t.Name).Op("=").Id("New" + t.ClientName()).Call(jen.Id("tx").Dot("config"))
		}
	})

	// Commit with middleware chain pattern
	f.Comment("Commit commits the transaction.")
	f.Func().Params(jen.Id("tx").Op("*").Id("Tx")).Id("Commit").Params().Error().Block(
		jen.List(jen.Id("txDriver"), jen.Id("ok")).Op(":=").Id("tx").Dot("config").Dot("driver").Op(".").Parens(jen.Op("*").Id("txDriver")),
		jen.If(jen.Op("!").Id("ok")).Block(jen.Return(jen.Qual("errors", "New").Call(jen.Lit("not in a transaction")))),
		// Create the base committer that performs the actual commit
		jen.Var().Id("fn").Id("Committer").Op("=").Id("CommitFunc").Call(
			jen.Func().Params(jen.Qual("context", "Context"), jen.Op("*").Id("Tx")).Error().Block(
				jen.Return(jen.Id("txDriver").Dot("tx").Dot("Commit").Call()),
			),
		),
		// Copy hooks under lock
		jen.Id("txDriver").Dot("mu").Dot("Lock").Call(),
		jen.Id("hooks").Op(":=").Append(jen.Index().Id("CommitHook").Call(jen.Nil()), jen.Id("txDriver").Dot("onCommit").Op("...")),
		jen.Id("txDriver").Dot("mu").Dot("Unlock").Call(),
		// Apply hooks in reverse order (middleware chain)
		jen.For(
			jen.Id("i").Op(":=").Len(jen.Id("hooks")).Op("-").Lit(1),
			jen.Id("i").Op(">=").Lit(0),
			jen.Id("i").Op("--"),
		).Block(
			jen.Id("fn").Op("=").Id("hooks").Index(jen.Id("i")).Call(jen.Id("fn")),
		),
		jen.Return(jen.Id("fn").Dot("Commit").Call(jen.Id("tx").Dot("ctx"), jen.Id("tx"))),
	)

	// Rollback with middleware chain pattern
	f.Comment("Rollback rolls back the transaction.")
	f.Func().Params(jen.Id("tx").Op("*").Id("Tx")).Id("Rollback").Params().Error().Block(
		jen.List(jen.Id("txDriver"), jen.Id("ok")).Op(":=").Id("tx").Dot("config").Dot("driver").Op(".").Parens(jen.Op("*").Id("txDriver")),
		jen.If(jen.Op("!").Id("ok")).Block(jen.Return(jen.Qual("errors", "New").Call(jen.Lit("not in a transaction")))),
		// Create the base rollbacker that performs the actual rollback
		jen.Var().Id("fn").Id("Rollbacker").Op("=").Id("RollbackFunc").Call(
			jen.Func().Params(jen.Qual("context", "Context"), jen.Op("*").Id("Tx")).Error().Block(
				jen.Return(jen.Id("txDriver").Dot("tx").Dot("Rollback").Call()),
			),
		),
		// Copy hooks under lock
		jen.Id("txDriver").Dot("mu").Dot("Lock").Call(),
		jen.Id("hooks").Op(":=").Append(jen.Index().Id("RollbackHook").Call(jen.Nil()), jen.Id("txDriver").Dot("onRollback").Op("...")),
		jen.Id("txDriver").Dot("mu").Dot("Unlock").Call(),
		// Apply hooks in reverse order (middleware chain)
		jen.For(
			jen.Id("i").Op(":=").Len(jen.Id("hooks")).Op("-").Lit(1),
			jen.Id("i").Op(">=").Lit(0),
			jen.Id("i").Op("--"),
		).Block(
			jen.Id("fn").Op("=").Id("hooks").Index(jen.Id("i")).Call(jen.Id("fn")),
		),
		jen.Return(jen.Id("fn").Dot("Rollback").Call(jen.Id("tx").Dot("ctx"), jen.Id("tx"))),
	)

	// OnCommit with CommitHook
	f.Comment("OnCommit adds a hook to call on commit.")
	f.Func().Params(jen.Id("tx").Op("*").Id("Tx")).Id("OnCommit").Params(jen.Id("f").Id("CommitHook")).Block(
		jen.Id("txDriver").Op(":=").Id("tx").Dot("config").Dot("driver").Op(".").Parens(jen.Op("*").Id("txDriver")),
		jen.Id("txDriver").Dot("mu").Dot("Lock").Call(),
		jen.Id("txDriver").Dot("onCommit").Op("=").Append(jen.Id("txDriver").Dot("onCommit"), jen.Id("f")),
		jen.Id("txDriver").Dot("mu").Dot("Unlock").Call(),
	)

	// OnRollback with RollbackHook
	f.Comment("OnRollback adds a hook to call on rollback.")
	f.Func().Params(jen.Id("tx").Op("*").Id("Tx")).Id("OnRollback").Params(jen.Id("f").Id("RollbackHook")).Block(
		jen.Id("txDriver").Op(":=").Id("tx").Dot("config").Dot("driver").Op(".").Parens(jen.Op("*").Id("txDriver")),
		jen.Id("txDriver").Dot("mu").Dot("Lock").Call(),
		jen.Id("txDriver").Dot("onRollback").Op("=").Append(jen.Id("txDriver").Dot("onRollback"), jen.Id("f")),
		jen.Id("txDriver").Dot("mu").Dot("Unlock").Call(),
	)

	// Context returns the transaction context.
	f.Comment("Context returns the transaction context.")
	f.Func().Params(jen.Id("tx").Op("*").Id("Tx")).Id("Context").Params().Qual("context", "Context").Block(
		jen.Return(jen.Id("tx").Dot("ctx")),
	)

	// Client
	f.Comment("Client returns a Client that binds to current transaction.")
	f.Func().Params(jen.Id("tx").Op("*").Id("Tx")).Id("Client").Params().Op("*").Id("Client").Block(
		jen.Id("tx").Dot("clientOnce").Dot("Do").Call(jen.Func().Params().Block(
			jen.Id("tx").Dot("client").Op("=").Op("&").Id("Client").Values(jen.Dict{jen.Id("config"): jen.Id("tx").Dot("config")}),
			jen.Id("tx").Dot("client").Dot("init").Call(),
		)),
		jen.Return(jen.Id("tx").Dot("client")),
	)

	// txDriver struct with middleware hook types
	f.Comment("txDriver wraps the dialect driver to provide transaction capabilities.")
	f.Type().Id("txDriver").Struct(
		jen.Id("tx").Qual(dialectPkg(), "Tx"),
		jen.Id("drv").Qual(dialectPkg(), "Driver"),
		jen.Id("mu").Qual("sync", "Mutex"),
		jen.Id("onCommit").Index().Id("CommitHook"),
		jen.Id("onRollback").Index().Id("RollbackHook"),
	)

	// txDriver methods
	f.Comment("Exec implements the dialect.Driver interface.")
	f.Func().Params(jen.Id("tx").Op("*").Id("txDriver")).Id("Exec").Params(
		jen.Id("ctx").Qual("context", "Context"), jen.Id("query").String(), jen.Id("args").Any(), jen.Id("v").Any(),
	).Error().Block(jen.Return(jen.Id("tx").Dot("tx").Dot("Exec").Call(jen.Id("ctx"), jen.Id("query"), jen.Id("args"), jen.Id("v"))))

	f.Comment("Query implements the dialect.Driver interface.")
	f.Func().Params(jen.Id("tx").Op("*").Id("txDriver")).Id("Query").Params(
		jen.Id("ctx").Qual("context", "Context"), jen.Id("query").String(), jen.Id("args").Any(), jen.Id("v").Any(),
	).Error().Block(jen.Return(jen.Id("tx").Dot("tx").Dot("Query").Call(jen.Id("ctx"), jen.Id("query"), jen.Id("args"), jen.Id("v"))))

	f.Comment("Close is a noop close.")
	f.Func().Params(jen.Id("tx").Op("*").Id("txDriver")).Id("Close").Params().Error().Block(jen.Return(jen.Nil()))

	f.Comment("Dialect returns the dialect of the driver.")
	f.Func().Params(jen.Id("tx").Op("*").Id("txDriver")).Id("Dialect").Params().String().Block(
		jen.Return(jen.Id("tx").Dot("drv").Dot("Dialect").Call()),
	)

	f.Comment("Tx returns the transaction wrapper (txDriver) to avoid Commit or Rollback calls")
	f.Comment("from the internal builders. Should be called only by the internal builders.")
	f.Func().Params(jen.Id("tx").Op("*").Id("txDriver")).Id("Tx").Params(jen.Qual("context", "Context")).Params(
		jen.Qual(dialectPkg(), "Tx"), jen.Error(),
	).Block(jen.Return(jen.Id("tx"), jen.Nil()))

	f.Comment("Commit is a nop commit for the internal builders.")
	f.Comment("User must call `Tx.Commit` in order to commit the transaction.")
	f.Func().Params(jen.Op("*").Id("txDriver")).Id("Commit").Params().Error().Block(jen.Return(jen.Nil()))

	f.Comment("Rollback is a nop rollback for the internal builders.")
	f.Comment("User must call `Tx.Rollback` in order to rollback the transaction.")
	f.Func().Params(jen.Op("*").Id("txDriver")).Id("Rollback").Params().Error().Block(jen.Return(jen.Nil()))

	// Feature: sql/execquery - ExecContext/QueryContext methods on txDriver
	if h.FeatureEnabled("sql/execquery") {
		genTxExecQueryMethods(f)
	}

	// WithTx helper function
	f.Comment("WithTx runs the given function within a transaction.")
	f.Comment("If the function returns an error, the transaction is rolled back.")
	f.Comment("If the function panics, the transaction is rolled back and the panic is re-raised.")
	f.Comment("Otherwise, the transaction is committed.")
	f.Func().Id("WithTx").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("client").Op("*").Id("Client"),
		jen.Id("fn").Func().Params(jen.Id("tx").Op("*").Id("Tx")).Error(),
	).Error().Block(
		jen.List(jen.Id("tx"), jen.Id("err")).Op(":=").Id("client").Dot("Tx").Call(jen.Id("ctx")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Id("err")),
		),
		jen.Defer().Func().Params().Block(
			jen.If(jen.Id("v").Op(":=").Recover(), jen.Id("v").Op("!=").Nil()).Block(
				jen.Id("tx").Dot("Rollback").Call(),
				jen.Panic(jen.Id("v")),
			),
		).Call(),
		jen.If(jen.Id("err").Op(":=").Id("fn").Call(jen.Id("tx")), jen.Id("err").Op("!=").Nil()).Block(
			jen.If(
				jen.Id("rerr").Op(":=").Id("tx").Dot("Rollback").Call(),
				jen.Id("rerr").Op("!=").Nil(),
			).Block(
				jen.Id("err").Op("=").Qual("fmt", "Errorf").Call(
					jen.Lit("%w: rolling back transaction: %v"),
					jen.Id("err"),
					jen.Id("rerr"),
				),
			),
			jen.Return(jen.Id("err")),
		),
		jen.If(jen.Id("err").Op(":=").Id("tx").Dot("Commit").Call(), jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("committing transaction: %w"), jen.Id("err"))),
		),
		jen.Return(jen.Nil()),
	)

	return f
}

// genTxExecQueryMethods generates ExecContext/QueryContext methods on txDriver.
// This is part of the sql/execquery feature.
func genTxExecQueryMethods(f *jen.File) {
	stdsqlPkg := "database/sql"

	// ExecContext method
	f.Comment("ExecContext allows calling the underlying ExecContext method of the transaction if it is supported by it.")
	f.Comment("See, database/sql#Tx.ExecContext for more information.")
	f.Func().Params(jen.Id("tx").Op("*").Id("txDriver")).Id("ExecContext").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("query").String(),
		jen.Id("args").Op("...").Any(),
	).Params(jen.Qual(stdsqlPkg, "Result"), jen.Error()).Block(
		jen.List(jen.Id("ex"), jen.Id("ok")).Op(":=").Id("tx").Dot("tx").Op(".").Parens(
			jen.Interface(
				jen.Id("ExecContext").Params(jen.Qual("context", "Context"), jen.String(), jen.Op("...").Any()).Params(jen.Qual(stdsqlPkg, "Result"), jen.Error()),
			),
		),
		jen.If(jen.Op("!").Id("ok")).Block(
			jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("Tx.ExecContext is not supported"))),
		),
		jen.Return(jen.Id("ex").Dot("ExecContext").Call(jen.Id("ctx"), jen.Id("query"), jen.Id("args").Op("..."))),
	)

	// QueryContext method
	f.Comment("QueryContext allows calling the underlying QueryContext method of the transaction if it is supported by it.")
	f.Comment("See, database/sql#Tx.QueryContext for more information.")
	f.Func().Params(jen.Id("tx").Op("*").Id("txDriver")).Id("QueryContext").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("query").String(),
		jen.Id("args").Op("...").Any(),
	).Params(jen.Op("*").Qual(stdsqlPkg, "Rows"), jen.Error()).Block(
		jen.List(jen.Id("q"), jen.Id("ok")).Op(":=").Id("tx").Dot("tx").Op(".").Parens(
			jen.Interface(
				jen.Id("QueryContext").Params(jen.Qual("context", "Context"), jen.String(), jen.Op("...").Any()).Params(jen.Op("*").Qual(stdsqlPkg, "Rows"), jen.Error()),
			),
		),
		jen.If(jen.Op("!").Id("ok")).Block(
			jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("Tx.QueryContext is not supported"))),
		),
		jen.Return(jen.Id("q").Dot("QueryContext").Call(jen.Id("ctx"), jen.Id("query"), jen.Id("args").Op("..."))),
	)
}
