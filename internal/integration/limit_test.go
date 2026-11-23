package integration

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// TestPostgresLimitLiteral: a query's LIMIT is in its text on PostgreSQL, so
// the generic plan that a cached statement takes after five runs sees the
// row count. A LIMIT parameter makes the planner guess a tenth of the table,
// which can pick a slower join (bench/compare's WhereHasEdge did: 30%
// slower than with a literal).
func TestPostgresLimitLiteral(t *testing.T) {
	for _, tg := range targets(t, nil) {
		if tg.d != velox.Postgres {
			continue
		}
		t.Run(tg.name, func(t *testing.T) {
			setup(t, tg)
			tg.exec(t, "INSERT INTO invoices (created_at, updated_at, id, org_id, number, total, paid, note, data) "+
				"SELECT now(), now(), i, 1, 'G-' || i, i, false, '', 'd' FROM generate_series(100, 299) AS i")
			tg.exec(t, "ANALYZE invoices")
			sql, args, err := tg.db.Query[testmodel.Invoice]().OrderBy(I.Total.Asc()).Limit(2).SQL(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(sql, " LIMIT 2") || len(args) != 0 {
				t.Errorf("Limit(2): %s %v, want it to end LIMIT 2 with no arguments", sql, args)
			}
			conn, err := tg.direct.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close() //nolint:errcheck // closing a test connection
			for _, q := range []string{"SET plan_cache_mode = force_generic_plan", "PREPARE vx_limit AS " + sql} {
				if _, err := conn.ExecContext(ctx, q); err != nil {
					t.Fatal(q, err)
				}
			}
			defer conn.ExecContext(ctx, "DEALLOCATE vx_limit; RESET plan_cache_mode") //nolint:errcheck // cleanup of a closing connection
			exec := "EXPLAIN (FORMAT JSON) EXECUTE vx_limit"
			if len(args) > 0 { // a LIMIT parameter, as before
				ps := make([]string, len(args))
				for i := range ps {
					ps[i] = fmt.Sprintf("$%d", i+1)
				}
				exec += "(" + strings.Join(ps, ", ") + ")"
			}
			var plan string
			if err := conn.QueryRowContext(ctx, exec, args...).Scan(&plan); err != nil {
				t.Fatal(err)
			}
			var p []struct {
				Plan struct {
					NodeType string  `json:"Node Type"`
					Rows     float64 `json:"Plan Rows"`
				}
			}
			if err := json.Unmarshal([]byte(plan), &p); err != nil || len(p) != 1 {
				t.Fatalf("plan %s: %v", plan, err)
			}
			if p[0].Plan.NodeType != "Limit" || p[0].Plan.Rows != 2 {
				t.Errorf("the generic plan's top node is %s of %v rows, want Limit of 2:\n%s", p[0].Plan.NodeType, p[0].Plan.Rows, plan)
			}
		})
	}
}
