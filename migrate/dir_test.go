package migrate_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/gen/loadjson"
	"github.com/syssam/velox2/migrate"
	"github.com/syssam/velox2/schema/field"
)

// sumFS is a directory of the files, with the velox.sum they should have.
func sumFS(t *testing.T, files map[string]string) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	for p, c := range files {
		out[p] = &fstest.MapFile{Data: []byte(c)}
	}
	return resum(t, out)
}

// resum is fsys with velox.sum rewritten.
func resum(t *testing.T, fsys fstest.MapFS) fstest.MapFS {
	t.Helper()
	delete(fsys, "velox.sum")
	b, err := migrate.Sum(fsys)
	if err != nil {
		t.Fatal(err)
	}
	fsys["velox.sum"] = &fstest.MapFile{Data: b}
	return fsys
}

func h1(data string) string {
	h := sha256.Sum256([]byte(data))
	return "h1:" + base64.StdEncoding.EncodeToString(h[:])
}

func asError(t *testing.T, err error, code string) *migrate.Error {
	t.Helper()
	var e *migrate.Error
	if !errors.As(err, &e) {
		t.Fatalf("got %v (%T), want a *migrate.Error", err, err)
	}
	if e.Code != code {
		t.Errorf("Code = %q, want %q (%v)", e.Code, code, err)
	}
	if !strings.HasPrefix(err.Error(), "migrate: ") {
		t.Errorf("%q does not start with %q", err, "migrate: ")
	}
	return e
}

var regexpName = regexp.MustCompile(`^[a-z0-9_]+$`)

const schemaPg = `{"version":1,"dialect":"postgres"}` + "\n"

const velox0 = `{"version": 1, "dialect": "postgres"}` + "\n"

func TestConfig(t *testing.T) {
	d, err := migrate.NewDir(migrate.Config{Dialect: velox.Postgres})
	if err != nil {
		t.Fatal(err)
	}
	want := migrate.Config{Dialect: velox.Postgres, Table: "velox_migrations", LockTimeout: 5 * time.Second}
	if d.Config != want {
		t.Errorf("Config = %+v, want %+v", d.Config, want)
	}
	if d.Snapshot == nil || !d.Snapshot.Equal(&migrate.Schema{Dialect: velox.Postgres}) {
		t.Errorf("Snapshot = %+v, want the empty schema", d.Snapshot)
	}
	if len(d.Files) != 0 {
		t.Errorf("Files = %v", d.Files)
	}

	// What Add writes reads back equal, every key present.
	for _, c := range []migrate.Config{
		want,
		{Dialect: velox.MySQL, Table: "_mig9", LockTimeout: 90 * time.Second},
		{Dialect: velox.SQLite, Table: strings.Repeat("t", 63), LockTimeout: 250 * time.Millisecond},
	} {
		d, err := migrate.NewDir(c)
		if err != nil {
			t.Fatal(err)
		}
		files, err := d.Add("x", mainPlan(), &migrate.Schema{Dialect: c.Dialect})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := files["velox.json"]; !ok {
			t.Fatalf("no velox.json in %v", slices.Sorted(maps.Keys(files)))
		}
		fsys := fstest.MapFS{}
		for p, b := range files {
			fsys[p] = &fstest.MapFile{Data: b}
		}
		got, err := migrate.ReadDir(fsys)
		if err != nil {
			t.Fatal(err)
		}
		if got.Config != c {
			t.Errorf("read back %+v, want %+v", got.Config, c)
		}
	}
	const json5 = "{\n  \"version\": 1,\n  \"dialect\": \"postgres\",\n  \"table\": \"velox_migrations\",\n  \"lock_timeout\": \"5s\"\n}\n"
	if got := string(migrate.ConfigJSON(want)); got != json5 {
		t.Errorf("velox.json = %q, want %q", got, json5)
	}

	// A lock timeout of 0s in velox.json turns it off; absent keys default.
	got, err := migrate.ReadDir(sumFS(t, map[string]string{"velox.json": `{"version":1,"dialect":"mysql","lock_timeout":"0s"}`}))
	if err != nil {
		t.Fatal(err)
	}
	if want := (migrate.Config{Dialect: velox.MySQL, Table: "velox_migrations"}); got.Config != want {
		t.Errorf("Config = %+v, want %+v", got.Config, want)
	}
	got, err = migrate.ReadDir(sumFS(t, map[string]string{"velox.json": velox0}))
	if err != nil {
		t.Fatal(err)
	}
	if got.Config != want {
		t.Errorf("Config = %+v, want %+v", got.Config, want)
	}

	for _, c := range []struct{ name, json string }{
		{"version 2", `{"version":2,"dialect":"postgres"}`},
		{"no version", `{"dialect":"postgres"}`},
		{"no dialect", `{"version":1}`},
		{"dialect oracle", `{"version":1,"dialect":"oracle"}`},
		{"unknown key", `{"version":1,"dialect":"postgres","tabel":"x"}`},
		{"key in other case", `{"version":1,"dialect":"postgres","Table":"x"}`},
		{"table a-b", `{"version":1,"dialect":"postgres","table":"a-b"}`},
		{"table 64 bytes", `{"version":1,"dialect":"postgres","table":"` + strings.Repeat("t", 64) + `"}`},
		{"table empty", `{"version":1,"dialect":"postgres","table":""}`},
		{"table digit first", `{"version":1,"dialect":"postgres","table":"1a"}`},
		{"negative timeout", `{"version":1,"dialect":"postgres","lock_timeout":"-1s"}`},
		{"timeout x", `{"version":1,"dialect":"postgres","lock_timeout":"x"}`},
		{"timeout number", `{"version":1,"dialect":"postgres","lock_timeout":5}`},
		{"not an object", `[1]`},
		{"malformed", `{"version":`},
	} {
		_, err := migrate.ReadDir(sumFS(t, map[string]string{"velox.json": c.json}))
		e := asError(t, err, "config")
		if e.Pos != "velox.json" {
			t.Errorf("%s: Pos = %q", c.name, e.Pos)
		}
	}
	for _, c := range []migrate.Config{
		{},
		{Dialect: velox.Postgres, Table: "a-b"},
		{Dialect: velox.Postgres, Table: strings.Repeat("t", 64)},
		{Dialect: velox.Postgres, LockTimeout: -time.Second},
	} {
		_, err := migrate.NewDir(c)
		_ = asError(t, err, "config")
	}
}

