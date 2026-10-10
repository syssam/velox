// Command velox-migrate rewrites an application written against velox's
// generated code for the code velox-gen generates, which package orm runs.
//
//	velox-migrate -generated example.com/app/velox -write -report todo.txt
//
// run in the application's module, before the old generated code is replaced:
// the tool needs it to know what every expression is. Afterwards generate
// with velox-gen (into the same directory, or another one named with -to), and
// build. What the tool could not rewrite is in the report, a line each with
// the reason; what it got wrong, the compiler says.
//
// It reads the application the way the compiler does, with types, and so
// knows that b in b.SetName(x) is a create builder of User wherever b came
// from. It does not compile anything: its own packages are type-checked in
// full and everything they import without function bodies, which for a few
// hundred entities is the difference between half a minute and the better
// part of an hour.
//
// What it rewrites, by example:
//
//	c.User.Create().SetName(n).SetNillableAge(a).Save(ctx)
//	    db.User.Create(c).Set(user.Name.Set(n), user.Age.SetNillable(a)).Save(ctx)
//	c.User.UpdateOneID(id).AddAge(1).Exec(ctx)
//	    db.User.UpdateByID(c, id).Set(user.Age.Add(1)).ExecOne(ctx)
//	c.User.Query().Where(user.NameField.EQ(n)).Order(user.ByName()).Exist(ctx)
//	    db.User.Query(c).Where(user.Name.EQ(n)).OrderBy(user.Name.Asc()).Exists(ctx)
//	q.Paginate(ctx, after, first, before, last, entity.WithUserOrder(o), entity.WithUserFilter(w.Filter))
//	    q.OrderBy(o.Term()).Where(w.P()).Paginate(ctx, after, first, before, last)
//	c.User.DeleteOneID(id).Exec(ctx)        db.User.DeleteByID(ctx, c, id)
//	c.User.GetX(ctx, id)                    orm.Must(db.User.Get(ctx, c, id))
//	user.HasPostsWith(p)                    user.Posts.HasWith(p)
//	velox.TypeUser, user.Role, *velox.Tx    user.ModelName, user.RoleEnum, *orm.Tx
//
// and the things that follow from a client no longer holding a connection
// and a predicate no longer being a function: a client passed as a value is
// the handle it held (its type becomes orm.Executor), an embedded client
// keeps its field name, a nil predicate is the zero predicate (p == nil is
// orm.IsZero(p), and so is testify's Nil of one, which would otherwise never
// hold, and NotNil always), a function on a selector used as a predicate is
// wrapped in orm.Raw.
//
// A pagination option kept in a variable or a slice becomes a function of
// the query, applied with orm.Apply; a setter passed as a value becomes a
// function that makes the assignment; an edge query from an entity is given
// the handle the entity was read on, when the function shows which.
//
// Select followed by All becomes Columns. An edge query from an entity that
// was handed to the function is given the handle of the context
// (orm.ExecutorFrom(ctx)) and reported: whether every caller's context
// carries one is not something the tool can see.
//
// Code generic over entities is rewritten with it: the constraint
// sql.PredicateFunc (or ~func(*sql.Selector)) becomes orm.Predicate, a
// parameter of type sql.Int64Field[P] becomes orm.OrderedColumn[int64, P], a
// column passed to one is passed by its address, and sql.AndPredicates
// becomes orm.And.
//
// What it leaves to a person: a query changed through Modify, the
// application's own plumbing around the client, cursors built by hand, and
// how the application opens its database.
//
// Where the result compiles and would do something else, it keeps what the
// code did or says so in the report; it does not leave that to be found in
// production. The tests of an error (IsNotFound, IsNotSingular, ...) become
// the runtime's, since velox's never hold for an error of the new code, and
// NewNotFoundError, which a test makes for a stand-in, orm.NotFound. A
// mutation asserted to an interface with a method velox generated per
// entity is reported: no mutation has such a method now, and the assertion
// would compile and never hold. A
// deferred call that became the argument of orm.Must is put inside a
// function, so that it still runs when the function returns. An edge query
// is given the handle its entity was read on only when the variable has one
// assignment and the handle is not reassigned; otherwise the context's, and
// a line in the report. Reported, and left as written: an assertion to the
// query or mutation type of one entity (every entity's is *orm.Query and
// *orm.Mutation now), Or of a list that may be empty (velox's matched every
// row, the runtime's none), P of an input with no condition (an error, now
// no condition), a limit in the option of an eager load (over all neighbors,
// now for each parent), a method value whose receiver may change before it
// is called, several orders or filters given to Paginate, and a call of a
// GraphQL edge method of an entity, which reads the handle of the context.
//
// It assumes the names velox-gen gives: a column is called like the field, an enum
// type like the field with Enum. A schema where one of those is taken (the
// generator then adds a suffix) needs those references fixed by hand.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
)

type listed struct {
	ImportPath   string
	Dir          string
	GoFiles      []string
	CgoFiles     []string
	TestGoFiles  []string
	XTestGoFiles []string
	ImportMap    map[string]string
	Standard     bool
	DepOnly      bool
	Module       *struct {
		Path string
		Main bool
	}
}

