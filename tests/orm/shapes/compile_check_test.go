package shapes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The promise of the per-entity predicate, setter, order and edge types is
// that mixing entities does not compile. Each snippet below is written into
// a scratch package of this module and built: the control must build, every
// other must fail with a type error on the marked line.

const compileHeader = `package main

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/orm"
	"github.com/syssam/velox/tests/orm/shapes/velox/article"
	"github.com/syssam/velox/tests/orm/shapes/velox/author"
	client "github.com/syssam/velox/tests/orm/shapes/velox"
	"github.com/syssam/velox/tests/orm/shapes/velox/entity"
	"github.com/syssam/velox/tests/orm/shapes/velox/scalar"
	"github.com/syssam/velox/tests/orm/shapes/velox/topic"
)

var (
	ctx = context.Background()
	db  *velox.DB
	_   = article.Table
	_   = author.Table
	_   = topic.Table
	_   = scalar.Table
	_   entity.Author
	_   orm.P
)

func main() {
`

type compileCase struct {
	name string
	body string
	// want is a substring of the compiler's error; "" means it must build.
	want string
}

var compileCases = []compileCase{
	{name: "control", body: `
	_, _ = client.Author.Query(db).
		Where(author.Name.EQ("x"), author.Articles.HasWith(article.Title.EQ("y"), article.Topics.HasWith(topic.Name.EQ("z")))).
		OrderBy(author.Age.Desc()).
		With(author.Articles.Where(article.Words.GT(1)).OrderBy(article.Title.Asc()).With(article.Topics), author.Passport, author.Edited.Load(client.Article.Query(db).Limit(1))).
		All(ctx)
	_, _ = client.Author.Create(db).Set(author.Name.Set("x"), author.Age.Set(1), author.Edited.Add(1, 2)).Save(ctx)
	_, _ = client.Author.CreateBulk(db, client.Author.Create(db), client.Author.Create(db)).Save(ctx)
	_, _ = client.Article.Update(db).Set(article.Words.Add(1), article.Author.Set(1), article.Topics.Remove(2)).Where(article.Author.Has()).Exec(ctx)
	_, _ = client.Article.Delete(db).Where(article.Or(article.Title.EQ("x"), article.Not(article.Editor.Has()))).Exec(ctx)
	_ = db.RunTx(ctx, func(tx *velox.Tx) error { _, err := client.Author.Query(tx).Count(ctx); return err })
	_, _ = client.Scalar.Update(db).Set(scalar.StringNil.Clear(), scalar.BytesNil.Clear()).Where(scalar.StringNil.IsNil(), scalar.StringPtr.NotNil()).Exec(ctx)
`},
	// One way to give a value: Set with the column's own assignment. The
	// per-field methods of velox and Ent are not generated.
	{name: "a Set<Field> method", want: "SetAge undefined",
		body: `	_, _ = client.Author.Create(db).SetAge(1).Save(ctx)`},
	{name: "predicate of another entity in Where", want: "cannot use article.Title.EQ",
		body: `	_, _ = client.Author.Query(db).Where(article.Title.EQ("x")).All(ctx)`},
	{name: "predicate of another entity in And", want: "cannot use article.Title.EQ",
		body: `	_, _ = client.Author.Query(db).Where(author.And(author.Name.EQ("x"), article.Title.EQ("x"))).All(ctx)`},
	{name: "predicate of another entity in Update.Where", want: "cannot use article.Title.EQ",
		body: `	_, _ = client.Author.Update(db).Set(author.Age.Set(1)).Where(article.Title.EQ("x")).Exec(ctx)`},
	{name: "predicate of another entity in Delete.Where", want: "cannot use article.Title.EQ",
		body: `	_, _ = client.Author.Delete(db).Where(article.Title.EQ("x")).Exec(ctx)`},
	{name: "edge predicate of another entity in Where", want: "cannot use article.Topics.Has",
		body: `	_, _ = client.Author.Query(db).Where(article.Topics.Has()).All(ctx)`},
	{name: "predicate of the owner inside HasWith", want: "cannot use author.Name.EQ",
		body: `	_, _ = client.Author.Query(db).Where(author.Articles.HasWith(author.Name.EQ("x"))).All(ctx)`},
	{name: "setter of another entity in Create", want: "cannot use article.Title.Set",
		body: `	_, _ = client.Author.Create(db).Set(article.Title.Set("x")).Save(ctx)`},
	{name: "setter of another entity in Update", want: "cannot use article.Title.Set",
		body: `	_, _ = client.Author.Update(db).Set(article.Title.Set("x")).Exec(ctx)`},
	{name: "edge setter of another entity", want: "cannot use article.Topics.Add",
		body: `	_, _ = client.Author.Update(db).Set(article.Topics.Add(1)).Exec(ctx)`},
	{name: "eager load of an edge of another entity", want: "cannot use article.Topics",
		body: `	_, _ = client.Author.Query(db).With(article.Topics).All(ctx)`},
	{name: "described eager load of an edge of another entity", want: "cannot use article.Topics.Load",
		body: `	_, _ = client.Author.Query(db).With(article.Topics.Load(client.Topic.Query(db))).All(ctx)`},
	{name: "condition of another entity on the neighbors of a load", want: "cannot use author.Name.EQ",
		body: `	_, _ = client.Author.Query(db).With(author.Articles.Where(author.Name.EQ("x"))).All(ctx)`},
	{name: "order of another entity on the neighbors of a load", want: "cannot use author.Name.Asc",
		body: `	_, _ = client.Author.Query(db).With(author.Articles.OrderBy(author.Name.Asc())).All(ctx)`},
	{name: "edge of the owner as an edge of the neighbors of a load", want: "cannot use author.Passport",
		body: `	_, _ = client.Author.Query(db).With(author.Articles.With(author.Passport)).All(ctx)`},
	{name: "query of another entity as the description of a load", want: "client.Topic.Query(db) does not match",
		body: `	_, _ = client.Author.Query(db).With(author.Articles.Load(client.Topic.Query(db))).All(ctx)`},
	{name: "order of another entity", want: "cannot use article.Title.Asc",
		body: `	_, _ = client.Author.Query(db).OrderBy(article.Title.Asc()).All(ctx)`},
	{name: "builder of another entity in CreateBulk", want: "cannot use client.Article.Create(db)",
		body: `	_, _ = client.Author.CreateBulk(db, client.Article.Create(db)).Save(ctx)`},
	{name: "value of the wrong type in a predicate", want: "cannot use",
		body: `	_, _ = client.Author.Query(db).Where(author.Age.EQ("x")).All(ctx)`},
	{name: "value of the wrong type in a setter", want: "cannot use",
		body: `	_, _ = client.Author.Create(db).Set(author.Name.Set(1)).Save(ctx)`},
	{name: "id of the wrong type in an edge setter", want: "cannot use",
		body: `	_, _ = client.Article.Create(db).Set(article.Author.Set("x")).Save(ctx)`},
	{name: "Add on a to-one edge", want: "article.Author.Add undefined",
		body: `	_, _ = client.Article.Update(db).Set(article.Author.Add(1)).Exec(ctx)`},
	{name: "Set on a to-many edge", want: "author.Articles.Set undefined",
		body: `	_, _ = client.Author.Update(db).Set(author.Articles.Set(1)).Exec(ctx)`},
	{name: "Clear on a required field", want: "article.Title.Clear undefined",
		body: `	_, _ = client.Article.Update(db).Set(article.Title.Clear()).Exec(ctx)`},
	{name: "Add on a string field", want: "article.Title.Add undefined",
		body: `	_, _ = client.Article.Update(db).Set(article.Title.Add("x")).Exec(ctx)`},
	{name: "substring predicate on an int field", want: "article.Words.Contains undefined",
		body: `	_, _ = client.Article.Query(db).Where(article.Words.Contains("1")).All(ctx)`},
	// Clear exists only where the column can hold NULL. (IsNil and NotNil
	// are on every column: see TestIsNilOnEveryColumn.)
	{name: "Clear on an Optional field whose column is NOT NULL", want: "scalar.StringOpt.Clear undefined",
		body: `	_, _ = client.Scalar.Update(db).Set(scalar.StringOpt.Clear()).Exec(ctx)`},
	{name: "Clear on a required Nillable field", want: "scalar.StringPtr.Clear undefined",
		body: `	_, _ = client.Scalar.Update(db).Set(scalar.StringPtr.Clear()).Exec(ctx)`},
	{name: "Clear on an Optional bytes field", want: "scalar.BytesOpt.Clear undefined",
		body: `	_, _ = client.Scalar.Create(db).Set(scalar.BytesOpt.Clear()).Save(ctx)`},
	// The compiler spells the package out: the generated root package is
	// called velox too.
	{name: "the driver under a handle as the executor", want: `does not implement "github.com/syssam/velox".Executor (missing method State)`,
		body: `	_, _ = client.Author.Query(db.Driver()).All(ctx)`},
	{name: "an untyped runtime predicate", want: "cannot use",
		body: `	_, _ = client.Author.Query(db).Where(orm.P{Op: orm.OpEQ, Col: "name", Val: "x"}).All(ctx)`},
}

func TestCompileTimeChecks(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a scratch package per case; skipped under -short")
	}
	root := moduleRoot(t)
	for _, c := range compileCases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir, importPath := scratch(t, "compile_"+c.name)
			src := compileHeader + c.body + "\n}\n"
			require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644))
			for _, tool := range []string{"build", "vet"} {
				args := []string{tool}
				if tool == "build" {
					args = append(args, "-o", os.DevNull)
				}
				out, err := run(t, root, "go", append(args, importPath)...)
				if c.want == "" {
					require.NoError(t, err, "the control must %s:\n%s", tool, out)
					continue
				}
				require.Error(t, err, "go %s accepted:\n%s", tool, c.body)
				require.Contains(t, out, c.want, "go %s failed for another reason", tool)
				require.Equal(t, 1, strings.Count(out, "main.go:"), "exactly one error, on the line under test:\n%s", out)
			}
		})
	}
}
