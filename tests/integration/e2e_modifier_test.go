package integration_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect/sql"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/user"
)

// valueAsString converts a value returned by (*Entity).Value() into a string,
// tolerating dialect type differences:
//
//   - SQLite and Postgres return string-typed expressions (UPPER(x),
//     TO_CHAR(...)) as Go string or []byte depending on column metadata.
//   - MySQL's go-sql-driver returns VARCHAR/TEXT results as []byte by default
//     unless the DSN has `columnsWithAlias=true` or you cast to CHAR.
//
// Real-world callers who want a portable string from a modifier-selected
// expression either call CAST(... AS CHAR) or do this conversion
// client-side. This helper handles both shapes.
func valueAsString(t *testing.T, v any) string {
	t.Helper()
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	default:
		require.Failf(t, "unexpected string-shaped value",
			"expected string | []byte from Value(); got %T (%v)", v, v)
		return ""
	}
}

// valueAsInt64 converts a value returned by (*Entity).Value() into an int64,
// tolerating dialect type differences:
//
//   - SQLite's SUM/COUNT/INT agg return int64 directly (modernc.org/sqlite).
//   - Postgres SUM(integer) returns numeric — lib/pq scans numeric into
//     []byte (the textual digit sequence, e.g. []byte("30")) because
//     numeric can exceed int64 range. COUNT(*) is bigint → int64 directly.
//   - MySQL SUM(integer) returns decimal — go-sql-driver/mysql scans decimal
//     into []byte as well.
//
// Real-world callers either parse this themselves or ask for a bigint-typed
// aggregate via CAST(... AS BIGINT). This helper is the smaller of the two
// branches; applying it in tests lets us pin the same value across dialects.
func valueAsInt64(t *testing.T, v any) int64 {
	t.Helper()
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case []byte:
		n, err := strconv.ParseInt(string(x), 10, 64)
		require.NoError(t, err, "dialect returned numeric-as-bytes %q; parse failed", string(x))
		return n
	case string:
		n, err := strconv.ParseInt(x, 10, 64)
		require.NoError(t, err, "dialect returned numeric-as-string %q; parse failed", x)
		return n
	default:
		require.Failf(t, "unexpected aggregate scalar type",
			"expected int64 | int | []byte | string from Value(); got %T (%v)", v, v)
		return 0
	}
}

// TestModifier_AppendSelect_ValueRoundTrip pins the happy-path real-world case
// where a Modify() callback uses AppendSelect to add a computed column
// alongside the entity's default projection. The result rows must include
// both the default fields (so entity scanning succeeds) and the extra alias
// (retrievable via entity.Value(name)). Before the ordering fix in
// runtime.BuildSelectorFrom the modifier's AppendSelect was silently
// clobbered by the default projection applied afterwards.
func TestModifier_AppendSelect_ValueRoundTrip(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()

		createUser(t, client, "Alice", "alice@mod.com")
		createUser(t, client, "Bob", "bob@mod.com")

		users, err := client.User.Query().
			Where(user.IDField.GT(0)).
			Modify(func(s *sql.Selector) {
				// AppendSelect adds to the default projection rather than
				// replacing it — entity fields still scan, plus we get the
				// extra alias for Value().
				s.AppendSelectAs("UPPER("+s.C(user.FieldName)+")", "upper_name")
			}).
			All(ctx)
		require.NoError(t, err)
		require.Len(t, users, 2)

		// Entity fields still scan — the default projection survived.
		for _, u := range users {
			require.NotZero(t, u.ID, "default id column must still be selected")
			require.NotEmpty(t, u.Name, "default name column must still be selected")
			require.NotEmpty(t, u.Email, "default email column must still be selected")
		}

		// The extra alias must be retrievable via Value(). Before the
		// assignValues default-case generator fix, this map-lookup
		// always returned "value was not selected" because the scanned
		// UnknownType result was silently discarded.
		gotNames := map[string]bool{}
		for _, u := range users {
			v, err := u.Value("upper_name")
			require.NoError(t, err, "Value(\"upper_name\") must retrieve modifier-selected column")
			gotNames[valueAsString(t, v)] = true
		}
		assert.True(t, gotNames["ALICE"], "upper_name for Alice must round-trip uppercased")
		assert.True(t, gotNames["BOB"], "upper_name for Bob must round-trip uppercased")
	})
}

