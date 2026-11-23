package integration

import (
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/internal/testmodel"
)

// TestPostgresInIsOneArrayParameter: on PostgreSQL an In list is
// = ANY($1), one array parameter, so the generic plan that a cached
// statement takes after five runs reads it as one array. An IN list of
// parameters is ANY (ARRAY[$1, $2, …]) in that plan, which PostgreSQL tests
// parameter by parameter on every row (decision 0120).
func TestPostgresInIsOneArrayParameter(t *testing.T) {
	for _, tg := range targets(t, nil) {
		if tg.d != velox.Postgres {
			continue
		}
		t.Run(tg.name, func(t *testing.T) {
			setup(t, tg)
			keys := make([]int64, 100)
			for i := range keys {
				keys[i] = int64(i + 1)
			}
			sql, args, err := tg.db.Query[testmodel.Item]().Where(testmodel.Items.InvoiceID.In(keys...)).Limit(10).SQL(ctx)
			if err != nil {
				t.Fatal(err)
			}
			lit, ok := args[0].(string)
			if len(args) != 1 || !ok || strings.ContainsRune(lit, '\'') {
				t.Fatalf("%s: arguments %v, want one array literal", sql, args)
			}
			conn, err := tg.direct.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close() //nolint:errcheck // closing a test connection
			for _, q := range []string{"SET plan_cache_mode = force_generic_plan", "PREPARE vx_in AS " + sql} {
				if _, err := conn.ExecContext(ctx, q); err != nil {
					t.Fatal(q, err)
				}
			}
			defer conn.ExecContext(ctx, "DEALLOCATE vx_in; RESET plan_cache_mode") //nolint:errcheck // cleanup of a closing connection
			var plan string
			// EXPLAIN EXECUTE takes no parameter of its own: the literal is
			// written in, and the plan is still the prepared statement's.
			if err := conn.QueryRowContext(ctx, "EXPLAIN (FORMAT JSON) EXECUTE vx_in('"+lit+"')").Scan(&plan); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(plan, "= ANY ($1)") || strings.Contains(plan, "ARRAY[$1, $2") {
				t.Errorf("the generic plan of %s does not read one array parameter:\n%s", sql, plan)
			}

			// The parameter has no cast: PostgreSQL types it as an array of
			// the column's type, so a text literal compares with a varchar,
			// a uuid and an enum column as an IN list's strings do.
			for _, q := range []string{
				// The type is kept: pgx caches a type's OID per connection, and
				// the targets share connections.
				"DROP TABLE IF EXISTS vx_any",
				"DO $$ BEGIN CREATE TYPE vx_mood AS ENUM ('sad', 'ok'); EXCEPTION WHEN duplicate_object THEN NULL; END $$",
				"CREATE TABLE vx_any (v varchar(8), u uuid, m vx_mood)",
				"INSERT INTO vx_any VALUES ('a', '123e4567-e89b-42d3-a456-426614174000', 'ok'), ('b', NULL, 'sad')",
			} {
				if _, err := conn.ExecContext(ctx, q); err != nil {
					t.Fatal(q, err)
				}
			}
			defer conn.ExecContext(ctx, "DROP TABLE vx_any") //nolint:errcheck // cleanup
			for q, arg := range map[string]string{
				`SELECT count(*) FROM vx_any WHERE v = ANY($1)`:  `{"a","c"}`,
				`SELECT count(*) FROM vx_any WHERE u = ANY($1)`:  `{"123e4567-e89b-42d3-a456-426614174000"}`,
				`SELECT count(*) FROM vx_any WHERE m = ANY($1)`:  `{"ok"}`,
				`SELECT count(*) FROM vx_any WHERE v <> ALL($1)`: `{"b"}`,
			} {
				var n int
				if err := conn.QueryRowContext(ctx, q, arg).Scan(&n); err != nil || n != 1 {
					t.Errorf("%s with %s: %d rows, %v; want 1", q, arg, n, err)
				}
			}
		})
	}
}

