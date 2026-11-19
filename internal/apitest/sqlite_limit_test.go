package apitest

import (
	"context"
	"strings"
	"testing"

	"github.com/syssam/velox/internal/testmodel"
)

// TestSQLiteLimitLiteral: on SQLite a query's LIMIT is in its text, not a
// parameter, so each limit is a statement of its own and reads its own rows
// through the render cache; OFFSET stays a parameter.
func TestSQLiteLimitLiteral(t *testing.T) {
	ctx := context.Background()
	db := connectInvoices(t, ":memory:")
	for _, n := range []int{2, 1, 3, 2} {
		q := db.Query[testmodel.Invoice]().OrderBy(I.ID.Asc()).Limit(n)
		sql, args, err := q.SQL(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if want := " LIMIT " + string(rune('0'+n)); !strings.HasSuffix(sql, want) || len(args) != 0 {
			t.Errorf("Limit(%d): %s %v, want it to end %q with no arguments", n, sql, args, want)
		}
		rows, err := q.All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != n {
			t.Errorf("Limit(%d) read %d rows", n, len(rows))
		}
	}
	rows, err := db.Query[testmodel.Invoice]().OrderBy(I.ID.Asc()).Limit(1).Offset(2).All(ctx)
	if err != nil || len(rows) != 1 || rows[0].ID != 3 {
		t.Errorf("Limit(1).Offset(2): %v %v, want invoice 3", rows, err)
	}

}
