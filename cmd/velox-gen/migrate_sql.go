package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/syssam/velox/internal/codegen"
	"github.com/syssam/velox/migrate"
)

// runMigrateSQL is velox-gen migrate sql: the CREATE statements of the
// schema package on stdout, each followed by ";\n", for Atlas.
//
// Atlas reads stdout as SQL, so it holds nothing else, and it is written
// once, only on success. It takes no -dir: it reads no directory.
func runMigrateSQL(args []string, dir string, env []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("migrate sql", stderr)
	var dialect dialectFlag
	fs.Var(&dialect, "dialect", "the dialect of the DDL: postgres, mysql or sqlite")
	patterns, code, done := parseFlags(fs, args)
	if done {
		return code
	}
	if len(patterns) > 1 {
		return tooMany("migrate sql", fs, stderr)
	}
	if dialect.d == 0 {
		_, _ = fmt.Fprintln(stderr, "velox-gen: migrate sql needs -dialect postgres, mysql or sqlite")
		fs.Usage()
		return 2
	}
	fail := func(err error) int {
		_, _ = fmt.Fprintf(stderr, "velox-gen: migrate sql: %s\n", strings.TrimPrefix(err.Error(), "migrate: "))
		return 1
	}

	// A package with no schemas is refused: Atlas would read an empty
	// output as an empty schema and plan to drop every table.
	pattern, err := schemaPackage(dir, patterns, env)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "velox-gen: %v\n", err)
		return 1
	}
	to, err := func() (*migrate.Schema, error) {
		m, err := codegen.Load(dir, pattern, env)
		if err != nil {
			return nil, err
		}
		g, err := codegen.Build(m)
		if err != nil {
			return nil, err
		}
		return codegen.Tables(g, dialect.d)
	}()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "velox-gen: %v\n", relErrors(dir, err))
		return 1
	}

	empty, err := migrate.NewDir(migrate.Config{Dialect: dialect.d})
	if err != nil {
		return fail(err)
	}
	plan, err := migrate.NewPlan(empty.Snapshot, to, migrate.PlanOptions{})
	if err != nil {
		return fail(relMigrate(dir, err))
	}
	if len(plan.Scripts) == 0 {
		return 0
	}
	if s := plan.Scripts[0]; len(plan.Scripts) != 1 || s.Kind != migrate.ScriptMain || !plainDirectives(s.Directives) {
		return fail(errors.New("the plan from an empty database is not one plain script"))
	}
	var b strings.Builder
	for _, s := range plan.Scripts[0].Statements {
		b.WriteString(s.SQL)
		b.WriteString(";\n")
	}
	if _, err := io.WriteString(stdout, b.String()); err != nil {
		return fail(err)
	}
	return 0
}

// plainDirectives reports whether d asks for nothing: a file with them
// needs no -- velox: line.
func plainDirectives(d migrate.Directives) bool {
	return !d.NoTransaction && !d.ForeignKeysOff && !d.LockTimeoutSet && len(d.Allow) == 0 && len(d.Drops) == 0
}