// TestModifier_AggregateOnlySelect_WithGroupBy mirrors the real-world
// aggregateSalesOrder pattern that exposed the ordering bug: the caller uses
// Modify() to build a pure aggregate SELECT (no entity columns) plus GROUP BY,
// then reads the aggregated values back via entity.Value(alias). This pins
// that:
//
//  1. Modifier's Select() REPLACES the default projection (ordering fix) — so
//     the emitted SQL is accepted by strict dialects (Postgres SQLSTATE 42803).
//  2. Aggregate values are stored in selectValues (generator default-case fix)
//     so entity.Value("alias") returns them.
//
// Without either fix this test fails: either the SQL is malformed (modifier
// Select clobbered by defaults → Postgres rejects; SQLite silently returns
// garbage rows) OR the scan succeeds but Value() reports "not selected".
func TestModifier_AggregateOnlySelect_WithGroupBy(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()

		mk := func(name string, role user.Role, age int) {
			_, err := client.User.Create().
				SetName(name).
				SetEmail(name + "@grp.com").
				SetAge(age).
				SetRole(role).
				SetCreatedAt(now).
				SetUpdatedAt(now).
				Save(ctx)
			require.NoError(t, err)
		}
		mk("u1", user.RoleUser, 10)
		mk("u2", user.RoleUser, 20)
		mk("a1", user.RoleAdmin, 100)

		users, err := client.User.Query().
			Modify(func(s *sql.Selector) {
				// Replace default projection with aggregate-only SELECT.
				s.Select(
					s.C(user.FieldRole),
				)
				s.AppendSelectAs("SUM("+s.C(user.FieldAge)+")", "total_age")
				s.AppendSelectAs("COUNT(*)", "head_count")
				s.GroupBy(s.C(user.FieldRole))
				s.OrderBy(sql.Asc(s.C(user.FieldRole)))
			}).
			All(ctx)
		require.NoError(t, err)
		require.Len(t, users, 2, "GROUP BY role produces one row per distinct role")

		// Because defaults were REPLACED, entity ID/Name/etc. are the
		// zero value — only role (the GROUP BY column) was selected.
		// That's expected: the caller is using .All() as a bulk-Scan
		// into the entity shape plus selectValues for the aggregates.
		byRole := map[string]*entity.User{}
		for _, u := range users {
			byRole[string(u.Role)] = u
		}

		for _, role := range []string{"user", "admin"} {
			u, ok := byRole[role]
			require.True(t, ok, "role %q must appear in grouped result", role)

			totalV, err := u.Value("total_age")
			require.NoError(t, err, "Value(\"total_age\") must retrieve the aggregate")
			count, err := u.Value("head_count")
			require.NoError(t, err, "Value(\"head_count\") must retrieve the aggregate")

			switch role {
			case "user":
				assert.Equal(t, int64(30), valueAsInt64(t, totalV), "sum of 10+20 == 30 for role=user")
				assert.Equal(t, int64(2), valueAsInt64(t, count), "2 users with role=user")
			case "admin":
				assert.Equal(t, int64(100), valueAsInt64(t, totalV), "sum == 100 for role=admin")
				assert.Equal(t, int64(1), valueAsInt64(t, count), "1 admin")
			}
		}
	})
}

// TestModifier_Where_RunsAlongsideDefaults pins that a Modify() callback that
// only adds Where predicates (no SELECT-list manipulation) composes correctly
// with the default entity-column projection. This is the most common
// real-world Modify() usage (add a predicate the Where-DSL can't express,
// e.g. raw SQL fragments) and must not regress when the ordering changes.
func TestModifier_Where_RunsAlongsideDefaults(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()

		createUser(t, client, "Alice", "alice@w.com")
		createUser(t, client, "Amy", "amy@w.com")
		createUser(t, client, "Bob", "bob@w.com")

		users, err := client.User.Query().
			Modify(func(s *sql.Selector) {
				s.Where(sql.Like(s.C(user.FieldName), "A%"))
			}).
			All(ctx)
		require.NoError(t, err)
		require.Len(t, users, 2, "Modify Where predicate must reduce 3 users to 2")
		for _, u := range users {
			assert.NotZero(t, u.ID, "default projection must survive — no clobber")
			assert.Contains(t, []string{"Alice", "Amy"}, u.Name)
		}
	})
}

