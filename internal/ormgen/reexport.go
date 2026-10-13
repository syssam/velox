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
		"ErrNotFound", "ErrNotSingular", "ErrTxStarted", "ErrTxDone", "ErrNoExecutor", "ErrNoEntity", "ErrBadCursor",
		"ErrUnique", "ErrForeignKey", "ErrCheck",
	}
	// rootFuncs are forwarded: the signature, and the arguments of the call.
	rootFuncs = []struct{ name, sig, call string }{
		{"Open", "(dialectName, dsn string) (*%[1]s.DB, error)", "(dialectName, dsn)"},
		{"OpenDB", "(dialectName string, db *%[3]s.DB) *%[1]s.DB", "(dialectName, db)"},
		{"ExecSQL", "(ctx %[2]s.Context, ex %[1]s.Executor, query string, args ...any) (%[3]s.Result, error)", "(ctx, ex, query, args...)"},
		{"QuerySQL", "(ctx %[2]s.Context, ex %[1]s.Executor, query string, args ...any) (*%[3]s.Rows, error)", "(ctx, ex, query, args...)"},
		{"Some", "[T any](v T) %[1]s.Opt[T]", "(v)"},
		{"Null", "[T any]() %[1]s.Opt[T]", "[T]()"},
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

// The names of package orm that an application writes, which the generated
// root package gives under its own name too: no file of an application
// needs to import package orm, or to know that the engine has a package.
var (
	// engineTypes are aliased: the name here, the name in package orm, the
	// doc comment.
	engineTypes = [][3]string{
		{"Agg", "Agg", "Agg is an aggregate of a grouped read: Count, Sum, Min, Max, Mean, named in\n// the result with its As."},
		{"EntityQuery", "Query", "EntityQuery is a read as an interceptor is handed it (Traverse, BeforeQuery):\n// its entity, its conditions, and WhereP to add one."},
		{"EntityMutation", "Mutation", "EntityMutation is a write as a hook finds it behind the Mutation it is handed:\n// the fields and edges it sets, and WhereP to narrow it."},
		{"Predicate", "Predicate", "Predicate is the constraint of a function written once for the predicates of\n// several entities: func recent[E velox.Predicate](...) E."},
	}
	// engineColumns are the plain column types, which are generic: what
	// a function written once for several entities takes, and a column's
	// As<Level> returns.
	engineColumns = []string{"ValueColumn", "BoolColumn", "EnumColumn", "OrderedColumn", "NumberColumn", "StringColumn"}
	engineConsts  = []string{
		"OpEQ", "OpNEQ", "OpGT", "OpGTE", "OpLT", "OpLTE", "OpIn", "OpNotIn", "OpIsNil", "OpNotNil",
		"OpContains", "OpHasPrefix", "OpHasSuffix", "OpEqualFold", "OpContainsFold",
		"SkipLocked", "NoWait",
	}
	// engineFuncs are forwarded: the doc comment, the signature, the body.
	// %[1]s is package orm, %[2]s the library, %[3]s context and %[4]s the
	// SQL builder.
	engineFuncs = []struct{ name, doc, sig, body string }{
		{"Count", "Count counts rows: an aggregate.", "() %[1]s.Agg", "return %[1]s.Count()"},
		{"Sum", "Sum adds up a column: an aggregate.", "(column string) %[1]s.Agg", "return %[1]s.Sum(column)"},
		{"Min", "Min is the smallest value of a column: an aggregate.", "(column string) %[1]s.Agg", "return %[1]s.Min(column)"},
		{"Max", "Max is the largest value of a column: an aggregate.", "(column string) %[1]s.Agg", "return %[1]s.Max(column)"},
		{"Mean", "Mean is the average of a column: an aggregate.", "(column string) %[1]s.Agg", "return %[1]s.Mean(column)"},
		{"And", "And matches the rows every one of ps matches, for the predicates of any entity.", "[T %[1]s.Predicate](ps ...T) T", "return %[1]s.And(ps...)"},
		{"Or", "Or matches the rows at least one of ps matches, for the predicates of any entity.", "[T %[1]s.Predicate](ps ...T) T", "return %[1]s.Or(ps...)"},
		{"Not", "Not matches the rows p does not match, for the predicates of any entity.", "[T %[1]s.Predicate](p T) T", "return %[1]s.Not(p)"},
		{"IsZero", "IsZero reports whether p is the zero predicate: no condition.", "[T %[1]s.Predicate](p T) bool", "return %[1]s.IsZero(p)"},
		{"Compile", "Compile returns a predicate as a function on a selector of its entity's table,\n// or nil for the zero predicate.", "[T %[1]s.Predicate](p T) func(*%[4]s.Selector)", "return %[1]s.Compile(p)"},
		{"Apply", "Apply calls each function on q and returns q: for the parts of a query that\n// were collected before it was built.", "[Q any](q Q, fns ...func(Q)) Q", "return %[1]s.Apply(q, fns...)"},
		{"Traverse", "Traverse makes an interceptor of a function that is handed every read before\n// it runs and may narrow it: a default filter. Register it with a handle's\n// Intercept.", "(f func(%[3]s.Context, *%[1]s.Query) error) %[2]s.Interceptor", "return %[1]s.TraverseFunc(f)"},
		{"BeforeQuery", "BeforeQuery makes an interceptor of a function that is handed every read\n// before it runs and may refuse it.", "(f func(%[3]s.Context, *%[1]s.Query) error) %[2]s.Interceptor", "return %[1]s.InterceptFunc(f)"},
		{"WithoutTotal", "WithoutTotal makes Paginate skip the count of all matching rows: the page's\n// TotalCount is then -1.", "(ctx %[3]s.Context) %[3]s.Context", "return %[1]s.WithoutTotal(ctx)"},
		{"StitchAll", "StitchAll gives every entity the code of its schema now, where each gets it\n// when it is first used.", "()", "%[1]s.StitchAll()"},
	}
)

// libraryNames returns the names of the library's root package that the
// generated root package gives under its own name.
func libraryNames() []string {
	names := append(append(append([]string{"Opt"}, rootTypes...), rootConsts...), rootVars...)
	for _, f := range rootFuncs {
		names = append(names, f.name)
	}
	return names
}

// engineForwards writes those declarations. taken holds the names the
// package already declares, the library's among them; orm, lib, ctx and dsql
// are the names package orm, the library, context and the SQL builder are
// imported under.
func engineForwards(taken map[string]bool, orm, lib, ctx, dsql string) string {
	var b strings.Builder
	var left []string
	fill := strings.NewReplacer("%[1]s", orm, "%[2]s", lib, "%[3]s", ctx, "%[4]s", dsql)
	b.WriteString("// What an application names of the engine, under this package's name: a file\n// of an application imports this package and the packages of its entities,\n// and never package orm.\ntype (\n")
	for _, t := range engineTypes {
		if taken[t[0]] {
			left = append(left, t[0])
			continue
		}
		fmt.Fprintf(&b, "\t// %s\n\t%s = %s.%s\n", t[2], t[0], orm, t[1])
	}
	b.WriteString(")\n\n// The plain column types: what a function written once for several entities\n// takes, and what a column's As<Level> returns (user.Age.AsOrdered()).\ntype (\n")
	for _, n := range engineColumns {
		if taken[n] {
			left = append(left, n)
			continue
		}
		fmt.Fprintf(&b, "\t%[1]s[T any, E %[2]s.Predicate] = %[2]s.%[1]s[T, E]\n", n, orm)
	}
	b.WriteString(")\n\n// The operators of a condition on a field named at run time (FieldPredicate),\n// and what a locking read does about a row that is already locked.\nconst (\n")
	for _, n := range engineConsts {
		if taken[n] {
			left = append(left, n)
			continue
		}
		fmt.Fprintf(&b, "\t%s = %s.%s\n", n, orm, n)
	}
	b.WriteString(")\n\n")
	for _, f := range engineFuncs {
		if taken[f.name] {
			left = append(left, f.name)
			continue
		}
		fmt.Fprintf(&b, "// %s\nfunc %s%s { %s }\n\n", f.doc, f.name, fill.Replace(f.sig), fill.Replace(f.body))
	}
	if len(left) > 0 {
		sort.Strings(left)
		fmt.Fprintf(&b, "// Not given under this package's name, because the schema has that name:\n// %s. They are package orm's.\n\n", strings.Join(left, ", "))
	}
	return b.String()
}

// RootNames returns the names of the library's root package that a generated
// root package gives under its own name, sorted. A schema with an entity of
// one of these names (or whose client type is) does not get that one: the
// entity keeps the name.
func RootNames() []string {
	names := libraryNames()
	for _, t := range engineTypes {
		names = append(names, t[0])
	}
	names = append(append(names, engineColumns...), engineConsts...)
	for _, f := range engineFuncs {
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
	// The one generic type: an alias of it has the type parameter too.
	if taken["Opt"] {
		left = append(left, "Opt")
	} else {
		fmt.Fprintf(&b, "// Opt is the library's Opt: a value of an input that may be absent, null or\n// given.\ntype Opt[T any] = %s.Opt[T]\n\n", lib)
	}
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