// TestInArrayMatchesAnInList: In and NotIn give the rows an IN list gives,
// on every dialect, for strings that an array literal quotes, for the
// largest and smallest int64, and for a NULL among the values: IN never
// matches it, and NOT IN with one matches no row.
func TestInArrayMatchesAnInList(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		specials := []string{`a"b`, `c\d`, "e,f", "{g}", " h ", "NULL", "null", "", "x y", "'q'", `\"`, "é"}
		for i, n := range specials {
			tg.exec(t, "INSERT INTO invoices (created_at, updated_at, id, org_id, number, total, paid, note, data) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
				t0, t0, int64(100+i), int64(3), n, 0.0, false, "", []byte("d"))
		}
		for i, id := range []int64{math.MaxInt64, math.MinInt64} {
			tg.exec(t, "INSERT INTO invoices (created_at, updated_at, id, org_id, number, total, paid, note, data) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
				t0, t0, id, int64(4), string(rune('a'+i)), 0.0, false, "", []byte("d"))
		}
		invoices := func(ps ...velox.Pred[testmodel.Invoice]) []int64 {
			t.Helper()
			got, err := tg.db.Query[testmodel.Invoice]().Where(ps...).OrderBy(I.ID.Asc()).AllowUnbounded().All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			return ids(got)
		}
		org3 := I.OrgID.EQ(3)
		for _, c := range []struct {
			name string
			got  []int64
			want []int64
		}{
			{"in strings", invoices(org3, I.Number.In(`a"b`, `c\d`, "e,f", "{g}", " h ", "NULL", "", `\"`)), []int64{100, 101, 102, 103, 104, 105, 107, 110}},
			{"not in strings", invoices(org3, I.Number.NotIn(`a"b`, `c\d`, "e,f", "{g}", " h ", "NULL", "", `\"`)), []int64{106, 108, 109, 111}},
			{"in no match", invoices(org3, I.Number.In("a", "NULL ", "{}")), []int64{}},
			{"in int64 bounds", invoices(I.ID.In(math.MaxInt64, math.MinInt64, 2)), []int64{math.MinInt64, 2, math.MaxInt64}},
			{"not in int64 bounds", invoices(I.OrgID.EQ(4), I.ID.NotIn(math.MaxInt64)), []int64{math.MinInt64}},
		} {
			if !slices.Equal(c.got, c.want) {
				t.Errorf("%s: %v, want %v", c.name, c.got, c.want)
			}
		}

		// Tickets' parent_id: 1 and 3 and 6 are NULL, 2 is 1, 4 and 5 are 3,
		// 7 and 8 are 6. A nil among the values reaches the list only through
		// the engine: the handles and FieldIn refuse it.
		col := testmodel.Tickets.ParentID.I
		in := func(op int, vs ...any) velox.Pred[testmodel.Ticket] {
			o := engine.OpIn
			if op != 0 {
				o = engine.OpNotIn
			}
			return velox.Pred[testmodel.Ticket](engine.NewPredicate(engine.Node{Op: o, Col: col, V: vs}))
		}
		tickets := func(p velox.Pred[testmodel.Ticket]) []int64 {
			t.Helper()
			got, err := tg.db.Query[testmodel.Ticket]().Where(p).OrderBy(testmodel.Tickets.ID.Asc()).AllowUnbounded().All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			out := []int64{}
			for _, x := range got {
				out = append(out, x.ID)
			}
			return out
		}
		for _, c := range []struct {
			name string
			p    velox.Pred[testmodel.Ticket]
			want []int64
		}{
			{"in with NULL", in(0, int64(3), nil), []int64{4, 5}},
			{"in NULL alone", in(0, nil), []int64{}},
			{"not in", in(1, int64(3)), []int64{2, 7, 8}},
			{"not in with NULL", in(1, int64(3), nil), []int64{}},
		} {
			if got := tickets(c.p); !slices.Equal(got, c.want) {
				t.Errorf("tickets %s: %v, want %v", c.name, got, c.want)
			}
		}
	})
}
