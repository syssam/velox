package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func noVerifyDirs(t *testing.T, dir string) {
	t.Helper()
	left, _ := filepath.Glob(filepath.Join(dir, ".velox-verify-*"))
	if len(left) > 0 {
		t.Errorf("verify left %v behind", left)
	}
}

func TestVerifyPasses(t *testing.T) {
	dir := tempModule(t, map[string]string{"billing/invoice.go": invoiceSrc})
	velox(t, dir, "./...")
	code, out, errs := velox(t, dir, "verify", "./...")
	if code != 0 || !strings.Contains(out, "velox-gen verify: 1 entities, 0 with errors") {
		t.Errorf("%d %q %q", code, out, errs)
	}
	noVerifyDirs(t, dir)
}

func TestVerifyReportsSchemaMistakes(t *testing.T) {
	src := strings.Replace(invoiceSrc, `{ s.Table("invoices") }`, `{ s.Tenant(x.ID) }`, 1)
	dir := tempModule(t, map[string]string{"billing/invoice.go": src})
	if code, _, errs := velox(t, dir, "./..."); code != 0 {
		t.Fatalf("generate: %q", errs)
	}
	code, _, errs := velox(t, dir, "verify", "./...")
	if code != 1 || !strings.Contains(errs, "expected a pointer to a field") {
		t.Errorf("%d %q", code, errs)
	}
}

func TestVerifyReportsStaleCode(t *testing.T) {
	dir := tempModule(t, map[string]string{"billing/invoice.go": invoiceSrc})
	velox(t, dir, "./...")
	writeFile(t, filepath.Join(dir, "billing/invoice.go"), strings.Replace(invoiceSrc, "Number    string", "Number    string\n\tNote      string", 1))
	code, _, errs := velox(t, dir, "verify", "./...")
	if code != 1 || !strings.Contains(errs, "generated code is out of date") {
		t.Errorf("%d %q", code, errs)
	}
}

// TestVerifySkipsEntitiesItCannotName is Review Focus 4.
func TestVerifySkipsEntitiesItCannotName(t *testing.T) {
	note := "package billing\n\nimport \"github.com/syssam/velox2\"\n\ntype note struct{ ID int64 }\n\nfunc (x *note) Schema(s *velox.Schema) {}\n"
	dir := tempModule(t, map[string]string{"billing/invoice.go": invoiceSrc, "billing/note.go": note})
	velox(t, dir, "./...")
	code, out, errs := velox(t, dir, "verify", "./...")
	if code != 0 || !strings.Contains(errs, "skipping billing.note") || !strings.Contains(out, "1 entities, 0 with errors") {
		t.Errorf("%d %q %q", code, out, errs)
	}
}

// TestVerifyLeavesNothingBehind is Review Focus 5: the temporary program is
// removed when the entities report errors and when it does not compile.
func TestVerifyLeavesNothingBehind(t *testing.T) {
	src := strings.Replace(invoiceSrc, `{ s.Table("invoices") }`, `{ s.Tenant(x.ID) }`, 1)
	dir := tempModule(t, map[string]string{"billing/invoice.go": src})
	velox(t, dir, "./...")
	if code, _, errs := velox(t, dir, "verify", "./..."); code != 1 || !strings.Contains(errs, "expected a pointer to a field") {
		t.Errorf("verify of a bad Schema: %d %q", code, errs)
	}
	noVerifyDirs(t, dir)
	if err := os.Remove(filepath.Join(dir, "billing/invoice_velox.go")); err != nil { // VeloxMeta gone: the program cannot compile
		t.Fatal(err)
	}
	if code, _, errs := velox(t, dir, "verify", "./..."); code != 1 || !strings.Contains(errs, "VeloxMeta") {
		t.Errorf("verify of an entity without its generated file: %d %q, want 1 and a compile error naming VeloxMeta", code, errs)
	}
	noVerifyDirs(t, dir)
}
