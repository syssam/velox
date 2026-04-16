package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/tests/integration/tag"
)

// TestUpsert_ReturnsSameIDOnConflict pins the "returns same ID"
// contract from the original session plan. ID(ctx) runs the upsert
// and returns the inserted-or-updated row's primary key, whether
// the row is brand-new or was already there. This matches Ent's
// *XxxUpsertOne.ID shape and is the reason the new method exists —
// callers who need the final row ID no longer have to Exec + run a
// follow-up Query-by-name.
func TestUpsert_ReturnsSameIDOnConflict(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	firstID, err := client.Tag.Create().
		SetName("return-same-id").
		OnConflict().
		UpdateNewValues().
		ID(ctx)
	require.NoError(t, err)
	assert.NotZero(t, firstID, "first upsert should produce a real ID")

	// Second upsert with the same unique name. The SQL emits
	// ON CONFLICT DO UPDATE SET ... RETURNING id, so the existing
	// row's id flows back through Save and out of ID — no extra
	// query, no zero return.
	secondID, err := client.Tag.Create().
		SetName("return-same-id").
		OnConflict().
		UpdateNewValues().
		ID(ctx)
	require.NoError(t, err)
	assert.Equal(t, firstID, secondID,
		"second upsert of the same unique key must return the existing row's ID")

	// Only one row in the DB.
	count, err := client.Tag.Query().Where(tag.NameField.EQ("return-same-id")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

// TestUpsert_IDX pins the panic-on-error wrapper.
func TestUpsert_IDX(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	id := client.Tag.Create().
		SetName("idx-happy").
		OnConflict().
		UpdateNewValues().
		IDX(ctx)
	assert.NotZero(t, id)
}

// TestUpsert_OnConflictUpdateNewValues verifies that a second insert on
// a duplicate unique key (Tag.name) does not error and does not create
// a duplicate row when ON CONFLICT DO UPDATE is requested.
func TestUpsert_OnConflictUpdateNewValues(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	require.NoError(t, client.Tag.Create().
		SetName("go").
		OnConflict().
		UpdateNewValues().
		Exec(ctx))

	require.NoError(t, client.Tag.Create().
		SetName("go").
		OnConflict().
		UpdateNewValues().
		Exec(ctx))

	count, err := client.Tag.Query().Where(tag.NameField.EQ("go")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

// TestUpsert_OnConflictDoNothing verifies the DO NOTHING resolution
// path. Previously the second insert blew up with sql.ErrNoRows because
// RETURNING was attached unconditionally and DO NOTHING produces no
// row on conflict. The sqlgraph creator now treats an empty RETURNING
// result as a successful no-op when an ON CONFLICT clause is present.
func TestUpsert_OnConflictDoNothing(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	require.NoError(t, client.Tag.Create().
		SetName("nothing").
		OnConflict().
		DoNothing().
		Exec(ctx))

	require.NoError(t, client.Tag.Create().
		SetName("nothing").
		OnConflict().
		DoNothing().
		Exec(ctx))

	count, err := client.Tag.Query().Where(tag.NameField.EQ("nothing")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

// TestUpsert_OnConflictIgnore verifies the Ignore (set each column to
// itself on conflict) resolution path.
func TestUpsert_OnConflictIgnore(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	require.NoError(t, client.Tag.Create().
		SetName("orm").
		OnConflict().
		Ignore().
		Exec(ctx))

	require.NoError(t, client.Tag.Create().
		SetName("orm").
		OnConflict().
		Ignore().
		Exec(ctx))

	count, err := client.Tag.Query().Where(tag.NameField.EQ("orm")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}
