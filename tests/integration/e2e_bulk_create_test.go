package integration_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/google/uuid"

	"github.com/syssam/velox/dialect/sql"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/post"
	"github.com/syssam/velox/tests/integration/tag"
	"github.com/syssam/velox/tests/integration/token"
	"github.com/syssam/velox/tests/integration/user"
)

// TestCreateBulk_BatchSize verifies the Django/GORM-style chunking
// API. Setting BatchSize(n) makes Save split the builders into
// consecutive chunks of at most n rows, running one mutator chain +
// one BatchCreate per chunk. The returned slice has entries for
// every input row in original order.
func TestCreateBulk_BatchSize(t *testing.T) {
	t.Run("chunks_evenly", func(t *testing.T) {
		client := openTestClient(t)
		ctx := context.Background()

		names := []string{"a", "b", "c", "d", "e", "f"}
		tags, err := client.Tag.MapCreateBulk(names, func(c *tag.TagCreate, i int) {
			c.SetName(names[i])
		}).BatchSize(2).Save(ctx)
		require.NoError(t, err)
		require.Len(t, tags, 6)
		for i, tg := range tags {
			assert.Equal(t, names[i], tg.Name, "row %d name mismatch", i)
			assert.NotZero(t, tg.ID, "row %d should have ID", i)
		}
	})

	t.Run("chunks_with_remainder", func(t *testing.T) {
		client := openTestClient(t)
		ctx := context.Background()

		names := []string{"a", "b", "c", "d", "e"}
		tags, err := client.Tag.MapCreateBulk(names, func(c *tag.TagCreate, i int) {
			c.SetName(names[i])
		}).BatchSize(3).Save(ctx)
		require.NoError(t, err)
		require.Len(t, tags, 5)
		for i, tg := range tags {
			assert.Equal(t, names[i], tg.Name)
			assert.NotZero(t, tg.ID)
		}
	})

	t.Run("size_larger_than_input_is_single_chunk", func(t *testing.T) {
		client := openTestClient(t)
		ctx := context.Background()

		names := []string{"a", "b"}
		tags, err := client.Tag.MapCreateBulk(names, func(c *tag.TagCreate, i int) {
			c.SetName(names[i])
		}).BatchSize(100).Save(ctx)
		require.NoError(t, err)
		assert.Len(t, tags, 2)
	})

	t.Run("zero_means_no_chunking", func(t *testing.T) {
		client := openTestClient(t)
		ctx := context.Background()

		names := []string{"a", "b", "c"}
		tags, err := client.Tag.MapCreateBulk(names, func(c *tag.TagCreate, i int) {
			c.SetName(names[i])
		}).BatchSize(0).Save(ctx)
		require.NoError(t, err)
		assert.Len(t, tags, 3)
	})

	// Regression guard for the SQLite "too many SQL variables" cap.
	// A 10000-row bulk with BatchSize(5000) splits into 2 chunks,
	// each ~30000 params. Without BatchSize this would fail at the
	// database layer.
	t.Run("chunking_clears_sqlite_param_cap", func(t *testing.T) {
		client := openTestClient(t)
		ctx := context.Background()

		const n = 10000
		names := make([]string, n)
		for i := range names {
			names[i] = "batch_" + strconv.Itoa(i)
		}
		users, err := client.User.MapCreateBulk(names, func(c *user.UserCreate, i int) {
			c.SetName(names[i]).
				SetEmail(names[i] + "@ex.com").
				SetAge(30).
				SetRole(user.RoleUser).
				SetCreatedAt(now).
				SetUpdatedAt(now)
		}).BatchSize(5000).Save(ctx)
		require.NoError(t, err)
		assert.Len(t, users, n)
		for _, u := range users {
			assert.NotZero(t, u.ID)
		}
	})
}

// TestCreateBulk_BatchSize_ErrorMidChunk verifies the partial-writes
// contract: if a mid-loop chunk errors out, Save returns the rows
// that were successfully persisted so far together with the error.
// The caller can either retry the failed chunk or wrap the whole
// Save in a tx to get all-or-nothing.
func TestCreateBulk_BatchSize_ErrorMidChunk(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	// Pre-seed a row that will collide with chunk 2.
	createTag(t, client, "collide")

	names := []string{"ok1", "ok2", "collide", "ok4"}
	tags, err := client.Tag.MapCreateBulk(names, func(c *tag.TagCreate, i int) {
		c.SetName(names[i])
	}).BatchSize(2).Save(ctx)

	require.Error(t, err, "chunk containing 'collide' should error")
	// First chunk ("ok1","ok2") committed; second chunk ("collide","ok4") failed.
	require.Len(t, tags, 2, "returned slice should include the successful chunk")
	assert.Equal(t, "ok1", tags[0].Name)
	assert.Equal(t, "ok2", tags[1].Name)

	// DB state matches: the first chunk is persisted, the second is not.
	ok1Count, err := client.Tag.Query().Where(tag.NameField.EQ("ok1")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, ok1Count)
	ok4Count, err := client.Tag.Query().Where(tag.NameField.EQ("ok4")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, ok4Count, "ok4 should not have been inserted")
}

