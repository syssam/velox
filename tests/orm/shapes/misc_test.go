package shapes

import (
	"context"
	"testing"

	client "github.com/syssam/velox/tests/orm/shapes/velox"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
	"github.com/syssam/velox/tests/orm/shapes/types"
	"github.com/syssam/velox/tests/orm/shapes/velox/bare"
	"github.com/syssam/velox/tests/orm/shapes/velox/entity"
	"github.com/syssam/velox/tests/orm/shapes/velox/labelkey"
	"github.com/syssam/velox/tests/orm/shapes/velox/weightkey"
)

// An entity with no field but its id: every statement has an empty column
// list somewhere.
func TestBare_EntityWithoutFields(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	a := must(client.Bare.Create(db).Save(ctx))
	require.NotZero(t, a.ID)
	b := must(client.Bare.Create(db).Save(ctx))
	assert.Equal(t, a.ID+1, b.ID)
	// (A bulk create without ids: TestBare_BulkCreateInsertsEveryRow.)
	rows := must(client.Bare.CreateBulk(db, client.Bare.Create(db).Set(bare.ID.Set(40)), client.Bare.Create(db).Set(bare.ID.Set(41))).Save(ctx))
	require.Len(t, rows, 2)
	assert.Equal(t, []int{40, 41}, []int{rows[0].ID, rows[1].ID})
	// Rows with and without an id cannot share a batch (the SQL layer's rule).
	_, err := client.Bare.CreateBulk(db, client.Bare.Create(db), client.Bare.Create(db).Set(bare.ID.Set(42))).Save(ctx)
	require.ErrorContains(t, err, "inconsistent id values for batch insert")
	assert.Equal(t, 4, must(client.Bare.Query(db).Count(ctx)))
	assert.Equal(t, a.ID, must(client.Bare.Query(db).Where(bare.ID.EQ(a.ID)).Only(ctx)).ID)
	assert.Len(t, must(client.Bare.Query(db).OrderBy(bare.ID.Desc()).Limit(2).All(ctx)), 2)
	// An update with nothing to assign finds its row.
	got := must(client.Bare.Update(db).Where(bare.ID.EQ(40)).SaveOne(ctx))
	assert.Equal(t, 40, got.ID)
	_, err = client.Bare.Update(db).Where(bare.ID.EQ(9999)).SaveOne(ctx)
	assert.ErrorIs(t, err, velox.ErrNotFound)
	assert.Equal(t, 1, must(client.Bare.Delete(db).Where(bare.ID.EQ(40)).Exec(ctx)))
	assert.Equal(t, 3, must(client.Bare.Delete(db).Exec(ctx)))
	assert.Zero(t, must(client.Bare.Query(db).Count(ctx)))
}

// namedKeys: label keys la, lb (string kind); weight keys w1 (id assigned by
// the database), w2 = 50; both items of la; w2 has labels la+lb; w1 and w2
// are peers.
func namedKeys(t *testing.T, db velox.Executor) (la, lb *entity.LabelKey, w1, w2 *entity.WeightKey) {
	t.Helper()
	ctx := context.Background()
	la = must(client.LabelKey.Create(db).Set(labelkey.ID.Set("la"), labelkey.Name.Set("a")).Save(ctx))
	lb = must(client.LabelKey.Create(db).Set(labelkey.ID.Set("lb"), labelkey.Name.Set("b")).Save(ctx))
	w1 = must(client.WeightKey.Create(db).Set(weightkey.Name.Set("w1"), weightkey.Owner.Set("la")).Save(ctx))
	w2 = must(client.WeightKey.Create(db).Set(weightkey.ID.Set(50), weightkey.Name.Set("w2"), weightkey.Owner.Set(la.ID), weightkey.Labels.Add("la", lb.ID)).Save(ctx))
	must(client.WeightKey.Update(db).Set(weightkey.Peers.Add(w2.ID)).Where(weightkey.ID.EQ(w1.ID)).Exec(ctx))
	return la, lb, w1, w2
}

// Ids with a named Go type, of a string kind and of an int kind.
func TestKeys_NamedGoTypeIDs(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	la, lb, w1, w2 := namedKeys(t, db)
	assert.Equal(t, types.Label("la"), la.ID)
	assert.NotZero(t, w1.ID, "an id the database assigned, in the named type")
	assert.Equal(t, types.Weight(50), w2.ID)
	assert.Equal(t, []types.Weight{w1.ID, 50}, must(client.WeightKey.Query(db).OrderBy(weightkey.ID.Asc()).IDs(ctx)))
	assert.Equal(t, []types.Label{"la", "lb"}, must(client.LabelKey.Query(db).OrderBy(labelkey.ID.Asc()).IDs(ctx)))
	assert.Equal(t, "w2", must(client.WeightKey.Query(db).Where(weightkey.ID.In(50, 51)).Only(ctx)).Name)

	// O2M / M2O: eager load, traversal, predicates.
	labels := must(client.LabelKey.Query(db).OrderBy(labelkey.ID.Asc()).With(labelkey.Items.Load(client.WeightKey.Query(db).OrderBy(weightkey.ID.Desc()))).All(ctx))
	require.Len(t, labels, 2)
	require.Len(t, labels[0].Edges.Items, 2)
	assert.Equal(t, types.Weight(50), labels[0].Edges.Items[0].ID)
	assert.Empty(t, labels[1].Edges.Items)
	weights := must(client.WeightKey.Query(db).OrderBy(weightkey.ID.Asc()).With(weightkey.Owner).All(ctx))
	assert.Equal(t, "a", weights[0].Edges.Owner.Name)
	assert.Equal(t, "a", weights[1].Edges.Owner.Name)
	assert.Equal(t, 2, must(la.QueryItems(db).Count(ctx)))
	assert.Equal(t, "a", must(w1.QueryOwner(db).Only(ctx)).Name, "the key of the entity Create returned")
	assert.Equal(t, []types.Label{"la"}, must(client.LabelKey.Query(db).Where(labelkey.Items.HasWith(weightkey.ID.EQ(50))).IDs(ctx)))
	assert.Equal(t, 2, must(client.WeightKey.Query(db).Where(weightkey.Owner.HasWith(labelkey.ID.EQ("la"))).Count(ctx)))

	// M2M: traversal, predicates and changes. (Eager load:
	// TestKeys_EdgesM2MEagerLoad.)
	assert.Equal(t, []types.Weight{50}, must(la.QueryWeights(db).IDs(ctx)))
	assert.Equal(t, []types.Label{"la", "lb"}, must(w2.QueryLabels(db).OrderBy(labelkey.ID.Asc()).IDs(ctx)))
	assert.Empty(t, must(w1.QueryLabels(db).All(ctx)))
	assert.Equal(t, []types.Weight{50}, must(w1.QueryPeers(db).IDs(ctx)))
	assert.Equal(t, []types.Weight{w1.ID}, must(w2.QueryPeers(db).IDs(ctx)))
	assert.Equal(t, 2, must(client.LabelKey.Query(db).Where(labelkey.Weights.HasWith(weightkey.ID.EQ(50))).Count(ctx)))
	assert.Equal(t, 2, must(client.WeightKey.Query(db).Where(weightkey.Peers.Has()).Count(ctx)))
	must(client.WeightKey.Update(db).Set(weightkey.Labels.Remove(lb.ID), weightkey.Peers.Clear()).Where(weightkey.ID.EQ(50)).Exec(ctx))
	assert.Equal(t, []types.Label{"la"}, must(w2.QueryLabels(db).IDs(ctx)))
	assert.Empty(t, must(w1.QueryPeers(db).All(ctx)))
}