// pkg is one type-checked package of the application.
type pkg struct {
	l     *listed
	types *types.Package
	// files and info are kept for the application's own packages only.
	files []*ast.File
	info  *types.Info
	// test holds the files of the package's _test.go, checked together with
	// the package (in-package tests) or as its external test package.
	tests []*unit
	errs  int
	all   []error
}

// unit is a set of files checked as one package, with the facts about them.
type unit struct {
	files []*ast.File
	info  *types.Info
	types *types.Package
	errs  []error
}

type program struct {
	fset *token.FileSet
	pkgs map[string]*pkg // by import path
	// own are the application's packages, in dependency order.
	own []*pkg
	// generated says whether a package of the main module is generated ORM
	// code: read for its types, not for what it calls.
	generated func(importPath string) bool
}

func newInfo() *types.Info {
	return &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
		Implicits:  map[ast.Node]types.Object{},
		Instances:  map[*ast.Ident]types.Instance{},
	}
}

// load lists the packages under dir with the go command and type-checks
// them in dependency order.
func load(dir string, patterns []string, generated func(string) bool) (*program, error) {
	cmd := exec.Command("go", append([]string{"list", "-deps", "-test", "-e", //nolint:gosec // the go command, on the patterns given to this one
		"-json=ImportPath,Dir,GoFiles,CgoFiles,TestGoFiles,XTestGoFiles,ImportMap,Standard,DepOnly,Module"}, patterns...)...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w\n%s", err, stderr.String())
	}
	p := &program{fset: token.NewFileSet(), pkgs: map[string]*pkg{}, generated: generated}
	dec := json.NewDecoder(bytes.NewReader(out))
	sizes := types.SizesFor("gc", "arm64")
	for {
		l := &listed{}
		if err := dec.Decode(l); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		// The test variants ("p [p.test]", "p.test") repeat the plain
		// packages, which come first and carry the test files' names.
		if strings.Contains(l.ImportPath, " [") || strings.HasSuffix(l.ImportPath, ".test") || p.pkgs[l.ImportPath] != nil {
			continue
		}
		own := l.Module != nil && l.Module.Main && !l.DepOnly && !generated(l.ImportPath)
		k := &pkg{l: l}
		p.pkgs[l.ImportPath] = k
		if l.ImportPath == "unsafe" {
			k.types = types.Unsafe
			continue
		}
		u := p.check(l, l.ImportPath, append(append([]string{}, l.GoFiles...), l.CgoFiles...), nil, own, sizes)
		k.types, k.errs = u.types, len(u.errs)
		if own {
			k.all = u.errs
		}
		if !own {
			continue
		}
		k.files, k.info = u.files, u.info
		p.own = append(p.own, k)
	}
	// The tests after every package: what only a test imports is listed
	// after the package the test is of.
	for _, k := range p.own {
		l := k.l
		if len(l.TestGoFiles) > 0 {
			// The package again with its tests, for the tests' sake: only
			// the test files' facts are read from it.
			t := p.check(l, l.ImportPath, append(append(append([]string{}, l.GoFiles...), l.CgoFiles...), l.TestGoFiles...), nil, true, sizes)
			if n := len(l.GoFiles) + len(l.CgoFiles); len(t.files) >= n {
				t.files = t.files[n:]
			}
			k.tests = append(k.tests, t)
			if len(l.XTestGoFiles) > 0 {
				k.tests = append(k.tests, p.check(l, l.ImportPath+"_test", l.XTestGoFiles, map[string]*types.Package{l.ImportPath: t.types}, true, sizes))
			}
		} else if len(l.XTestGoFiles) > 0 {
			k.tests = append(k.tests, p.check(l, l.ImportPath+"_test", l.XTestGoFiles, nil, true, sizes))
		}
	}
	return p, nil
}

// check parses and type-checks the named files of l as one package.
func (p *program) check(l *listed, path string, names []string, override map[string]*types.Package, bodies bool, sizes types.Sizes) *unit {
	u := &unit{}
	mode := parser.SkipObjectResolution
	if bodies {
		mode |= parser.ParseComments
	}
	for _, name := range names {
		f, err := parser.ParseFile(p.fset, filepath.Join(l.Dir, name), nil, mode)
		if err != nil {
			u.errs = append(u.errs, err)
			if f == nil {
				continue
			}
		}
		u.files = append(u.files, f)
	}
	conf := types.Config{
		IgnoreFuncBodies: !bodies,
		FakeImportC:      true,
		Sizes:            sizes,
		Error:            func(err error) { u.errs = append(u.errs, err) },
		Importer: importerFunc(func(to string) (*types.Package, error) {
			if mapped, ok := l.ImportMap[to]; ok {
				to = mapped
			}
			if o := override[to]; o != nil {
				return o, nil
			}
			if dep := p.pkgs[to]; dep != nil && dep.types != nil {
				return dep.types, nil
			}
			return nil, fmt.Errorf("package %s was not listed", to)
		}),
	}
	if bodies {
		u.info = newInfo()
	}
	u.types, _ = conf.Check(path, p.fset, u.files, u.info)
	if !bodies {
		u.files = nil
	}
	return u
}

type importerFunc func(path string) (*types.Package, error)

func (f importerFunc) Import(path string) (*types.Package, error) { return f(path) }
