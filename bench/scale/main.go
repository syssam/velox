// Command scale generates an application of n velox schemas, generated into
// one gen package, with s services that each use five entities, and measures
// what velox costs it to build: a cold build, the rebuild after adding a field
// to one schema and regenerating, a service's binary size and the linker's
// peak memory. With -check it compares the measurements with
// recorded thresholds and exits 1 when one is exceeded. With -graphql the
// schemas also carry the velox-graphql annotations, velox-gen runs with that
// plugin, and each service calls the GraphQL code of its five entities too.
//
// Every build runs under a -toolexec wrapper that reads the linker's peak
// memory, so the times include that wrapper's overhead. The overhead is the
// same in every run and the thresholds were calibrated on the same setup, so
// the figures compare with each other, not with a plain go build.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/syssam/velox2/internal/tempmod"
)

type config struct {
	n, services int
	graphql     bool   // generate the GraphQL code too, with the velox-graphql plugin
	dir         string // where the application is written: empty or missing; a temporary directory when empty
}

type report struct {
	N                  int     `json:"n"`
	Services           int     `json:"services"`
	ColdBuildS         float64 `json:"cold_build_s"`
	SchemaEditRebuildS float64 `json:"schema_edit_rebuild_s"`
	ServiceBinaryMB    float64 `json:"service_binary_mb"`
	LinkPeakMB         float64 `json:"link_peak_mb"`
}

func main() {
	var c config
	flag.IntVar(&c.n, "n", 500, "entities (a multiple of 10)")
	flag.IntVar(&c.services, "s", 100, "services")
	flag.BoolVar(&c.graphql, "graphql", false, "also generate and link the GraphQL code (velox-graphql)")
	thresholds := flag.String("check", "", "a thresholds file to check against")
	asJSON := flag.Bool("json", false, "print the report as JSON")
	flag.Parse()
	r, err := run(c)
	if err != nil {
		fmt.Fprintln(os.Stderr, "scale:", err)
		os.Exit(2)
	}
	if *asJSON {
		b, _ := json.MarshalIndent(r, "", "  ")
		fmt.Println(string(b))
	} else {
		fmt.Printf("n=%d services=%d cold %.1fs, schema edit + regenerate + rebuild %.2fs, service %.1f MB, link peak %.0f MB\n",
			r.N, r.Services, r.ColdBuildS, r.SchemaEditRebuildS, r.ServiceBinaryMB, r.LinkPeakMB)
	}
	if *thresholds != "" {
		if problems := check(r, c.thresholdsKey(), *thresholds); len(problems) > 0 {
			for _, p := range problems {
				fmt.Fprintln(os.Stderr, "scale: over threshold:", p)
			}
			os.Exit(1)
		}
	}
}

// thresholdsKey is the key of thresholds.json that holds this configuration's
// thresholds: the entities, and "-graphql" after them for the GraphQL variant.
func (c config) thresholdsKey() string {
	if c.graphql {
		return strconv.Itoa(c.n) + "-graphql"
	}
	return strconv.Itoa(c.n)
}

// repo is the root of this repository: bench/scale's grandparent.
func repo() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot locate the repository")
	}
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}

