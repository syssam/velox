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
//
// The build cost is checked as CPU time (user plus system, of each command and
// the children it reaped: the compiler and linker under go build) relative to a
// reference build in the same run: a small module of its own that imports
// net/http, built cold before the cold build and again after the schema edit.
// CPU time counts the work, not the wait, so it does not depend on -p (the
// local gate's GOFLAGS=-p=2 against CI's default) or on how many cores are
// free. Contention still slows each CPU-second (shared caches, the clock),
// which the reference, measured on the same machine at the same time, absorbs.
// The wall-clock seconds are printed for information and not checked.
package main

import (
	"context"
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

	"github.com/syssam/velox/internal/buildcost"
	"github.com/syssam/velox/internal/tempmod"
)

type config struct {
	n, services int
	graphql     bool   // generate the GraphQL code too, with the velox-graphql plugin
	m2m         int    // many-to-many edges to add (addM2M)
	through     bool   // make each many-to-many edge's links an edge schema
	dir         string // where the application is written: empty or missing; a temporary directory when empty
}

type report struct {
	N        int `json:"n"`
	Services int `json:"services"`
	// The reference build's CPU and wall seconds, before the cold build and
	// after the schema edit; RefCPUS is the mean of the two CPU times.
	RefCPUBeforeS  float64 `json:"ref_cpu_before_s"`
	RefCPUAfterS   float64 `json:"ref_cpu_after_s"`
	RefWallBeforeS float64 `json:"ref_wall_before_s"`
	RefWallAfterS  float64 `json:"ref_wall_after_s"`
	RefCPUS        float64 `json:"ref_cpu_s"`
	// Wall-clock seconds, printed only.
	ColdBuildS         float64 `json:"cold_build_s"`
	SchemaEditRebuildS float64 `json:"schema_edit_rebuild_s"`
	// CPU seconds, and their ratios to RefCPUS, which are checked.
	ColdBuildCPUS         float64 `json:"cold_build_cpu_s"`
	SchemaEditRebuildCPUS float64 `json:"schema_edit_rebuild_cpu_s"`
	ColdBuildX            float64 `json:"cold_build_x"`
	SchemaEditRebuildX    float64 `json:"schema_edit_rebuild_x"`
	ServiceBinaryMB       float64 `json:"service_binary_mb"`
	LinkPeakMB            float64 `json:"link_peak_mb"`
}

func main() {
	var c config
	flag.IntVar(&c.n, "n", 500, "entities (a multiple of 10)")
	flag.IntVar(&c.services, "s", 100, "services")
	flag.BoolVar(&c.graphql, "graphql", false, "also generate and link the GraphQL code (velox-graphql)")
	flag.IntVar(&c.m2m, "m2m", 0, "many-to-many edges to add, at most n/5")
	flag.BoolVar(&c.through, "through", false, "make each -m2m edge's links an edge schema (edge.Through) with one more field")
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
		fmt.Printf("n=%d services=%d reference CPU %.2fs, %.2fs (wall %.2fs, %.2fs); cold %.2fx (CPU %.1fs, wall %.1fs), schema edit + regenerate + rebuild %.2fx (CPU %.1fs, wall %.1fs); service %.1f MB, link peak %.0f MB\n",
			r.N, r.Services, r.RefCPUBeforeS, r.RefCPUAfterS, r.RefWallBeforeS, r.RefWallAfterS,
			r.ColdBuildX, r.ColdBuildCPUS, r.ColdBuildS, r.SchemaEditRebuildX, r.SchemaEditRebuildCPUS, r.SchemaEditRebuildS,
			r.ServiceBinaryMB, r.LinkPeakMB)
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
// thresholds: the entities, "-graphql" after them for the GraphQL variant,
// and "-m2m<k>" and "-through" for the many-to-many probe, which has none.
func (c config) thresholdsKey() string {
	k := strconv.Itoa(c.n)
	if c.graphql {
		k += "-graphql"
	}
	if c.m2m > 0 {
		k += "-m2m" + strconv.Itoa(c.m2m)
	}
	if c.through {
		k += "-through"
	}
	return k
}

// repo is the root of this repository: bench/scale's grandparent.
func repo() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot locate the repository")
	}
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}

