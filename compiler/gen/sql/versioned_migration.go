package sql

import (
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genVersionedMigration generates the migrate/migrate.go file with versioned migration support.
// This is part of the sql/versioned-migration feature.
func genVersionedMigration(h gen.GeneratorHelper) *jen.File {
	f := h.NewFile("migrate")

	f.ImportName("context", "context")
	f.ImportName("database/sql", "sql")
	f.ImportName("errors", "errors")
	f.ImportName("fmt", "fmt")
	f.ImportName("io/fs", "fs")
	f.ImportName("path/filepath", "filepath")
	f.ImportName("sort", "sort")
	f.ImportName("strings", "strings")
	f.ImportName("time", "time")

	// Migration struct
	f.Comment("Migration represents a versioned database migration.")
	f.Type().Id("Migration").Struct(
		jen.Id("Version").String().Tag(map[string]string{"json": "version"}),
		jen.Id("Name").String().Tag(map[string]string{"json": "name"}),
		jen.Id("SQL").String().Tag(map[string]string{"json": "sql"}),
		jen.Id("Applied").Qual("time", "Time").Tag(map[string]string{"json": "applied,omitempty"}),
	)

	// MigrationDir interface
	f.Comment("MigrationDir is the interface for migration file directories.")
	f.Type().Id("MigrationDir").Interface(
		jen.Comment("Files returns all migration files in the directory."),
		jen.Id("Files").Params().Params(jen.Index().String(), jen.Error()),
		jen.Comment("ReadFile reads a migration file by name."),
		jen.Id("ReadFile").Params(jen.String()).Params(jen.Index().Byte(), jen.Error()),
	)

	// LocalDir implements MigrationDir for local filesystem
	f.Comment("LocalDir implements MigrationDir for the local filesystem.")
	f.Type().Id("LocalDir").Struct(
		jen.Id("path").String(),
	)

	// NewLocalDir constructor
	f.Comment("NewLocalDir creates a new LocalDir for the given path.")
	f.Func().Id("NewLocalDir").Params(jen.Id("path").String()).Op("*").Id("LocalDir").Block(
		jen.Return(jen.Op("&").Id("LocalDir").Values(jen.Dict{
			jen.Id("path"): jen.Id("path"),
		})),
	)

	// LocalDir.Files method
	f.Comment("Files returns all .sql files in the directory.")
	f.Func().Params(jen.Id("d").Op("*").Id("LocalDir")).Id("Files").Params().Params(
		jen.Index().String(),
		jen.Error(),
	).Block(
		jen.List(jen.Id("entries"), jen.Id("err")).Op(":=").Qual("os", "ReadDir").Call(jen.Id("d").Dot("path")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Id("err")),
		),
		jen.Var().Id("files").Index().String(),
		jen.For(jen.List(jen.Id("_"), jen.Id("e")).Op(":=").Range().Id("entries")).Block(
			jen.If(jen.Op("!").Id("e").Dot("IsDir").Call().Op("&&").Qual("strings", "HasSuffix").Call(
				jen.Id("e").Dot("Name").Call(),
				jen.Lit(".sql"),
			)).Block(
				jen.Id("files").Op("=").Append(jen.Id("files"), jen.Id("e").Dot("Name").Call()),
			),
		),
		jen.Qual("sort", "Strings").Call(jen.Id("files")),
		jen.Return(jen.Id("files"), jen.Nil()),
	)

	// LocalDir.ReadFile method
	f.Comment("ReadFile reads a migration file by name.")
	f.Func().Params(jen.Id("d").Op("*").Id("LocalDir")).Id("ReadFile").Params(
		jen.Id("name").String(),
	).Params(jen.Index().Byte(), jen.Error()).Block(
		jen.Return(jen.Qual("os", "ReadFile").Call(
			jen.Qual("path/filepath", "Join").Call(jen.Id("d").Dot("path"), jen.Id("name")),
		)),
	)

	// MigrationRunner struct
	f.Comment("MigrationRunner runs versioned migrations.")
	f.Type().Id("MigrationRunner").Struct(
		jen.Id("db").Op("*").Qual("database/sql", "DB"),
		jen.Id("dir").Id("MigrationDir"),
		jen.Id("table").String(),
	)

	// NewMigrationRunner constructor
	f.Comment("NewMigrationRunner creates a new MigrationRunner.")
	f.Func().Id("NewMigrationRunner").Params(
		jen.Id("db").Op("*").Qual("database/sql", "DB"),
		jen.Id("dir").Id("MigrationDir"),
	).Op("*").Id("MigrationRunner").Block(
		jen.Return(jen.Op("&").Id("MigrationRunner").Values(jen.Dict{
			jen.Id("db"):    jen.Id("db"),
			jen.Id("dir"):   jen.Id("dir"),
			jen.Id("table"): jen.Lit("schema_migrations"),
		})),
	)

	// WithTable option
	f.Comment("WithTable sets the migration history table name.")
	f.Func().Params(jen.Id("r").Op("*").Id("MigrationRunner")).Id("WithTable").Params(
		jen.Id("table").String(),
	).Op("*").Id("MigrationRunner").Block(
		jen.Id("r").Dot("table").Op("=").Id("table"),
		jen.Return(jen.Id("r")),
	)

	// Status method
	f.Comment("Status returns the current migration status.")
	f.Func().Params(jen.Id("r").Op("*").Id("MigrationRunner")).Id("Status").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Index().Op("*").Id("Migration"), jen.Error()).Block(
		jen.Comment("Ensure migrations table exists"),
		jen.If(jen.Id("err").Op(":=").Id("r").Dot("ensureTable").Call(jen.Id("ctx")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Id("err")),
		),
		jen.Comment("Get applied migrations"),
		jen.List(jen.Id("applied"), jen.Id("err")).Op(":=").Id("r").Dot("appliedMigrations").Call(jen.Id("ctx")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Id("err")),
		),
		jen.Comment("Get all migration files"),
		jen.List(jen.Id("files"), jen.Id("err")).Op(":=").Id("r").Dot("dir").Dot("Files").Call(),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Id("err")),
		),
		jen.Comment("Build status list"),
		jen.Id("appliedMap").Op(":=").Make(jen.Map(jen.String()).Qual("time", "Time")),
		jen.For(jen.List(jen.Id("_"), jen.Id("m")).Op(":=").Range().Id("applied")).Block(
			jen.Id("appliedMap").Index(jen.Id("m").Dot("Version")).Op("=").Id("m").Dot("Applied"),
		),
		jen.Var().Id("migrations").Index().Op("*").Id("Migration"),
		jen.For(jen.List(jen.Id("_"), jen.Id("f")).Op(":=").Range().Id("files")).Block(
			jen.Id("version").Op(":=").Id("r").Dot("versionFromFile").Call(jen.Id("f")),
			jen.Id("m").Op(":=").Op("&").Id("Migration").Values(jen.Dict{
				jen.Id("Version"): jen.Id("version"),
				jen.Id("Name"):    jen.Id("f"),
			}),
			jen.If(jen.List(jen.Id("t"), jen.Id("ok")).Op(":=").Id("appliedMap").Index(jen.Id("version")), jen.Id("ok")).Block(
				jen.Id("m").Dot("Applied").Op("=").Id("t"),
			),
			jen.Id("migrations").Op("=").Append(jen.Id("migrations"), jen.Id("m")),
		),
		jen.Return(jen.Id("migrations"), jen.Nil()),
	)

	// Up method
	f.Comment("Up runs all pending migrations.")
	f.Func().Params(jen.Id("r").Op("*").Id("MigrationRunner")).Id("Up").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Error().Block(
		jen.List(jen.Id("pending"), jen.Id("err")).Op(":=").Id("r").Dot("pendingMigrations").Call(jen.Id("ctx")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Id("err")),
		),
		jen.For(jen.List(jen.Id("_"), jen.Id("m")).Op(":=").Range().Id("pending")).Block(
			jen.If(jen.Id("err").Op(":=").Id("r").Dot("runMigration").Call(jen.Id("ctx"), jen.Id("m")), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("migration %s failed: %w"), jen.Id("m").Dot("Version"), jen.Id("err"))),
			),
		),
		jen.Return(jen.Nil()),
	)

	// Helper methods
	f.Comment("ensureTable creates the migrations table if it doesn't exist.")
	f.Func().Params(jen.Id("r").Op("*").Id("MigrationRunner")).Id("ensureTable").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Error().Block(
		jen.Id("query").Op(":=").Qual("fmt", "Sprintf").Call(
			jen.Lit(`CREATE TABLE IF NOT EXISTS %s (
				version VARCHAR(255) PRIMARY KEY,
				applied TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`),
			jen.Id("r").Dot("table"),
		),
		jen.List(jen.Id("_"), jen.Id("err")).Op(":=").Id("r").Dot("db").Dot("ExecContext").Call(jen.Id("ctx"), jen.Id("query")),
		jen.Return(jen.Id("err")),
	)

	f.Comment("appliedMigrations returns all applied migrations.")
	f.Func().Params(jen.Id("r").Op("*").Id("MigrationRunner")).Id("appliedMigrations").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Index().Op("*").Id("Migration"), jen.Error()).Block(
		jen.Id("query").Op(":=").Qual("fmt", "Sprintf").Call(
			jen.Lit("SELECT version, applied FROM %s ORDER BY version"),
			jen.Id("r").Dot("table"),
		),
		jen.List(jen.Id("rows"), jen.Id("err")).Op(":=").Id("r").Dot("db").Dot("QueryContext").Call(jen.Id("ctx"), jen.Id("query")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Id("err")),
		),
		jen.Defer().Id("rows").Dot("Close").Call(),
		jen.Var().Id("migrations").Index().Op("*").Id("Migration"),
		jen.For(jen.Id("rows").Dot("Next").Call()).Block(
			jen.Var().Id("m").Id("Migration"),
			jen.If(jen.Id("err").Op(":=").Id("rows").Dot("Scan").Call(jen.Op("&").Id("m").Dot("Version"), jen.Op("&").Id("m").Dot("Applied")), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			),
			jen.Id("migrations").Op("=").Append(jen.Id("migrations"), jen.Op("&").Id("m")),
		),
		jen.Return(jen.Id("migrations"), jen.Id("rows").Dot("Err").Call()),
	)

	f.Comment("pendingMigrations returns all pending migrations.")
	f.Func().Params(jen.Id("r").Op("*").Id("MigrationRunner")).Id("pendingMigrations").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Index().Op("*").Id("Migration"), jen.Error()).Block(
		jen.List(jen.Id("status"), jen.Id("err")).Op(":=").Id("r").Dot("Status").Call(jen.Id("ctx")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Id("err")),
		),
		jen.Var().Id("pending").Index().Op("*").Id("Migration"),
		jen.For(jen.List(jen.Id("_"), jen.Id("m")).Op(":=").Range().Id("status")).Block(
			jen.If(jen.Id("m").Dot("Applied").Dot("IsZero").Call()).Block(
				jen.List(jen.Id("content"), jen.Id("err")).Op(":=").Id("r").Dot("dir").Dot("ReadFile").Call(jen.Id("m").Dot("Name")),
				jen.If(jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Id("err")),
				),
				jen.Id("m").Dot("SQL").Op("=").String().Call(jen.Id("content")),
				jen.Id("pending").Op("=").Append(jen.Id("pending"), jen.Id("m")),
			),
		),
		jen.Return(jen.Id("pending"), jen.Nil()),
	)

	f.Comment("runMigration executes a single migration.")
	f.Func().Params(jen.Id("r").Op("*").Id("MigrationRunner")).Id("runMigration").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("m").Op("*").Id("Migration"),
	).Error().Block(
		jen.List(jen.Id("tx"), jen.Id("err")).Op(":=").Id("r").Dot("db").Dot("BeginTx").Call(jen.Id("ctx"), jen.Nil()),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Id("err")),
		),
		jen.Defer().Func().Params().Block(
			jen.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Id("tx").Dot("Rollback").Call(),
			),
		).Call(),
		jen.Comment("Execute migration SQL"),
		jen.If(jen.List(jen.Id("_"), jen.Id("err")).Op("=").Id("tx").Dot("ExecContext").Call(jen.Id("ctx"), jen.Id("m").Dot("SQL")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Id("err")),
		),
		jen.Comment("Record migration"),
		jen.Id("query").Op(":=").Qual("fmt", "Sprintf").Call(
			jen.Lit("INSERT INTO %s (version) VALUES ($1)"),
			jen.Id("r").Dot("table"),
		),
		jen.If(jen.List(jen.Id("_"), jen.Id("err")).Op("=").Id("tx").Dot("ExecContext").Call(jen.Id("ctx"), jen.Id("query"), jen.Id("m").Dot("Version")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Id("err")),
		),
		jen.Return(jen.Id("tx").Dot("Commit").Call()),
	)

	f.Comment("versionFromFile extracts the version from a migration filename.")
	f.Func().Params(jen.Id("r").Op("*").Id("MigrationRunner")).Id("versionFromFile").Params(
		jen.Id("filename").String(),
	).String().Block(
		jen.Comment("Expected format: 20060102150405_name.sql"),
		jen.Id("parts").Op(":=").Qual("strings", "SplitN").Call(jen.Id("filename"), jen.Lit("_"), jen.Lit(2)),
		jen.If(jen.Len(jen.Id("parts")).Op(">").Lit(0)).Block(
			jen.Return(jen.Id("parts").Index(jen.Lit(0))),
		),
		jen.Return(jen.Id("filename")),
	)

	return f
}
