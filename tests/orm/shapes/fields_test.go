package shapes

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox/tests/orm/shapes/velox/predicate"

	client "github.com/syssam/velox/tests/orm/shapes/velox"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
	"github.com/syssam/velox/tests/orm/shapes/schema"
	"github.com/syssam/velox/tests/orm/shapes/types"
	"github.com/syssam/velox/tests/orm/shapes/velox/defaultshape"
	"github.com/syssam/velox/tests/orm/shapes/velox/entity"
	"github.com/syssam/velox/tests/orm/shapes/velox/enumshape"
	"github.com/syssam/velox/tests/orm/shapes/velox/errfield"
	"github.com/syssam/velox/tests/orm/shapes/velox/gotypeshape"
	"github.com/syssam/velox/tests/orm/shapes/velox/mixedin"
	"github.com/syssam/velox/tests/orm/shapes/velox/mixinonly"
	"github.com/syssam/velox/tests/orm/shapes/velox/renamed"
	"github.com/syssam/velox/tests/orm/shapes/velox/validated"
)

// invalid asserts err is a validation error of the given schema field.
func invalid(t *testing.T, err error, field string, msgAndArgs ...any) *velox.ValidationError {
	t.Helper()
	var verr *velox.ValidationError
	require.ErrorAs(t, err, &verr, msgAndArgs...)
	assert.Equal(t, field, verr.Field, msgAndArgs...)
	return verr
}

func TestEnum_DefaultsValuesAndRejection(t *testing.T) {
	ctx := context.Background()
	db := open(t)

	// size has no default and is required.
	_, err := client.EnumShape.Create(db).Save(ctx)
	invalid(t, err, "size")

	a := must(client.EnumShape.Create(db).Set(enumshape.Size.Set(enumshape.SizeSmall)).Save(ctx))
	assert.Equal(t, enumshape.StateDraft, a.State)
	assert.Equal(t, enumshape.PhaseInProgress, a.Phase)
	assert.Equal(t, types.TierFree, a.Tier)
	assert.Nil(t, a.Mood)
	assert.Nil(t, a.Grade)
	assert.Equal(t, "S", string(a.Size), "a named value stores its value, not its name")
	assert.Equal(t, "S", rawValue[string](t, db, "SELECT size FROM enum_shapes WHERE id = ?", a.ID))
	assert.Equal(t, "in-progress", rawValue[string](t, db, "SELECT phase FROM enum_shapes WHERE id = ?", a.ID))
	assert.Equal(t, "free", rawValue[string](t, db, "SELECT tier FROM enum_shapes WHERE id = ?", a.ID))

	b := must(client.EnumShape.Create(db).Set(
		enumshape.State.Set(enumshape.StateLive), enumshape.Mood.Set(enumshape.MoodAngry),
		enumshape.Phase.Set(enumshape.PhaseDraft), enumshape.Size.Set(enumshape.SizeLarge),
		enumshape.Tier.Set(types.TierPro), enumshape.Grade.Set(types.TierFree),
	).Save(ctx))
	c := must(client.EnumShape.Create(db).Set(enumshape.Size.Set(enumshape.SizeSmall), enumshape.State.Set(enumshape.StateGone)).Save(ctx))

	rows := must(client.EnumShape.Query(db).OrderBy(enumshape.ID.Asc()).All(ctx))
	require.Len(t, rows, 3)
	assert.Nil(t, rows[0].Mood)
	require.NotNil(t, rows[1].Mood)
	assert.Equal(t, enumshape.MoodAngry, *rows[1].Mood)
	require.NotNil(t, rows[1].Grade)
	assert.Equal(t, types.TierFree, *rows[1].Grade)
	assert.Equal(t, types.TierPro, rows[1].Tier)
	assert.Equal(t, enumshape.PhaseDraft, rows[1].Phase)
	assert.Nil(t, rows[2].Mood, "a NULL after a value is nil again")
	assert.Nil(t, rows[2].Grade)
	assert.Equal(t, enumshape.StateGone, rows[2].State)

	// Invalid values are rejected before any SQL, for both kinds of enum, on
	// create and on update.
	_, err = client.EnumShape.Create(db).Set(enumshape.Size.Set("XL")).Save(ctx)
	invalid(t, err, "size")
	_, err = client.EnumShape.Create(db).Set(enumshape.Size.Set(enumshape.SizeSmall), enumshape.State.Set("")).Save(ctx)
	invalid(t, err, "state")
	_, err = client.EnumShape.Create(db).Set(enumshape.Size.Set(enumshape.SizeSmall), enumshape.Tier.Set("gold")).Save(ctx)
	invalid(t, err, "tier")
	_, err = client.EnumShape.Create(db).Set(enumshape.Size.Set(enumshape.SizeSmall), enumshape.Grade.Set("gold")).Save(ctx)
	invalid(t, err, "grade")
	// The name of a named value is not a value.
	_, err = client.EnumShape.Create(db).Set(enumshape.Size.Set("Small")).Save(ctx)
	invalid(t, err, "size")
	_, err = client.EnumShape.Update(db).Set(enumshape.State.Set("bogus"), enumshape.Size.Set(enumshape.SizeLarge)).Where(enumshape.ID.EQ(a.ID)).Exec(ctx)
	invalid(t, err, "state")
	_, err = client.EnumShape.Update(db).Set(enumshape.Mood.Set("sad")).Where(enumshape.ID.EQ(a.ID)).SaveOne(ctx)
	invalid(t, err, "mood")
	_, err = client.EnumShape.Update(db).Set(enumshape.Tier.Set("gold")).Where(enumshape.ID.EQ(a.ID)).SaveOne(ctx)
	invalid(t, err, "tier")
	assert.Equal(t, 3, must(client.EnumShape.Query(db).Count(ctx)))
	got := must(client.EnumShape.Query(db).Where(enumshape.ID.EQ(a.ID)).Only(ctx))
	assert.Equal(t, enumshape.SizeSmall, got.Size, "a rejected update wrote nothing")

	// Predicates.
	ids := func(ps ...predicate.EnumShape) []int {
		return must(client.EnumShape.Query(db).Where(ps...).OrderBy(enumshape.ID.Asc()).IDs(ctx))
	}
	assert.Equal(t, []int{a.ID}, ids(enumshape.State.EQ(enumshape.StateDraft)))
	assert.Equal(t, []int{b.ID, c.ID}, ids(enumshape.State.NEQ(enumshape.StateDraft)))
	assert.Equal(t, []int{a.ID, c.ID}, ids(enumshape.State.In(enumshape.StateDraft, enumshape.StateGone)))
	assert.Equal(t, []int{b.ID}, ids(enumshape.State.NotIn(enumshape.StateDraft, enumshape.StateGone)))
	assert.Equal(t, []int{a.ID, c.ID}, ids(enumshape.Mood.IsNil()))
	assert.Equal(t, []int{b.ID}, ids(enumshape.Mood.NotNil()))
	assert.Equal(t, []int{b.ID}, ids(enumshape.Tier.EQ(types.TierPro)))
	assert.Equal(t, []int{b.ID}, ids(enumshape.Grade.In(types.TierFree, types.TierPro)))
	assert.Equal(t, []int{b.ID}, ids(enumshape.Phase.EQ(enumshape.PhaseDraft)), "phase=draft is not state=draft")

	// Update and clear.
	got = must(client.EnumShape.Update(db).Set(enumshape.Mood.Clear(), enumshape.Grade.Clear(), enumshape.Phase.Set(enumshape.PhaseOnHold), enumshape.Tier.Set(types.TierFree)).
		Where(enumshape.ID.EQ(b.ID)).SaveOne(ctx))
	assert.Nil(t, got.Mood)
	assert.Nil(t, got.Grade)
	assert.Equal(t, enumshape.PhaseOnHold, got.Phase)
	assert.Equal(t, types.TierFree, got.Tier)
}

