package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/syssam/velox/internal/buildcost"
	"github.com/syssam/velox/internal/tempmod"
)

// appPrefix is the import path of every app's module.
const appPrefix = "example.com/tmp"

// action is the part of a -debug-actiongraph action that the build
// measurement reads. A build action with a Cmd ran the compiler; one without
// was served from the cache. The same holds for link. CmdUser and CmdSys are
// the user and system CPU time of the commands it ran (under -toolexec, the
// wrapper's, which include the tool's).
type action struct {
	Mode            string
	Package         string
	Cmd             []string
	CmdUser, CmdSys time.Duration
}

// readActions reads the action graph that go build -debug-actiongraph wrote.
func readActions(path string) ([]action, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var as []action
	if err := json.Unmarshal(b, &as); err != nil {
		return nil, fmt.Errorf("action graph %s: %w", path, err)
	}
	return as, nil
}

// under reports whether the import path pkg is prefix or below it.
func under(pkg, prefix string) bool {
	return pkg == prefix || strings.HasPrefix(pkg, prefix+"/")
}

// actionCounts is what an action graph says a build did: its build (compile)
// and link actions and how many of them reran, and the CPU seconds of the
// build actions of the app (AppS) and of everything else (DepS).
type actionCounts struct {
	Build, BuildRerun, Link, LinkRerun int
	AppS, DepS                         float64
}

// countActions counts the build and link actions of the action graph in path,
// and the reruns among them. AppS and DepS sum CmdUser + CmdSys over the
// build actions of the packages under appPrefix and over the others; a cached
// action ran no command and adds nothing.
func countActions(path, appPrefix string) (actionCounts, error) {
	as, err := readActions(path)
	if err != nil {
		return actionCounts{}, err
	}
	var c actionCounts
	var app, dep time.Duration
	for _, a := range as {
		switch a.Mode {
		case "build":
			c.Build++
			if len(a.Cmd) > 0 {
				c.BuildRerun++
			}
			if under(a.Package, appPrefix) {
				app += a.CmdUser + a.CmdSys
			} else {
				dep += a.CmdUser + a.CmdSys
			}
		case "link":
			c.Link++
			if len(a.Cmd) > 0 {
				c.LinkRerun++
			}
		}
	}
	c.AppS, c.DepS = app.Seconds(), dep.Seconds()
	return c, nil
}

// reranUnder counts the build actions of the action graph in path that reran
// for a package under prefix but not under exclude (empty: nothing excluded).
func reranUnder(path, prefix, exclude string) (int, error) {
	as, err := readActions(path)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, a := range as {
		if a.Mode == "build" && len(a.Cmd) > 0 && under(a.Package, prefix) && (exclude == "" || !under(a.Package, exclude)) {
			n++
		}
	}
	return n, nil
}

// buildConfig is one build measurement: an ORM at N entities with Services
// services, the Run-th time. Tools holds the generator binaries, and Raw is
// the directory the action graphs are written to.
type buildConfig struct {
	ORM              orm
	N, Services, Run int
	Tools, Raw       string
}

// buildReport is one build measurement. The *_x fields are CPU seconds over
// RefCPUS, the mean of the reference builds before and after.
type buildReport struct {
	ORM      string `json:"orm"`
	N        int    `json:"n"`
	Services int    `json:"services"`
	Run      int    `json:"run"`

	CodegenWallS float64 `json:"codegen_wall_s"`
	CodegenCPUS  float64 `json:"codegen_cpu_s"`
	CodegenX     float64 `json:"codegen_x"`
	GenLines     int     `json:"gen_lines"`
	GenBytes     int     `json:"gen_bytes"`

	// The cold build, with an empty GOCACHE, and the CPU seconds of its build
	// actions of the app's packages and of the others (from the action graph).
	ColdWallS      float64 `json:"cold_wall_s"`
	ColdCPUS       float64 `json:"cold_cpu_s"`
	ColdX          float64 `json:"cold_x"`
	ColdAppActionS float64 `json:"cold_app_action_s"`
	ColdDepActionS float64 `json:"cold_dep_action_s"`

	// After the schema edit: the regeneration and the rebuild, and
	// EditX = (regen + rebuild CPU) / ref. RebuildRerun and RebuildCached
	// count the rebuild's build actions, RebuildRelinks its links that ran.
	RegenWallS     float64 `json:"regen_wall_s"`
	RegenCPUS      float64 `json:"regen_cpu_s"`
	RebuildWallS   float64 `json:"rebuild_wall_s"`
	RebuildCPUS    float64 `json:"rebuild_cpu_s"`
	EditX          float64 `json:"edit_x"`
	RebuildRerun   int     `json:"rebuild_rerun"`
	RebuildCached  int     `json:"rebuild_cached"`
	RebuildRelinks int     `json:"rebuild_relinks"`

	ServiceBinaryMB float64 `json:"service_binary_mb"`
	LinkPeakMB      float64 `json:"link_peak_mb"`

	RefCPUBeforeS float64 `json:"ref_cpu_before_s"`
	RefCPUAfterS  float64 `json:"ref_cpu_after_s"`
	RefCPUS       float64 `json:"ref_cpu_s"`
}