// run builds the application that c describes and returns its build costs.
func run(c config) (report, error) {
	ctx := context.Background()
	r := report{N: c.n, Services: c.services}
	if c.n < 10 || c.n%10 != 0 || c.services < 1 {
		return r, fmt.Errorf("-n must be a multiple of 10 and -s at least 1")
	}
	if c.m2m < 0 || c.m2m > c.n/5 || c.through && c.m2m == 0 {
		return r, fmt.Errorf("-m2m must be between 0 and n/5, and -through needs it")
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
	if err := addM2M(schemas, c.m2m, c.through); err != nil {
		return r, err
	}
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
	if out, err := exec.CommandContext(ctx, "go", "-C", root, "build", "-o", gen, "./cmd/velox-gen").CombinedOutput(); err != nil {
		return r, fmt.Errorf("building velox-gen: %w\n%s", err, out)
	}
	wrapper := filepath.Join(tools, "linkmem")
	if err := buildcost.BuildWrapper(wrapper); err != nil {
		return r, err
	}
	velox := func() (cpu float64, err error) {
		args := []string{"-out", "gen"}
		if c.graphql {
			args = append(args, "-plugin", "velox-graphql")
		}
		cmd := exec.CommandContext(ctx, gen, append(args, "./schema")...)
		cmd.Dir = app
		if c.graphql {
			// The plugin is a tool of the module, built by go tool, offline.
			cmd.Env = append(os.Environ(), "GOFLAGS="+tempmod.GoFlags("-mod=mod"), "GOPROXY=off", "GOWORK=off")
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return 0, fmt.Errorf("velox-gen: %w\n%s", err, out)
		}
		return buildcost.CPUSeconds(cmd), nil
	}
	if _, err := velox(); err != nil {
		return r, err
	}
	if err := writeFiles(app, services); err != nil {
		return r, err
	}
	memFile := filepath.Join(tools, "linkmem.txt")
	build := func() (cpu float64, err error) {
		cmd := exec.CommandContext(ctx, "go", "build", "-toolexec", wrapper, "-o", bin+string(filepath.Separator), "./...")
		cmd.Dir = app
		cmd.Env = append(os.Environ(), "GOCACHE="+cache, "GOFLAGS="+tempmod.GoFlags("-mod=mod"), "GOPROXY=off", "GOWORK=off", buildcost.LinkMemEnv+"="+memFile)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return 0, fmt.Errorf("go build: %w\n%s%s", err, out, tempmod.Hint(string(out)))
		}
		return buildcost.CPUSeconds(cmd), nil
	}
	ref := filepath.Join(c.dir, "ref")
	if err := buildcost.WriteReference(ref); err != nil {
		return r, err
	}
	if r.RefCPUBeforeS, r.RefWallBeforeS, err = buildcost.BuildReference(ref, filepath.Join(c.dir, "refcache-before"), wrapper); err != nil {
		return r, err
	}
	start := time.Now()
	if r.ColdBuildCPUS, err = build(); err != nil {
		return r, err
	}
	r.ColdBuildS = time.Since(start).Seconds()

	// The schema edit: a new field on the last schema. The edit itself is not
	// timed; the regeneration and the rebuild of every service are.
	if err := editSchema(app, c.n); err != nil {
		return r, err
	}
	start = time.Now()
	genCPU, err := velox()
	if err != nil {
		return r, err
	}
	buildCPU, err := build()
	if err != nil {
		return r, err
	}
	r.SchemaEditRebuildS = time.Since(start).Seconds()
	r.SchemaEditRebuildCPUS = genCPU + buildCPU
	if r.RefCPUAfterS, r.RefWallAfterS, err = buildcost.BuildReference(ref, filepath.Join(c.dir, "refcache-after"), wrapper); err != nil {
		return r, err
	}
	r.RefCPUS = (r.RefCPUBeforeS + r.RefCPUAfterS) / 2
	if r.RefCPUS <= 0 {
		return r, fmt.Errorf("the reference build measured no CPU time")
	}
	r.ColdBuildX = r.ColdBuildCPUS / r.RefCPUS
	r.SchemaEditRebuildX = r.SchemaEditRebuildCPUS / r.RefCPUS

	if fi, err := os.Stat(filepath.Join(bin, "s0")); err == nil {
		r.ServiceBinaryMB = float64(fi.Size()) / (1 << 20)
	} else {
		return r, err
	}
	r.LinkPeakMB, err = buildcost.PeakMB(memFile)
	return r, err
}