func TestGoType_RoundTripValidatorsAndPredicates(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	price := types.Decimal{Cents: 1234}

	// price and pair are required.
	_, err := client.GoTypeShape.Create(db).Set(gotypeshape.Pair.Set(types.Pair{A: 1, B: 2})).Save(ctx)
	invalid(t, err, "price")
	_, err = client.GoTypeShape.Create(db).Set(gotypeshape.Price.Set(price)).Save(ctx)
	invalid(t, err, "pair")

	a := must(client.GoTypeShape.Create(db).Set(gotypeshape.Price.Set(price), gotypeshape.Pair.Set(types.Pair{A: 1, B: 2})).Save(ctx))
	b := must(client.GoTypeShape.Create(db).Set(
		gotypeshape.Label.Set("lab"), gotypeshape.Alias.Set("ali"), gotypeshape.Nick.Set("nick"),
		gotypeshape.Weight.Set(7), gotypeshape.Bonus.Set(10), gotypeshape.Extra.Set(3),
		gotypeshape.Ratio.Set(0.75),
		gotypeshape.Price.Set(types.Decimal{Cents: -5}), gotypeshape.Cost.Set(types.Decimal{Cents: 99}),
		gotypeshape.Fee.Set(types.Decimal{Cents: 250}), gotypeshape.Tax.Set(types.Decimal{}),
		gotypeshape.Pair.Set(types.Pair{A: -3, B: 4}), gotypeshape.PairNil.Set(types.Pair{A: 5, B: 6}),
	).Save(ctx))
	c := must(client.GoTypeShape.Create(db).Set(gotypeshape.Price.Set(types.Decimal{Cents: 100}), gotypeshape.Pair.Set(types.Pair{})).Save(ctx))

	check := func(name string, a, b, c *entity.GoTypeShape) {
		t.Helper()
		// Defaults, through the custom types.
		assert.Equal(t, types.Label("lbl"), a.Label, name)
		assert.Nil(t, a.Alias, name)
		assert.Equal(t, types.Label(""), a.Nick, name)
		assert.Equal(t, types.Weight(1), a.Weight, name)
		assert.Equal(t, types.Weight(0), a.Bonus, name)
		assert.Equal(t, types.Ratio(0.5), a.Ratio, name)
		assert.Equal(t, price, a.Price, name)
		assert.Equal(t, types.Decimal{}, a.Cost, name)
		assert.Nil(t, a.Fee, name)
		assert.Equal(t, types.Decimal{Cents: 150}, a.Tax, name)
		assert.Equal(t, types.Pair{A: 1, B: 2}, a.Pair, name)
		assert.Nil(t, a.PairNil, name)
		// Values.
		assert.Equal(t, types.Label("lab"), b.Label, name)
		require.NotNil(t, b.Alias, name)
		assert.Equal(t, types.Label("ali"), *b.Alias, name)
		assert.Equal(t, types.Label("nick"), b.Nick, name)
		assert.Equal(t, types.Weight(7), b.Weight, name)
		assert.Equal(t, types.Weight(10), b.Bonus, name)
		assert.Equal(t, types.Weight(3), b.Extra, name)
		assert.Equal(t, types.Ratio(0.75), b.Ratio, name)
		assert.Equal(t, types.Decimal{Cents: -5}, b.Price, name)
		assert.Equal(t, types.Decimal{Cents: 99}, b.Cost, name)
		require.NotNil(t, b.Fee, name)
		assert.Equal(t, types.Decimal{Cents: 250}, *b.Fee, name)
		assert.Equal(t, types.Decimal{}, b.Tax, name+": an explicit zero is not replaced by the default")
		assert.Equal(t, types.Pair{A: -3, B: 4}, b.Pair, name)
		require.NotNil(t, b.PairNil, name)
		assert.Equal(t, types.Pair{A: 5, B: 6}, *b.PairNil, name)
		// NULL after a value.
		assert.Nil(t, c.Alias, name)
		assert.Nil(t, c.Fee, name)
		assert.Nil(t, c.PairNil, name)
		assert.Equal(t, types.Decimal{}, c.Cost, name)
		assert.Equal(t, types.Weight(0), c.Extra, name)
		assert.Equal(t, types.Label(""), c.Nick, name)
	}
	check("created", a, b, c)
	rows := must(client.GoTypeShape.Query(db).OrderBy(gotypeshape.ID.Asc()).All(ctx))
	require.Len(t, rows, 3)
	check("queried", rows[0], rows[1], rows[2])

	// The Valuer, not the Go kind, decides what is stored.
	assert.Equal(t, "12.34", rawValue[string](t, db, "SELECT price FROM go_type_shapes WHERE id = ?", a.ID))
	assert.Equal(t, "-0.05", rawValue[string](t, db, "SELECT price FROM go_type_shapes WHERE id = ?", b.ID))
	assert.Equal(t, "1:2", rawValue[string](t, db, "SELECT pair FROM go_type_shapes WHERE id = ?", a.ID))
	assert.Equal(t, "1.50", rawValue[string](t, db, "SELECT tax FROM go_type_shapes WHERE id = ?", a.ID))

	// Validators see the basic value of a custom type, on create and update.
	base := []predicate.GoTypeShapeSetter{gotypeshape.Price.Set(price), gotypeshape.Pair.Set(types.Pair{})}
	for field, bad := range map[string]predicate.GoTypeShapeSetter{
		"label":  gotypeshape.Label.Set(""),
		"alias":  gotypeshape.Alias.Set(""),
		"weight": gotypeshape.Weight.Set(0),
		"bonus":  gotypeshape.Bonus.Set(11),
	} {
		_, err := client.GoTypeShape.Create(db).Set(base...).Set(bad).Save(ctx)
		invalid(t, err, field, "create")
		_, err = client.GoTypeShape.Update(db).Set(gotypeshape.Nick.Set("changed"), bad).Where(gotypeshape.ID.EQ(a.ID)).Exec(ctx)
		invalid(t, err, field, "update")
	}
	_, err = client.GoTypeShape.Create(db).Set(base...).Set(gotypeshape.Label.Set("123456789")).Save(ctx)
	invalid(t, err, "label")
	assert.Equal(t, 3, must(client.GoTypeShape.Query(db).Count(ctx)))
	assert.Zero(t, must(client.GoTypeShape.Query(db).Where(gotypeshape.Nick.EQ("changed")).Count(ctx)), "a rejected update wrote nothing")

	// Predicates take the custom type; substring operators take a string.
	ids := func(ps ...predicate.GoTypeShape) []int {
		return must(client.GoTypeShape.Query(db).Where(ps...).OrderBy(gotypeshape.ID.Asc()).IDs(ctx))
	}
	assert.Equal(t, []int{b.ID}, ids(gotypeshape.Label.EQ(types.Label("lab"))))
	assert.Equal(t, []int{a.ID, c.ID}, ids(gotypeshape.Label.In("lbl", "zzz")))
	assert.Equal(t, []int{a.ID, b.ID, c.ID}, ids(gotypeshape.Label.HasPrefix("l")))
	assert.Equal(t, []int{b.ID}, ids(gotypeshape.Alias.NotNil()))
	assert.Equal(t, []int{b.ID}, ids(gotypeshape.Weight.GT(types.Weight(1))))
	assert.Equal(t, []int{b.ID}, ids(gotypeshape.Ratio.GT(0.5)))
	assert.Equal(t, []int{a.ID}, ids(gotypeshape.Price.EQ(price)))
	assert.Equal(t, []int{a.ID, c.ID}, ids(gotypeshape.Price.In(price, types.Decimal{Cents: 100})))
	assert.Equal(t, []int{b.ID}, ids(gotypeshape.Fee.EQ(types.Decimal{Cents: 250})))
	assert.Equal(t, []int{a.ID, c.ID}, ids(gotypeshape.Fee.IsNil()))
	assert.Equal(t, []int{b.ID}, ids(gotypeshape.Pair.EQ(types.Pair{A: -3, B: 4})))
	assert.Equal(t, []int{a.ID, c.ID}, ids(gotypeshape.PairNil.IsNil()))

	// Update: set, add, clear.
	got := must(client.GoTypeShape.Update(db).Set(
		gotypeshape.Weight.Add(2), gotypeshape.Label.Set("new"), gotypeshape.Alias.Clear(), gotypeshape.Fee.Clear(),
		gotypeshape.PairNil.Clear(), gotypeshape.Price.Set(types.Decimal{Cents: 1}), gotypeshape.Pair.Set(types.Pair{A: 9, B: 9}),
	).Where(gotypeshape.ID.EQ(b.ID)).SaveOne(ctx))
	assert.Equal(t, types.Weight(9), got.Weight)
	assert.Equal(t, types.Label("new"), got.Label)
	assert.Nil(t, got.Alias)
	assert.Nil(t, got.Fee)
	assert.Nil(t, got.PairNil)
	assert.Equal(t, types.Decimal{Cents: 1}, got.Price)
	assert.Equal(t, types.Pair{A: 9, B: 9}, got.Pair)
}