func run(c config) (report, error) {
	r := report{N: c.n, Services: c.services}
	if c.n < 10 || c.n%10 != 0 || c.services < 1 {
		return r, fmt.Errorf("-n must be a multiple of 10 and -s at least 1")
	}
	root, err := repo()
	if err != nil {
		return r, err
	}
	if c.dir == "" {
		if c.dir, err = os.MkdirTemp("", "velox-scale-"); err != nil {
			return r, err
		}
		defer func() { _ = os.RemoveAll(c.dir) }()
	} else if entries, err := os.ReadDir(c.dir); err == nil && len(entries) > 0 {
		// Its build cache would be warm, and the cold build would not be cold.
		return r, fmt.Errorf("directory %s is not empty: a reused directory would warm the build cache", c.dir)
	}
	app, tools, cache, bin := filepath.Join(c.dir, "app"), filepath.Join(c.dir, "tools"), filepath.Join(c.dir, "cache"), filepath.Join(c.dir, "bin")
	for _, d := range []string{tools, cache, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return r, err
		}
	}
	schemas, services := generate(c.n, c.services, c.graphql)
	// The services import gen, which does not exist until velox-gen has run, and
	// go mod tidy cannot resolve an import of a missing package: the module is
	// written and tidied with the schemas alone, and the services follow gen.
	write := tempmod.Write
	if c.graphql {
		write = tempmod.WriteGraphQL
	}
	if err := write(app, root, schemas); err != nil {
		return r, err
	}
	gen := filepath.Join(tools, "velox-gen")
	if out, err := exec.Command("go", "-C", root, "build", "-o", gen, "./cmd/velox-gen").CombinedOutput(); err != nil {
		return r, fmt.Errorf("building velox-gen: %v\n%s", err, out)
	}
	wrapper := filepath.Join(tools, "linkmem")
	if err := buildWrapper(wrapper); err != nil {
		return r, err
	}
	velox := func() error {
		args := []string{"-out", "gen"}
		if c.graphql {
			args = append(args, "-plugin", "velox-graphql")
		}
		cmd := exec.Command(gen, append(args, "./schema")...)
		cmd.Dir = app
		if c.graphql {
			// The plugin is a tool of the module, built by go tool, offline.
			cmd.Env = append(os.Environ(), "GOFLAGS="+tempmod.GoFlags("-mod=mod"), "GOPROXY=off", "GOWORK=off")
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("velox-gen: %v\n%s", err, out)
		}
		return nil
	}
	if err := velox(); err != nil {
		return r, err
	}
	for name, src := range services {
		p := filepath.Join(app, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return r, err
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			return r, err
		}
	}
	memFile := filepath.Join(tools, "linkmem.txt")
	build := func() error {
		cmd := exec.Command("go", "build", "-toolexec", wrapper, "-o", bin+string(filepath.Separator), "./...")
		cmd.Dir = app
		cmd.Env = append(os.Environ(), "GOCACHE="+cache, "GOFLAGS="+tempmod.GoFlags("-mod=mod"), "GOPROXY=off", "GOWORK=off", "VELOX_LINKMEM="+memFile)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("go build: %v\n%s%s", err, out, tempmod.Hint(string(out)))
		}
		return nil
	}
	start := time.Now()
	if err := build(); err != nil {
		return r, err
	}
	r.ColdBuildS = time.Since(start).Seconds()

	// The schema edit: a new field on the last schema. The edit itself is not
	// timed; the regeneration and the rebuild of every service are.
	p := filepath.Join(app, "schema", fmt.Sprintf("e%d.go", c.n-1))
	src, err := os.ReadFile(p)
	if err != nil {
		return r, err
	}
	changed := strings.Replace(string(src), noteField, noteField+fmt.Sprintf("\n\t\tfield.Int64(\"extra_%d\"),", time.Now().UnixNano()), 1)
	if changed == string(src) {
		return r, fmt.Errorf("cannot edit %s", p)
	}
	if err := os.WriteFile(p, []byte(changed), 0o644); err != nil {
		return r, err
	}
	start = time.Now()
	if err := velox(); err != nil {
		return r, err
	}
	if err := build(); err != nil {
		return r, err
	}
	r.SchemaEditRebuildS = time.Since(start).Seconds()

	if fi, err := os.Stat(filepath.Join(bin, "s0")); err == nil {
		r.ServiceBinaryMB = float64(fi.Size()) / (1 << 20)
	} else {
		return r, err
	}
	r.LinkPeakMB, err = peakMB(memFile)
	return r, err
}

// noteField is the line of every schema that the edit appends a field after.
const noteField = `field.String("note").Optional(),`

