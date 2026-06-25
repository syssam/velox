package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect/sql"
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

// TestUpsert_SetField_EagerConflictBinding exercises the per-column SetXxx /
// Update upsert path. Every other upsert test uses UpdateNewValues / DoNothing /
// Ignore, which append a single resolver to create.conflict directly — so the
// SetXxx / Update path had ZERO coverage.
//
// The bug class it pins: each upsert setter binds its mutator into its OWN
// sql.ResolveWith conflict option eagerly, closing over the immutable argument.
// The old design instead accumulated mutators into a struct field, folded them
// into one resolver later, and cleared the field — but sql.ResolveWith is lazy
// (the closure runs while the INSERT is written), so the deferred read hit the
// cleared (nil) field and rendered an EMPTY "DO UPDATE SET" → "syntax error at
// or near \"RETURNING\"". The eager design makes that unrepresentable: no shared
// mutable state exists to clear.
//
// This asserts the setter actually MUTATES the row on conflict (not merely that
// the statement parses): an empty DO UPDATE SET would either error or no-op.
func TestUpsert_SetField_EagerConflictBinding(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	firstID, err := client.Tag.Create().
		SetName("eager-before").
		OnConflict().
		UpdateNewValues().
		ID(ctx)
	require.NoError(t, err)
	assert.NotZero(t, firstID)

	// Same unique name → conflict. Resolve through the SetXxx / Update path and
	// have the resolver rewrite the row to a new value, proving the eager
	// ResolveWith mutator runs at SQL-build time.
	secondID, err := client.Tag.Create().
		SetName("eager-before").
		OnConflict().
		Update(func(s *sql.UpdateSet) {
			s.Set(tag.FieldName, "eager-after")
		}).
		ID(ctx)
	require.NoError(t, err)
	assert.Equal(t, firstID, secondID,
		"per-column Update upsert must resolve the existing row, not error")

	// The DO UPDATE SET actually ran: the row was rewritten, not left untouched.
	afterCount, err := client.Tag.Query().Where(tag.NameField.EQ("eager-after")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, afterCount, "eager Update setter must rewrite the row on conflict")

	beforeCount, err := client.Tag.Query().Where(tag.NameField.EQ("eager-before")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, beforeCount, "old value must be gone after the conflict update")
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