func TestDefaults_AppliedOnlyWhenUnset(t *testing.T) {
	ctx := context.Background()
	db := open(t)

	// code has no default.
	_, err := client.DefaultShape.Create(db).Set(defaultshape.Name.Set("x")).Save(ctx)
	invalid(t, err, "code")

	before := schema.DefaultCalls.Load()
	start := time.Now().Add(-time.Second)
	a := must(client.DefaultShape.Create(db).Set(defaultshape.Name.Set("a"), defaultshape.Code.Set("ca")).Save(ctx))
	assert.Equal(t, before+1, schema.DefaultCalls.Load(), "the default func runs once per row")

	checkDefaults := func(name string, a *entity.DefaultShape) {
		t.Helper()
		assert.True(t, a.CreatedAt.After(start), name)
		assert.True(t, a.UpdatedAt.After(start), name)
		assert.Equal(t, 1, a.Revision, name)
		assert.Equal(t, "n/a", a.Note, name)
		assert.Equal(t, "opt", a.OptNote, name)
		require.NotNil(t, a.NilNote, name)
		assert.Equal(t, "nil", *a.NilNote, name)
		assert.True(t, a.Active, name)
		assert.Equal(t, 0.5, a.Ratio, name)
		assert.Equal(t, float32(0.25), a.Ratio32, name)
		assert.Equal(t, int8(7), a.Tiny, name)
		assert.Equal(t, int64(1<<40), a.Big, name)
		assert.Equal(t, uint32(0xff), a.Mask, name)
		assert.Equal(t, []byte("dflt"), a.Blob, name)
		assert.NotEqual(t, uuid.Nil, a.UID, name)
		assert.True(t, strings.HasPrefix(a.Token, "tok-"), name)
		assert.Equal(t, 42, a.Counter, name)
		assert.Equal(t, []string{"a", "b"}, a.Tags, name)
		assert.Zero(t, a.Touched, name+": an update default does not run on create")
		assert.Nil(t, a.Seen, name)
	}
	checkDefaults("created", a)
	got := must(client.DefaultShape.Query(db).Where(defaultshape.ID.EQ(a.ID)).Only(ctx))
	checkDefaults("queried", got)
	assert.Equal(t, a.UID, got.UID, "the entity Create returns holds the value that was written")
	assert.Equal(t, a.Token, got.Token)
	assert.True(t, a.CreatedAt.Equal(got.CreatedAt), "%v != %v", a.CreatedAt, got.CreatedAt)

	// Explicit values win, including zero values, and the default funcs do
	// not run.
	before = schema.DefaultCalls.Load()
	uid := uuid.MustParse("44444444-4444-4444-8444-444444444444")
	b := must(client.DefaultShape.Create(db).Set(
		defaultshape.Name.Set("b"), defaultshape.Code.Set("cb"),
		defaultshape.CreatedAt.Set(t0), defaultshape.UpdatedAt.Set(t0),
		defaultshape.Revision.Set(0), defaultshape.Note.Set(""), defaultshape.OptNote.Set(""), defaultshape.NilNote.Set(""),
		defaultshape.Active.Set(false), defaultshape.Ratio.Set(0), defaultshape.Ratio32.Set(0), defaultshape.Tiny.Set(0),
		defaultshape.Big.Set(0), defaultshape.Mask.Set(0), defaultshape.Blob.Set([]byte{}), defaultshape.UID.Set(uid),
		defaultshape.Token.Set(""), defaultshape.Counter.Set(0), defaultshape.Tags.Set([]string{}),
	).Save(ctx))
	assert.Equal(t, before, schema.DefaultCalls.Load(), "a default func does not run for a field that was set")
	for name, b := range map[string]*entity.DefaultShape{"created": b, "queried": must(client.DefaultShape.Query(db).Where(defaultshape.ID.EQ(b.ID)).Only(ctx))} {
		assert.True(t, b.CreatedAt.Equal(t0), name)
		assert.True(t, b.UpdatedAt.Equal(t0), name)
		assert.Zero(t, b.Revision, name)
		assert.Zero(t, b.Note, name)
		assert.Zero(t, b.OptNote, name)
		require.NotNil(t, b.NilNote, name)
		assert.Zero(t, *b.NilNote, name)
		assert.False(t, b.Active, name)
		assert.Zero(t, b.Ratio, name)
		assert.Zero(t, b.Ratio32, name)
		assert.Zero(t, b.Tiny, name)
		assert.Zero(t, b.Big, name)
		assert.Zero(t, b.Mask, name)
		assert.Empty(t, b.Blob, name)
		assert.Equal(t, uid, b.UID, name)
		assert.Zero(t, b.Token, name)
		assert.Zero(t, b.Counter, name)
		assert.Empty(t, b.Tags, name)
	}

	// Each row of a bulk create gets its own default-func values.
	rows := must(client.DefaultShape.CreateBulk(db,
		client.DefaultShape.Create(db).Set(defaultshape.Name.Set("d"), defaultshape.Code.Set("cd")),
		client.DefaultShape.Create(db).Set(defaultshape.Name.Set("e"), defaultshape.Code.Set("ce"), defaultshape.Token.Set("mine")),
		client.DefaultShape.Create(db).Set(defaultshape.Name.Set("f"), defaultshape.Code.Set("cf")),
	).Save(ctx))
	require.Len(t, rows, 3)
	assert.NotEqual(t, rows[0].Token, rows[2].Token)
	assert.Equal(t, "mine", rows[1].Token)
	assert.NotEqual(t, rows[0].UID, rows[2].UID)
	for i, r := range rows {
		got := must(client.DefaultShape.Query(db).Where(defaultshape.ID.EQ(r.ID)).Only(ctx))
		assert.Equal(t, r.Token, got.Token, "row %d", i)
		assert.Equal(t, r.UID, got.UID, "row %d", i)
		assert.Equal(t, "n/a", got.Note, "row %d", i)
		assert.Equal(t, []string{"a", "b"}, got.Tags, "row %d", i)
	}
}