// generate returns the application's files (path → source): n schemas e0..e<n-1>
// of the schema package, which velox-gen turns into the gen package, and
// services s0..s<services-1> that each query five entities of gen.
//
// The schemas have the fields name (string), amount (float64), created_at
// (time) and note (an optional string), and these relations, which keep the
// shape of the old tree of domain packages. The schemas fall into groups of ten,
// and group g holds e<10g> to e<10g+9>.
//   - e<j> with j%10 != 0 belongs to e<j-1>, the previous schema of its group:
//     it has the inverse edge From("prev", E<j-1>.Type).Ref("next").Unique().Required()
//     bound to its field prev_id, and E<j-1> has the matching To("next").
//   - e<10k> with k > 0, the head of group k, belongs to e<10*((k-1)/2)>, the
//     head of group (k-1)/2: From("up", ...).Ref("downs<10k>").Unique().Required() bound
//     to up_id, and the matching To("downs<10k>") on that head. A head has
//     two children at most, so the old edge name "downs" would be declared
//     twice on one schema, which Build refuses; the child's number makes the
//     name unique.
//
// With graphql, the schemas also carry the annotations of velox-graphql: a
// root query field (e<i>s), OrderField and WhereOps on name, and each service
// calls the root connection function and the next edge's method of each of its
// five entities. Every entity E<j> with j%10 < 5 has a next edge.
//
// Every foreign key is a plain field of the schema that holds it, as an inverse
// edge needs one to bind with Field, and the edge is Required because Build
// wants the field Optional exactly when the edge is not Required.
func generate(n, services int, graphql bool) (schemas, svc map[string]string) {
	groups := n / 10
	parent := func(k int) int { return 10 * ((k - 1) / 2) } // the head a group head k > 0 belongs to
	downs := map[int][]int{}                                // head → the group heads that belong to it
	for k := 1; k < groups; k++ {
		downs[parent(k)] = append(downs[parent(k)], 10*k)
	}
	schemas = map[string]string{}
	for i := range n {
		var fields, edges strings.Builder
		nameField, annotations, gqlImport := `field.String("name")`, "", ""
		if graphql {
			nameField = `field.String("name").Annotations(graphql.OrderField("NAME"), graphql.WhereOps())`
			annotations = fmt.Sprintf("\nfunc (E%[1]d) Annotations() []schema.Annotation {\n\treturn []schema.Annotation{graphql.QueryField(\"e%[1]ds\")}\n}\n", i)
			gqlImport = "\t\"github.com/syssam/velox2/contrib/graphql\"\n"
		}
		fields.WriteString("\t\t" + nameField + ",\n\t\tfield.Float(\"amount\"),\n\t\tfield.Time(\"created_at\"),\n\t\t" + noteField + "\n")
		switch {
		case i%10 != 0:
			fields.WriteString("\t\tfield.Int64(\"prev_id\"),\n")
			fmt.Fprintf(&edges, "\t\tedge.From(\"prev\", E%d.Type).Ref(\"next\").Unique().Required().Field(\"prev_id\"),\n", i-1)
		case i > 0:
			fields.WriteString("\t\tfield.Int64(\"up_id\"),\n")
			fmt.Fprintf(&edges, "\t\tedge.From(\"up\", E%d.Type).Ref(\"downs%d\").Unique().Required().Field(\"up_id\"),\n", parent(i/10), i)
		}
		if i%10 != 9 {
			fmt.Fprintf(&edges, "\t\tedge.To(\"next\", E%d.Type),\n", i+1)
		}
		for _, d := range downs[i] {
			fmt.Fprintf(&edges, "\t\tedge.To(\"downs%d\", E%d.Type),\n", d, d)
		}
		schemas[filepath.Join("schema", fmt.Sprintf("e%d.go", i))] = fmt.Sprintf(`package schema

import (
%[4]s	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
)

// E%[1]d is a generated schema.
type E%[1]d struct{ schema.Schema }

func (E%[1]d) Fields() []schema.Field {
	return []schema.Field{
%[2]s	}
}

func (E%[1]d) Edges() []schema.Edge {
	return []schema.Edge{
%[3]s	}
}
%[5]s`, i, fields.String(), edges.String(), gqlImport, annotations)
	}
	svc = map[string]string{}
	for s := range services {
		var use strings.Builder
		for m := range 5 {
			e := 10*((s*7+m)%groups) + m
			fmt.Fprintf(&use, "\tsink(db.Query[gen.E%d]().Where(gen.E%ds.ID.EQ(1)).Limit(1).SQL(ctx))\n", e, e)
			if graphql {
				fmt.Fprintf(&use, "\tgqlSink(gen.E%[1]dsConnection(ctx, nil, nil, nil, nil, nil, nil))\n\tgqlSink((&gen.E%[1]d{}).NextGQL(ctx, nil, nil, nil, nil, nil, nil))\n", e)
			}
		}
		helper := ""
		if graphql {
			helper = "\nfunc gqlSink(v any, err error) { fmt.Println(v == nil, err) }\n"
		}
		svc[filepath.Join(fmt.Sprintf("s%d", s), "main.go")] = fmt.Sprintf(
			"package main\n\nimport (\n\t\"context\"\n\t\"fmt\"\n\n\t\"github.com/syssam/velox2\"\n\t\"example.com/tmp/gen\"\n)\n\nfunc sink(s string, _ []any, _ error) { fmt.Println(len(s)) }\n%s\nfunc main() {\n\tctx := context.Background()\n\tdb := velox.Open(velox.Postgres, nil)\n%s}\n",
			helper, use.String())
	}
	return schemas, svc
}

