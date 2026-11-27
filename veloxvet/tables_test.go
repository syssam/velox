package veloxvet_test

import (
	"go/types"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"

	"github.com/syssam/velox/veloxvet"
)

const (
	veloxPath   = "github.com/syssam/velox"
	untypedPath = veloxPath + "/untyped"
	enginePath  = veloxPath + "/internal/engine"
)

// ignoredQuery are Query's and TableQuery's exported methods that the
// Unbounded analyzer leaves out of its table on purpose: they read one
// row or one value, set their own bound, or return no query. A method
// left out returns a query the analyzer does not know, so it never makes
// it report a bounded read.
var ignoredQuery = map[string]bool{
	veloxPath + ".Query.One":      true,
	veloxPath + ".Query.First":    true,
	veloxPath + ".Query.Count":    true,
	veloxPath + ".Query.Exists":   true,
	veloxPath + ".Query.Paginate": true,
	veloxPath + ".Query.Ordered":  true,
	veloxPath + ".Query.Reads":    true,

	enginePath + ".TableQuery.One":       true,
	enginePath + ".TableQuery.First":     true,
	enginePath + ".TableQuery.Count":     true,
	enginePath + ".TableQuery.Exists":    true,
	enginePath + ".TableQuery.LoadEach":  true,
	enginePath + ".TableQuery.CountEach": true,
	enginePath + ".TableQuery.Order":     true,
	enginePath + ".TableQuery.Ordered":   true,
	enginePath + ".TableQuery.Selects":   true,
	enginePath + ".TableQuery.Table":     true,
}

// ignoredFuncs are velox's and untyped's other exported functions and
// methods that take or return a query and that the analyzer leaves out of
// its table on purpose: a plain aggregate reads one value, ScanOne sets
// its own Limit, LoadEach and CountEach set AllowUnbounded, and QueryDB
// reads no row.
var ignoredFuncs = map[string]bool{
	veloxPath + ".ScanOne":   true,
	veloxPath + ".Sum":       true,
	veloxPath + ".SumNull":   true,
	veloxPath + ".Avg":       true,
	veloxPath + ".AvgNull":   true,
	veloxPath + ".Min":       true,
	veloxPath + ".MinNull":   true,
	veloxPath + ".Max":       true,
	veloxPath + ".MaxNull":   true,
	veloxPath + ".LoadEach":  true,
	veloxPath + ".CountEach": true,
	untypedPath + ".QueryDB": true,
}

// lockIgnored are the exported functions and methods that take a query and
// return none that LockTx leaves out of lockReads: they refuse a lock
// whatever the handle (Count, the aggregates, LoadEach, CountEach), or read
// no row.
var lockIgnored = map[string]bool{
	veloxPath + ".Query.Count":           true,
	veloxPath + ".Query.Ordered":         true,
	veloxPath + ".Query.Reads":           true,
	enginePath + ".TableQuery.Count":     true,
	enginePath + ".TableQuery.LoadEach":  true,
	enginePath + ".TableQuery.CountEach": true,
	enginePath + ".TableQuery.Order":     true,
	enginePath + ".TableQuery.Ordered":   true,
	enginePath + ".TableQuery.Selects":   true,
	enginePath + ".TableQuery.Table":     true,
	veloxPath + ".Sum":                   true,
	veloxPath + ".SumNull":               true,
	veloxPath + ".Avg":                   true,
	veloxPath + ".AvgNull":               true,
	veloxPath + ".Min":                   true,
	veloxPath + ".MinNull":               true,
	veloxPath + ".Max":                   true,
	veloxPath + ".MaxNull":               true,
	veloxPath + ".CountBy":               true,
	veloxPath + ".SumBy":                 true,
	veloxPath + ".AvgBy":                 true,
	veloxPath + ".MinBy":                 true,
	veloxPath + ".MaxBy":                 true,
	veloxPath + ".SumByNull":             true,
	veloxPath + ".AvgByNull":             true,
	veloxPath + ".MinByNull":             true,
	veloxPath + ".MaxByNull":             true,
	veloxPath + ".LoadEach":              true,
	veloxPath + ".CountEach":             true,
	untypedPath + ".QueryDB":             true,
}