func TestDefaults_UpdateDefaultAndImmutable(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	a := must(client.DefaultShape.Create(db).Set(defaultshape.Name.Set("a"), defaultshape.Code.Set("ca"), defaultshape.UpdatedAt.Set(t0)).Save(ctx))
	b := must(client.DefaultShape.Create(db).Set(defaultshape.Name.Set("b"), defaultshape.Code.Set("cb"), defaultshape.UpdatedAt.Set(t0)).Save(ctx))

	// Not set: the update defaults run.
	got := must(client.DefaultShape.Update(db).Set(defaultshape.Name.Set("a2")).Where(defaultshape.ID.EQ(a.ID)).SaveOne(ctx))
	assert.Equal(t, "a2", got.Name)
	assert.Equal(t, schema.DefaultShapeRevision, got.Revision)
	assert.True(t, got.UpdatedAt.After(t0), "updated_at = %v", got.UpdatedAt)
	assert.Equal(t, int64(77), got.Touched)
	require.NotNil(t, got.Seen)
	assert.True(t, got.Seen.After(t0))
	assert.True(t, got.CreatedAt.Equal(a.CreatedAt), "created_at has no update default")
	// The other row did not move.
	other := must(client.DefaultShape.Query(db).Where(defaultshape.ID.EQ(b.ID)).Only(ctx))
	assert.Equal(t, 1, other.Revision)
	assert.True(t, other.UpdatedAt.Equal(t0))
	assert.Zero(t, other.Touched)
	assert.Nil(t, other.Seen)

	// Set, added or cleared: the update default of that column does not run.
	t1 := t0.Add(48 * time.Hour)
	got = must(client.DefaultShape.Update(db).Set(
		defaultshape.Revision.Set(5), defaultshape.UpdatedAt.Set(t1), defaultshape.Touched.Set(0), defaultshape.Seen.Clear(),
	).Where(defaultshape.ID.EQ(a.ID)).SaveOne(ctx))
	assert.Equal(t, 5, got.Revision)
	assert.True(t, got.UpdatedAt.Equal(t1), "updated_at = %v", got.UpdatedAt)
	assert.Zero(t, got.Touched, "an explicit zero is not replaced by the update default")
	assert.Nil(t, got.Seen)
	assert.Equal(t, 1, rawValue[int](t, db, "SELECT seen IS NULL FROM default_shapes WHERE id = ?", a.ID))
	got = must(client.DefaultShape.Update(db).Set(defaultshape.Revision.Add(2), defaultshape.Touched.Add(1)).Where(defaultshape.ID.EQ(a.ID)).SaveOne(ctx))
	assert.Equal(t, 7, got.Revision, "Add on a column with an update default adds to the stored value")
	assert.Equal(t, int64(1), got.Touched)

	// Exec over several rows applies the update defaults to each.
	n := must(client.DefaultShape.Update(db).Set(defaultshape.Note.Set("bulk")).Exec(ctx))
	assert.Equal(t, 2, n)
	for _, r := range must(client.DefaultShape.Query(db).All(ctx)) {
		assert.Equal(t, schema.DefaultShapeRevision, r.Revision)
		assert.Equal(t, "bulk", r.Note)
		assert.Equal(t, int64(77), r.Touched)
	}

	// Immutable fields are rejected on update, and the rest of the update is
	// not written.
	_, err := client.DefaultShape.Update(db).Set(defaultshape.Note.Set("never"), defaultshape.Code.Set("zz")).Where(defaultshape.ID.EQ(a.ID)).Exec(ctx)
	invalid(t, err, "code")
	_, err = client.DefaultShape.Update(db).Set(defaultshape.CreatedAt.Set(t0)).Where(defaultshape.ID.EQ(a.ID)).SaveOne(ctx)
	invalid(t, err, "created_at")
	_, err = client.DefaultShape.Update(db).Set(defaultshape.Code.Set("ca")).Where(defaultshape.ID.EQ(a.ID)).SaveOne(ctx)
	invalid(t, err, "code", "even the same value")
	got = must(client.DefaultShape.Query(db).Where(defaultshape.ID.EQ(a.ID)).Only(ctx))
	assert.Equal(t, "ca", got.Code)
	assert.Equal(t, "bulk", got.Note)
	assert.True(t, got.CreatedAt.Equal(a.CreatedAt))
}

