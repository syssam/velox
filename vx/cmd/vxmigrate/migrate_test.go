package main

import (
	"bytes"
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
	for _, line := range strings.Split(strings.TrimSpace(string(notes)), "\n") {
		if line != "" && !strings.Contains(line, "open_test.go") {
			t.Errorf("left for a person outside open_test.go: %s", line)
		}
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
