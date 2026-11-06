package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
)

// shared is a run of the harness at n=60 with two services, plain or with
// GraphQL, made once for the tests that read it: a run builds the
// application and a reference module cold, each with a GOCACHE of its own,
// so it is the slow part of every test here.
type shared struct {
	once sync.Once
	dir  string
	r    report
	err  error
}

var sharedRuns [2]shared // plain, GraphQL

// sharedRun is the shared run of the variant, made on first use, and the
// directory it wrote; TestMain removes it.
func sharedRun(t *testing.T, graphql bool) (report, string) {
	t.Helper()
	s := &sharedRuns[0]
	if graphql {
		s = &sharedRuns[1]
	}
	s.once.Do(func() {
		if s.dir, s.err = os.MkdirTemp("", "velox-scale-test-"); s.err != nil {
			return
		}
		s.r, s.err = run(config{n: 60, services: 2, graphql: graphql, dir: s.dir})
	})
	if s.err != nil {
		t.Fatal(s.err)
	}
	return s.r, s.dir
}

func TestMain(m *testing.M) {
	code := m.Run()
	for i := range sharedRuns {
		if dir := sharedRuns[i].dir; dir != "" {
			_ = os.RemoveAll(dir)
		}
	}
	os.Exit(code)
}

// TestScaleHarnessSmall is Review Focus 5: the harness generates, builds and
// measures a small application, and -check fails when a threshold is exceeded.
func TestScaleHarnessSmall(t *testing.T) {
	t.Parallel() // the variants build in their own directories and GOCACHE
	if testing.Short() {
		t.Skip("builds an application; run without -short")
	}
	r, _ := sharedRun(t, false)
	measured(t, r)
	th := filepath.Join(t.TempDir(), "th.json")
	tight := map[string]map[string]any{"20": {"cold_build_x": r.ColdBuildX / 1000}}
	b, _ := json.Marshal(tight)
	if err := os.WriteFile(th, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if problems := check(r, "20", th); len(problems) == 0 {
		t.Error("check passed a measurement 1000 times over its threshold")
	}
}

// measured fails t unless r holds every measurement: the wall and CPU times,
// the reference's CPU time, the ratios, the binary and the link peak.
func measured(t *testing.T, r report) {
	t.Helper()
	if r.ColdBuildS <= 0 || r.SchemaEditRebuildS <= 0 || r.ServiceBinaryMB <= 0 || r.LinkPeakMB <= 0 ||
		r.RefCPUS <= 0 || r.ColdBuildCPUS <= 0 || r.SchemaEditRebuildCPUS <= 0 || r.ColdBuildX <= 0 || r.SchemaEditRebuildX <= 0 {
		t.Fatalf("a measurement is missing: %+v", r)
	}
	if want := r.ColdBuildCPUS / r.RefCPUS; r.ColdBuildX != want {
		t.Errorf("cold_build_x = %v, want cold CPU / reference CPU = %v", r.ColdBuildX, want)
	}
}

// TestServiceLinksOnlyItsEntities guards the link graph: a service that uses
// five entities links those and the targets of their relations, one hop, and
// no other entity of the schema. A relation that named its target's metadata
// in the owner's Descriptor would link the whole connected model (the service's
// binary grew with n until the use site named the target). The threshold in
// thresholds.json catches the size; this names the entities.
func TestServiceLinksOnlyItsEntities(t *testing.T) {
	t.Parallel() // the variants build in their own directories and GOCACHE
	if testing.Short() {
		t.Skip("builds an application; run without -short")
	}
	linksOnlyItsEntities(t, false)
}

// TestGraphQLServiceLinksOnlyItsEntities is the same guard for the GraphQL
// variant: the generated GraphQL code (the connections, edges, where inputs and
// order terms, named E<k>…, e<k>…) of an entity is linked by the service that
// calls it or that reaches it by one relation, and not otherwise. A GraphQL
// type that named every neighbour's code would link the whole connected model.
func TestGraphQLServiceLinksOnlyItsEntities(t *testing.T) {
	t.Parallel() // the variants build in their own directories and GOCACHE
	if testing.Short() {
		t.Skip("builds an application; run without -short")
	}
	linksOnlyItsEntities(t, true)
}

func linksOnlyItsEntities(t *testing.T, graphql bool) {
	const n, s = 60, 2 // sharedRun's
	_, dir := sharedRun(t, graphql)
	schemas, services := generate(n, s, graphql)
	src := services[filepath.Join("s0", "main.go")]
	entity := regexp.MustCompile(`gen\.E(\d+)\b`)
	ref := regexp.MustCompile(`\bE(\d+)\.Type\b`)
	used, allowed := map[string]bool{}, map[string]bool{}
	for _, m := range entity.FindAllStringSubmatch(src, -1) {
		used[m[1]] = true
		allowed[m[1]] = true
		// The targets of the used entity's relations: one hop.
		for _, r := range ref.FindAllStringSubmatch(schemas[filepath.Join("schema", "e"+m[1]+".go")], -1) {
			allowed[r[1]] = true
		}
	}
	if len(used) != 5 {
		t.Fatalf("s0 uses %d entities, want 5: %v", len(used), used)
	}
	out, err := exec.Command("go", "tool", "nm", filepath.Join(dir, "bin", "s0")).Output()
	if err != nil {
		t.Fatal(err)
	}
	// The symbols the gen package itself defines for entity k: its methods,
	// handles, metadata and Descriptor function, E<k>.*, (*E<k>).*, E<k>s, e<k>Meta
	// and e<k>Descriptor. The type descriptors and generic instantiations that
	// mention E<k> (the field Many[E<k>] of a neighbour's struct) are data
	// about a type and do not link its code, so they are not counted.
	sym := regexp.MustCompile(`(?m)^\s*\S+ [A-Za-z] example\.com/tmp/gen\.\(?\*?[Ee](\d+)(?:s|Meta|Descriptor)?(?:\.|$)`)
	if graphql {
		// Also the GraphQL code: E<k>sConnection, (*E<k>).Next,
		// (*E<k>WhereInput).Pred, e<k>TermGQL and the like.
		sym = regexp.MustCompile(`(?m)^\s*\S+ [A-Za-z] example\.com/tmp/gen\.\(?\*?[Ee](\d+)[A-Za-z]*\)?(?:\.|$)`)
	}
	present := map[string]bool{}
	for _, m := range sym.FindAllStringSubmatch(string(out), -1) {
		present[m[1]] = true
	}
	for k := range used {
		if !present[k] {
			t.Errorf("entity E%s, which s0 uses, has no symbol: the scan is broken", k)
		}
	}
	for k := range present {
		if !allowed[k] {
			t.Errorf("s0 links entity E%s, which it neither uses nor reaches by one relation from a used entity", k)
		}
	}
	t.Logf("%d of %d entities have symbols in s0: %d used, %d allowed", len(present), n, len(used), len(allowed))
}

// TestScaleHarnessSmallGraphQL: the GraphQL variant generates, builds and
// measures a small application, and its schemas and services carry what the
// variant promises.
func TestScaleHarnessSmallGraphQL(t *testing.T) {
	t.Parallel() // the variants build in their own directories and GOCACHE
	if testing.Short() {
		t.Skip("builds an application; run without -short")
	}
	r, _ := sharedRun(t, true)
	measured(t, r)
	schemas, services := generate(20, 2, true)
	for _, want := range []string{`graphql.QueryField("e7s")`, `graphql.OrderField("NAME"), graphql.WhereOps()`} {
		if src := schemas[filepath.Join("schema", "e7.go")]; !strings.Contains(src, want) {
			t.Errorf("e7 lacks %s", want)
		}
	}
	src := services[filepath.Join("s0", "main.go")]
	if got := strings.Count(src, "Connection(ctx"); got != 5 {
		t.Errorf("s0 calls %d root connection functions, want 5", got)
	}
	if got := strings.Count(src, ".Next(ctx"); got != 5 {
		t.Errorf("s0 calls %d edge methods, want 5", got)
	}
	if plain, _ := generate(20, 2, false); strings.Contains(plain[filepath.Join("schema", "e7.go")], "graphql") {
		t.Error("the plain variant mentions graphql")
	}
}

// TestCheckPassesAGenerousThreshold: check reports nothing when every
// measurement is under its threshold, and ignores comment keys.
func TestCheckPassesAGenerousThreshold(t *testing.T) {
	r := report{N: 20, ColdBuildX: 10, SchemaEditRebuildX: 5, ServiceBinaryMB: 5, LinkPeakMB: 100}
	generous := map[string]map[string]any{"20": {
		"_comment": "ignored", "cold_build_x": 20.0, "schema_edit_rebuild_x": 10.0,
		"service_binary_mb": 10.0, "link_peak_mb": 200.0,
	}}
	th := filepath.Join(t.TempDir(), "th.json")
	b, _ := json.Marshal(generous)
	if err := os.WriteFile(th, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if problems := check(r, "20", th); len(problems) != 0 {
		t.Errorf("a generous threshold produced problems: %v", problems)
	}
	if problems := check(r, "30", th); len(problems) != 1 || !strings.Contains(problems[0], "no thresholds for n=30") {
		t.Errorf("an n with no thresholds: %v", problems)
	}
}

// TestCheckRefusesAMissingOrUnknownKey: the file and the code agree on the keys.
func TestCheckRefusesAMissingOrUnknownKey(t *testing.T) {
	r := report{N: 20}
	write := func(th map[string]any) string {
		p := filepath.Join(t.TempDir(), "th.json")
		b, _ := json.Marshal(map[string]map[string]any{"20": th})
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	all := map[string]any{"cold_build_x": 1.0, "schema_edit_rebuild_x": 1.0, "service_binary_mb": 1.0, "link_peak_mb": 1.0}
	if problems := check(r, "20", write(all)); len(problems) != 0 {
		t.Errorf("a complete file: %v", problems)
	}
	missing := maps.Clone(all)
	delete(missing, "link_peak_mb")
	if problems := check(r, "20", write(missing)); len(problems) != 1 || !strings.Contains(problems[0], "missing threshold link_peak_mb") {
		t.Errorf("a missing key: %v", problems)
	}
	// The wall-clock seconds are printed, not checked: a threshold on them is unknown.
	for _, k := range []string{"leaf_rebuild_s", "cold_build_s", "schema_edit_rebuild_s"} {
		unknown := maps.Clone(all)
		unknown[k] = 1.0
		if problems := check(r, "20", write(unknown)); len(problems) != 1 || !strings.Contains(problems[0], "unknown measurement "+k) {
			t.Errorf("an unknown key %s: %v", k, problems)
		}
	}
}

// TestGenerateShape: n schemas and s services, and every inverse edge has the
// To edge it names on its target, which Build requires.
func TestGenerateShape(t *testing.T) {
	const n, s = 60, 7
	schemas, services := generate(n, s, false)
	if len(schemas) != n || len(services) != s {
		t.Fatalf("got %d schemas and %d services, want %d and %d", len(schemas), len(services), n, s)
	}
	from := regexp.MustCompile(`edge\.From\("\w+", E(\d+)\.Type\)\.Ref\("(\w+)"\)`)
	inverses := 0
	for i := range n {
		src := schemas[filepath.Join("schema", fmt.Sprintf("e%d.go", i))]
		for _, m := range from.FindAllStringSubmatch(src, -1) {
			inverses++
			target := schemas[filepath.Join("schema", fmt.Sprintf("e%s.go", m[1]))]
			if want := fmt.Sprintf(`edge.To(%q, E%d.Type)`, m[2], i); !strings.Contains(target, want) {
				t.Errorf("e%d: no %s on e%s", i, want, m[1])
			}
		}
	}
	// Every schema but the first of its group belongs to the previous one, and
	// every group head but the first belongs to another head.
	if want := (n - n/10) + (n/10 - 1); inverses != want {
		t.Errorf("%d inverse edges, want %d", inverses, want)
	}
}

// TestCommittedThresholds reads the real thresholds.json (no build, so it runs
// under -short too): it must parse, and every key that is not a comment must
// name a measurement that check knows.
func TestCommittedThresholds(t *testing.T) {
	b, err := os.ReadFile("thresholds.json")
	if err != nil {
		t.Fatal(err)
	}
	var all map[string]map[string]any
	if err := json.Unmarshal(b, &all); err != nil {
		t.Fatalf("thresholds.json does not parse: %v", err)
	}
	for _, key := range []string{"500", "500-graphql"} {
		if _, ok := all[key]; !ok {
			t.Errorf("no thresholds for %s, which CI checks", key)
		}
	}
	known := []string{"cold_build_x", "schema_edit_rebuild_x", "service_binary_mb", "link_peak_mb"}
	for n, th := range all {
		for k, v := range th {
			if strings.HasPrefix(k, "_") {
				continue
			}
			if !slices.Contains(known, k) {
				t.Errorf("n=%s: %q is not a measurement", n, k)
			}
			if _, ok := v.(float64); !ok {
				t.Errorf("n=%s: %s is not a number: %v", n, k, v)
			}
		}
	}
	// A run that meets every recorded threshold exactly passes check.
	for _, key := range []string{"500", "500-graphql"} {
		checkAtThresholds(t, all[key], key)
	}
}

// checkAtThresholds: a run exactly at th, the thresholds under key, passes.
func checkAtThresholds(t *testing.T, th map[string]any, key string) {
	t.Helper()
	r := report{N: 500}
	for k, v := range th {
		f, _ := v.(float64)
		switch k {
		case "cold_build_x":
			r.ColdBuildX = f
		case "schema_edit_rebuild_x":
			r.SchemaEditRebuildX = f
		case "service_binary_mb":
			r.ServiceBinaryMB = f
		case "link_peak_mb":
			r.LinkPeakMB = f
		}
	}
	if problems := check(r, key, "thresholds.json"); len(problems) != 0 {
		t.Errorf("%s: a run exactly at the thresholds: %v", key, problems)
	}
}

// TestRunRefusesAnUsedDirectory: a reused directory holds a warm build cache,
// which would make the cold build not cold.
func TestRunRefusesAnUsedDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "leftover"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(config{n: 20, services: 4, dir: dir}); err == nil || !strings.Contains(err.Error(), "is not empty") {
		t.Fatalf("got %v, want a refusal", err)
	}
}