func TestValidators_CreateAndUpdate(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	valid := func() []predicate.ValidatedSetter {
		return []predicate.ValidatedSetter{
			validated.One.Set("1"), validated.Two.Set("22"), validated.Three.Set("abc"),
			validated.Score.Set(50), validated.Rank.Set(5), validated.Even.Set(2),
		}
	}
	a := must(client.Validated.Create(db).Set(valid()...).Save(ctx))
	assert.Nil(t, a.Opt)
	// An unset Optional field is stored as its zero value and that value is
	// not validated, even where the validator would reject it.
	for name, r := range map[string]*entity.Validated{"created": a, "queried": must(client.Validated.Query(db).Only(ctx))} {
		assert.Zero(t, r.Ratio, name)
		assert.Zero(t, r.OptPos, name)
		assert.Zero(t, r.OptNe, name)
		assert.Empty(t, r.OptRaw, name)
	}
	assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM validateds WHERE opt_pos = 0 AND opt_ne = '' AND length(opt_raw) = 0 AND ratio = 0"))

	cases := []struct {
		field string
		set   predicate.ValidatedSetter
	}{
		{"one", validated.One.Set("")},
		{"two", validated.Two.Set("")},       // first validator
		{"two", validated.Two.Set("123456")}, // second validator
		{"three", validated.Three.Set("a")},
		{"three", validated.Three.Set("abcdefg")},
		{"three", validated.Three.Set("AB1")},
		{"score", validated.Score.Set(-1)},
		{"score", validated.Score.Set(101)},
		{"rank", validated.Rank.Set(0)},
		{"rank", validated.Rank.Set(11)},
		{"even", validated.Even.Set(3)},
		{"ratio", validated.Ratio.Set(1.5)},
		{"ratio", validated.Ratio.Set(-0.1)},
		{"i8", validated.I8.Set(0)},
		{"i16", validated.I16.Set(-6)},
		{"i32", validated.I32.Set(-1)},
		{"i64", validated.I64.Set(1001)},
		{"u", validated.U.Set(10)},
		{"u8", validated.U8.Set(0)},
		{"u64", validated.U64.Set(6)},
		{"f32", validated.F32.Set(0)},
		{"raw", validated.Raw.Set([]byte{})},
		{"raw", validated.Raw.Set([]byte("12345"))},
		{"opt", validated.Opt.Set("ab")},
		// The zero value is rejected when it is SET.
		{"opt_pos", validated.OptPos.Set(0)},
		{"opt_ne", validated.OptNe.Set("")},
		{"opt_raw", validated.OptRaw.Set([]byte{})},
	}
	for _, c := range cases {
		_, err := client.Validated.Create(db).Set(valid()...).Set(c.set).Save(ctx)
		invalid(t, err, c.field, "create %s", c.field)
		// On update, next to a valid assignment that must not be written.
		_, err = client.Validated.Update(db).Set(validated.One.Set("changed"), c.set).Where(validated.ID.EQ(a.ID)).Exec(ctx)
		invalid(t, err, c.field, "update/exec %s", c.field)
		_, err = client.Validated.Update(db).Set(validated.Rank.Set(7), c.set).Where(validated.ID.EQ(a.ID)).SaveOne(ctx)
		invalid(t, err, c.field, "update/save %s", c.field)
		// In a bulk create, as the middle row.
		_, err = client.Validated.CreateBulk(db, client.Validated.Create(db).Set(valid()...), client.Validated.Create(db).Set(valid()...).Set(c.set), client.Validated.Create(db).Set(valid()...)).Save(ctx)
		invalid(t, err, c.field, "bulk %s", c.field)
	}
	assert.Equal(t, 1, must(client.Validated.Query(db).Count(ctx)), "rejected creates wrote nothing")
	only := must(client.Validated.Query(db).Only(ctx))
	assert.Equal(t, "1", only.One, "rejected updates wrote nothing")
	assert.Equal(t, 5, only.Rank)
	// The last setter of a column is the one that counts, and the one that is
	// validated: an invalid value that is overridden is not an error.
	only = must(client.Validated.Update(db).Set(validated.One.Set(""), validated.One.Set("ok")).Where(validated.ID.EQ(a.ID)).SaveOne(ctx))
	assert.Equal(t, "ok", only.One)
	must(client.Validated.Update(db).Set(validated.One.Set("1")).Where(validated.ID.EQ(a.ID)).Exec(ctx))

	// The schema's own error is reachable.
	_, err := client.Validated.Create(db).Set(valid()...).Set(validated.Even.Set(7)).Save(ctx)
	require.ErrorIs(t, err, schema.ErrOdd)

	// The boundary values pass, on create and on update.
	edge := []predicate.ValidatedSetter{
		validated.Two.Set("12345"), validated.Three.Set("ab"), validated.Score.Set(0), validated.Rank.Set(10),
		validated.Ratio.Set(1), validated.I8.Set(1), validated.I16.Set(-5), validated.I32.Set(0), validated.I64.Set(1000),
		validated.U.Set(9), validated.U8.Set(1), validated.U64.Set(5), validated.F32.Set(0.5),
		validated.Raw.Set([]byte("1234")), validated.Opt.Set("abc"),
	}
	b := must(client.Validated.Create(db).Set(valid()...).Set(edge...).Save(ctx))
	assert.Equal(t, "abc", *b.Opt)
	got := must(client.Validated.Update(db).Set(edge...).Set(validated.Score.Set(100)).Where(validated.ID.EQ(a.ID)).SaveOne(ctx))
	assert.Equal(t, 100, got.Score)
	assert.Equal(t, int16(-5), got.I16)
	assert.Equal(t, uint64(5), got.U64)
	assert.Equal(t, []byte("1234"), got.Raw)
	// Clearing a validated nillable field is not validated.
	got = must(client.Validated.Update(db).Set(validated.Opt.Clear()).Where(validated.ID.EQ(a.ID)).SaveOne(ctx))
	assert.Nil(t, got.Opt)
	// An update that does not name the zero-filled fields leaves them alone
	// and does not validate what is stored.
	got = must(client.Validated.Update(db).Set(validated.Score.Set(1)).Where(validated.ID.EQ(a.ID)).SaveOne(ctx))
	assert.Zero(t, got.OptPos)
	got = must(client.Validated.Update(db).Set(validated.OptPos.Set(3), validated.OptNe.Set("x"), validated.OptRaw.Set([]byte("r"))).Where(validated.ID.EQ(a.ID)).SaveOne(ctx))
	assert.Equal(t, 3, got.OptPos)
}