// buildWrapper builds the -toolexec program that runs each tool and, for the
// linker, appends its peak memory in bytes to $VELOX_LINKMEM.
func buildWrapper(path string) error {
	dir, err := os.MkdirTemp("", "velox-linkmem-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	src := `package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

func main() {
	cmd := exec.Command(os.Args[1], os.Args[2:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	if cmd.ProcessState != nil && strings.TrimSuffix(filepath.Base(os.Args[1]), ".exe") == "link" {
		if ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
			peak := int64(ru.Maxrss)
			if runtime.GOOS == "linux" {
				peak *= 1024 // kilobytes on Linux, bytes on macOS
			}
			if f, ferr := os.OpenFile(os.Getenv("VELOX_LINKMEM"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); ferr == nil {
				fmt.Fprintln(f, peak)
				f.Close()
			}
		}
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "linkmem:", err) // the tool did not start
		os.Exit(1)
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module linkmem\n\ngo 1.27.0\n"), 0o644); err != nil {
		return err
	}
	cmd := exec.Command("go", "build", "-o", path, ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("building the toolexec wrapper: %v\n%s", err, out)
	}
	return nil
}

func peakMB(file string) (float64, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return 0, fmt.Errorf("no linker measurements: %w", err)
	}
	var peak int64
	for _, line := range strings.Fields(string(b)) {
		if v, err := strconv.ParseInt(line, 10, 64); err == nil && v > peak {
			peak = v
		}
	}
	return float64(peak) / (1 << 20), nil
}

// check compares r with the thresholds recorded under key ("500", or
// "500-graphql" for the GraphQL variant).
func check(r report, key, path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return []string{err.Error()}
	}
	// A key starting with _ inside a thresholds object is a comment. A top-level _ key
	// is not supported: its value is not an object, and parsing fails.
	var all map[string]map[string]any
	if err := json.Unmarshal(b, &all); err != nil {
		return []string{err.Error()}
	}
	th, ok := all[key]
	if !ok {
		return []string{fmt.Sprintf("no thresholds for n=%s in %s", key, path)}
	}
	got := map[string]float64{
		"cold_build_s": r.ColdBuildS, "schema_edit_rebuild_s": r.SchemaEditRebuildS,
		"service_binary_mb": r.ServiceBinaryMB, "link_peak_mb": r.LinkPeakMB,
	}
	var problems []string
	for k, raw := range th {
		if strings.HasPrefix(k, "_") {
			continue // a comment
		}
		most, ok := raw.(float64)
		if !ok {
			problems = append(problems, fmt.Sprintf("threshold %s is not a number", k))
			continue
		}
		v, ok := got[k]
		if !ok {
			problems = append(problems, "unknown measurement "+k)
			continue
		}
		if v > most {
			problems = append(problems, fmt.Sprintf("%s = %.2f, over %.2f", k, v, most))
		}
	}
	for k := range got {
		if _, ok := th[k]; !ok {
			problems = append(problems, "missing threshold "+k)
		}
	}
	return problems
}