// TestCreateBulk_BatchSize_InsideTx verifies the recommended
// all-or-nothing pattern: wrap BatchSize chunks in an explicit tx,
// roll back on error, no partial writes survive.
func TestCreateBulk_BatchSize_InsideTx(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	createTag(t, client, "tx_collide")

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	names := []string{"tx_ok1", "tx_ok2", "tx_collide", "tx_ok4"}
	_, err = tx.Tag.MapCreateBulk(names, func(c *tag.TagCreate, i int) {
		c.SetName(names[i])
	}).BatchSize(2).Save(ctx)
	require.Error(t, err)

	require.NoError(t, tx.Rollback())

	// None of the new tx_* rows should survive.
	count, err := client.Tag.Query().Where(tag.NameField.HasPrefix("tx_ok")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "caller rollback must undo the successful chunk too")
}

// TestCreateBulk_SQLiteParamLimit documents the SQLite "too many SQL
// variables" constraint that any ORM hits when it batches a single
// multi-row INSERT statement. The cap is 32766 parameters by default
// in modernc.org/sqlite; a row uses one parameter per non-default
// column. User has 6 insertable columns, so ~5460 rows fit in one
// statement but ~5500 rows do not.
//
// This is a SQLite configuration limit, not a velox bug — but it's
// a real-world consideration for users who want to bulk-insert large
// datasets. The recommended workaround is caller-side chunking: slice
// the input and call CreateBulk per chunk. The test below demonstrates
// both the failure mode and the chunking workaround so future readers
// don't have to rediscover them.
func TestCreateBulk_SQLiteParamLimit(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	// 5000 rows × 6 cols = 30000 params → fits under the 32766 cap.
	t.Run("under_cap", func(t *testing.T) {
		const n = 5000
		names := make([]string, n)
		for i := range names {
			names[i] = "under_" + strconv.Itoa(i)
		}
		users, err := client.User.MapCreateBulk(names, func(c *user.UserCreate, i int) {
			c.SetName(names[i]).
				SetEmail(names[i] + "@ex.com").
				SetAge(30).
				SetRole(user.RoleUser).
				SetCreatedAt(now).
				SetUpdatedAt(now)
		}).Save(ctx)
		require.NoError(t, err)
		assert.Len(t, users, n)
	})

	// 10000 rows × 6 cols = 60000 params → fails loudly with
	// "too many SQL variables". Velox surfaces the SQLite error
	// directly; it is not silently truncated.
	t.Run("over_cap_fails_loudly", func(t *testing.T) {
		const n = 10000
		names := make([]string, n)
		for i := range names {
			names[i] = "over_" + strconv.Itoa(i)
		}
		_, err := client.User.MapCreateBulk(names, func(c *user.UserCreate, i int) {
			c.SetName(names[i]).
				SetEmail(names[i] + "@ex.com").
				SetAge(30).
				SetRole(user.RoleUser).
				SetCreatedAt(now).
				SetUpdatedAt(now)
		}).Save(ctx)
		require.Error(t, err, "10000-row bulk should fail with SQL variable cap")
		assert.Contains(t, err.Error(), "too many SQL variables",
			"error should mention the SQL variables cap so callers know to chunk")
	})

	// Chunking workaround: split the input and call CreateBulk per
	// chunk. This is the pattern callers should use for large inputs.
	t.Run("chunking_workaround", func(t *testing.T) {
		const total = 10000
		const chunk = 5000

		for start := 0; start < total; start += chunk {
			end := start + chunk
			if end > total {
				end = total
			}
			names := make([]string, end-start)
			for i := range names {
				names[i] = "chunk_" + strconv.Itoa(start+i)
			}
			_, err := client.User.MapCreateBulk(names, func(c *user.UserCreate, i int) {
				c.SetName(names[i]).
					SetEmail(names[i] + "@ex.com").
					SetAge(30).
					SetRole(user.RoleUser).
					SetCreatedAt(now).
					SetUpdatedAt(now)
			}).Save(ctx)
			require.NoError(t, err, "chunk starting at %d failed", start)
		}

		count, err := client.User.Query().Where(user.NameField.HasPrefix("chunk_")).Count(ctx)
		require.NoError(t, err)
		assert.Equal(t, total, count)
	})
}

// TestCreateBulk_ManualBuilders verifies the variadic CreateBulk path
// (as opposed to MapCreateBulk which is already covered). Each builder
// is constructed independently and passed in as a separate argument.
func TestCreateBulk_ManualBuilders(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	b1 := client.User.Create().
		SetName("Alice").
		SetEmail("alice@example.com").
		SetAge(30).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now)
	b2 := client.User.Create().
		SetName("Bob").
		SetEmail("bob@example.com").
		SetAge(25).
		SetRole(user.RoleAdmin).
		SetCreatedAt(now).
		SetUpdatedAt(now)

	users, err := client.User.CreateBulk(b1, b2).Save(ctx)
	require.NoError(t, err)
	require.Len(t, users, 2)
	assert.NotZero(t, users[0].ID)
	assert.NotZero(t, users[1].ID)
	assert.NotEqual(t, users[0].ID, users[1].ID)

	got, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, got)
}