// Fields named like the locals of the generated validator and default wiring.
func TestValidators_FieldsNamedLikeGeneratedLocals(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	_, err := client.ErrField.Create(db).Save(ctx)
	invalid(t, err, "err")
	a := must(client.ErrField.Create(db).Set(errfield.Err.Set("e")).Save(ctx))
	assert.Equal(t, 1, a.Errs)
	assert.Equal(t, "fn", a.Fn)
	assert.Equal(t, "fns", a.Fns)
	assert.Equal(t, "def", a.Def)
	assert.Equal(t, "c", a.Checks)
	assert.Equal(t, "m", a.M)
	assert.Equal(t, "d", a.D)
	assert.Equal(t, "f", a.F)
	assert.Equal(t, "v", a.V)
	assert.Equal(t, "fs", a.Fields)
	assert.Equal(t, "mx", a.Mixin)
	for field, bad := range map[string]predicate.ErrFieldSetter{
		"err": errfield.Err.Set("toolong"), "errs": errfield.Errs.Set(10), "fn": errfield.Fn.Set(""), "fns": errfield.Fns.Set("123456"),
		"def": errfield.Def.Set(""), "checks": errfield.Checks.Set(""), "m": errfield.M.Set(""), "d": errfield.D.Set(""),
		"f": errfield.F.Set(""), "v": errfield.V.Set(""), "fields": errfield.Fields.Set(""), "mixin": errfield.Mixin.Set(""),
	} {
		_, err := client.ErrField.Create(db).Set(errfield.Err.Set("e"), bad).Save(ctx)
		invalid(t, err, field, "create")
		_, err = client.ErrField.Update(db).Set(bad).Where(errfield.ID.EQ(a.ID)).Exec(ctx)
		invalid(t, err, field, "update")
	}
}

