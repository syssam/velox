package ddl

import (
	"strings"
	"testing"

	"github.com/syssam/velox"
)

// Symbol caps a long name as veloxe's Atlas did, with the 128-bit FNV-1
// hash. The wanted names were computed apart from Go, with FNV-1 128 written
// out in Python, for two of a veloxe application's index names.
func TestSymbolVeloxe(t *testing.T) {
	for name, want := range map[string]string{
		"automation_execution_automationexecution_workspace_id_status_resume_at": "automation_execution_automatio_009179eeee5db713197c831c9f95cfea",
		"marketplace_installation_access_grant_ix_grant_previous_refresh_hash":   "marketplace_installation_acces_1397193d2fcff10b4aa4048cf8724597",
	} {
		if got := Symbol(velox.Postgres, name); got != want {
			t.Errorf("%s:\n got %s\nwant %s", name, got, want)
		}
	}
	short := strings.Repeat("a", 63)
	if Symbol(velox.Postgres, short) != short || Symbol(velox.MySQL, short+"a") != short+"a" {
		t.Error("a name within the limit is kept")
	}
}