func TestSum(t *testing.T) {
	files := map[string]string{
		"0001_a.sql":  "CREATE TABLE a (id int);\n",
		"0002_b.sql":  "CREATE TABLE b (id int);\n",
		"schema.json": schemaPg,
		"velox.json":  velox0,
	}
	fsys := sumFS(t, files)
	got, err := migrate.Sum(fsys)
	if err != nil {
		t.Fatal(err)
	}
	body := "0001_a.sql " + h1(files["0001_a.sql"]) + "\n" +
		"0002_b.sql " + h1(files["0002_b.sql"]) + "\n" +
		"schema.json " + h1(schemaPg) + "\n" +
		"velox.json " + h1(velox0) + "\n"
	if want := h1(body) + "\n" + body; string(got) != want {
		t.Errorf("Sum =\n%s\nwant\n%s", got, want)
	}
	if again, _ := migrate.Sum(fsys); !bytes.Equal(again, got) {
		t.Error("Sum differs on a second run")
	}
	// Files velox.sum does not cover change nothing.
	fsys["README.md"] = &fstest.MapFile{Data: []byte("hello")}
	fsys["sub/0009_x.sql"] = &fstest.MapFile{Data: []byte("SELECT 1;")}
	fsys["notes.sql.bak"] = &fstest.MapFile{Data: []byte("x")}
	if more, err := migrate.Sum(fsys); err != nil || !bytes.Equal(more, got) {
		t.Errorf("Sum with other files = %q, %v; want %q", more, err, got)
	}
	if _, err := migrate.ReadDir(fsys); err != nil {
		t.Errorf("ReadDir with other files: %v", err)
	}
	// An empty directory.
	empty, err := migrate.Sum(fstest.MapFS{})
	if err != nil || string(empty) != h1("")+"\n" {
		t.Errorf("Sum of nothing = %q, %v", empty, err)
	}
}

func TestSumCRLF(t *testing.T) {
	lf := dirOf(t, velox.Postgres, step{to: mustTables(velox.Postgres, nil)})
	crlf := fstest.MapFS{}
	for p, f := range lf {
		crlf[p] = &fstest.MapFile{Data: bytes.ReplaceAll(f.Data, []byte("\n"), []byte("\r\n"))}
	}
	if !bytes.Contains(crlf["velox.sum"].Data, []byte("\r\n")) || !bytes.Contains(crlf["schema.json"].Data, []byte("\r\n")) {
		t.Fatal("the copy is not CRLF")
	}
	a, err := migrate.Sum(lf)
	if err != nil {
		t.Fatal(err)
	}
	b, err := migrate.Sum(crlf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("Sum differs:\n%s\n%s", a, b)
	}
	got, err := migrate.ReadDir(crlf)
	if err != nil {
		t.Fatal(err)
	}
	want, err := migrate.ReadDir(lf)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) == 0 || len(got.Files) != len(want.Files) || got.Files[0].Sum != want.Files[0].Sum {
		t.Errorf("files differ: %+v, %+v", got.Files, want.Files)
	}
	// LF files with a CRLF velox.sum.
	mixed := cloneFS(lf)
	mixed["velox.sum"] = &fstest.MapFile{Data: crlf["velox.sum"].Data}
	if _, err := migrate.ReadDir(mixed); err != nil {
		t.Errorf("CRLF velox.sum: %v", err)
	}
	// A byte order mark changes no sum.
	bom := cloneFS(lf)
	for p, f := range bom {
		if strings.HasSuffix(p, ".sql") {
			bom[p] = &fstest.MapFile{Data: append([]byte("\xEF\xBB\xBF"), f.Data...)}
		}
	}
	if c, err := migrate.Sum(bom); err != nil || !bytes.Equal(c, a) {
		t.Errorf("Sum with a BOM = %q, %v", c, err)
	}
}