func TestMixins_DefaultsValidatorsAndUpdateDefaults(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	start := time.Now().Add(-time.Second)

	_, err := client.MixedIn.Create(db).Save(ctx)
	invalid(t, err, "name")
	a := must(client.MixedIn.Create(db).Set(mixedin.Name.Set("a")).Save(ctx))
	for name, a := range map[string]*entity.MixedIn{"created": a, "queried": must(client.MixedIn.Query(db).Only(ctx))} {
		assert.True(t, a.CreatedAt.After(start), name)
		assert.True(t, a.UpdatedAt.After(start), name)
		assert.Equal(t, "system", a.CreatedBy, name)
		assert.Equal(t, 1, a.Version, name)
		assert.False(t, a.Archived, name)
		assert.Nil(t, a.ArchivedAt, name)
		assert.Zero(t, a.Reason, name)
		assert.Equal(t, "slug", a.Slug, name)
		assert.True(t, a.Due.Equal(time.Unix(1000, 0)), name)
	}

	// Each validator is the one of its own descriptor: a mixin's field at
	// index 0 and the schema's own field at index 0 are different fields.
	for field, bad := range map[string]predicate.MixedInSetter{
		"name":       mixedin.Name.Set(""),
		"created_by": mixedin.CreatedBy.Set(""),
		"reason":     mixedin.Reason.Set("12345"),
		"slug":       mixedin.Slug.Set("1234567"),
	} {
		_, err := client.MixedIn.Create(db).Set(mixedin.Name.Set("x"), bad).Save(ctx)
		invalid(t, err, field, "create")
		_, err = client.MixedIn.Update(db).Set(bad).Where(mixedin.ID.EQ(a.ID)).Exec(ctx)
		invalid(t, err, field, "update")
	}
	// What one validator rejects, the fields next to it accept.
	b := must(client.MixedIn.Create(db).Set(mixedin.Name.Set("1234567"), mixedin.CreatedBy.Set("12345"), mixedin.Reason.Set("1234"), mixedin.Slug.Set("123456")).Save(ctx))
	assert.Equal(t, "1234", b.Reason)

	rawExec(t, db, "UPDATE mixed_ins SET updated_at = ? WHERE id = ?", t0, a.ID)
	got := must(client.MixedIn.Update(db).Set(mixedin.Archived.Set(true), mixedin.ArchivedAt.Set(t0)).Where(mixedin.ID.EQ(a.ID)).SaveOne(ctx))
	assert.True(t, got.Archived)
	require.NotNil(t, got.ArchivedAt)
	assert.True(t, got.ArchivedAt.Equal(t0))
	assert.Equal(t, 2, got.Version, "the mixin's update default")
	assert.True(t, got.UpdatedAt.After(start), "updated_at = %v", got.UpdatedAt)
	_, err = client.MixedIn.Update(db).Set(mixedin.CreatedAt.Set(t0)).Where(mixedin.ID.EQ(a.ID)).Exec(ctx)
	invalid(t, err, "created_at")

	// An entity whose every field comes from a mixin and has a default is
	// created without a single setter.
	m := must(client.MixinOnly.Create(db).Save(ctx))
	require.NotZero(t, m.ID)
	assert.Equal(t, "system", m.CreatedBy)
	assert.Equal(t, 1, m.Version)
	_, err = client.MixinOnly.Create(db).Set(mixinonly.CreatedBy.Set("")).Save(ctx)
	invalid(t, err, "created_by")
	m2 := must(client.MixinOnly.Update(db).Set(mixinonly.CreatedBy.Set("me")).Where(mixinonly.ID.EQ(m.ID)).SaveOne(ctx))
	assert.Equal(t, 2, m2.Version)
}

