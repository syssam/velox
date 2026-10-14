// Command scale generates an application of n velox entities in a tree of
// domain packages, with s services that each use five entities, and measures
// what velox costs it to build: a cold build, rebuilds after editing a leaf
// and a root entity, a service's binary size and the linker's peak memory
// (design §9). With -check it compares the measurements with recorded
// thresholds and exits 1 when one is exceeded.
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
	dir         string // where the application is written: empty or missing; a temporary directory when empty
}

type report struct {
	N               int     `json:"n"`
	Services        int     `json:"services"`
	ColdBuildS      float64 `json:"cold_build_s"`
	LeafRebuildS    float64 `json:"leaf_rebuild_s"`
	RootRebuildS    float64 `json:"root_rebuild_s"`
	ServiceBinaryMB float64 `json:"service_binary_mb"`
	LinkPeakMB      float64 `json:"link_peak_mb"`
}

func main() {
	var c config
	flag.IntVar(&c.n, "n", 500, "entities (a multiple of 10)")
	flag.IntVar(&c.services, "s", 100, "services")
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
		fmt.Printf("n=%d services=%d cold %.1fs, leaf rebuild %.2fs, root rebuild %.2fs, service %.1f MB, link peak %.0f MB\n",
			r.N, r.Services, r.ColdBuildS, r.LeafRebuildS, r.RootRebuildS, r.ServiceBinaryMB, r.LinkPeakMB)
	}
	if *thresholds != "" {
		if problems := check(r, c.n, *thresholds); len(problems) > 0 {
			for _, p := range problems {
				fmt.Fprintln(os.Stderr, "scale: over threshold:", p)
			}
			os.Exit(1)
		}
	}
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
	domains := c.n / 10
	if err := tempmod.Write(app, root, generate(domains, c.services)); err != nil {
		return r, err
	}
	gen := filepath.Join(tools, "velox-gen")
	if out, err := exec.Command("go", "-C", filepath.Join(root, "cmd", "velox-gen"), "build", "-o", gen, ".").CombinedOutput(); err != nil {
		return r, fmt.Errorf("building velox-gen: %v\n%s", err, out)
	}
	wrapper := filepath.Join(tools, "linkmem")
	if err := buildWrapper(wrapper); err != nil {
		return r, err
	}
	velox := func(pkgs ...string) error {
		cmd := exec.Command(gen, pkgs...)
		cmd.Dir = app
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("velox-gen %v: %v\n%s", pkgs, err, out)
		}
		return nil
	}
	if err := velox("./..."); err != nil {
		return r, err
	}
	memFile := filepath.Join(tools, "linkmem.txt")
	build := func() (float64, error) {
		start := time.Now()
		cmd := exec.Command("go", "build", "-toolexec", wrapper, "-o", bin+string(filepath.Separator), "./...")
		cmd.Dir = app
		cmd.Env = append(os.Environ(), "GOCACHE="+cache, "GOFLAGS=-mod=mod", "GOPROXY=off", "GOWORK=off", "VELOX_LINKMEM="+memFile)
		if out, err := cmd.CombinedOutput(); err != nil {
			return 0, fmt.Errorf("go build: %v\n%s", err, out)
		}
		return time.Since(start).Seconds(), nil
	}
	if r.ColdBuildS, err = build(); err != nil {
		return r, err
	}
	edit := func(domain int) (float64, error) {
		p := filepath.Join(app, fmt.Sprintf("d%d", domain), "e5.go")
		src, err := os.ReadFile(p)
		if err != nil {
			return 0, err
		}
		field := fmt.Sprintf("\tExtra%d int64\n}", time.Now().UnixNano())
		changed := strings.Replace(string(src), "\n}\n\nfunc (x *E5) Schema", "\n"+field+"\n\nfunc (x *E5) Schema", 1)
		if changed == string(src) {
			return 0, fmt.Errorf("cannot edit %s", p)
		}
		if err := os.WriteFile(p, []byte(changed), 0o644); err != nil {
			return 0, err
		}
		if err := velox(fmt.Sprintf("./d%d", domain)); err != nil {
			return 0, err
		}
		return build()
	}
	if r.LeafRebuildS, err = edit(domains - 1); err != nil {
		return r, err
	}
	if r.RootRebuildS, err = edit(0); err != nil {
		return r, err
	}
	if fi, err := os.Stat(filepath.Join(bin, "s0")); err == nil {
		r.ServiceBinaryMB = float64(fi.Size()) / (1 << 20)
	} else {
		return r, err
	}
	r.LinkPeakMB, err = peakMB(memFile)
	return r, err
}

