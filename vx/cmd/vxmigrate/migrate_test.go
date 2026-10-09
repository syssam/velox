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
// first; then the tool rewrites a copy for vx's generated code of the same
// schema, and the same test, rewritten with it, must pass again. What is
// asserted is behavior before and after, not the shape of the output.
func TestMigrateKeepsBehavior(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs an application twice")
	}
	const (
		from = "github.com/syssam/velox/tests/integration"
		to   = "github.com/syssam/velox/vx/tests/db"
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
		"./cmd/vxmigrate/" + filepath.ToSlash(app)}, &log)
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
	contextHandle := 0
	for _, line := range strings.Split(strings.TrimSpace(string(notes)), "\n") {
		switch {
		case line == "", strings.Contains(line, "open_test.go"):
		case strings.Contains(line, "QueryPosts is given the handle of the context"):
			// Rewritten, and reported: whether the context carries a
			// handle is not something the tool can see.
			contextHandle++
		default:
			t.Errorf("left for a person outside open_test.go: %s", line)
		}
	}
	if contextHandle != 1 {
		t.Errorf("%d edge queries were reported as given the context's handle, want 1", contextHandle)
	}
	// That file, as a person migrating would write it.
	opened, err := os.ReadFile("testdata/open_test.go.vx")
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
		"!orm.P(p).IsZero()",
		"predicate.User(orm.Raw(func(s *sql.Selector) {",
		"user.Posts.HasWith(post.Status.EQ(post.StatusDraft))",
		".Set(user.Role.Set(user.RoleAdmin), user.Nickname.Clear(), user.Age.Add(1)).SaveOne(ctx)",
		"orm.One(db.User.UpdateByID(s.Client, id).Set(user.Name.Set(name)).Exec(ctx))",
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
		"if p := idIn(&user.ID, ids...); !orm.P(p).IsZero() {", // a column passed as a value
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
	if err != nil || len(sources) != 2 {
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
	err = run([]string{"-dir", "../..", "-generated", "github.com/syssam/velox/tests/integration", "-to", "github.com/syssam/velox/vx/tests/db",
		"-write", "-report", report, "./cmd/vxmigrate/" + filepath.ToSlash(dir)}, &log)
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
		"Set(post.Title.Set(title), post.Author.Set(author.ID)).Save(ctx)",
		// A column's name where no rule rewrites the call around it.
		"s.OrderBy(sql.Asc(user.ColumnName))",
		// Likes, not LikedPosts.
		"orm.One(db.User.UpdateByID(c, userID).Set(user.Likes.Add(likeIDs...)).Exec(ctx))",
		// A package name the application has taken, in another file.
		"return slicespkg.Contains(user.RoleValues(), r)",
	} {
		if !strings.Contains(flat, text) {
			t.Errorf("the migrated file does not contain %q", text)
		}
	}
	// An entity whose handle the text does not settle is not given one.
	for _, text := range []string{"u.QueryPosts(tx)", "u.QueryPosts(c)", "v.QueryPosts(tx)", "u.QueryPosts(a)", "u.QueryPosts(b)"} {
		if strings.Contains(flat, text) {
			t.Errorf("the migrated file contains %q: a handle the text does not settle", text)
		}
	}
	if n := strings.Count(flat, ".QueryPosts(orm.ExecutorFrom(ctx))"); n != 3 {
		t.Errorf("%d edge queries were given the context's handle, want 3", n)
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
	err = run([]string{"-dir", "../..", "-generated", "github.com/syssam/velox/tests/integration", "-report", report, "./cmd/vxmigrate/testdata/app"}, &log)
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
	if !strings.Contains(log.String(), "references rewritten in 3 files") {
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