// Columns and a table that are not named after the field and the entity.
func TestRenamed_StorageKeys(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	a := must(client.Renamed.Create(db).Save(ctx))
	assert.Equal(t, "untitled", a.Title)
	assert.Nil(t, a.Count)
	assert.Equal(t, renamed.KindA, a.Kind)
	b := must(client.Renamed.Create(db).Set(renamed.Title.Set("b"), renamed.Count.Set(3), renamed.Kind.Set(renamed.KindB), renamed.Secret.Set("hunter2")).Save(ctx))

	assert.Equal(t, "b", rawValue[string](t, db, "SELECT ttl FROM "+renamed.Table+" WHERE cnt = 3 AND knd = 'b'"))

	// Errors name the schema field, not the column.
	_, err := client.Renamed.Create(db).Set(renamed.Title.Set("")).Save(ctx)
	invalid(t, err, "title")
	_, err = client.Renamed.Update(db).Set(renamed.Kind.Set("z")).Where(renamed.ID.EQ(a.ID)).Exec(ctx)
	invalid(t, err, "kind")

	rows := must(client.Renamed.Query(db).Where(renamed.Title.NEQ("zzz"), renamed.Kind.In(renamed.KindA, renamed.KindB)).OrderBy(renamed.Title.Desc()).All(ctx))
	require.Len(t, rows, 2)
	assert.Equal(t, a.ID, rows[0].ID)
	assert.Equal(t, b.ID, rows[1].ID)
	require.NotNil(t, rows[1].Count)
	assert.Equal(t, 3, *rows[1].Count)
	assert.Equal(t, "hunter2", rows[1].Secret)
	assert.True(t, rows[1].SeenAt.IsZero())

	got := must(client.Renamed.Update(db).Set(renamed.Count.Add(4), renamed.Title.Set("b2")).Where(renamed.Count.EQ(3)).SaveOne(ctx))
	assert.Equal(t, 7, *got.Count)
	assert.False(t, got.SeenAt.IsZero(), "the update default of a renamed column")
	got = must(client.Renamed.Update(db).Set(renamed.Count.Clear()).Where(renamed.ID.EQ(b.ID)).SaveOne(ctx))
	assert.Nil(t, got.Count)

	// A sensitive field does not reach JSON.
	out, err := json.Marshal(rows[1])
	require.NoError(t, err)
	assert.NotContains(t, string(out), "hunter2")
}