func TestReadDirStale(t *testing.T) {
	d := velox.Postgres
	good := dirOf(t, d, step{to: mustTables(d, nil)}, step{to: mustTables(d, nil), name: "again"})
	var sql string
	for p := range good {
		if strings.HasSuffix(p, ".sql") {
			sql = p
		}
	}
	if sql == "" {
		t.Fatal("no SQL file")
	}
	if _, err := migrate.ReadDir(good); err != nil {
		t.Fatal(err)
	}
	edit := func(fsys fstest.MapFS, path, add string) {
		fsys[path] = &fstest.MapFile{Data: append(slices.Clone(fsys[path].Data), add...)}
	}
	for name, mutate := range map[string]func(fstest.MapFS){
		"edited sql":         func(f fstest.MapFS) { edit(f, sql, "-- edit\n") },
		"added sql":          func(f fstest.MapFS) { f["0099_new.sql"] = &fstest.MapFile{Data: []byte("SELECT 1;\n")} },
		"removed sql":        func(f fstest.MapFS) { delete(f, sql) },
		"renamed sql":        func(f fstest.MapFS) { f["0098_renamed.sql"] = f[sql]; delete(f, sql) },
		"edited schema.json": func(f fstest.MapFS) { edit(f, "schema.json", " ") },
		"edited velox.json":  func(f fstest.MapFS) { edit(f, "velox.json", " ") },
		"removed velox.json": func(f fstest.MapFS) { delete(f, "velox.json") },
		"no velox.sum":       func(f fstest.MapFS) { delete(f, "velox.sum") },
		"empty velox.sum":    func(f fstest.MapFS) { f["velox.sum"] = &fstest.MapFile{} },
		"a stale sum wins over a malformed file": func(f fstest.MapFS) {
			f[sql] = &fstest.MapFile{Data: []byte("SELECT 'open;")}
		},
		"a stale sum wins over a bad name": func(f fstest.MapFS) {
			f["Bad Name.sql"] = &fstest.MapFile{Data: []byte("SELECT 1;")}
		},
		"a stale sum wins over a bad velox.json": func(f fstest.MapFS) {
			f["velox.json"] = &fstest.MapFile{Data: []byte("{")}
		},
	} {
		fsys := cloneFS(good)
		mutate(fsys)
		_, err := migrate.ReadDir(fsys)
		var se *migrate.SumError
		if !errors.As(err, &se) {
			t.Errorf("%s: got %v (%T), want a *SumError", name, err, err)
			continue
		}
		if se.Path != "velox.sum" {
			t.Errorf("%s: Path = %q", name, se.Path)
		}
		const text = "migrate: velox.sum is out of date; run velox-gen migrate hash if the edits are intended"
		if err.Error() != text {
			t.Errorf("%s: %q", name, err)
		}
	}
}

const (
	createA = "CREATE TABLE a (id int);\n"
	createB = "CREATE TABLE b (id int);\n"
)