// generate writes the application's packages (path → source).
func generate(domains, services int) map[string]string {
	files := map[string]string{}
	for d := range domains {
		pkg := fmt.Sprintf("d%d", d)
		for k := range 10 {
			var b strings.Builder
			fmt.Fprintf(&b, "package %s\n\nimport (\n\t\"time\"\n\n\t\"github.com/syssam/velox2\"\n", pkg)
			if k == 0 && d > 0 {
				fmt.Fprintf(&b, "\n\t\"example.com/tmp/d%d\"\n", (d-1)/2)
			}
			fmt.Fprintf(&b, ")\n\n// E%d is a generated entity.\ntype E%d struct {\n\tID        int64\n\tName      string\n\tAmount    float64\n\tCreatedAt time.Time\n\tNote      *string\n", k, k)
			switch {
			case k == 0 && d > 0:
				fmt.Fprintf(&b, "\tUpID      int64\n\tUp        velox.One[d%d.E0]\n", (d-1)/2)
			case k > 0:
				fmt.Fprintf(&b, "\tPrevID    int64\n\tPrev      velox.One[E%d]\n", k-1)
			}
			if k == 0 {
				b.WriteString("\tNext      velox.Many[E1]\n")
			}
			if k == 1 {
				b.WriteString("\tE0ID      int64\n")
			}
			fmt.Fprintf(&b, "}\n\nfunc (x *E%d) Schema(s *velox.Schema) {}\n", k)
			files[filepath.Join(pkg, fmt.Sprintf("e%d.go", k))] = b.String()
		}
	}
	for s := range services {
		var imp, use strings.Builder
		seen := map[int]bool{}
		for m := range 5 {
			d := (s*7 + m) % domains
			if !seen[d] {
				seen[d] = true
				fmt.Fprintf(&imp, "\t\"example.com/tmp/d%d\"\n", d)
			}
			fmt.Fprintf(&use, "\tsink(db.Query[d%d.E%d]().Where(d%d.E%ds.ID.EQ(1)).Limit(1).SQL())\n", d, m, d, m)
		}
		files[filepath.Join(fmt.Sprintf("s%d", s), "main.go")] = fmt.Sprintf(
			"package main\n\nimport (\n\t\"fmt\"\n\n\t\"github.com/syssam/velox2\"\n%s)\n\nfunc sink(s string, _ []any) { fmt.Println(len(s)) }\n\nfunc main() {\n\tdb := velox.Open(velox.Postgres, nil)\n%s}\n",
			imp.String(), use.String())
	}
	return files
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

// check compares r with the thresholds recorded for n entities.
func check(r report, n int, path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return []string{err.Error()}
	}
	// A key starting with _ inside an n object is a comment. A top-level _ key
	// is not supported: its value is not an object, and parsing fails.
	var all map[string]map[string]any
	if err := json.Unmarshal(b, &all); err != nil {
		return []string{err.Error()}
	}
	th, ok := all[strconv.Itoa(n)]
	if !ok {
		return []string{fmt.Sprintf("no thresholds for n=%d in %s", n, path)}
	}
	got := map[string]float64{
		"cold_build_s": r.ColdBuildS, "leaf_rebuild_s": r.LeafRebuildS, "root_rebuild_s": r.RootRebuildS,
		"service_binary_mb": r.ServiceBinaryMB, "link_peak_mb": r.LinkPeakMB,
	}
	var problems []string
	for k, raw := range th {
		if strings.HasPrefix(k, "_") {
			continue // a comment
		}
		max, ok := raw.(float64)
		if !ok {
			problems = append(problems, fmt.Sprintf("threshold %s is not a number", k))
			continue
		}
		v, ok := got[k]
		if !ok {
			problems = append(problems, "unknown measurement "+k)
			continue
		}
		if v > max {
			problems = append(problems, fmt.Sprintf("%s = %.2f, over %.2f", k, v, max))
		}
	}
	return problems
}