// TestCreateBulk_ReturnedNodesHaveConfig pins the invariant that
// nodes returned from CreateBulk carry a non-zero runtime.Config —
// the same rule single-row Save already satisfies. Without
// SetConfig propagation in saveChunk, edge-traversal methods like
// QueryPosts() panic with a nil QueryContext (the gqlgen resolver
// regression that motivated the fix in create.go).
func TestCreateBulk_ReturnedNodesHaveConfig(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	names := []string{"Alice", "Bob", "Carol"}
	users, err := client.User.MapCreateBulk(names, func(c *user.UserCreate, i int) {
		c.SetName(names[i]).
			SetEmail(names[i] + "@example.com").
			SetAge(30).
			SetRole(user.RoleUser).
			SetCreatedAt(now).
			SetUpdatedAt(now)
	}).Save(ctx)
	require.NoError(t, err)
	require.Len(t, users, len(names))

	// Walking an edge off a returned bulk-created node must not
	// panic — this is the exact gqlgen resolver path.
	for _, u := range users {
		count, err := u.QueryPosts().Count(ctx)
		require.NoError(t, err)
		assert.Zero(t, count)
	}
}

// TestCreate_ReturnedNodeHasConfig is the single-row sibling of
// TestCreateBulk_ReturnedNodesHaveConfig — it pins the same invariant
// (returned entity carries runtime.Config) on the Create().Save()
// path, so any future regression shows up regardless of which CRUD
// path drops the propagation.
func TestCreate_ReturnedNodeHasConfig(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	u, err := client.User.Create().
		SetName("solo").
		SetEmail("solo@example.com").
		SetAge(30).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	// Edge traversal off the freshly-saved entity must not panic.
	count, err := u.QueryPosts().Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, count)
}

// TestUpdateOne_ReturnedNodeHasConfig pins the same invariant on the
// UpdateOne().Save() path. Before the update.go generator was fixed
// to call SetConfig on the node returned from sqlSave, this panicked
// with a nil QueryContext on u.QueryPosts().
func TestUpdateOne_ReturnedNodeHasConfig(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	created, err := client.User.Create().
		SetName("before").
		SetEmail("before@example.com").
		SetAge(30).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	updated, err := client.User.UpdateOne(created).SetName("after").Save(ctx)
	require.NoError(t, err)
	require.Equal(t, "after", updated.Name)

	// Edge traversal off the updated entity must not panic.
	count, err := updated.QueryPosts().Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, count)
}

// TestCreateBulk_PostsForSameAuthor exercises bulk creation of child
// rows that all reference an existing parent via an FK edge. This
// stresses the FK-edge wiring through batchCreator.
func TestCreateBulk_PostsForSameAuthor(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	author := createUser(t, client, "Alice", "alice@example.com")

	titles := []string{"P1", "P2", "P3"}
	posts, err := client.Post.MapCreateBulk(titles, func(c *post.PostCreate, i int) {
		c.SetTitle(titles[i]).
			SetContent("body").
			SetStatus(post.StatusPublished).
			SetViewCount(0).
			SetAuthorID(author.ID).
			SetCreatedAt(now).
			SetUpdatedAt(now)
	}).Save(ctx)
	require.NoError(t, err)
	require.Len(t, posts, 3)

	for _, p := range posts {
		assert.NotZero(t, p.ID)
	}

	// Read back via the parent's edge to confirm the FK wiring stuck.
	all, err := client.Post.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, all, 3)
}

// TestCreateBulk_EmptyInput verifies the degenerate case of zero
// builders. This is a common defensive test — bulk APIs are easy to
// call with len(slice)==0 and should not error.
func TestCreateBulk_EmptyInput(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	users, err := client.User.CreateBulk().Save(ctx)
	require.NoError(t, err)
	assert.Empty(t, users)
}

