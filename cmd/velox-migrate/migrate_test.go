package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The application in testdata/app is written against velox's generated code
// for the test schema, with a test of what it does. The test runs there
// first; then the tool rewrites a copy for the code velox-gen generates from the same
// schema, and the same test, rewritten with it, must pass again. What is
// asserted is behavior before and after, not the shape of the output.
func TestMigrateKeepsBehavior(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs an application twice")
	}
	const (
		from = "github.com/syssam/velox/tests/integration"
		to   = "github.com/syssam/velox/tests/orm/db"
	)
	gotest := func(dir string) string {
		t.Helper()
		cmd := exec.Command("go", "test", "-count=1", "./"+dir) //nolint:gosec // the go command on a directory of this test
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go test ./%s: %v\n%s", dir, err, out)
		}
		return string(out)
	}
	gotest("testdata/app")

	// A copy inside the module, so that it builds against the same packages.
	work, err := os.MkdirTemp("testdata", "work-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(work) })
	app := filepath.Join(work, "app")
	if err := os.Mkdir(app, 0o750); err != nil {
		t.Fatal(err)
	}
	sources, err := filepath.Glob("testdata/app/*.go")
	if err != nil || len(sources) < 3 {
		t.Fatal(sources, err)
	}
	for _, src := range sources {
		data, err := os.ReadFile(src) //nolint:gosec // a file of testdata
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(app, filepath.Base(src)), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var log bytes.Buffer
	report := filepath.Join(work, "report.txt")
	err = run([]string{"-dir", "../..", "-generated", from, "-to", to, "-write", "-report", report,
		"./cmd/velox-migrate/" + filepath.ToSlash(app)}, &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if !strings.Contains(log.String(), "(0 files did not parse afterwards)") {
		t.Errorf("a rewritten file does not parse:\n%s", log.String())
	}
	// What is left for a person is in the one file that opens the
	// database, which is the application's own business.
	notes, err := os.ReadFile(report) //nolint:gosec // written by the run above
	if err != nil {
		t.Fatal(err)
	}
	// Rewritten and reported, each once: what the tool cannot see for
	// itself (whether the context carries a handle), or does in another
	// way than the text did (a deferred call, the last of two orders, the
	// runtime's type of a validation error).
	reported := map[string]int{
		"QueryPosts is given the handle of the context":    0,
		"a deferred (or go) call is inside a function now": 0,
		"more than one order or more than one filter":      0,
		"ValidationError is the runtime's type now":        0,
	}
next:
	for _, line := range strings.Split(strings.TrimSpace(string(notes)), "\n") {
		if line == "" || strings.Contains(line, "open_test.go") {
			continue
		}
		for text := range reported {
			if strings.Contains(line, text) {
				reported[text]++
				continue next
			}
		}
		t.Errorf("left for a person outside open_test.go: %s", line)
	}
	for text, n := range reported {
		if n != 1 {
			t.Errorf("%q was reported %d times, want once", text, n)
		}
	}
	// That file, as a person migrating would write it.
	opened, err := os.ReadFile("testdata/open_test.go.orm")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "open_test.go"), opened, 0o600); err != nil {
		t.Fatal(err)
	}

	migrated, err := os.ReadFile(filepath.Join(app, "app.go")) //nolint:gosec // written by the run above
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(migrated), from) {
		t.Errorf("the migrated application still imports velox's generated code:\n%s", migrated)
	}
	// A few shapes worth pinning: they are what a reader of the result sees.
	for _, want := range []string{
		"Client *orm.DB", // the embedded client keeps its field name
		"db.User.Create(s.Client).",
		"user.Nickname.SetNillable(nick),", // setters joined into one Set
		"return predicate.User{}",          // a nil predicate
		"!orm.IsZero(p)",
		"predicate.User(orm.Raw(func(s *sql.Selector) {",
		"user.Posts.HasWith(post.Status.EQ(post.StatusDraft))",
		".Set(user.Role.Set(user.RoleAdmin), user.Nickname.Clear(), user.Age.Add(1)).SaveOne(ctx)",
		"db.User.UpdateByID(s.Client, id).Set(user.Name.Set(name)).ExecOne(ctx)",
		"db.User.DeleteByID(ctx, s.Client, id)",
		".OrderBy(orderBy.Term()).Where(where.P()).Paginate(ctx, after, first, nil, nil)",
		"s.Client.Begin(ctx)",
		"moveAll(ctx, tx, from, to)", // a client passed as a value is the handle
		"posts orm.Executor",
		"WithPosts(db.Post.Query(nil).Where(post.Title.HasPrefix(prefix)))",
		"func roleOf(s string) user.RoleEnum { return user.RoleEnum(s) }",
		`user.ModelName + ":" + string(u.Role)`,
		".Set(post.Tags.Add(tagIDs...))", // AddTagIDs: the edge is Tags
		".Set(post.Tags.Remove(tagIDs...))",
		"opts := make([]func(*entity.UserQuery), 0, 3)", // options kept in a slice
		"q.OrderBy(o.Term())",
		"p = predicate.User(orm.Invalid(err))",
		"orm.Apply(q, opts...).Paginate(ctx, nil, first, nil, nil)",
		"q, ok := db.User.Query(s.Client), true",
		`.Select().GroupBy(post.ColumnStatus).Aggregate(orm.As(orm.Count(), "n")).Scan(ctx, &rows)`,
		"u.QueryPosts(s.Client).OrderBy(post.Title.Asc()).All(ctx)", // the handle the entity was read on
		"p.QueryAuthor(s.Client).Only(ctx)",                         // through the loop over what a query returned
		"func(v string) *entity.UserUpdate {",                       // a setter passed as a value
		"return upd.Set(user.Nickname.Clear())",
		"func(v user.RoleEnum) *entity.UserUpdate {",
		"guarded(where, func(w *filter.PostWhereInput) (predicate.Post, error) { return w.P(), nil })", // P as a value
		"late := func() (predicate.Post, error) { return where.P(), nil }",
		"func idIn[P orm.Predicate](id orm.OrderedColumn[int, P], ids ...int) P {", // generic over entities
		"return *new(P)",
		"func both[P orm.Predicate](ps ...P) P {",
		"return P(orm.And(ps...))",
		"Text orm.TextColumn[P]",
		"if p := idIn(&user.ID, ids...); !orm.IsZero(p) {", // a column passed as a value
		"titled[predicate.Post]{Text: &post.Title}",
		"var rename = func(b *entity.UserUpdate, v string) *entity.UserUpdate {", // a method expression
		"return u.QueryPosts(orm.ExecutorFrom(ctx)).Count(ctx)",                  // an entity handed over: the context's handle
		".OrderBy(user.Name.Asc()).Columns(user.ColumnName).All(ctx)",            // entities with some columns
	} {
		if !strings.Contains(string(migrated), want) {
			t.Errorf("the migrated application does not contain %q", want)
		}
	}
	if t.Failed() {
		t.Logf("migrated app.go:\n%s", migrated)
	}
	// And of the cases reviewers added, where the text is the point: an
	// assertion left as it was would compile and assert nothing.
	for file, wants := range map[string][]string{
		"more.go": {
			"func Missing(what string) error { return orm.NotFound(what) }",
			".BatchSize(size).Save(ctx)",
			"return errors.Is(err, orm.ErrWhereDepth), orm.MaxWhereDepth",
		},
		"more_test.go": {
			`require.True(t, orm.IsZero(Scope(true, "ann")))`,
			`require.False(t, orm.IsZero(Scope(false, "ann")))`,
			`checks.True(orm.IsZero(Scope(true, "ann")), "sees all")`,
			`checks.Falsef(orm.IsZero(Scope(false, "ann")), "narrowed to %s", "ann")`,
			`assert.False(unheard{}, orm.IsZero(Scope(true, "ann")))`,
		},
	} {
		text, err := os.ReadFile(filepath.Join(app, file)) //nolint:gosec // written by the run above
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range wants {
			if !strings.Contains(string(text), want) {
				t.Errorf("the migrated %s does not contain %q:\n%s", file, want, text)
			}
		}
	}
	gotest(app)
}

