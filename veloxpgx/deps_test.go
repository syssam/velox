package veloxpgx_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestCoreLinksNoDriver pins why this package exists: an application that uses
// velox without it links no database driver. velox, the generated code's only
// import of this module, reads the drivers' errors by their type names instead.
func TestCoreLinksNoDriver(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	out, err := exec.Command("go", "list", "-deps", "github.com/syssam/velox2").CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for _, p := range strings.Fields(string(out)) {
		for _, driver := range []string{"github.com/jackc/", "github.com/go-sql-driver/", "modernc.org/"} {
			if strings.HasPrefix(p, driver) {
				t.Errorf("velox links %s", p)
			}
		}
	}
}