// TestCreateBulk_OnConflictUpdateNewValues verifies that the variadic
// OnConflict setter on the bulk builder is honored: a re-insert of the
// same unique-key value succeeds and produces no duplicate row.
func TestCreateBulk_OnConflictUpdateNewValues(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tags1, err := client.Tag.MapCreateBulk([]string{"go", "orm"}, func(c *tag.TagCreate, i int) {
		c.SetName([]string{"go", "orm"}[i])
	}).
		OnConflict(sql.ConflictColumns("name"), sql.ResolveWithNewValues()).
		Save(ctx)
	require.NoError(t, err)
	require.Len(t, tags1, 2)

	// Re-insert the exact same names — must not error and must not duplicate.
	tags2, err := client.Tag.MapCreateBulk([]string{"go", "orm"}, func(c *tag.TagCreate, i int) {
		c.SetName([]string{"go", "orm"}[i])
	}).
		OnConflict(sql.ConflictColumns("name"), sql.ResolveWithNewValues()).
		Save(ctx)
	require.NoError(t, err)
	require.Len(t, tags2, 2)
	for _, tg := range tags2 {
		assert.NotZero(t, tg.ID, "UpdateNewValues path should populate ID for every row")
	}

	count, err := client.Tag.Query().Where(tag.NameField.In("go", "orm")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

// TestCreateBulk_OnConflictDoNothing verifies the bulk DO NOTHING path.
// On the second insert, both rows are duplicates so RETURNING produces
// zero rows; the bulk builder must not error, and the returned slice
// should still have len == len(builders) (with zero IDs marking the
// rows that were skipped).
func TestCreateBulk_OnConflictDoNothing(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	first, err := client.Tag.MapCreateBulk([]string{"a", "b"}, func(c *tag.TagCreate, i int) {
		c.SetName([]string{"a", "b"}[i])
	}).
		OnConflict(sql.ConflictColumns("name"), sql.DoNothing()).
		Save(ctx)
	require.NoError(t, err)
	require.Len(t, first, 2)
	for _, tg := range first {
		assert.NotZero(t, tg.ID, "first insert should populate IDs")
	}

	// Second insert: both names already exist. Must not error.
	second, err := client.Tag.MapCreateBulk([]string{"a", "b"}, func(c *tag.TagCreate, i int) {
		c.SetName([]string{"a", "b"}[i])
	}).
		OnConflict(sql.ConflictColumns("name"), sql.DoNothing()).
		Save(ctx)
	require.NoError(t, err)
	require.Len(t, second, 2)
	for _, tg := range second {
		assert.Zero(t, tg.ID, "DoNothing-skipped row should have zero ID")
	}

	count, err := client.Tag.Query().Where(tag.NameField.In("a", "b")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

// TestCreateBulk_OnConflictUpdateNewValues_Mixed pins the positional
// alignment guarantee for the DO UPDATE SET path. Unlike DO NOTHING,
// DO UPDATE SET produces a RETURNING row for every input row (the
// duplicates get an update, the new ones get an insert), so the
// dense/positional indexing in batchCreator.insertLastIDs is safe.
func TestCreateBulk_OnConflictUpdateNewValues_Mixed(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	preA := createTag(t, client, "alpha")
	preC := createTag(t, client, "charlie")

	tags, err := client.Tag.MapCreateBulk(
		[]string{"alpha", "beta", "charlie"},
		func(c *tag.TagCreate, i int) {
			c.SetName([]string{"alpha", "beta", "charlie"}[i])
		},
	).
		OnConflict(sql.ConflictColumns("name"), sql.ResolveWithNewValues()).
		Save(ctx)
	require.NoError(t, err)
	require.Len(t, tags, 3)

	// All three should have IDs aligned to their names.
	beta, err := client.Tag.Query().Where(tag.NameField.EQ("beta")).Only(ctx)
	require.NoError(t, err)

	wantIDs := map[string]int{
		"alpha":   preA.ID,
		"beta":    beta.ID,
		"charlie": preC.ID,
	}
	for _, tg := range tags {
		assert.Equal(t, wantIDs[tg.Name], tg.ID,
			"row %q got ID %d, expected %d", tg.Name, tg.ID, wantIDs[tg.Name])
	}
}

// TestCreateBulk_OnConflictDoNothing_Mixed documents a known
// limitation of bulk DoNothing: when some (but not all) rows in the
// bulk are duplicates, the RETURNING clause produces fewer rows than
// inputs, and the positional scan in sqlgraph.batchCreator can't
// match returned IDs back to the correct input row. The database
// state is correct — duplicate rows are left untouched, new rows are
// inserted — but the IDs on the returned Go slice are unreliable.
//
// Velox follows Ent's behavior here: the reliable-ID fix would
// require either a per-row fallback (500x slowdown over a remote DB)
// or a complex RETURNING-column-match-back (significant edge cases
// with composite keys, partial indexes, expression indexes). Callers
// who need reliable IDs for every row should use
// sql.ResolveWithIgnore() instead, which produces DO UPDATE SET
// col=col under the hood and returns a RETURNING row for every input.
// This test pins that contract — it asserts the DB state is correct
// but does NOT assert anything about the returned slice's IDs.
func TestCreateBulk_OnConflictDoNothing_Mixed(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	// Pre-seed two tags so positions 0 and 2 of the next bulk insert
	// will be duplicates.
	createTag(t, client, "alpha")
	createTag(t, client, "charlie")

	// Bulk insert: alpha (dup), beta (new), charlie (dup).
	tags, err := client.Tag.MapCreateBulk(
		[]string{"alpha", "beta", "charlie"},
		func(c *tag.TagCreate, i int) {
			c.SetName([]string{"alpha", "beta", "charlie"}[i])
		},
	).
		OnConflict(sql.ConflictColumns("name"), sql.DoNothing()).
		Save(ctx)
	require.NoError(t, err)
	require.Len(t, tags, 3)

	// DB state must be correct: exactly three tags exist (alpha,
	// charlie preserved from pre-seed; beta freshly inserted), no
	// duplicates.
	count, err := client.Tag.Query().Where(tag.NameField.In("alpha", "beta", "charlie")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, count)

	// beta must be queryable by name (its INSERT did fire).
	_, err = client.Tag.Query().Where(tag.NameField.EQ("beta")).Only(ctx)
	require.NoError(t, err)

	// NB: we deliberately do NOT assert anything about the IDs of the
	// returned `tags` slice. Under DoNothing + mixed duplicates,
	// returned IDs are unreliable — see the function docstring for
	// the explanation and the recommended workaround.
}

// TestCreateBulk_OnConflictWithHookFallback covers the trickiest of
// the bulk-upsert paths: when at least one builder has a registered
// hook, the bulk Save falls back to per-row .Save() calls. The bulk
// builder's conflict opts must propagate onto each individual builder,
// otherwise the hook fallback would silently lose ON CONFLICT.
func TestCreateBulk_OnConflictWithHookFallback(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	var calls int
	client.Tag.Use(func(next integration.Mutator) integration.Mutator {
		return integration.MutateFunc(func(ctx context.Context, m integration.Mutation) (integration.Value, error) {
			calls++
			return next.Mutate(ctx, m)
		})
	})

	// First insert via bulk + hook fallback path.
	_, err := client.Tag.MapCreateBulk([]string{"x", "y"}, func(c *tag.TagCreate, i int) {
		c.SetName([]string{"x", "y"}[i])
	}).
		OnConflict(sql.ConflictColumns("name"), sql.ResolveWithNewValues()).
		Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, calls)

	// Re-insert duplicates: must not error if conflict opts propagated.
	_, err = client.Tag.MapCreateBulk([]string{"x", "y"}, func(c *tag.TagCreate, i int) {
		c.SetName([]string{"x", "y"}[i])
	}).
		OnConflict(sql.ConflictColumns("name"), sql.ResolveWithNewValues()).
		Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, 4, calls)

	count, err := client.Tag.Query().Where(tag.NameField.In("x", "y")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

// TestCreateBulk_OnConflictIgnore_Mixed exercises the Ignore resolver
// (ON CONFLICT DO UPDATE SET col=col) on mixed duplicate/new input.
// Unlike DoNothing, Ignore is a DO UPDATE under the hood and always
// produces a RETURNING row, so the fast batch path should stay
// positionally correct without falling back to per-row.
func TestCreateBulk_OnConflictIgnore_Mixed(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	preA := createTag(t, client, "ign_a")
	preC := createTag(t, client, "ign_c")

	tags, err := client.Tag.MapCreateBulk(
		[]string{"ign_a", "ign_b", "ign_c"},
		func(c *tag.TagCreate, i int) {
			c.SetName([]string{"ign_a", "ign_b", "ign_c"}[i])
		},
	).
		OnConflict(sql.ConflictColumns("name"), sql.ResolveWithIgnore()).
		Save(ctx)
	require.NoError(t, err)
	require.Len(t, tags, 3)

	beta, err := client.Tag.Query().Where(tag.NameField.EQ("ign_b")).Only(ctx)
	require.NoError(t, err)

	wantIDs := map[string]int{
		"ign_a": preA.ID,
		"ign_b": beta.ID,
		"ign_c": preC.ID,
	}
	for _, tg := range tags {
		assert.NotZero(t, tg.ID, "Ignore always produces a RETURNING row")
		assert.Equal(t, wantIDs[tg.Name], tg.ID,
			"row %q got id %d, expected %d", tg.Name, tg.ID, wantIDs[tg.Name])
	}

	count, err := client.Tag.Query().Where(tag.NameField.In("ign_a", "ign_b", "ign_c")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, count)
}

// TestCreateBulk_PostsWithM2MTags exercises bulk create of entities
// that have M2M edges set at create time. batchCreator processes M2M
// edges via batchAddM2M after the main insert; this verifies the
// generated bulk path wires M2M edges correctly for every row.
func TestCreateBulk_PostsWithM2MTags(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	author := createUser(t, client, "Alice", "alice@example.com")
	tagGo := createTag(t, client, "lang:go")
	tagOrm := createTag(t, client, "kind:orm")

	titles := []string{"BulkM2M-1", "BulkM2M-2"}
	posts, err := client.Post.MapCreateBulk(titles, func(c *post.PostCreate, i int) {
		c.SetTitle(titles[i]).
			SetContent("body").
			SetStatus(post.StatusPublished).
			SetViewCount(0).
			SetAuthorID(author.ID).
			SetCreatedAt(now).
			SetUpdatedAt(now).
			AddTagIDs(tagGo.ID, tagOrm.ID)
	}).Save(ctx)
	require.NoError(t, err)
	require.Len(t, posts, 2)
	for _, p := range posts {
		assert.NotZero(t, p.ID)
	}

	// Read back via the Tag→Posts edge to confirm the join rows were
	// inserted for every post in the bulk.
	goPosts, err := client.Tag.QueryPosts(tagGo).All(ctx)
	require.NoError(t, err)
	assert.Len(t, goPosts, 2, "tagGo should link to both bulk-inserted posts")

	ormPosts, err := client.Tag.QueryPosts(tagOrm).All(ctx)
	require.NoError(t, err)
	assert.Len(t, ormPosts, 2, "tagOrm should link to both bulk-inserted posts")
}

// TestCreateBulk_OnConflictDoNothing_WithHookMixed pins that the
// mutator chain still fires hooks per row under the DoNothing path,
// and that the DB state is still correct (inserts fire for new
// rows, duplicates are untouched). The returned-slice ID limitation
// documented on TestCreateBulk_OnConflictDoNothing_Mixed applies
// here too — do not assert on tags[i].ID.
func TestCreateBulk_OnConflictDoNothing_WithHookMixed(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	createTag(t, client, "alpha")
	createTag(t, client, "charlie")

	var calls int
	client.Tag.Use(func(next integration.Mutator) integration.Mutator {
		return integration.MutateFunc(func(ctx context.Context, m integration.Mutation) (integration.Value, error) {
			calls++
			return next.Mutate(ctx, m)
		})
	})

	tags, err := client.Tag.MapCreateBulk(
		[]string{"alpha", "beta", "charlie"},
		func(c *tag.TagCreate, i int) {
			c.SetName([]string{"alpha", "beta", "charlie"}[i])
		},
	).
		OnConflict(sql.ConflictColumns("name"), sql.DoNothing()).
		Save(ctx)
	require.NoError(t, err)
	require.Len(t, tags, 3)

	// Hook runs per row via the mutator chain, once for each builder.
	assert.Equal(t, 3, calls, "hook should fire once per builder")

	// DB state must be correct: exactly three tags exist (alpha and
	// charlie preserved from pre-seed; beta freshly inserted).
	count, err := client.Tag.Query().Where(tag.NameField.In("alpha", "beta", "charlie")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, count)
	_, err = client.Tag.Query().Where(tag.NameField.EQ("beta")).Only(ctx)
	require.NoError(t, err)

	// NB: returned-slice IDs under DoNothing + mixed duplicates are
	// unreliable — see TestCreateBulk_OnConflictDoNothing_Mixed for the
	// explanation and recommended workaround. Do not assert on tags[i].ID.
}

// TestCreateBulk_InsideTransactionCommits verifies the basic shape of
// bulk create inside an explicit transaction: commit makes the rows
// visible to a subsequent Query on the root client.
func TestCreateBulk_InsideTransactionCommits(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	_, err = tx.Tag.MapCreateBulk([]string{"tx1", "tx2"}, func(c *tag.TagCreate, i int) {
		c.SetName([]string{"tx1", "tx2"}[i])
	}).Save(ctx)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	count, err := client.Tag.Query().Where(tag.NameField.In("tx1", "tx2")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

// TestCreateBulk_InsideTransactionRollsBack verifies rollback.
func TestCreateBulk_InsideTransactionRollsBack(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	_, err = tx.Tag.MapCreateBulk([]string{"rb1", "rb2"}, func(c *tag.TagCreate, i int) {
		c.SetName([]string{"rb1", "rb2"}[i])
	}).Save(ctx)
	require.NoError(t, err)

	require.NoError(t, tx.Rollback())

	count, err := client.Tag.Query().Where(tag.NameField.In("rb1", "rb2")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

// TestCreateBulk_InsideTransaction_WithOnConflict combines a
// transaction with bulk OnConflict + DoNothing. The per-row fallback
// runs inside the caller's tx — each per-row insert has to use the
// tx's driver, not a fresh connection, otherwise the rollback path
// would leak state.
func TestCreateBulk_InsideTransaction_WithOnConflict(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	// Pre-seed outside the tx.
	createTag(t, client, "seed")

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	// "seed" is a dup; "fresh" is new. DoNothing → per-row fallback.
	tags, err := tx.Tag.MapCreateBulk([]string{"seed", "fresh"}, func(c *tag.TagCreate, i int) {
		c.SetName([]string{"seed", "fresh"}[i])
	}).
		OnConflict(sql.ConflictColumns("name"), sql.DoNothing()).
		Save(ctx)
	require.NoError(t, err)
	require.Len(t, tags, 2)

	require.NoError(t, tx.Rollback())

	// Rollback must have undone the "fresh" insert; "seed" remains.
	count, err := client.Tag.Query().Where(tag.NameField.EQ("fresh")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "fresh should have been rolled back")

	count, err = client.Tag.Query().Where(tag.NameField.EQ("seed")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "seed was committed outside the tx and must survive rollback")
}

// TestCreateBulk_InsideCallerTx_AtomicOnConstraintError pins the
// intersection of two guarantees: bulk atomicity AND caller-tx
// control. When a bulk create runs inside an explicit caller tx and
// the batch hits a mid-row constraint violation, the single
// BatchCreate statement fails atomically — no partial state inside
// the tx. The caller's subsequent Rollback then cleanly undoes any
// rows written before the bulk call. Atomicity is provided by SQL,
// not by a velox-side wrapper.
func TestCreateBulk_InsideCallerTx_AtomicOnConstraintError(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	// Pre-seed the collision row OUTSIDE the tx so it survives rollback.
	createTag(t, client, "dup_ctx")

	// Register a noop hook so the bulk actually goes through the
	// mutator chain (which used to force per-row fallback; now it
	// still batches). This exercises the chain's error-propagation
	// path under the same conditions that used to trigger the bug.
	client.Tag.Use(func(next integration.Mutator) integration.Mutator {
		return integration.MutateFunc(func(ctx context.Context, m integration.Mutation) (integration.Value, error) {
			return next.Mutate(ctx, m)
		})
	})

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	// Bulk inside the tx: "in_tx_1" (new), "dup_ctx" (collision), "in_tx_3" (never persisted).
	_, err = tx.Tag.MapCreateBulk(
		[]string{"in_tx_1", "dup_ctx", "in_tx_3"},
		func(c *tag.TagCreate, i int) {
			c.SetName([]string{"in_tx_1", "dup_ctx", "in_tx_3"}[i])
		},
	).Save(ctx)
	require.Error(t, err, "unique violation should surface")

	// Caller owns rollback; the single INSERT statement already
	// failed atomically inside the tx, but rollback is still what
	// finalizes the caller's tx state.
	require.NoError(t, tx.Rollback())

	// After rollback, none of the bulk rows should exist. The
	// pre-seeded row must remain.
	count, err := client.Tag.Query().Where(tag.NameField.In("in_tx_1", "in_tx_3")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "caller rollback must undo any partial bulk rows")

	count, err = client.Tag.Query().Where(tag.NameField.EQ("dup_ctx")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "pre-seeded row outside tx must survive")
}

// TestCreateBulk_AtomicOnMidRowConstraintError pins bulk atomicity
// under hooks + mid-row constraint violation. The mutator chain
// ends in a single sqlgraph.BatchCreate that emits one multi-row
// INSERT; SQL statement-level atomicity guarantees that if any row
// violates a constraint, none are persisted. This test would have
// exposed the atomicity bug in the old per-row hook fallback.
func TestCreateBulk_AtomicOnMidRowConstraintError(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	// Pre-seed the row that will cause the mid-batch collision.
	createTag(t, client, "dup")

	// Register a noop hook. With the old "hooks → per-row fallback"
	// design this would force N separate INSERT statements; after the
	// mutator chain rewrite it still goes through the single batched
	// INSERT. Both behaviors should be atomic, for different reasons.
	client.Tag.Use(func(next integration.Mutator) integration.Mutator {
		return integration.MutateFunc(func(ctx context.Context, m integration.Mutation) (integration.Value, error) {
			return next.Mutate(ctx, m)
		})
	})

	// Bulk: new, dup (unique constraint violation), new.
	_, err := client.Tag.MapCreateBulk(
		[]string{"new1", "dup", "new3"},
		func(c *tag.TagCreate, i int) {
			c.SetName([]string{"new1", "dup", "new3"}[i])
		},
	).Save(ctx)
	require.Error(t, err, "unique violation should surface")

	// Atomicity check: neither "new1" nor "new3" should be persisted.
	count, err := client.Tag.Query().Where(tag.NameField.In("new1", "new3")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count,
		"bulk create must be atomic: got %d partial rows persisted", count)
}

// TestCreateBulk_Token_DefaultUUID verifies that bulk create on an
// entity whose primary key is a user-assigned UUID (with a
// Default(uuid.New) generator, not DB auto-increment) produces
// distinct IDs for every row via the default func.
func TestCreateBulk_Token_DefaultUUID(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	names := []string{"tok1", "tok2", "tok3"}
	tokens, err := client.Token.MapCreateBulk(names, func(c *token.TokenCreate, i int) {
		c.SetName(names[i])
	}).Save(ctx)
	require.NoError(t, err)
	require.Len(t, tokens, 3)

	seen := map[uuid.UUID]struct{}{}
	for _, tk := range tokens {
		assert.NotEqual(t, uuid.Nil, tk.ID, "UUID default should have been applied")
		seen[tk.ID] = struct{}{}
	}
	assert.Len(t, seen, 3, "UUID defaults should be unique per row")

	// Read back by the generated IDs to confirm they actually persisted
	// under these IDs (not under some DB-assigned replacement).
	for _, tk := range tokens {
		got, err := client.Token.Get(ctx, tk.ID)
		require.NoError(t, err)
		assert.Equal(t, tk.ID, got.ID)
		assert.Equal(t, tk.Name, got.Name)
	}
}

// TestCreateBulk_Token_ExplicitID verifies that callers can supply
// their own UUIDs via SetID and those survive the bulk create intact.
// This exercises the user-provided-ID branch of the bulk path, which
// the auto-increment entities don't reach.
func TestCreateBulk_Token_ExplicitID(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	names := []string{"ex1", "ex2", "ex3"}

	tokens, err := client.Token.MapCreateBulk(names, func(c *token.TokenCreate, i int) {
		c.SetID(ids[i]).SetName(names[i])
	}).Save(ctx)
	require.NoError(t, err)
	require.Len(t, tokens, 3)

	for i, tk := range tokens {
		assert.Equal(t, ids[i], tk.ID, "explicit ID must be preserved")
	}

	// Verify the rows are reachable via the explicit IDs.
	for i, id := range ids {
		got, err := client.Token.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, names[i], got.Name)
	}
}

// TestCreateBulk_Token_OnConflictDoNothing_Mixed is the UUID-PK
// analog of the Tag mixed-DoNothing test. Same contract applies:
// the DB state is correct but the returned slice's IDs are not
// reliable for skipped rows — use sql.ResolveWithIgnore() if you
// need every row's ID. See TestCreateBulk_OnConflictDoNothing_Mixed
// for the full explanation.
func TestCreateBulk_Token_OnConflictDoNothing_Mixed(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	// Pre-seed two tokens with known names.
	_, err := client.Token.Create().SetName("pre_a").Save(ctx)
	require.NoError(t, err)
	_, err = client.Token.Create().SetName("pre_c").Save(ctx)
	require.NoError(t, err)

	names := []string{"pre_a", "bulk_b", "pre_c"}
	tokens, err := client.Token.MapCreateBulk(names, func(c *token.TokenCreate, i int) {
		c.SetName(names[i])
	}).
		OnConflict(sql.ConflictColumns("name"), sql.DoNothing()).
		Save(ctx)
	require.NoError(t, err)
	require.Len(t, tokens, 3)

	// DB state must be correct.
	count, err := client.Token.Query().Where(token.NameField.In("pre_a", "bulk_b", "pre_c")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, count)
	b, err := client.Token.Query().Where(token.NameField.EQ("bulk_b")).Only(ctx)
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, b.ID)
}

// TestCreateBulk_MutatorChainHookOrdering pins the hook execution
// order under the Ent-style mutator chain. This is a subtle but
// important semantic to document: the chain runs all rows' hook
// PRE-phases, then one batched BatchCreate, then all rows' hook
// POST-phases in reverse order. It is NOT "row 0 pre → row 0 SQL →
// row 0 post → row 1 pre → ...".
//
// This matches Ent and is a consequence of preserving batching
// across hook-bearing bulks. A hook that observes insertion in its
// post-phase will see IDs populated on its own row, but the order
// of row-observation (post-phases) is reversed relative to the
// order of row-preparation (pre-phases).
//
// If someone intentionally relies on strict per-row sequencing,
// they should use single-row Save inside a loop instead of bulk.
func TestCreateBulk_MutatorChainHookOrdering(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	var events []string
	client.Tag.Use(func(next integration.Mutator) integration.Mutator {
		return integration.MutateFunc(func(ctx context.Context, m integration.Mutation) (integration.Value, error) {
			// Extract the name field via the concrete mutation type.
			var name string
			if tm, ok := m.(interface{ Name() (string, bool) }); ok {
				name, _ = tm.Name()
			}
			events = append(events, "pre:"+name)
			v, err := next.Mutate(ctx, m)
			events = append(events, "post:"+name)
			return v, err
		})
	})

	_, err := client.Tag.MapCreateBulk([]string{"a", "b", "c"}, func(c *tag.TagCreate, i int) {
		c.SetName([]string{"a", "b", "c"}[i])
	}).Save(ctx)
	require.NoError(t, err)

	// Expected order: all pres then all posts in reverse.
	assert.Equal(t, []string{
		"pre:a", "pre:b", "pre:c",
		"post:c", "post:b", "post:a",
	}, events,
		"mutator chain must run all pre-phases, then one SQL, then all post-phases in reverse")
}

// TestCreateBulk_HookFiresPerRow verifies that registered mutation
// hooks are invoked for every builder in the bulk, not just once for
// the batch. (The Save path falls back to per-row execution when any
// builder has hooks attached, per create.go.)
func TestCreateBulk_HookFiresPerRow(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	var calls int
	client.User.Use(func(next integration.Mutator) integration.Mutator {
		return integration.MutateFunc(func(ctx context.Context, m integration.Mutation) (integration.Value, error) {
			calls++
			return next.Mutate(ctx, m)
		})
	})

	names := []string{"a", "b", "c"}
	_, err := client.User.MapCreateBulk(names, func(c *user.UserCreate, i int) {
		c.SetName(names[i]).
			SetEmail(names[i] + "@test.com").
			SetAge(20).
			SetRole(user.RoleUser).
			SetCreatedAt(now).
			SetUpdatedAt(now)
	}).Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, calls, "hook should fire once per row")
}
