package integration_test

// NULL-path and bulk-update coverage, dialect-parameterized.
//
// testschema User.nickname is the only Nillable field in the prototype, added
// specifically so the compiled integration suite exercises the clear-to-NULL
// chain (typed ClearNickname → mutation.clearedFields → spec.ClearField →
// SET nickname = NULL), NULL-aware aggregates/ordering, and cursor pagination
// over a NULL-able order column. Before nickname existed none of these paths
// had e2e coverage — the same blind-spot class that let the multi-order
// before-cursor bug ship (see testschema/user.go).
//
// NULL ordering placement (NULLS FIRST vs LAST) is deliberately NOT asserted:
// SQLite and MySQL sort NULLs first under ASC, Postgres sorts them last.
// Assertions here are restricted to properties that must hold on every
// dialect (row counts, the sorted non-NULL subsequence, sweep uniqueness).

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/contrib/graphql/gqlrelay"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/user"
)

// createUserNick creates a user with an explicit nickname (NULL when nick is nil).
func createUserNick(t *testing.T, client *integration.Client, name, email string, nick *string) *entity.User {
	t.Helper()
	b := client.User.Create().
		SetName(name).
		SetEmail(email).
		SetAge(30).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now)
	if nick != nil {
		b.SetNickname(*nick)
	}
	u, err := b.Save(context.Background())
	require.NoError(t, err)
	return u
}

func strp(s string) *string { return &s }

// TestMultiDialect_UpdateMany pins predicate-scoped bulk UPDATE — the write
// analog of the BulkOnConflict test. Each dialect emits its own UPDATE ...
// WHERE syntax and affected-row accounting (MySQL famously reports "changed"
// vs "matched" depending on driver flags); velox must return the matched-row
// count and leave non-matching rows untouched on every engine.
func TestMultiDialect_UpdateMany(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()

		createUser(t, client, "BulkA", "bulka@multi.com")
		createUser(t, client, "BulkB", "bulkb@multi.com")
		createUser(t, client, "Spectator", "spectator@multi.com")

		n, err := client.User.Update().
			Where(user.NameField.In("BulkA", "BulkB")).
			SetRole(user.RoleAdmin).
			Save(ctx)
		require.NoError(t, err)
		assert.Equal(t, 2, n, "UpdateMany must report the matched-row count on every dialect")

		admins, err := client.User.Query().Where(user.RoleField.EQ(user.RoleAdmin)).Count(ctx)
		require.NoError(t, err)
		assert.Equal(t, 2, admins)

		spectator, err := client.User.Query().Where(user.EmailField.EQ("spectator@multi.com")).Only(ctx)
		require.NoError(t, err)
		assert.Equal(t, user.RoleUser, spectator.Role, "rows outside the predicate must be untouched")
	})
}

// TestMultiDialect_ClearToNull pins the clear-to-NULL chain end-to-end:
// typed ClearNickname() → clearedFields → spec.ClearField → SET nickname =
// NULL, then reads the NULL back through both the entity scan (*string nil)
// and the IsNull/NotNull predicates. Covers UpdateOne and predicate-scoped
// bulk Update variants — the bulk variant runs ClearField through the
// UpdateMany sqlSave, a separate generated code path.
func TestMultiDialect_ClearToNull(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()

		u := createUserNick(t, client, "Nicked", "nicked@multi.com", strp("nick"))
		require.NotNil(t, u.Nickname)
		assert.Equal(t, "nick", *u.Nickname)

		// UpdateOne clear → NULL.
		got, err := client.User.UpdateOneID(u.ID).ClearNickname().Save(ctx)
		require.NoError(t, err)
		assert.Nil(t, got.Nickname, "ClearNickname must null the returned entity field")

		reread, err := client.User.Get(ctx, u.ID)
		require.NoError(t, err)
		assert.Nil(t, reread.Nickname, "cleared nickname must read back as NULL on every dialect")

		// Predicates over the NULL.
		nullCount, err := client.User.Query().Where(user.NicknameField.IsNull()).Count(ctx)
		require.NoError(t, err)
		assert.Equal(t, 1, nullCount, "IsNull must match the cleared row")
		notNullCount, err := client.User.Query().Where(user.NicknameField.NotNull()).Count(ctx)
		require.NoError(t, err)
		assert.Equal(t, 0, notNullCount, "NotNull must exclude the cleared row")

		// Bulk clear through the UpdateMany sqlSave path.
		v := createUserNick(t, client, "Bulky", "bulky@multi.com", strp("bulk"))
		w := createUserNick(t, client, "Keeper", "keeper@multi.com", strp("keep"))
		n, err := client.User.Update().
			Where(user.IDField.EQ(v.ID)).
			ClearNickname().
			Save(ctx)
		require.NoError(t, err)
		assert.Equal(t, 1, n)

		vr, err := client.User.Get(ctx, v.ID)
		require.NoError(t, err)
		assert.Nil(t, vr.Nickname, "bulk ClearNickname must null the matched row")
		wr, err := client.User.Get(ctx, w.ID)
		require.NoError(t, err)
		require.NotNil(t, wr.Nickname)
		assert.Equal(t, "keep", *wr.Nickname, "bulk ClearNickname must not touch rows outside the predicate")
	})
}