// writeFiles writes files (path → source) under dir, making their
// directories.
func writeFiles(dir string, files map[string]string) error {
	for name, src := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// editSchema adds a new field after noteField in the last of the n schemas
// of the application at app.
func editSchema(app string, n int) error {
	p := filepath.Join(app, "schema", fmt.Sprintf("e%d.go", n-1))
	src, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	changed := strings.Replace(string(src), noteField, noteField+fmt.Sprintf("\n\t\tfield.Int64(\"extra_%d\"),", time.Now().UnixNano()), 1)
	if changed == string(src) {
		return fmt.Errorf("cannot edit %s", p)
	}
	return os.WriteFile(p, []byte(changed), 0o644)
}

// noteField is the line of every schema that the edit appends a field after.
const noteField = `field.String("note").Optional().Default(""),`

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
			gqlImport = "\t\"github.com/syssam/velox/contrib/graphql\"\n"
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
%[4]s	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
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
				fmt.Fprintf(&use, "\tgqlSink(db.Query[gen.E%[1]d]().Paginate(ctx, nil, nil, nil, nil))\n\tgqlSink((&gen.E%[1]d{}).Next(ctx, nil, nil, nil, nil, nil, nil))\n", e)
			}
		}
		helper := ""
		if graphql {
			helper = "\nfunc gqlSink(v any, err error) { fmt.Println(v == nil, err) }\n"
		}
		svc[filepath.Join(fmt.Sprintf("s%d", s), "main.go")] = fmt.Sprintf(
			"package main\n\nimport (\n\t\"context\"\n\t\"fmt\"\n\n\t\"github.com/syssam/velox\"\n\t\"example.com/tmp/gen\"\n)\n\nfunc sink(s string, _ []any, _ error) { fmt.Println(len(s)) }\n%s\nfunc main() {\n\tctx := context.Background()\n\tdb := velox.Open(velox.Postgres, nil)\n%s}\n",
			helper, use.String())
	}
	return schemas, svc
}

// addM2M adds k many-to-many edges to schemas, which generate made: E<i>,
// for the first k i with i%5 == 1, gets To("tags", E<i+2>.Type), and E<i+2>
// its inverse "tagged". With through, each edge goes through an edge schema
// L<i> of its own, with the two key fields and a role with a default, the
// schema an edge schema of a port from ent has. The probe compares the build
// cost of the two: a generated join entity per edge, or an edge schema.
func addM2M(schemas map[string]string, k int, through bool) error {
	for j := range k {
		i := 5*j + 1
		th := ""
		if through {
			th = fmt.Sprintf(".Through(\"links\", L%d.Type)", i)
			schemas[filepath.Join("schema", fmt.Sprintf("l%d.go", i))] = fmt.Sprintf(`package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// L%[1]d is the edge schema of E%[1]d's tags.
type L%[1]d struct{ schema.Schema }

func (L%[1]d) Fields() []schema.Field {
	return []schema.Field{
		field.String("role").Default("member"),
		field.Int64("e%[1]d_id"),
		field.Int64("e%[2]d_id"),
	}
}

func (L%[1]d) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("owner", E%[1]d.Type).Unique().Required().Field("e%[1]d_id").OnDelete(edge.Cascade),
		edge.To("target", E%[2]d.Type).Unique().Required().Field("e%[2]d_id").OnDelete(edge.Cascade),
	}
}
`, i, i+2)
		}
		if err := addEdge(schemas, i, fmt.Sprintf("edge.To(\"tags\", E%d.Type)%s", i+2, th)); err != nil {
			return err
		}
		if err := addEdge(schemas, i+2, fmt.Sprintf("edge.From(\"tagged\", E%d.Type).Ref(\"tags\")", i)); err != nil {
			return err
		}
	}
	return nil
}

// addEdge adds the edge e first among schema E<i>'s edges, or fails when
// the schema has no Edges method as generate writes it.
func addEdge(schemas map[string]string, i int, e string) error {
	p := filepath.Join("schema", fmt.Sprintf("e%d.go", i))
	head := fmt.Sprintf("func (E%d) Edges() []schema.Edge {\n\treturn []schema.Edge{\n", i)
	if !strings.Contains(schemas[p], head) {
		return fmt.Errorf("%s has no Edges method to add %s to", p, e)
	}
	schemas[p] = strings.Replace(schemas[p], head, head+"\t\t"+e+",\n", 1)
	return nil
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
		"cold_build_x": r.ColdBuildX, "schema_edit_rebuild_x": r.SchemaEditRebuildX,
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