// The cases in testdata/notes are what an independent review of the tool
// found it getting wrong without saying so: code that compiled afterwards
// and did something else (an assertion that never holds, a deferred call
// that runs at once, the wrong handle), and code that no longer compiled
// with no word on why. Each is now rewritten so that it does what it did,
// or reported; the fixture says which, line by line.
func TestMigrateReportsWhatItCannotKeep(t *testing.T) {
	if testing.Short() {
		t.Skip("lists and type-checks packages")
	}
	work, err := os.MkdirTemp("testdata", "work-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(work) })
	dir := filepath.Join(work, "notes")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{} // "file:line" -> text
	sources, err := filepath.Glob("testdata/notes/*.go")
	if err != nil || len(sources) != 3 {
		t.Fatal(sources, err)
	}
	for _, src := range sources {
		data, err := os.ReadFile(src) //nolint:gosec // a file of testdata
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(src)), data, 0o600); err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if _, text, ok := strings.Cut(line, "// note: "); ok {
				want[fmt.Sprintf("%s:%d", filepath.Base(src), i+1)] = strings.TrimSpace(text)
			}
		}
	}
	if len(want) < 20 {
		t.Fatalf("the fixture carries %d expected notes", len(want))
	}

	var log bytes.Buffer
	report := filepath.Join(work, "report.txt")
	err = run([]string{"-dir", "../..", "-generated", "github.com/syssam/velox/tests/integration", "-to", "github.com/syssam/velox/tests/orm/db",
		"-write", "-report", report, "./cmd/velox-migrate/" + filepath.ToSlash(dir)}, &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if !strings.Contains(log.String(), "(0 files did not parse afterwards)") {
		t.Errorf("a rewritten file does not parse:\n%s", log.String())
	}
	notes, err := os.ReadFile(report) //nolint:gosec // written by the run above
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(notes)), "\n") {
		// <path>/<file>:<line>: <text>
		rest := line[strings.LastIndex(line, "/")+1:]
		parts := strings.SplitN(rest, ": ", 2)
		if len(parts) != 2 {
			t.Errorf("a line of the report: %q", line)
			continue
		}
		got[parts[0]] = append(got[parts[0]], parts[1])
	}
	for at, text := range want {
		found := false
		for _, note := range got[at] {
			found = found || strings.Contains(note, text)
		}
		if !found {
			t.Errorf("%s: not reported: %q (reported there: %q)", at, text, got[at])
		}
	}
	for at, notes := range got {
		if _, expected := want[at]; !expected {
			t.Errorf("%s: reported and not expected: %q", at, notes)
		}
	}

	migrated, err := os.ReadFile(filepath.Join(dir, "notes.go")) //nolint:gosec // written by the run above
	if err != nil {
		t.Fatal(err)
	}
	flat := strings.Join(strings.Fields(string(migrated)), " ")
	for _, text := range []string{
		// The tests of an error are the runtime's.
		"case orm.IsNotSingular(err), orm.IsNotFound(err), orm.IsValidationError(err):",
		"var ve *orm.ValidationError",
		"return ve.Field",
		`return &orm.ValidationError{Entity: "User", Field: "name", Err: errors.New("x")}`,
		// The driver under a transaction; the handle of a context.
		`return tx.Driver().Exec(ctx, "SELECT 1", []any{key}, nil)`,
		"ctx = orm.WithExecutor(ctx, c) ctx = orm.WithExecutor(ctx, tx)",
		"return orm.TxFrom(ctx) != nil && orm.ExecutorFrom(ctx) != nil",
		// Order terms in a variable.
		"order := func(s *sql.Selector) { s.OrderBy(sql.Desc(s.C(user.ColumnCreatedAt)), sql.Desc(s.C(user.ColumnName))) } return db.User.Query(c).OrderBy(predicate.UserOrderTerm(orm.RawOrder(order))).First(ctx)",
		"byAge := func(s *sql.Selector) { s.OrderBy(sql.Asc(s.C(user.ColumnAge))) } return db.User.Query(c).OrderBy(predicate.UserOrderTerm(orm.RawOrder(byAge))).First(ctx)",
		// An order written as a function; a column named at run time; a list of functions.
		`OrderBy(predicate.UserOrderTerm(orm.RawOrder(func(s *sql.Selector) { s.OrderBy(s.C(user.ColumnNickname) + " NULLS LAST") })), predicate.UserOrderTerm{Col: column, Desc: true}).All(ctx)`,
		"OrderBy(predicate.UserOrderTerm(orm.RawOrder(func(s *sql.Selector) { for _, f := range fs { f(s) } }))).All(ctx)",
		// A predicate where a function on a selector is wanted.
		"func asFunction(p predicate.User) func(*sql.Selector) { return orm.Compile(p) }",
		// The where input's own additions, and Filter.
		"where.Predicates = append(where.Predicates, user.Age.GT(1)) return where.P(), error(nil)",
		// A client that is only queried on.
		"func countUsers(ctx context.Context, c orm.Executor) (int, error) {",
		"func countIn(ctx context.Context, tx *orm.Tx) (int, error) { return countUsers(ctx, tx) }",
		"func begins(ctx context.Context, c *orm.DB) error {",
		"func countIfAny(ctx context.Context, c *orm.DB) (int, error) {",
		// An option kept in a variable shapes the query it is given.
		"func loadWith(ctx context.Context, c orm.Executor, opt func(*entity.PostQuery)) ([]*entity.User, error) { return db.User.Query(c).WithPosts(orm.Apply(db.Post.Query(nil), opt)).All(ctx) }",
		"type exister interface { Exists(ctx context.Context) (bool, error) }",
		"Owner orm.OrderedColumn[int, P]", // every column can be asked IsNil
		"func orderField() *postpkg.OrderField { return new(postpkg.OrderField) }",
		"func tables(ctx context.Context, c *orm.DB) error { return migrate.Create(ctx, c) }",
		"func defaultRole() user.RoleEnum { return user.DefaultRole() }",
		"return orm.First(db.User.Query(c).Limit(1).IDs(ctx))",
		"return db.User.Query(c).ForUpdate(orm.SkipLocked).All(ctx)",
		"OrderBy(predicate.UserOrderTerm(orm.RawOrder(sql.OrderByField(user.ColumnNickname, sql.OrderAsc(), sql.OrderNullsFirst()).ToFunc())), user.ID.Asc()).All(ctx)",
		"return who.QueryPosts(c).All(ctx)",
		`func noField(o *entity.PostOrder) bool { return o == nil || o.Field == "" }`,
		"func columnOf(o *entity.PostOrder) string { return o.Term().Col }",
		"func validPointer(r *user.RoleEnum) bool { return slicespkg.Contains(user.RoleValues(), *r) }",
		"func isRole(r user.RoleEnum) bool { return slicespkg.Contains(user.RoleValues(), r) }",
		"func anyOfThem[Q exister](ctx context.Context, q Q) (bool, error) { return q.Exists(ctx) }",
		`func opened(dsn string) (*orm.DB, error) { return orm.Open("sqlite", dsn) }`,
		// The start of a query as a value.
		"func starter(c *orm.DB) func() *entity.UserQuery { return func() *entity.UserQuery { return db.User.Query(c) } }",
		// Some columns of an eager load's neighbors.
		"WithAuthor(db.User.Query(nil).Columns(user.ColumnName)).All(ctx)",
		// Conditions that are not predicate types keep the library's combinator.
		"func plain(fs ...func(*sql.Selector)) func(*sql.Selector) { return sql.AndPredicates(fs...) }",
		// Deferred: still later.
		"defer func() { orm.Must(db.User.Update(c).Set(user.Age.Set(0)).Exec(ctx)) }()",
		`go func() { orm.Must(db.User.Create(c).Set(user.Name.Set("x")).Save(ctx)) }()`,
		// The last order; no order.
		"return db.Post.Query(c).OrderBy(b.Term()).Paginate(ctx, nil, nil, nil, nil) //",
		"return db.Post.Query(c).Paginate(ctx, nil, nil, nil, nil) }",
		// The adapter of package intercept, called.
		"return orm.TraverseFunc(func(ctx context.Context, q *orm.Query) error { q.Limit(100)",
		// A parameter that does not capture the builder.
		"return func(v_ string) *entity.UserUpdate { return v.Set(user.Name.Set(v_)) }",
		// A first argument that is itself rewritten.
		`return db.User.CreateBulk(c, db.User.Create(c).Set(user.Name.Set("a"), user.Email.Set("a@x")), db.User.Create(c).Set(user.Name.Set("b"), user.Email.Set("b@x"))).Save(ctx)`,
		// Predicates in a field and in a map.
		"cond: predicate.User(orm.Raw(func(s *sql.Selector) {",
		`"none": predicate.User{}`,
		// The id of an entity given by its address.
		"Set(postpkg.Title.Set(title), postpkg.Author.Set(author.ID)).Save(ctx)",
		// (postpkg: the package's tests declare a name post.)
		// The values of the errors.
		"return errors.Is(err, orm.ErrNotFound) || errors.Is(err, orm.ErrNotSingular)",
		// A last order that is nil is no order; an order written out.
		"return db.Post.Query(c).Paginate(ctx, nil, nil, nil, nil) // note: more than one order",
		"OrderBy((&entity.PostOrder{Field: f}).Term()).Paginate(",
		// Unwrap as a statement.
		"_ = u if err := tx.Commit(); err != nil {",
		// The two results of P, returned.
		"return where.P(), error(nil) //",
		// Predicates in a literal behind a pointer.
		`return []*rule{{name: "raw", cond: predicate.User(orm.Raw(func(s *sql.Selector) { s.Where(sql.EQ("name", "y")) }))}, {name: "none", cond: predicate.User{}}}`,
		// A column's name where no rule rewrites the call around it.
		"s.OrderBy(sql.Asc(user.ColumnName))",
		// Likes, not LikedPosts.
		"db.User.UpdateByID(c, userID).Set(user.Likes.Add(likeIDs...)).ExecOne(ctx)",
		// A package name the application has taken, in another file.
		"return slicespkg.Contains(user.RoleValues(), r)",
	} {
		if !strings.Contains(flat, text) {
			t.Errorf("the migrated file does not contain %q", text)
		}
	}
	// An entity whose handle the text does not settle is not given one.
	for _, text := range []string{"u.QueryPosts(tx)", "u.QueryPosts(c)", "v.QueryPosts(tx)", "u.QueryPosts(a)", "u.QueryPosts(b)",
		"prev.QueryPosts(c)", "us[0].QueryPosts(c)", "us[0].QueryPosts(other)", "u.QueryPosts(other)"} {
		if strings.Contains(flat, text) {
			t.Errorf("the migrated file contains %q: a handle the text does not settle", text)
		}
	}
	if n := strings.Count(flat, ".QueryPosts(orm.ExecutorFrom(ctx))"); n != 9 {
		t.Errorf("%d edge queries were given the context's handle, want 9", n)
	}
	// And one the text does settle: read on a field of the receiver, kept
	// on the receiver and changed in a field since.
	if !strings.Contains(flat, `s.last = u u.Name = "seen" return u.QueryPosts(s.c).Count(ctx)`) {
		t.Error("an entity read on a field of the receiver was not given that handle")
	}
	for _, text := range []string{
		"func keptDefault() func(*entity.PostQuery) { return func(q *entity.PostQuery) {} }",
		"defer func() {}()",
	} {
		if !strings.Contains(flat, text) {
			t.Errorf("the migrated file does not contain %q", text)
		}
	}
	if t.Failed() {
		t.Logf("migrated notes.go:\n%s", migrated)
	}
}

// Without -write nothing is written: the report says what would be done.
func TestMigrateWithoutWriteChangesNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("lists and type-checks packages")
	}
	before, err := os.ReadFile("testdata/app/app.go")
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	report := filepath.Join(t.TempDir(), "report.txt")
	err = run([]string{"-dir", "../..", "-generated", "github.com/syssam/velox/tests/integration", "-report", report, "./cmd/velox-migrate/testdata/app"}, &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	after, err := os.ReadFile("testdata/app/app.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the application was rewritten without -write")
	}
	if !strings.Contains(log.String(), "references rewritten in 5 files") {
		t.Errorf("the run does not say what it would do:\n%s", log.String())
	}
	if _, err := os.Stat(report); err != nil {
		t.Error(err)
	}
}

func TestRunNeedsTheGeneratedPackage(t *testing.T) {
	if err := run(nil, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "-generated is required") {
		t.Errorf("without -generated: %v", err)
	}
}
