package ormgen

import (
	"fmt"
	"sort"
	"strings"
)

// What the generated root package gives of the library's root package under
// its own name. The generated package is usually called velox too (the
// directory of the generated tree is ./velox), and a Go file cannot import
// two packages under one name: without these, every file that starts a query
// and also names a transaction or tests an error would import the library
// under an alias. They are aliases and forwarding functions, so velox.Tx is
// one type whichever of the two packages a file imports. Ent's generated
// package does the same for the same reason.
var (
	// rootTypes are aliased: type DB = velox.DB.
	rootTypes = []string{
		// The handle, and what a paged list returns.
		"DB", "Tx", "Executor", "Cursor", "PageInfo",
		// What a hook, an interceptor or a policy is written with.
		"Op", "Hook", "Value", "Query", "QueryContext", "Querier", "QuerierFunc",
		"Interceptor", "InterceptFunc", "Traverser", "TraverseFunc", "Policy",
		"Mutator", "Mutation", "MutateFunc",
		// The errors of a query or mutation.
		"NotFoundError", "NotSingularError", "NotLoadedError", "ConstraintError", "ValidationError",
	}
	rootConsts = []string{"OpCreate", "OpUpdate", "OpUpdateOne", "OpDelete", "OpDeleteOne"}
	rootVars   = []string{
		"ErrNotFound", "ErrNotSingular", "ErrTxStarted", "ErrTxDone", "ErrNoExecutor", "ErrBadCursor",
		"ErrUnique", "ErrForeignKey", "ErrCheck",
	}
	// rootFuncs are forwarded: the signature, and the arguments of the call.
	rootFuncs = []struct{ name, sig, call string }{
		{"Open", "(dialectName, dsn string) (*%[1]s.DB, error)", "(dialectName, dsn)"},
		{"OpenDB", "(dialectName string, db *%[3]s.DB) *%[1]s.DB", "(dialectName, db)"},
		{"SQLDB", "(db *%[1]s.DB) *%[3]s.DB", "(db)"},
		{"WithExecutor", "(ctx %[2]s.Context, ex %[1]s.Executor) %[2]s.Context", "(ctx, ex)"},
		{"ExecutorFrom", "(ctx %[2]s.Context) %[1]s.Executor", "(ctx)"},
		{"TxFrom", "(ctx %[2]s.Context) *%[1]s.Tx", "(ctx)"},
		{"Must", "[T any](v T, err error) T", "(v, err)"},
		{"First", "[T any](vs []T, err error) (T, error)", "(vs, err)"},
		{"Check", "(err error)", "(err)"},
		{"IsNotFound", "(err error) bool", "(err)"},
		{"IsNotSingular", "(err error) bool", "(err)"},
		{"IsNotLoaded", "(err error) bool", "(err)"},
		{"IsConstraintError", "(err error) bool", "(err)"},
		{"IsValidationError", "(err error) bool", "(err)"},
		{"IsRetryable", "(err error) bool", "(err)"},
		{"MaskNotFound", "(err error) error", "(err)"},
		{"NewNotFoundError", "(label string) *%[1]s.NotFoundError", "(label)"},
		{"NewNotSingularError", "(label string) *%[1]s.NotSingularError", "(label)"},
		{"NewNotLoadedError", "(edge string) *%[1]s.NotLoadedError", "(edge)"},
		{"NewConstraintError", "(msg string, wrap error) *%[1]s.ConstraintError", "(msg, wrap)"},
		{"NewValidationError", "(name string, err error) *%[1]s.ValidationError", "(name, err)"},
	}
)

// RootNames returns the names of the library's root package that a generated
// root package gives under its own name, sorted. A schema with an entity of
// one of these names (or whose client type is) does not get that one: the
// entity keeps the name.
func RootNames() []string {
	names := append(append(append([]string(nil), rootTypes...), rootConsts...), rootVars...)
	for _, f := range rootFuncs {
		names = append(names, f.name)
	}
	sort.Strings(names)
	return names
}

// reexports writes the declarations. taken holds the names the package
// declares for the schema; lib, ctx and sql are the names the library,
// context and database/sql are imported under.
func reexports(taken map[string]bool, lib, ctx, sql string) string {
	var b strings.Builder
	var left []string
	free := func(names []string) []string {
		var out []string
		for _, n := range names {
			if taken[n] {
				left = append(left, n)
				continue
			}
			out = append(out, n)
		}
		return out
	}
	group := func(keyword, doc string, names []string) {
		if names = free(names); len(names) == 0 {
			return
		}
		fmt.Fprintf(&b, "%s\n%s (\n", doc, keyword)
		for _, n := range names {
			fmt.Fprintf(&b, "\t%s = %s.%s\n", n, lib, n)
		}
		b.WriteString(")\n\n")
	}
	group("type", "// The library's types under this package's name: a file that starts its\n// queries here needs one import. They are aliases, so "+lib+".Tx is one type\n// whichever of the two packages a file imports.", rootTypes)
	group("const", "// The operations of a mutation.", rootConsts)
	group("var", "// The library's error values.", rootVars)
	for _, f := range rootFuncs {
		if taken[f.name] {
			left = append(left, f.name)
			continue
		}
		ret := "return "
		if f.name == "Check" {
			ret = ""
		}
		sig := f.sig
		if strings.Contains(sig, "%") {
			sig = fmt.Sprintf(sig, lib, ctx, sql)
		}
		fmt.Fprintf(&b, "// %[1]s is the library's %[1]s.\nfunc %[1]s%[2]s { %[3]s%[4]s.%[1]s%[5]s }\n\n", f.name, sig, ret, lib, f.call)
	}
	if len(left) > 0 {
		sort.Strings(left)
		fmt.Fprintf(&b, "// Not given under this package's name, because the schema has that name:\n// %s. Import the library for them.\n\n", strings.Join(left, ", "))
	}
	return b.String()
}
