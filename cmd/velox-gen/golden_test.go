package main

import (
	"bytes"
	"flag"
	"os"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "write internal/testmodel's generated files")

// TestTestmodelIsReproduced is the golden test: generating internal/testmodel
// gives exactly its committed *_velox.go files. invoice_velox.go was written
// by hand in plan 1, before velox-gen existed.
func TestTestmodelIsReproduced(t *testing.T) {
	cfg := config{dir: root}
	ps, err := load(cfg, []string{"./internal/testmodel"})
	if err != nil {
		t.Fatal(err)
	}
	res, probs := plan(ps, cfg)
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	if len(res.write) != 3 || len(res.remove) != 0 {
		t.Errorf("writes %d files and removes %v; want 3 (invoice, line, sample) and none", len(res.write), res.remove)
	}
	for path, want := range res.write {
		if *update {
			if err := os.WriteFile(path, want, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s differs from what velox-gen writes (err %v); velox-gen writes:\n%s", path, err, want)
		}
	}
}

func generate(t *testing.T, fixture string) (string, string) {
	t.Helper()
	cfg := config{dir: root}
	ps := loadFixture(t, cfg, "./internal/testmodel/testdata/"+fixture)
	res, probs := plan(ps, cfg)
	var msgs []string
	for _, p := range probs {
		msgs = append(msgs, p.msg)
	}
	var src []byte
	for _, s := range res.write {
		src = s
	}
	return string(src), strings.Join(msgs, "\n")
}

func TestHandleNames(t *testing.T) {
	if _, probs := generate(t, "collision"); !strings.Contains(probs, "Order: its handles variable Orders is already declared at") {
		t.Errorf("collision: %q", probs)
	}
	src, probs := generate(t, "directive")
	if probs != "" || !strings.Contains(src, "var Bills = struct {") {
		t.Errorf("directive: problems %q, source:\n%s", probs, src)
	}
}

// TestImportNamesAvoidThePackagesIdentifiers is Review Focus 3.
func TestImportNamesAvoidThePackagesIdentifiers(t *testing.T) {
	src, probs := generate(t, "aliases")
	if probs != "" {
		t.Fatal(probs)
	}
	for _, want := range []string{"\ttime2 \"time\"\n", "At velox.OrderedCol[Event, time2.Time]"} {
		if !strings.Contains(src, want) {
			t.Errorf("source lacks %q:\n%s", want, src)
		}
	}
}