// dbIgnored are the exported functions and methods of velox and untyped
// that return a *velox.DB which LockTx does not take for a transaction's:
// a new DB, or one it cannot tell is in a transaction.
var dbIgnored = map[string]bool{
	veloxPath + ".Connect":     true,
	veloxPath + ".Open":        true,
	veloxPath + ".OpenConn":    true,
	veloxPath + ".OpenShards":  true,
	veloxPath + ".Mutation.DB": true,
	untypedPath + ".SourceDB":  true,
	untypedPath + ".QueryDB":   true,
}

// txIgnored are the functions that take a function of a DB and begin no
// transaction for it: a filter's.
var txIgnored = map[string]bool{
	veloxPath + ".WithFilter":   true,
	veloxPath + ".Deferred.Set": true,
}

// ignoredInsert are InsertQuery's exported methods that ZeroDefault treats
// as writing a column: a chain through one is not checked.
var ignoredInsert = map[string]bool{"Set": true, "SetInput": true}

// TestTablesMatchAPI loads velox's packages and checks the analyzers'
// hand-kept tables against them: every exported method of Query,
// TableQuery and InsertQuery, and every other exported function of velox
// and untyped that takes or returns a query, is in a table or an ignored
// list, and every name in a table or a list exists.
func TestTablesMatchAPI(t *testing.T) {
	cfg := &packages.Config{Mode: packages.NeedName | packages.NeedTypes, Dir: testModule(t)}
	pkgs, err := packages.Load(cfg, veloxPath, untypedPath, enginePath)
	if err != nil {
		t.Fatal(err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		t.Fatal("velox's packages do not load")
	}
	byPath := map[string]*types.Package{}
	for _, p := range pkgs {
		byPath[p.PkgPath] = p.Types
	}

	all := map[string]*types.Func{} // every function and method, by funcKey
	for _, p := range byPath {
		scope := p.Scope()
		for _, name := range scope.Names() {
			switch obj := scope.Lookup(name).(type) {
			case *types.Func:
				all[p.Path()+"."+name] = obj
			case *types.TypeName:
				if obj.IsAlias() {
					continue
				}
				if n, ok := obj.Type().(*types.Named); ok {
					for m := range n.Methods() {
						all[p.Path()+"."+name+"."+m.Name()] = m
					}
				}
			}
		}
	}

	queries := []string{veloxPath + ".Query", enginePath + ".TableQuery"}
	for k := range all {
		if !all[k].Exported() {
			continue
		}
		recv := k[:strings.LastIndex(k, ".")]
		switch {
		case slices.Contains(queries, recv):
			if !veloxvet.Roles[k] && !ignoredQuery[k] {
				t.Errorf("%s is neither in Unbounded's roles nor in ignoredQuery", k)
			}
		case recv == veloxPath+".InsertQuery":
			name := k[len(recv)+1:]
			if !veloxvet.InsertKeeps[name] && !veloxvet.InsertRuns[name] && !ignoredInsert[name] {
				t.Errorf("%s is neither in ZeroDefault's insertKeeps or insertRuns nor in ignoredInsert", k)
			}
		case strings.HasPrefix(k, enginePath+"."):
		case mentionsQuery(all[k].Signature()):
			if !veloxvet.Roles[k] && !ignoredFuncs[k] {
				t.Errorf("%s takes or returns a query and is neither in Unbounded's roles nor in ignoredFuncs", k)
			}
		}
	}

	// LockTx's tables: every function that takes a query and returns none
	// is a locking read or ignored, every function that takes a function of
	// a DB opens a transaction, and every function that returns a DB is
	// classified.
	for k, f := range all {
		recv := k[:strings.LastIndex(k, ".")]
		if !f.Exported() || strings.HasPrefix(k, enginePath+".") && recv != enginePath+".TableQuery" {
			continue
		}
		if r := f.Signature().Recv(); r != nil && !exportedRecv(r.Type()) {
			continue
		}
		sig := f.Signature()
		takesQuery := slices.Contains(queries, recv) || mentionsQuery(sig)
		if takesQuery && !returnsQuery(sig) && !veloxvet.LockReads[k] && !lockIgnored[k] {
			t.Errorf("%s reads a query and is neither in LockTx's lockReads nor in lockIgnored", k)
		}
		if takesDBFunc(sig) && !veloxvet.TxOpeners[k] && !txIgnored[k] {
			t.Errorf("%s takes a function of a DB and is not in LockTx's txOpeners", k)
		}
		if returnsDB(sig) && !veloxvet.TxResults[k] && !veloxvet.SameHandle[k] && !dbIgnored[k] {
			t.Errorf("%s returns a DB and is in none of LockTx's txResults and sameHandle, nor in dbIgnored", k)
		}
	}
	for k := range veloxvet.QueryHandle {
		if r := veloxvet.Roles[k]; !r {
			t.Errorf("%s is in LockTx's queryHandle but not in Unbounded's roles", k)
		}
	}

	for _, set := range []map[string]bool{
		veloxvet.Roles, ignoredQuery, ignoredFuncs, veloxvet.LockApply, veloxvet.LockReads, veloxvet.TxOpeners,
		veloxvet.TxResults, veloxvet.SameHandle, veloxvet.QueryHandle, lockIgnored, dbIgnored, txIgnored,
	} {
		for k := range set {
			if all[k] == nil {
				t.Errorf("%s is in a table but velox has no such function", k)
			}
		}
	}
	for _, set := range []map[string]bool{veloxvet.InsertKeeps, veloxvet.InsertRuns, ignoredInsert} {
		for name := range set {
			if all[veloxPath+".InsertQuery."+name] == nil {
				t.Errorf("InsertQuery.%s is in a table but InsertQuery has no such method", name)
			}
		}
	}
}

// mentionsQuery reports whether a parameter or a result of sig is a
// velox.Query or a TableQuery, or a pointer or a slice of one.
func mentionsQuery(sig *types.Signature) bool {
	for _, tuple := range []*types.Tuple{sig.Params(), sig.Results()} {
		for v := range tuple.Variables() {
			if isQueryType(v.Type()) {
				return true
			}
		}
	}
	return false
}

func isQueryType(t types.Type) bool {
	switch u := types.Unalias(t).(type) {
	case *types.Pointer:
		return isQueryType(u.Elem())
	case *types.Slice:
		return isQueryType(u.Elem())
	case *types.Named:
		obj := u.Origin().Obj()
		if obj.Pkg() == nil {
			return false
		}
		k := obj.Pkg().Path() + "." + obj.Name()
		return k == veloxPath+".Query" || k == enginePath+".TableQuery"
	}
	return false
}

// returnsQuery reports whether a result of sig is a query.
func returnsQuery(sig *types.Signature) bool {
	for v := range sig.Results().Variables() {
		if isQueryType(v.Type()) {
			return true
		}
	}
	return false
}

// returnsDB reports whether a result of sig is a *velox.DB.
func returnsDB(sig *types.Signature) bool {
	for v := range sig.Results().Variables() {
		if isDBType(v.Type()) {
			return true
		}
	}
	return false
}

// takesDBFunc reports whether a parameter of sig is a function that takes
// a *velox.DB.
func takesDBFunc(sig *types.Signature) bool {
	for v := range sig.Params().Variables() {
		if f, ok := v.Type().Underlying().(*types.Signature); ok {
			for p := range f.Params().Variables() {
				if isDBType(p.Type()) {
					return true
				}
			}
		}
	}
	return false
}

func isDBType(t types.Type) bool {
	p, ok := types.Unalias(t).(*types.Pointer)
	if !ok {
		return false
	}
	n, ok := types.Unalias(p.Elem()).(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == veloxPath && n.Obj().Name() == "DB"
}

// exportedRecv reports whether a method's receiver type is exported.
func exportedRecv(t types.Type) bool {
	if p, ok := types.Unalias(t).(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Exported()
}