func TestReadDir(t *testing.T) {
	d := velox.Postgres
	schemaJSON := func(d velox.Dialect) string {
		b, err := (migrate.Schema{Dialect: d}).MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		return string(b) + "\n"
	}
	base := func(extra map[string]string) map[string]string {
		m := map[string]string{"velox.json": velox0, "schema.json": schemaJSON(d)}
		maps.Copy(m, extra)
		return m
	}

	// Files by version, numerically.
	got, err := migrate.ReadDir(sumFS(t, base(map[string]string{"0010_b.sql": createB, "0002_a.sql": createA, "10000_c.sql": "SELECT 1;\n"})))
	if err != nil {
		t.Fatal(err)
	}
	var versions []uint64
	var paths []string
	for _, f := range got.Files {
		versions, paths = append(versions, f.Version), append(paths, f.Path)
	}
	if !slices.Equal(versions, []uint64{2, 10, 10000}) || !slices.Equal(paths, []string{"0002_a.sql", "0010_b.sql", "10000_c.sql"}) {
		t.Errorf("files = %v %v", versions, paths)
	}
	if got.Files[0].Statements[0] != "CREATE TABLE a (id int)" || got.Files[0].Sum != h1(createA) {
		t.Errorf("file = %+v", got.Files[0])
	}

	// The empty directory.
	for _, d := range allDialects {
		got, err := migrate.ReadDir(sumFS(t, map[string]string{"velox.json": `{"version":1,"dialect":"` + d.String() + `"}`}))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.Snapshot, &migrate.Schema{Dialect: d}) || len(got.Files) != 0 {
			t.Errorf("%v: Snapshot = %+v, Files = %v", d, got.Snapshot, got.Files)
		}
	}

	// Errors.
	for _, c := range []struct {
		name  string
		files map[string]string
		code  string
		text  string
	}{
		{"two files of version 2", base(map[string]string{"0002_a.sql": createA, "0002_b.sql": createB}), "directory", "0002_a.sql and 0002_b.sql have one version"},
		{"two files of one number", base(map[string]string{"0002_a.sql": createA, "00002_b.sql": createB}), "directory", "have one version"},
		{"no velox.json", map[string]string{"schema.json": schemaJSON(d), "0001_a.sql": createA}, "config", "velox.json"},
		{"no schema.json", map[string]string{"velox.json": velox0, "0001_a.sql": createA}, "directory", "schema.json"},
		{"schema.json without files", base(nil), "directory", "schema.json"},
		{"schema.json of mysql", base(map[string]string{"0001_a.sql": createA, "schema.json": schemaJSON(velox.MySQL)}), "directory", "mysql"},
		{"malformed schema.json", base(map[string]string{"0001_a.sql": createA, "schema.json": "{"}), "json", "schema.json"},
		{"a file's error", base(map[string]string{"0001_a.sql": "SELECT 'open;\n"}), "file", "0001_a.sql:1"},
		{"a bad file name", base(map[string]string{"1_a.sql": createA}), "file", "1_a.sql"},
		{"a directive MySQL refuses", base(map[string]string{"0001_a.sql": "-- velox:foreign-keys off\n" + createA}), "file", "foreign-keys"},
	} {
		_, err := migrate.ReadDir(sumFS(t, c.files))
		e := asError(t, err, c.code)
		if !strings.Contains(err.Error(), c.text) {
			t.Errorf("%s: %q does not contain %q", c.name, err, c.text)
		}
		_ = e
	}
}

// handPlan is a plan of three scripts, as PostgreSQL gives for an enum value,
// a table and an index built online.
func handPlan() *migrate.Plan {
	return &migrate.Plan{
		Changes: []migrate.Change{migrate.CreateTable{Table: migrate.Table{Name: "t"}}},
		Scripts: []migrate.Script{
			{Kind: migrate.EnumScript, Directives: migrate.Directives{NoTransaction: true},
				Statements: []migrate.Statement{{SQL: "ALTER TYPE e ADD VALUE 'x'", Unit: 1}}},
			{Kind: migrate.MainScript, Statements: []migrate.Statement{{SQL: "CREATE TABLE t (id bigint)"}, {SQL: "CREATE TABLE u (id bigint)", Review: "check; it"}}},
			{Kind: migrate.OnlineScript, Directives: migrate.Directives{NoTransaction: true},
				Statements: []migrate.Statement{{SQL: "CREATE INDEX CONCURRENTLY i ON t (id)", Unit: 1}}},
		},
	}
}

// mainPlan is handPlan's main script alone, which every dialect runs.
func mainPlan() *migrate.Plan {
	return &migrate.Plan{Changes: handPlan().Changes, Scripts: handPlan().Scripts[1:2]}
}

var tSchema = migrate.Schema{Dialect: velox.Postgres, Tables: []migrate.Table{{
	Name: "t", Columns: []migrate.Column{{Name: "id", Type: "bigint"}}, PrimaryKey: []string{"id"},
}}}

const header = "-- Generated by velox-gen (devel) migrate diff. Review before applying; velox.sum checks it.\n"