// TestModifier_ValueReturnsNotSelected_WhenNoDefaultCase is a diagnostic test
// pinning the contract of entity.Value() on a plain query (no Modify): the
// method returns a structured "not selected" error, not a silent nil. If the
// generator regresses and stops emitting the default case in assignValues,
// TestModifier_AppendSelect_ValueRoundTrip fails; this test confirms Value()
// still behaves correctly on *unselected* columns (the negative case).
func TestModifier_ValueReturnsNotSelected_WhenNoDefaultCase(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	createUser(t, client, "Alice", "alice@nosel.com")

	users, err := client.User.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)

	_, err = users[0].Value("nonexistent_alias")
	require.Error(t, err, "Value on an unselected column must error, not return nil")
	assert.Contains(t, err.Error(), "not selected",
		"error message must communicate that the column wasn't in the SELECT list")
}

// TestModifier_AggregateOnlySelect_EmptyResultSet pins that a modifier-built
// aggregate query over a predicate that matches nothing returns an empty
// []*Entity without panicking or erroring. This is the "no rows matched"
// edge case that callers rely on (e.g. dashboards showing zero when a filter
// excludes everything).
func TestModifier_AggregateOnlySelect_EmptyResultSet(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()
		createUser(t, client, "Alice", "alice@empty.com")

		users, err := client.User.Query().
			Where(user.IDField.EQ(-999)). // matches nothing
			Modify(func(s *sql.Selector) {
				s.Select(s.C(user.FieldRole))
				s.AppendSelectAs("COUNT(*)", "head_count")
				s.GroupBy(s.C(user.FieldRole))
			}).
			All(ctx)
		require.NoError(t, err, "empty-result aggregate query must not error")
		assert.Empty(t, users, "no matching rows ⇒ empty slice")
	})
}

// TestModifier_Postgres_DateTruncGroupBy_RealWorld exactly mirrors the
// aggregateSalesOrder bug: a Modify() builds a GROUP BY over a date-truncation
// expression plus a SUM aggregate. Pre-fix, Postgres rejected the emitted SQL
// with SQLSTATE 42803 because the default entity-column projection was
// appended while GROUP BY/ORDER BY referenced only the truncation alias.
// This test runs Postgres-only because SQLite is lax about GROUP BY
// correctness — it would return arbitrary values for the default columns
// without erroring, hiding the bug.
func TestModifier_Postgres_DateTruncGroupBy_RealWorld(t *testing.T) {
	client, cleanup := openPostgresOrSkip(t)
	defer cleanup()
	ctx := context.Background()

	// Seed: create users across two "periods" by varying created_at.
	// Period 1: ages 10, 20 → SUM=30. Period 2: ages 30, 40 → SUM=70.
	p1 := now.AddDate(0, -1, 0)
	p2 := now
	for _, row := range []struct {
		name string
		age  int
		at   time.Time
	}{
		{"u_p1_10", 10, p1},
		{"u_p1_20", 20, p1},
		{"u_p2_30", 30, p2},
		{"u_p2_40", 40, p2},
	} {
		_, err := client.User.Create().
			SetName(row.name).
			SetEmail(row.name + "@dt.com").
			SetAge(row.age).
			SetRole(user.RoleUser).
			SetCreatedAt(row.at).
			SetUpdatedAt(row.at).
			Save(ctx)
		require.NoError(t, err)
	}

	users, err := client.User.Query().
		Modify(func(s *sql.Selector) {
			s.Select("TO_CHAR(" + s.C(user.FieldCreatedAt) + ", 'YYYY-MM') AS period")
			s.AppendSelectAs("SUM("+s.C(user.FieldAge)+")", "total_age")
			s.AppendSelectAs("COUNT(*)", "head_count")
			s.GroupBy("TO_CHAR(" + s.C(user.FieldCreatedAt) + ", 'YYYY-MM')")
			s.OrderBy(sql.Asc("TO_CHAR(" + s.C(user.FieldCreatedAt) + ", 'YYYY-MM')"))
		}).
		All(ctx)
	require.NoError(t, err, "Postgres must accept the aggregate SELECT — ordering fix + default-case fix required")
	require.Len(t, users, 2, "two distinct year-month periods expected")

	totals := make([]int64, 0, 2)
	counts := make([]int64, 0, 2)
	for _, u := range users {
		period, err := u.Value("period")
		require.NoError(t, err, "period alias must be retrievable via Value()")
		require.NotEmpty(t, period, "TO_CHAR period must be non-empty")

		total, err := u.Value("total_age")
		require.NoError(t, err, "total_age alias must be retrievable via Value()")
		totals = append(totals, valueAsInt64(t, total))

		count, err := u.Value("head_count")
		require.NoError(t, err, "head_count alias must be retrievable via Value()")
		counts = append(counts, valueAsInt64(t, count))
	}
	assert.ElementsMatch(t, []int64{30, 70}, totals,
		"period totals must match seeded ages: 10+20=30 and 30+40=70")
	assert.ElementsMatch(t, []int64{2, 2}, counts,
		"each period has 2 users")
}