// TestMultiDialect_NullAggregates pins aggregate semantics over NULLs and the
// empty set. SQL aggregates skip NULL inputs (MIN/MAX over a mixed column see
// only the non-NULL values) and SUM over zero rows yields NULL, not 0 — every
// dialect agrees on the standard but each scans it back differently, so the
// scan layer must normalize.
func TestMultiDialect_NullAggregates(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()

		createUserNick(t, client, "AggA", "agga@multi.com", strp("alpha"))
		createUserNick(t, client, "AggB", "aggb@multi.com", nil) // NULL nickname
		createUserNick(t, client, "AggC", "aggc@multi.com", strp("charlie"))

		// MIN/MAX must skip the NULL row.
		var minmax []struct {
			Min string `json:"min"`
			Max string `json:"max"`
		}
		err := client.User.Query().
			Aggregate(
				integration.As(integration.Min(user.FieldNickname), "min"),
				integration.As(integration.Max(user.FieldNickname), "max"),
			).
			Scan(ctx, &minmax)
		require.NoError(t, err)
		require.Len(t, minmax, 1)
		assert.Equal(t, "alpha", minmax[0].Min, "MIN must ignore NULLs on every dialect")
		assert.Equal(t, "charlie", minmax[0].Max, "MAX must ignore NULLs on every dialect")

		// SUM over the empty set is NULL — pin via a nullable scan target.
		var sums []struct {
			Sum *int `json:"sum"`
		}
		err = client.User.Query().
			Where(user.NameField.EQ("NoSuchUser")).
			Aggregate(integration.As(integration.Sum(user.FieldAge), "sum")).
			Scan(ctx, &sums)
		require.NoError(t, err)
		require.Len(t, sums, 1, "aggregate over zero rows still yields one result row")
		assert.Nil(t, sums[0].Sum, "SUM over the empty set must scan as NULL (nil), not 0, on every dialect")
	})
}

// TestMultiDialect_OrderByNullable pins ORDER BY over a NULL-able column.
// NULL placement is dialect-divergent (SQLite/MySQL: NULLs first under ASC;
// Postgres: NULLs last), so the test asserts only the dialect-independent
// contract: every row comes back exactly once and the non-NULL values appear
// in ascending order relative to each other.
func TestMultiDialect_OrderByNullable(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()

		createUserNick(t, client, "OrdA", "orda@multi.com", nil)
		createUserNick(t, client, "OrdB", "ordb@multi.com", strp("bravo"))
		createUserNick(t, client, "OrdC", "ordc@multi.com", nil)
		createUserNick(t, client, "OrdD", "ordd@multi.com", strp("alpha"))

		users, err := client.User.Query().Order(user.ByNickname()).All(ctx)
		require.NoError(t, err)
		require.Len(t, users, 4, "ORDER BY a NULL-able column must not drop rows")

		var nonNull []string
		for _, u := range users {
			if u.Nickname != nil {
				nonNull = append(nonNull, *u.Nickname)
			}
		}
		require.Len(t, nonNull, 2)
		assert.True(t, sort.StringsAreSorted(nonNull),
			"non-NULL values must be mutually ordered ascending regardless of where the dialect places NULLs")
	})
}

