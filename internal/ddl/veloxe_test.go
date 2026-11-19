package ddl

import (
	"strings"
	"testing"

	"github.com/syssam/velox"
)

// VeloxeSymbol caps a long name as veloxe's Atlas did, with the 128-bit FNV-1
// hash. The wanted names were computed apart from Go, with FNV-1 128 written
// out in Python, for two of a veloxe application's index names.
func TestVeloxeSymbol(t *testing.T) {
	for name, want := range map[string]string{
		"automation_execution_automationexecution_workspace_id_status_resume_at": "automation_execution_automatio_009179eeee5db713197c831c9f95cfea",
		"marketplace_installation_access_grant_ix_grant_previous_refresh_hash":   "marketplace_installation_acces_1397193d2fcff10b4aa4048cf8724597",
	} {
		if got := VeloxeSymbol(velox.Postgres, name); got != want {
			t.Errorf("%s:\n got %s\nwant %s", name, got, want)
		}
		if got := SymbolFor("veloxe", velox.Postgres, name); got != want {
			t.Errorf("SymbolFor veloxe %s: %s", name, got)
		}
		if got := SymbolFor("", velox.Postgres, name); got != Symbol(velox.Postgres, name) {
			t.Errorf("SymbolFor ent %s: %s", name, got)
		}
	}
	short := strings.Repeat("a", 63)
	if VeloxeSymbol(velox.Postgres, short) != short || VeloxeSymbol(velox.MySQL, short+"a") != short+"a" {
		t.Error("a name within the limit is kept")
	}
}