// TestModifier_Postgres_MultipleAliasedAggregates verifies that multiple
// aliased aggregates in one Modify (count, sum, avg, min, max) are each
// retrievable via Value() after the assignValues default-case fix. Postgres
// is the natural target because it returns each aggregate with its native
// type (count→bigint, sum→numeric, avg→numeric, min/max→input type), which
// exercises all the scan paths in one query.
func TestModifier_Postgres_MultipleAliasedAggregates(t *testing.T) {
	client, cleanup := openPostgresOrSkip(t)
	defer cleanup()
	ctx := context.Background()

	for i, age := range []int{10, 20, 30, 40, 50} {
		_, err := client.User.Create().
			SetName(fmt.Sprintf("u%d", i)).
			SetEmail(fmt.Sprintf("u%d@agg.com", i)).
			SetAge(age).
			SetRole(user.RoleUser).
			SetCreatedAt(now).
			SetUpdatedAt(now).
			Save(ctx)
		require.NoError(t, err)
	}

	users, err := client.User.Query().
		Modify(func(s *sql.Selector) {
			s.Select(s.C(user.FieldRole))
			s.AppendSelectAs("COUNT(*)", "n")
			s.AppendSelectAs("SUM("+s.C(user.FieldAge)+")", "sum_age")
			// CAST avg to bigint so both dialects return int-shaped value.
			s.AppendSelectAs("CAST(AVG("+s.C(user.FieldAge)+") AS BIGINT)", "avg_age")
			s.AppendSelectAs("MIN("+s.C(user.FieldAge)+")", "min_age")
			s.AppendSelectAs("MAX("+s.C(user.FieldAge)+")", "max_age")
			s.GroupBy(s.C(user.FieldRole))
		}).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1, "one role group")

	u := users[0]
	for alias, want := range map[string]int64{
		"n":       5,
		"sum_age": 150,
		"avg_age": 30,
		"min_age": 10,
		"max_age": 50,
	} {
		v, err := u.Value(alias)
		require.NoError(t, err, "Value(%q) must be retrievable", alias)
		assert.Equal(t, want, valueAsInt64(t, v), "aggregate %q", alias)
	}
}

// TestModifier_CoalesceAggregate_HandlesEmptySet pins the real-world pattern
// for handling SUM/AVG over a filter that may match no rows: wrap the
// aggregate in COALESCE(..., 0) so the emitted SQL returns a typed 0
// instead of NULL.
//
// Velox's sql.UnknownType is `type UnknownType any` (matches Ent's definition
// exactly). It cannot scan a SQL NULL because bare any isn't a sql.Scanner —
// both database/sql drivers error with "unsupported Scan, storing
// driver.Value type <nil> into type *sql.UnknownType". Callers who need
// explicit NULL semantics must select via COALESCE or wrap the column with
// CAST, which is the idiom everyone uses for dashboard-style aggregates
// anyway. This test demonstrates the working pattern.
func TestModifier_CoalesceAggregate_HandlesEmptySet(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()
		// No seed — aggregate fires over an empty set.

		rows, err := client.User.Query().
			Modify(func(s *sql.Selector) {
				s.Select("COUNT(*)")
				s.AppendSelectAs(
					"COALESCE(SUM("+s.C(user.FieldAge)+"), 0)",
					"sum_age",
				)
			}).
			All(ctx)
		require.NoError(t, err, "COALESCEd aggregate over empty set must scan cleanly on every dialect")
		require.Len(t, rows, 1, "ungrouped aggregate returns one row even over empty set")

		v, err := rows[0].Value("sum_age")
		require.NoError(t, err, "Value(\"sum_age\") must be retrievable")
		assert.Equal(t, int64(0), valueAsInt64(t, v),
			"COALESCE turns NULL into typed 0 — safe for Int scans on any dialect")
	})
}