// TestMultiDialect_PaginateNullableOrder_NonNullCursors pins cursor pagination
// ordered by the NULL-able nickname column for the supported case: every row
// carries a value, so each page boundary cursor holds a real (non-NULL) value
// and pagination runs through gqlrelay.multiPredicate with composite row-value
// comparisons. Nicknames are assigned in reverse of insertion order so the
// NICKNAME ASC sweep must reorder rows (IDs descending), proving the order
// column — not the ID tiebreaker — drives the pages.
func TestMultiDialect_PaginateNullableOrder_NonNullCursors(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()

		const total = 6
		for i := 1; i <= total; i++ {
			createUserNick(t, client,
				fmt.Sprintf("Page%02d", i),
				fmt.Sprintf("page%02d@multi.com", i),
				strp(fmt.Sprintf("nick%02d", total+1-i))) // reverse order: id 1 → nick06
		}

		orderBy := userOrderBy(t, "NICKNAME", entity.OrderDirectionAsc)
		first := 2
		seen := map[int]bool{}
		var gotIDs []int
		var cursor *gqlrelay.Cursor
		for page := 1; page <= total; page++ {
			conn, err := client.User.Query().Paginate(ctx, cursor, &first, nil, nil, withUserOrder(t, orderBy))
			require.NoError(t, err)
			for _, e := range conn.Edges {
				require.False(t, seen[e.Node.ID], "ID %d appeared twice while paginating by nickname", e.Node.ID)
				seen[e.Node.ID] = true
				gotIDs = append(gotIDs, e.Node.ID)
			}
			if !conn.PageInfo.HasNextPage {
				break
			}
			c := roundTripCursor(t, conn.PageInfo.EndCursor)
			cursor = &c
		}
		assert.Equal(t, []int{6, 5, 4, 3, 2, 1}, gotIDs,
			"NICKNAME ASC must yield IDs in reverse insertion order on every dialect — wrong order means the cursor predicate compared the wrong column")
	})
}

// TestMultiDialect_PaginateNullableOrder_NullCursors pins that Paginate
// ordered by a NULL-able column reaches every row when page boundaries land
// on NULL values, forward and backward, ascending and descending, on every
// dialect. The cursor predicate compared `nickname = NULL` / `nickname >
// NULL`, which SQL never satisfies, so the next page was empty with
// HasNextPage true (entgql does the same). It is now NULL-aware and places
// NULLs where each dialect sorts them (dialect.CapNullsFirst: first in
// ascending order on SQLite and MySQL, last on PostgreSQL).
func TestMultiDialect_PaginateNullableOrder_NullCursors(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()
		nicks := []*string{strp("bbb"), nil, nil, strp("aaa"), nil, strp("ccc")}
		for i, n := range nicks {
			createUserNick(t, client, fmt.Sprintf("Null%d", i), fmt.Sprintf("null%d@multi.com", i), n)
		}

		for _, dir := range []entity.OrderDirection{entity.OrderDirectionAsc, entity.OrderDirectionDesc} {
			orderBy := userOrderBy(t, "NICKNAME", dir)
			all, err := client.User.Query().Paginate(ctx, nil, ptr(100), nil, nil, withUserOrder(t, orderBy))
			require.NoError(t, err)
			want := ids(all)
			require.Len(t, want, len(nicks))

			var fwd []int
			var after *gqlrelay.Cursor
			for range nicks {
				conn, err := client.User.Query().Paginate(ctx, after, ptr(2), nil, nil, withUserOrder(t, orderBy))
				require.NoError(t, err)
				fwd = append(fwd, ids(conn)...)
				if !conn.PageInfo.HasNextPage {
					break
				}
				c := roundTripCursor(t, conn.PageInfo.EndCursor)
				after = &c
			}
			require.Equal(t, want, fwd, "%s forward walk", dir)

			var bwd []int
			var before *gqlrelay.Cursor
			for range nicks {
				conn, err := client.User.Query().Paginate(ctx, nil, nil, before, ptr(2), withUserOrder(t, orderBy))
				require.NoError(t, err)
				bwd = append(ids(conn), bwd...)
				if !conn.PageInfo.HasPreviousPage {
					break
				}
				c := roundTripCursor(t, conn.PageInfo.StartCursor)
				before = &c
			}
			require.Equal(t, want, bwd, "%s backward walk", dir)
		}
	})
}
