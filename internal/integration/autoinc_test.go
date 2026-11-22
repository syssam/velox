package integration

import (
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox/internal/dbenv"
	"github.com/syssam/velox/internal/testmodel"
)

// TestMySQLBatchKeysIncrement: on a MySQL session whose auto_increment_increment
// is 2 (the DSN parameter sets it on each connection), an InsertAll's rows take
// the keys MySQL stored, 2 apart. Read as consecutive, the second row took the
// first row's key plus one, a key no row has.
func TestMySQLBatchKeysIncrement(t *testing.T) {
	dsn := dbenv.Need(t, "VELOX_TEST_MYSQL")
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	tg := lifted([]target{mysqlTarget(t, dsn+sep+"auto_increment_increment=2", nil)})[0]
	setup(t, tg)
	xs := []testmodel.Invoice{newInvoice(9, "S-1"), newInvoice(9, "S-2"), newInvoice(9, "S-3")}
	if err := tg.db.InsertAll(xs).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := tg.db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(9)).OrderBy(I.ID.Asc()).AllowUnbounded().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(xs), ids(stored); !slices.Equal(got, want) {
		t.Errorf("keys read back %v, stored %v", got, want)
	}
	if len(stored) == 3 && stored[1].ID-stored[0].ID != 2 {
		t.Errorf("the session's step did not apply: %v", ids(stored))
	}
}