// coldCache, when set, is the GOCACHE of the cold build and the rebuild in
// place of a fresh one. Only a test sets it, to show that a warm cache fails.
var coldCache string

// measureBuild measures c.ORM's build cost in a fresh temporary directory,
// which it removes afterwards. The code generation is timed with the ordinary
// GOCACHE warm for that ORM's dependencies: a discarded code generation in a
// throwaway app comes first. It fails when the cold build took any build
// action from the cache, or when no action of the generated package reran
// after the schema edit.
func measureBuild(c buildConfig) (r buildReport, err error) {
	o := c.ORM
	r = buildReport{ORM: o.Name, N: c.N, Services: c.Services, Run: c.Run}
	if c.N < 10 || c.N%10 != 0 || c.Services < 1 {
		return r, fmt.Errorf("n must be a multiple of 10 and services at least 1")
	}
	if err := os.MkdirAll(c.Raw, 0o755); err != nil {
		return r, err
	}
	dir, err := os.MkdirTemp("", "compare-build-")
	if err != nil {
		return r, err
	}
	defer func() {
		if rerr := os.RemoveAll(dir); err == nil {
			err = rerr
		}
	}()
	app, bin, ref, wrapper := filepath.Join(dir, "app"), filepath.Join(dir, "bin"), filepath.Join(dir, "ref"), filepath.Join(dir, "linkmem")
	cache := filepath.Join(dir, "cache")
	if coldCache != "" {
		cache = coldCache
	}
	if err := buildcost.BuildWrapper(wrapper); err != nil {
		return r, err
	}
	if err := buildcost.WriteReference(ref); err != nil {
		return r, err
	}
	graph := func(kind string) string {
		return filepath.Join(c.Raw, fmt.Sprintf("ag-%s-n%d-r%d-%s.json", o.Name, c.N, c.Run, kind))
	}
	build := func(kind string) (cpu, wall float64, err error) {
		cmd := exec.Command("go", "build", "-toolexec", wrapper, "-debug-actiongraph="+graph(kind), "-o", bin+string(filepath.Separator), "./...")
		cmd.Dir = app
		cmd.Env = goEnv("GOCACHE="+cache, buildcost.LinkMemEnv+"="+filepath.Join(dir, "link-"+kind+".txt"))
		start := time.Now()
		out, err := cmd.CombinedOutput()
		wall = time.Since(start).Seconds()
		if err != nil {
			return 0, wall, fmt.Errorf("%s %s build: %v\n%s%s", o.Name, kind, err, out, tempmod.Hint(string(out)))
		}
		return buildcost.CPUSeconds(cmd), wall, nil
	}

	// 1. The app, with its schemas.
	if err := writeApp(o, app, o.Schema(c.N)); err != nil {
		return r, err
	}
	// 2. The reference build, before.
	if r.RefCPUBeforeS, _, err = buildcost.BuildReference(ref, filepath.Join(dir, "refcache-before"), wrapper); err != nil {
		return r, err
	}
	// A discarded code generation in a throwaway app with the same schemas,
	// so that the ordinary GOCACHE is warm for this ORM's dependencies (ent's
	// go list -export, veloxe's loader). The measured app is new, so old
	// velox's .velox cache in it is not shared.
	warm := filepath.Join(dir, "warm")
	if err := writeApp(o, warm, o.Schema(c.N)); err != nil {
		return r, err
	}
	if _, _, err := codegen(warm, o, c.Tools); err != nil {
		return r, fmt.Errorf("warm-up: %w", err)
	}
	if err := os.RemoveAll(warm); err != nil {
		return r, err
	}
	// 3. The code generation, timed, and 4. its size.
	if r.CodegenCPUS, r.CodegenWallS, err = codegen(app, o, c.Tools); err != nil {
		return r, err
	}
	if r.GenLines, r.GenBytes, err = countGenerated(filepath.Join(app, o.GenDir), o.genSkip()); err != nil {
		return r, err
	}
	// 5. The services, which import the generated code.
	services := map[string]string{}
	for s := range c.Services {
		services[filepath.Join(fmt.Sprintf("s%d", s), "main.go")] = o.Service(serviceEntities(s, c.N))
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
	// 6. The cold build, which must have compiled everything it built.
	if r.ColdCPUS, r.ColdWallS, err = build("cold"); err != nil {
		return r, err
	}
	cold, err := countActions(graph("cold"), appPrefix)
	if err != nil {
		return r, err
	}
	if cold.BuildRerun != cold.Build {
		return r, fmt.Errorf("%s: cold build reused %d of %d build actions from the cache (GOCACHE %s)", o.Name, cold.Build-cold.BuildRerun, cold.Build, cache)
	}
	r.ColdAppActionS, r.ColdDepActionS = cold.AppS, cold.DepS
	// 7. The schema edit, the regeneration and the rebuild, which must have
	// recompiled some of the generated code.
	if err := editSchema(app, o, c.N); err != nil {
		return r, err
	}
	if r.RegenCPUS, r.RegenWallS, err = codegen(app, o, c.Tools); err != nil {
		return r, err
	}
	if r.RebuildCPUS, r.RebuildWallS, err = build("edit"); err != nil {
		return r, err
	}
	edit, err := countActions(graph("edit"), appPrefix)
	if err != nil {
		return r, err
	}
	r.RebuildRerun, r.RebuildCached, r.RebuildRelinks = edit.BuildRerun, edit.Build-edit.BuildRerun, edit.LinkRerun
	// ent's schema package is inside its generated directory, and is not generated.
	genPkg := appPrefix + "/" + filepath.ToSlash(o.GenDir)
	if n, err := reranUnder(graph("edit"), genPkg, appPrefix+"/"+filepath.ToSlash(o.SchemaDir)); err != nil {
		return r, err
	} else if n == 0 {
		return r, fmt.Errorf("%s: no action of the generated package reran after the schema edit (%s)", o.Name, genPkg)
	}
	// 8. The reference build, after.
	if r.RefCPUAfterS, _, err = buildcost.BuildReference(ref, filepath.Join(dir, "refcache-after"), wrapper); err != nil {
		return r, err
	}
	r.RefCPUS = (r.RefCPUBeforeS + r.RefCPUAfterS) / 2
	if r.RefCPUS <= 0 {
		return r, fmt.Errorf("the reference build measured no CPU time")
	}
	r.CodegenX = r.CodegenCPUS / r.RefCPUS
	r.ColdX = r.ColdCPUS / r.RefCPUS
	r.EditX = (r.RegenCPUS + r.RebuildCPUS) / r.RefCPUS
	// 9. A service's size, and the cold build's peak link memory.
	fi, err := os.Stat(filepath.Join(bin, "s0"))
	if err != nil {
		return r, err
	}
	r.ServiceBinaryMB = float64(fi.Size()) / (1 << 20)
	r.LinkPeakMB, err = buildcost.PeakMB(filepath.Join(dir, "link-cold.txt"))
	return r, err
}

// buildMain is the build subcommand: one measureBuild, written to -out as
// JSON. It writes -out.tmp first and renames it, so that an -out that exists
// is complete.
func buildMain(args []string) int {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	name := fs.String("orm", "", "the ORM: velox, veloxe or ent")
	var c buildConfig
	fs.IntVar(&c.N, "n", 100, "entities (a multiple of 10)")
	fs.IntVar(&c.Services, "s", 20, "services")
	fs.IntVar(&c.Run, "run", 1, "the run's number, for the action graphs' names")
	fs.StringVar(&c.Tools, "tools", "", "the directory of the generator binaries")
	fs.StringVar(&c.Raw, "raw", "", "the directory the action graphs are written to")
	out := fs.String("out", "", "the report's JSON file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *name == "" || c.Tools == "" || c.Raw == "" || *out == "" || fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "compare build: -orm, -tools, -raw and -out are required, and nothing else")
		return 2
	}
	var err error
	if c.ORM, err = ormByName(*name); err != nil {
		fmt.Fprintln(os.Stderr, "compare build:", err)
		return 2
	}
	if err := writeBuildReport(c, *out); err != nil {
		fmt.Fprintln(os.Stderr, "compare build:", err)
		return 1
	}
	return 0
}

// writeBuildReport measures c and writes the report to out, through out.tmp.
func writeBuildReport(c buildConfig, out string) error {
	r, err := measureBuild(c)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := out + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, out); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	return nil
}