func TestDirAdd(t *testing.T) {
	d := velox.Postgres
	plan := handPlan()
	three := map[string]string{"0001_a.sql": createA, "0002_b.sql": createB, "0003_c.sql": "SELECT 1;\n"}
	dir, err := migrate.ReadDir(dirOf(t, d, step{files: three}))
	if err != nil {
		t.Fatal(err)
	}
	before, err := migrate.ReadDir(dirOf(t, d, step{files: three}))
	if err != nil {
		t.Fatal(err)
	}
	planCopy := handPlan()
	files, err := dir.Add("x", plan, &tSchema)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := slices.Sorted(maps.Keys(files)), []string{"0004_x_enum.sql", "0005_x.sql", "0006_x_online.sql", "schema.json", "velox.sum"}; !slices.Equal(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(dir, before) {
		t.Error("Add changed the Dir")
	}
	if !reflect.DeepEqual(plan, planCopy) {
		t.Error("Add changed the plan")
	}
	for i, p := range []string{"0004_x_enum.sql", "0005_x.sql", "0006_x_online.sql"} {
		body := migrate.WriteScript(plan.Scripts[i])
		if i == 0 {
			body = strings.Replace(body, "-- velox:no-transaction\n", "-- velox:no-transaction\n-- velox:lock-timeout 5s\n", 1)
		}
		if got := string(files[p]); got != header+body {
			t.Errorf("%s =\n%s\nwant\n%s", p, got, header+body)
		}
	}
	if got := string(files["schema.json"]); !strings.HasSuffix(got, "\n") || !strings.HasPrefix(got, "{\n  \"version\": 1,\n  \"dialect\": \"postgres\",\n  \"tables\": [\n") {
		t.Errorf("schema.json = %q", got)
	}

	// ReadDir accepts the result, and velox.sum covers the old files and the new.
	fsys := dirOf(t, d, step{files: three})
	for p, b := range files {
		fsys[p] = &fstest.MapFile{Data: b}
	}
	got, err := migrate.ReadDir(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 6 || !got.Snapshot.Equal(&tSchema) {
		t.Errorf("ReadDir: %d files, snapshot %+v", len(got.Files), got.Snapshot)
	}
	for i, f := range got.Files {
		if migrate.FileGenerated(f) != (i >= 3) {
			t.Errorf("%s: generated = %v", f.Path, i < 3)
		}
	}
	if got.Files[3].Directives.LockTimeout != 5*time.Second || !got.Files[3].Directives.LockTimeoutSet || !got.Files[3].Directives.NoTransaction {
		t.Errorf("enum file directives = %+v", got.Files[3].Directives)
	}
	if want, _ := migrate.Sum(fsys); !bytes.Equal(files["velox.sum"], want) {
		t.Errorf("velox.sum =\n%s\nwant\n%s", files["velox.sum"], want)
	}

	// A new directory: velox.json too, and versions from 0001.
	nd, err := migrate.NewDir(migrate.Config{Dialect: d})
	if err != nil {
		t.Fatal(err)
	}
	files, err = nd.Add("x", plan, &tSchema)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := slices.Sorted(maps.Keys(files)), []string{"0001_x_enum.sql", "0002_x.sql", "0003_x_online.sql", "schema.json", "velox.json", "velox.sum"}; !slices.Equal(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	if got := string(files["velox.json"]); got != string(migrate.ConfigJSON(nd.Config)) {
		t.Errorf("velox.json = %q", got)
	}
	fsys = fstest.MapFS{}
	for p, b := range files {
		fsys[p] = &fstest.MapFile{Data: b}
	}
	if _, err := migrate.ReadDir(fsys); err != nil {
		t.Error(err)
	}

	// A plan with the default name; a plan of a main script alone.
	files, err = dir.Add("", mainPlan(), &tSchema)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files["0004_create_t.sql"]; !ok || len(files) != 3 {
		t.Errorf("files = %v", slices.Sorted(maps.Keys(files)))
	}

	// Refusals.
	mysql := &migrate.Schema{Dialect: velox.MySQL}
	for _, c := range []struct {
		name string
		plan *migrate.Plan
		to   *migrate.Schema
		file string
	}{
		{"a nil plan", nil, &tSchema, "x"},
		{"an empty plan", &migrate.Plan{Changes: handPlan().Changes}, &tSchema, "x"},
		{"a nil schema", plan, nil, "x"},
		{"a schema of MySQL", plan, mysql, "x"},
		{"a name in capitals", plan, &tSchema, "Add_X"},
		{"a name with a dash", plan, &tSchema, "add-x"},
		{"a name of 41 bytes", plan, &tSchema, strings.Repeat("a", 41)},
	} {
		files, err := dir.Add(c.file, c.plan, c.to)
		_ = asError(t, err, "directory")
		if files != nil {
			t.Errorf("%s: files = %v", c.name, files)
		}
	}
	if _, err := dir.Add(strings.Repeat("a", 40), plan, &tSchema); err != nil {
		t.Errorf("a name of 40 bytes: %v", err)
	}
	if _, err := dir.Add("", &migrate.Plan{Scripts: plan.Scripts}, &tSchema); err == nil {
		t.Error("a plan with no change and no name was accepted")
	}

	// Add is deterministic.
	a, _ := dir.Add("x", plan, &tSchema)
	b2, _ := dir.Add("x", plan, &tSchema)
	if !reflect.DeepEqual(a, b2) {
		t.Error("Add differs on a second run")
	}
}

// TestDirAddEnumLockTimeout: an enum script without a lock timeout takes the
// Config's; the main script takes none.
func TestDirAddEnumLockTimeout(t *testing.T) {
	d := velox.Postgres
	c := caseNamed("enum-append")
	from, _ := models(t, c, d)
	to := mustTables(d, func(m *loadjson.Model) {
		setEnum(m, "User", "role", "admin", "viewer", "owner")
		addField(m, "User", field.String("nickname").Optional())
	})
	fsys := dirOf(t, d, step{to: from})
	fsys["velox.json"] = &fstest.MapFile{Data: []byte(`{"version": 1, "dialect": "postgres", "lock_timeout": "7s"}` + "\n")}
	resum(t, fsys)
	dir, err := migrate.ReadDir(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if dir.Config.LockTimeout != 7*time.Second {
		t.Fatalf("Config = %+v", dir.Config)
	}
	plan, err := migrate.NewPlan(dir.Snapshot, to, migrate.PlanOptions{Allow: c.allow})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Scripts[0].Kind != migrate.EnumScript || plan.Scripts[0].Directives.LockTimeoutSet {
		t.Fatalf("scripts = %+v", plan.Scripts)
	}
	files, err := dir.Add("", plan, to)
	if err != nil {
		t.Fatal(err)
	}
	var enum, main string
	for p, b := range files {
		switch {
		case strings.HasSuffix(p, "_enum.sql"):
			enum = string(b)
		case strings.HasSuffix(p, ".sql"):
			main = string(b)
		}
	}
	if !strings.Contains(enum, "\n-- velox:lock-timeout 7s\n") {
		t.Errorf("enum file:\n%s", enum)
	}
	if main == "" || strings.Contains(main, "lock-timeout") {
		t.Errorf("main file:\n%s", main)
	}
	// The plan's script is not written through.
	if plan.Scripts[0].Directives.LockTimeoutSet {
		t.Error("Add changed the plan's script")
	}
	// A lock timeout the plan has is kept.
	plan, err = migrate.NewPlan(dir.Snapshot, to, migrate.PlanOptions{Allow: c.allow, LockTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	files, _ = dir.Add("", plan, to)
	for p, b := range files {
		if strings.HasSuffix(p, "_enum.sql") && !strings.Contains(string(b), "-- velox:lock-timeout 2s\n") {
			t.Errorf("enum file:\n%s", b)
		}
	}
}

func TestDefaultName(t *testing.T) {
	d := velox.MySQL
	caseChanges := func(name string) []migrate.Change {
		c := caseNamed(name)
		from, to := models(t, c, d)
		return diff(t, from, to, c.opts)
	}
	users := migrate.TableRef{Name: "users"}
	for _, c := range []struct {
		name    string
		changes []migrate.Change
		want    string
	}{
		{"create-tables", caseChanges("create-tables"), "create_orgs"},
		{"add-column-optional", caseChanges("add-column-optional"), "add_users_nickname"},
		{"drop-column", []migrate.Change{migrate.DropColumn{Table: users, Column: migrate.Column{Name: "legacy"}}}, "drop_users_legacy"},
		{"schema-qualified", []migrate.Change{migrate.CreateTable{Table: migrate.Table{Schema: "billing", Name: "invoices"}}}, "create_billing_invoices"},
		{"cut at 40 bytes", []migrate.Change{migrate.CreateTable{Table: migrate.Table{Name: strings.Repeat("a", 60)}}}, "create_" + strings.Repeat("a", 33)},
		{"no trailing underscore", []migrate.Change{migrate.CreateTable{Table: migrate.Table{Name: strings.Repeat("a", 32) + "_" + strings.Repeat("b", 10)}}}, "create_" + strings.Repeat("a", 32)},
		{"capitals and dashes", []migrate.Change{migrate.CreateTable{Table: migrate.Table{Name: "Users-X"}}}, "create_users_x"},
		{"drop-table", []migrate.Change{migrate.DropTable{Table: migrate.Table{Name: "posts"}}}, "drop_posts"},
		{"rename-column", []migrate.Change{migrate.RenameColumn{Table: users, From: "a", To: "b"}}, "rename_users_b"},
		{"enum then table", []migrate.Change{migrate.CreateEnum{Enum: migrate.Enum{Name: "role"}}, migrate.CreateTable{Table: migrate.Table{Name: "orgs"}}}, "create_orgs"},
		{"enum then column", []migrate.Change{migrate.AlterEnum{To: migrate.Enum{Name: "role"}}, migrate.CreateEnum{Enum: migrate.Enum{Name: "x"}}, migrate.AddColumn{Table: users, Column: migrate.Column{Name: "age"}}}, "add_users_age"},
		{"enums only", []migrate.Change{migrate.AlterEnum{To: migrate.Enum{Name: "role"}}, migrate.CreateEnum{Enum: migrate.Enum{Name: "x"}}}, "alter_role"},
		{"create-enum", []migrate.Change{migrate.CreateEnum{Enum: migrate.Enum{Name: "role"}}}, "create_role"},
		{"set-table-comment", []migrate.Change{migrate.SetTableComment{Table: users}}, "set_users"},
		{"non-ASCII", []migrate.Change{migrate.CreateTable{Table: migrate.Table{Name: "café"}}}, "create_caf"},
	} {
		got := migrate.DefaultName(c.changes)
		if got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
		if len(got) > 40 || !regexpName.MatchString(got) {
			t.Errorf("%s: %q is not a file name", c.name, got)
		}
	}
	if got := migrate.DefaultName(nil); got != "" {
		t.Errorf("DefaultName(nil) = %q", got)
	}
}

// goldenCases are the cases whose directories are pinned byte for byte.
var goldenCases = []string{"create-tables", "add-index", "enum-insert", "drop-column"}

// readTree is the files under root, by slash path.
func readTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = string(b)
		return err
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return out
}

// caseDir is the directory of the case on d: from, then to.
func caseDir(t *testing.T, c testCase, d velox.Dialect) fstest.MapFS {
	t.Helper()
	from, to := models(t, c, d)
	return dirOf(t, d, step{to: from}, step{to: to, opts: migrate.PlanOptions{Diff: c.opts, Allow: c.allow}})
}

func TestDirGolden(t *testing.T) {
	for _, name := range goldenCases {
		c := caseNamed(name)
		for _, d := range c.on() {
			got := map[string]string{}
			for p, f := range caseDir(t, c, d) {
				got[p] = string(f.Data)
			}
			root := filepath.Join("testdata", "dir", name+"."+d.String())
			if *update {
				if err := os.RemoveAll(root); err != nil {
					t.Fatal(err)
				}
				for p, s := range got {
					path := filepath.Join(root, filepath.FromSlash(p))
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				continue
			}
			want := readTree(t, root)
			if len(want) == 0 {
				t.Errorf("%s: no golden files (run with -update to write them)", root)
			}
			for _, p := range slices.Sorted(maps.Keys(got)) {
				if w, ok := want[p]; !ok {
					t.Errorf("%s/%s is not a golden file (run with -update)", root, p)
				} else if w != got[p] {
					t.Errorf("%s/%s differs (run with -update to write it):\n%s\nwant\n%s", root, p, got[p], w)
				}
			}
			for p := range want {
				if _, ok := got[p]; !ok {
					t.Errorf("%s/%s is not written any more", root, p)
				}
			}
		}
	}
}

// TestDirReadsWhatAddWrites: ReadDir of a directory Add wrote has the model
// that made it as its snapshot, and the statements Render wrote as its last
// files'.
func TestDirReadsWhatAddWrites(t *testing.T) {
	for _, c := range cases {
		for _, d := range c.on() {
			from, to := models(t, c, d)
			dir, err := migrate.ReadDir(caseDir(t, c, d))
			if err != nil {
				t.Errorf("%s on %v: %v", c.name, d, err)
				continue
			}
			if !dir.Snapshot.Equal(to) {
				t.Errorf("%s on %v: the snapshot is not the model", c.name, d)
			}
			changes := diff(t, from, to, c.opts)
			scripts := render(t, from, to, changes, migrate.RenderOptions{LockTimeout: 5 * time.Second, Allow: c.allow})
			if len(dir.Files) < len(scripts) {
				t.Errorf("%s on %v: %d files for %d scripts", c.name, d, len(dir.Files), len(scripts))
				continue
			}
			last := dir.Files[len(dir.Files)-len(scripts):]
			for i, s := range scripts {
				var want []string
				for _, st := range s.Statements {
					want = append(want, st.SQL)
				}
				if !slices.Equal(last[i].Statements, want) {
					t.Errorf("%s on %v: file %s has\n%q\nwant\n%q", c.name, d, last[i].Path, last[i].Statements, want)
				}
			}
		}
	}
}

func TestDirDeterministic(t *testing.T) {
	for _, c := range cases {
		for _, d := range c.on() {
			a, b := caseDir(t, c, d), caseDir(t, c, d)
			if !maps.EqualFunc(a, b, func(x, y *fstest.MapFile) bool { return bytes.Equal(x.Data, y.Data) }) {
				t.Errorf("%s on %v: the directory differs on a second run", c.name, d)
			}
		}
	}
}

func TestDirOfSteps(t *testing.T) {
	d := velox.MySQL
	c := caseNamed("create-tables")
	from, to := models(t, c, d)

	// An empty plan adds no file.
	fsys := dirOf(t, d, step{to: from})
	if got := slices.Sorted(maps.Keys(fsys)); !slices.Equal(got, []string{"velox.json", "velox.sum"}) {
		t.Errorf("files = %v", got)
	}
	if _, err := migrate.ReadDir(fsys); err != nil {
		t.Error(err)
	}
	if got := slices.Sorted(maps.Keys(dirOf(t, d))); !slices.Equal(got, []string{"velox.json", "velox.sum"}) {
		t.Errorf("dirOf with no step: %v", got)
	}
	// Add of an empty plan is still refused.
	dir, err := migrate.ReadDir(fsys)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := migrate.NewPlan(from, from, migrate.PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dir.Add("x", plan, from); err == nil {
		t.Error("Add accepted an empty plan")
	}

	// A files-only first step writes the empty schema's schema.json.
	fsys = dirOf(t, d, step{files: map[string]string{"0001_hand.sql": createA}})
	if s := string(fsys["schema.json"].Data); s != "{\n  \"version\": 1,\n  \"dialect\": \"mysql\"\n}\n" {
		t.Errorf("schema.json = %q", s)
	}
	dir, err = migrate.ReadDir(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(dir.Files) != 1 || !dir.Snapshot.Equal(from) {
		t.Errorf("Files = %v, Snapshot = %+v", dir.Files, dir.Snapshot)
	}
	// The next step plans from there, and a hand file after it keeps the model.
	fsys = extend(t, fsys, d, step{to: to, files: map[string]string{"0009_backfill.sql": "UPDATE a SET id = 1;\n"}})
	dir, err = migrate.ReadDir(fsys)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range dir.Files {
		paths = append(paths, f.Path)
	}
	if !slices.Equal(paths, []string{"0001_hand.sql", "0002_create_orgs.sql", "0009_backfill.sql"}) || !dir.Snapshot.Equal(to) {
		t.Errorf("paths = %v", paths)
	}
}

func TestConfigStrict(t *testing.T) {
	for _, c := range []struct{ name, json, key string }{
		{"duplicate table", `{"version":1,"dialect":"postgres","table":"a","table":"b"}`, "table"},
		{"duplicate version", `{"version":1,"version":1,"dialect":"postgres"}`, "version"},
		{"null table", `{"version":1,"dialect":"postgres","table":null}`, "table"},
		{"null lock_timeout", `{"version":1,"dialect":"postgres","lock_timeout":null}`, "lock_timeout"},
		{"null version", `{"version":null,"dialect":"postgres"}`, "version"},
	} {
		_, err := migrate.ReadDir(sumFS(t, map[string]string{"velox.json": c.json}))
		e := asError(t, err, "config")
		if !strings.Contains(err.Error(), `"`+c.key+`"`) || e.Pos != "velox.json" {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	// Trailing text is still refused.
	_, err := migrate.ReadDir(sumFS(t, map[string]string{"velox.json": velox0 + `{}`}))
	_ = asError(t, err, "config")
}

// TestJSONFilesBOM: a byte order mark does not stop velox.json or schema.json
// from being read, as velox.sum already ignores it.
func TestJSONFilesBOM(t *testing.T) {
	const bom = "\xEF\xBB\xBF"
	got, err := migrate.ReadDir(sumFS(t, map[string]string{
		"velox.json":  bom + `{"version":1,"dialect":"postgres","table":"m"}`,
		"schema.json": bom + schemaPg,
		"0001_a.sql":  createA,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.Table != "m" || len(got.Files) != 1 || !got.Snapshot.Equal(&migrate.Schema{Dialect: velox.Postgres}) {
		t.Errorf("Config = %+v, Files = %d, Snapshot = %+v", got.Config, len(got.Files), got.Snapshot)
	}
}

// TestDirPathOf: a version's path is its file's, else the name Add gives.
func TestDirPathOf(t *testing.T) {
	d, err := migrate.ReadDir(dirOf(t, velox.SQLite, files("0001_a.sql", "CREATE TABLE a (x integer);")))
	if err != nil {
		t.Fatal(err)
	}
	if got := d.PathOf(1, "other"); got != "0001_a.sql" {
		t.Errorf("version 1: %s", got)
	}
	if got := d.PathOf(12, "users"); got != "0012_users.sql" {
		t.Errorf("version 12: %s", got)
	}
	if got := d.PathOf(12345, "x"); got != "12345_x.sql" {
		t.Errorf("version 12345: %s", got)
	}
}
