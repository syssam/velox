// Package matrix is the schema-shape matrix: every test here pins the runtime
// behaviour of one schema shape in the generated project under ./velox. A
// test must fail when its feature silently does nothing — asserting that a
// value was stored, or that the code compiles, is not enough.
package matrix

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
	"github.com/syssam/velox/privacy"

	"example.com/matrix/schema"
	"example.com/matrix/types"
	gen "example.com/matrix/velox"
	"example.com/matrix/velox/enrollment"
	"example.com/matrix/velox/entity"
	"example.com/matrix/velox/enumshape"
	"example.com/matrix/velox/hookonly"
	"example.com/matrix/velox/mixinonly"
	"example.com/matrix/velox/policyonly"
	"example.com/matrix/velox/tenantdoc"
	"example.com/matrix/velox/validated"
)

func stamp(prefix string, op velox.Op) string { return prefix + ":" + op.String() }

// Shape 1: a schema-level Policy() is the entity's only runtime code.
func TestPolicyOnly_SchemaPolicyGuardsQueriesAndMutations(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()
	deny := schema.DenyContext(ctx)

	row, err := client.PolicyOnly.Create().SetName("a").Save(ctx)
	require.NoError(t, err, "an allowed create")
	got, err := client.PolicyOnly.Query().Where(policyonly.NameField.EQ("a")).Only(ctx)
	require.NoError(t, err, "an allowed query")
	assert.Equal(t, row.ID, got.ID)

	_, err = client.PolicyOnly.Query().All(deny)
	require.ErrorIs(t, err, privacy.Deny, "a denied query")
	_, err = client.PolicyOnly.Query().Count(deny)
	require.ErrorIs(t, err, privacy.Deny, "a denied count")
	_, err = client.PolicyOnly.Get(deny, row.ID)
	require.ErrorIs(t, err, privacy.Deny, "a denied get")

	_, err = client.PolicyOnly.Create().SetName("b").Save(deny)
	require.ErrorIs(t, err, privacy.Deny, "a denied create")
	_, err = client.PolicyOnly.CreateBulk(client.PolicyOnly.Create().SetName("c")).Save(deny)
	require.ErrorIs(t, err, privacy.Deny, "a denied bulk create")
	_, err = client.PolicyOnly.Update().SetName("x").Save(deny)
	require.ErrorIs(t, err, privacy.Deny, "a denied bulk update")
	_, err = client.PolicyOnly.UpdateOneID(row.ID).SetName("x").Save(deny)
	require.ErrorIs(t, err, privacy.Deny, "a denied update-one Save")
	err = client.PolicyOnly.UpdateOneID(row.ID).SetName("x").Exec(deny)
	require.ErrorIs(t, err, privacy.Deny, "a denied update-one Exec")
	_, err = client.PolicyOnly.Delete().Exec(deny)
	require.ErrorIs(t, err, privacy.Deny, "a denied bulk delete")
	err = client.PolicyOnly.DeleteOneID(row.ID).Exec(deny)
	require.ErrorIs(t, err, privacy.Deny, "a denied delete-one")

	names, err := client.PolicyOnly.Query().Select(policyonly.FieldName).Strings(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, names, "no denied mutation may have written")
}

// Shape 2: schema-level Hooks() is the entity's only runtime code.
func TestHookOnly_SchemaHookRunsOnEveryMutationPath(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()
	runs := func() int64 { return schema.HookOnlyRuns.Load() }
	reload := func(id int) string { return client.HookOnly.GetX(ctx, id).Stamp }

	before := runs()
	one, err := client.HookOnly.Create().SetName("one").Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, before+1, runs(), "Create")
	assert.Equal(t, stamp("schema", velox.OpCreate), one.Stamp)
	assert.Equal(t, stamp("schema", velox.OpCreate), reload(one.ID))

	before = runs()
	bulk, err := client.HookOnly.CreateBulk(
		client.HookOnly.Create().SetName("b1"),
		client.HookOnly.Create().SetName("b2"),
	).Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, before+2, runs(), "CreateBulk runs the hook once per row")
	for _, b := range bulk {
		assert.Equal(t, stamp("schema", velox.OpCreate), reload(b.ID), "CreateBulk row %s", b.Name)
	}

	before = runs()
	n, err := client.HookOnly.Update().Where(hookonly.NameField.EQ("b1")).SetName("b1x").Save(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	assert.Equal(t, before+1, runs(), "Update")
	assert.Equal(t, stamp("schema", velox.OpUpdate), reload(bulk[0].ID))
	assert.Equal(t, stamp("schema", velox.OpCreate), reload(bulk[1].ID), "the row the update did not match")

	before = runs()
	saved, err := client.HookOnly.UpdateOneID(one.ID).SetName("one-saved").Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, before+1, runs(), "UpdateOne.Save")
	assert.Equal(t, stamp("schema", velox.OpUpdateOne), saved.Stamp)

	// Reset the column so the Exec path has to write it again.
	_, err = client.ExecContext(ctx, "UPDATE hook_onlies SET stamp = 'reset' WHERE id = ?", one.ID)
	require.NoError(t, err)
	require.Equal(t, "reset", reload(one.ID))
	before = runs()
	require.NoError(t, client.HookOnly.UpdateOneID(one.ID).SetName("one-exec").Exec(ctx))
	assert.Equal(t, before+1, runs(), "UpdateOne.Exec")
	assert.Equal(t, stamp("schema", velox.OpUpdateOne), reload(one.ID))
	assert.Equal(t, "one-exec", client.HookOnly.GetX(ctx, one.ID).Name)

	before = runs()
	require.NoError(t, client.HookOnly.DeleteOneID(one.ID).Exec(ctx))
	assert.Equal(t, before+1, runs(), "DeleteOne")
}

// Shape 3: hooks and policy come only from a mixin.
func TestMixinOnly_HookAndPolicyComeFromMixin(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()
	deny := schema.DenyContext(ctx)

	before := schema.MixinRuns.Load()
	row, err := client.MixinOnly.Create().SetName("m").Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, before+1, schema.MixinRuns.Load())
	assert.Equal(t, stamp("mixin", velox.OpCreate), client.MixinOnly.GetX(ctx, row.ID).Stamp)

	require.NoError(t, client.MixinOnly.UpdateOneID(row.ID).SetName("m2").Exec(ctx))
	assert.Equal(t, stamp("mixin", velox.OpUpdateOne), client.MixinOnly.GetX(ctx, row.ID).Stamp)
	_, err = client.MixinOnly.Update().SetName("m3").Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, stamp("mixin", velox.OpUpdate), client.MixinOnly.GetX(ctx, row.ID).Stamp)
	assert.Equal(t, before+3, schema.MixinRuns.Load())

	_, err = client.MixinOnly.Query().Where(mixinonly.NameField.EQ("m3")).All(deny)
	require.ErrorIs(t, err, privacy.Deny, "the mixin's query policy")
	_, err = client.MixinOnly.Create().SetName("no").Save(deny)
	require.ErrorIs(t, err, privacy.Deny, "the mixin's mutation policy on create")
	err = client.MixinOnly.UpdateOneID(row.ID).SetName("no").Exec(deny)
	require.ErrorIs(t, err, privacy.Deny, "the mixin's mutation policy on update")
	assert.Equal(t, before+3, schema.MixinRuns.Load(), "a denied mutation must not reach the hook")
	assert.Equal(t, "m3", client.MixinOnly.GetX(ctx, row.ID).Name)
}

func tenantCtx(tenant string) context.Context {
	return privacy.WithViewer(context.Background(), &privacy.SimpleViewer{UserID: "u", UserTenant: tenant})
}

// Shape 4: a row filter scopes reads, bulk writes and single-row writes.
func TestTenantDoc_RowFilterScopesReadsAndWrites(t *testing.T) {
	client := openClient(t)
	acme, globex := tenantCtx("acme"), tenantCtx("globex")

	seed := func(ctx context.Context, tenant, title string) *entity.TenantDoc {
		d, err := client.TenantDoc.Create().SetTenantID(tenant).SetTitle(title).Save(ctx)
		require.NoError(t, err)
		return d
	}
	a1, a2 := seed(acme, "acme", "a1"), seed(acme, "acme", "a2")
	g1, g2 := seed(globex, "globex", "g1"), seed(globex, "globex", "g2")
	titles := func(ctx context.Context) []string {
		got, err := client.TenantDoc.Query().Order(tenantdoc.ByID()).Select(tenantdoc.FieldTitle).Strings(ctx)
		require.NoError(t, err)
		return got
	}

	_, err := client.TenantDoc.Query().All(context.Background())
	require.ErrorIs(t, err, privacy.Deny, "a read with no viewer")

	// Reads.
	assert.Equal(t, []string{"a1", "a2"}, titles(acme))
	assert.Equal(t, []string{"g1", "g2"}, titles(globex))
	n, err := client.TenantDoc.Query().Count(acme)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	_, err = client.TenantDoc.Get(acme, g1.ID)
	require.True(t, gen.IsNotFound(err), "Get of another tenant's row: %v", err)
	ok, err := client.TenantDoc.Query().Where(tenantdoc.IDField.EQ(g1.ID)).Exist(acme)
	require.NoError(t, err)
	assert.False(t, ok)

	// Bulk update with no Where of its own.
	n, err = client.TenantDoc.Update().SetTitle("A").Save(acme)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, []string{"A", "A"}, titles(acme))
	assert.Equal(t, []string{"g1", "g2"}, titles(globex), "bulk update crossed the tenant")

	// UpdateOne on an out-of-scope row: NotFound, nothing written.
	err = client.TenantDoc.UpdateOneID(g1.ID).SetTitle("stolen").Exec(acme)
	require.True(t, gen.IsNotFound(err), "UpdateOne.Exec out of scope: %v", err)
	_, err = client.TenantDoc.UpdateOneID(g2.ID).SetTitle("stolen").Save(acme)
	require.True(t, gen.IsNotFound(err), "UpdateOne.Save out of scope: %v", err)
	assert.Equal(t, []string{"g1", "g2"}, titles(globex))
	// ...and in scope it works.
	require.NoError(t, client.TenantDoc.UpdateOneID(a1.ID).SetTitle("a1b").Exec(acme))
	assert.Equal(t, []string{"a1b", "A"}, titles(acme))

	// Deletes.
	err = client.TenantDoc.DeleteOneID(g1.ID).Exec(acme)
	require.True(t, gen.IsNotFound(err), "DeleteOne out of scope: %v", err)
	n, err = client.TenantDoc.Delete().Exec(acme)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Empty(t, titles(acme))
	assert.Equal(t, []string{"g1", "g2"}, titles(globex), "bulk delete crossed the tenant")
	_ = a2
}

// Shape 5: join rows created through an M2M edge get the edge schema's
// defaults, on Create and on UpdateOne, from either side.
func TestEdgeSchema_JoinRowDefaults(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()
	before := time.Now().Add(-time.Minute)

	c1 := client.Course.Create().SetTitle("c1").SaveX(ctx)
	c2 := client.Course.Create().SetTitle("c2").SaveX(ctx)
	c3 := client.Course.Create().SetTitle("c3").SaveX(ctx)

	s, err := client.Student.Create().SetName("s").AddCourseIDs(c1.ID).Save(ctx)
	require.NoError(t, err, "Create through the M2M edge")
	_, err = client.Student.UpdateOneID(s.ID).AddCourseIDs(c2.ID).Save(ctx)
	require.NoError(t, err, "UpdateOne.Save through the M2M edge")
	require.NoError(t, client.Course.UpdateOneID(c3.ID).AddStudentIDs(s.ID).Exec(ctx), "UpdateOne.Exec from the inverse side")
	s2, err := client.Course.Create().SetTitle("c4").AddStudentIDs(s.ID).Save(ctx)
	require.NoError(t, err, "Create from the inverse side")

	rows, err := client.Enrollment.Query().Order(enrollment.ByCourseID()).All(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 4)
	for i, want := range []int{c1.ID, c2.ID, c3.ID, s2.ID} {
		r := rows[i]
		assert.Equal(t, want, r.CourseID)
		assert.Equal(t, s.ID, r.StudentID)
		assert.Equal(t, enrollment.StatusActive, r.Status, "enum default")
		assert.Equal(t, 3, r.Credits, "int default")
		assert.True(t, r.EnrolledAt.After(before), "time default: %v", r.EnrolledAt)
	}

	courses, err := client.Student.QueryCourses(s).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 4, courses)
}

// Shape 6: enum default, nillable enum, GoType enum.
func TestEnumShape_DefaultsNillableAndGoType(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()

	d, err := client.EnumShape.Create().Save(ctx)
	require.NoError(t, err)
	d = client.EnumShape.GetX(ctx, d.ID)
	assert.Equal(t, enumshape.StateDraft, d.State)
	assert.Nil(t, d.Mood)
	assert.Equal(t, types.TierFree, d.Tier)
	assert.Nil(t, d.Grade)

	s, err := client.EnumShape.Create().
		SetState(enumshape.StateLive).SetMood(enumshape.MoodAngry).
		SetTier(types.TierPro).SetGrade(types.TierPro).Save(ctx)
	require.NoError(t, err)
	s = client.EnumShape.GetX(ctx, s.ID)
	assert.Equal(t, enumshape.StateLive, s.State)
	require.NotNil(t, s.Mood)
	assert.Equal(t, enumshape.MoodAngry, *s.Mood)
	assert.Equal(t, types.TierPro, s.Tier)
	require.NotNil(t, s.Grade)
	assert.Equal(t, types.TierPro, *s.Grade)

	n, err := client.EnumShape.Query().Where(enumshape.MoodField.IsNull()).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	// Every enum validator runs, generated type or GoType, on both paths.
	_, err = client.EnumShape.Create().SetState("bogus").Save(ctx)
	require.True(t, gen.IsValidationError(err), "generated enum on create: %v", err)
	_, err = client.EnumShape.Create().SetTier("bogus").Save(ctx)
	require.True(t, gen.IsValidationError(err), "GoType enum on create: %v", err)
	_, err = client.EnumShape.Create().SetGrade("bogus").Save(ctx)
	require.True(t, gen.IsValidationError(err), "nillable GoType enum on create: %v", err)
	err = client.EnumShape.UpdateOneID(s.ID).SetMood("bogus").Exec(ctx)
	require.True(t, gen.IsValidationError(err), "generated enum on update: %v", err)
	err = client.EnumShape.UpdateOneID(s.ID).SetTier("bogus").Exec(ctx)
	require.True(t, gen.IsValidationError(err), "GoType enum on update: %v", err)

	require.NoError(t, client.EnumShape.UpdateOneID(s.ID).ClearMood().ClearGrade().Exec(ctx))
	s = client.EnumShape.GetX(ctx, s.ID)
	assert.Nil(t, s.Mood)
	assert.Nil(t, s.Grade)
}

// Shape 7: string and int GoTypes from a sibling package, with validators
// and defaults.
func TestGoTypeShape_ValidatorsAndDefaults(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()

	d, err := client.GoTypeShape.Create().Save(ctx)
	require.NoError(t, err)
	d = client.GoTypeShape.GetX(ctx, d.ID)
	assert.Equal(t, types.Label("lbl"), d.Label)
	assert.Equal(t, types.Weight(1), d.Weight)
	assert.Nil(t, d.Alias)

	s, err := client.GoTypeShape.Create().SetLabel("abc").SetAlias("al").SetWeight(7).SetBonus(10).Save(ctx)
	require.NoError(t, err)
	s = client.GoTypeShape.GetX(ctx, s.ID)
	assert.Equal(t, types.Label("abc"), s.Label)
	require.NotNil(t, s.Alias)
	assert.Equal(t, types.Label("al"), *s.Alias)
	assert.Equal(t, types.Weight(7), s.Weight)
	assert.Equal(t, types.Weight(10), s.Bonus)

	_, err = client.GoTypeShape.Create().SetLabel("").Save(ctx)
	require.True(t, gen.IsValidationError(err), "NotEmpty on create: %v", err)
	_, err = client.GoTypeShape.Create().SetLabel("123456789").Save(ctx)
	require.True(t, gen.IsValidationError(err), "MaxLen on create: %v", err)
	_, err = client.GoTypeShape.Create().SetAlias("").Save(ctx)
	require.True(t, gen.IsValidationError(err), "NotEmpty on a nillable GoType: %v", err)
	_, err = client.GoTypeShape.Create().SetWeight(0).Save(ctx)
	require.True(t, gen.IsValidationError(err), "Positive on create: %v", err)
	_, err = client.GoTypeShape.Create().SetBonus(11).Save(ctx)
	require.True(t, gen.IsValidationError(err), "Range on create: %v", err)

	err = client.GoTypeShape.UpdateOneID(s.ID).SetLabel("123456789").Exec(ctx)
	require.True(t, gen.IsValidationError(err), "MaxLen on update: %v", err)
	_, err = client.GoTypeShape.Update().SetWeight(-1).Save(ctx)
	require.True(t, gen.IsValidationError(err), "Positive on bulk update: %v", err)
	assert.Equal(t, types.Weight(7), client.GoTypeShape.GetX(ctx, s.ID).Weight)
}

// Shape 8: UUID id with a Go default.
func TestKeys_UUIDDefault(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()

	a := client.UUIDKey.Create().SetName("a").SaveX(ctx)
	b := client.UUIDKey.Create().SetName("b").SaveX(ctx)
	require.NotEqual(t, uuid.Nil, a.ID)
	require.NotEqual(t, a.ID, b.ID)
	fixed := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	c := client.UUIDKey.Create().SetID(fixed).SetName("c").SaveX(ctx)
	assert.Equal(t, fixed, c.ID)

	bulk, err := client.UUIDKey.CreateBulk(
		client.UUIDKey.Create().SetName("d"),
		client.UUIDKey.Create().SetName("e"),
	).Save(ctx)
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, bulk[0].ID)
	require.NotEqual(t, bulk[0].ID, bulk[1].ID)

	got, err := client.UUIDKey.Get(ctx, b.ID)
	require.NoError(t, err)
	assert.Equal(t, "b", got.Name)
	assert.Equal(t, "e", client.UUIDKey.GetX(ctx, bulk[1].ID).Name)
	ids, err := client.UUIDKey.Query().IDs(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{a.ID, b.ID, c.ID, bulk[0].ID, bulk[1].ID}, ids)
}

// Shape 8: string id with DefaultFunc and validators — the ID validator
// must fire.
func TestKeys_StringDefaultFuncAndIDValidator(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()

	a := client.StringKey.Create().SetName("a").SaveX(ctx)
	b := client.StringKey.Create().SetName("b").SaveX(ctx)
	require.NotEmpty(t, a.ID, "DefaultFunc")
	require.NotEqual(t, a.ID, b.ID)
	assert.Equal(t, "a", client.StringKey.GetX(ctx, a.ID).Name)

	c := client.StringKey.Create().SetID("explicit").SetName("c").SaveX(ctx)
	assert.Equal(t, "explicit", c.ID)

	_, err := client.StringKey.Create().SetID("").SetName("x").Save(ctx)
	require.True(t, gen.IsValidationError(err), "NotEmpty on the id: %v", err)
	_, err = client.StringKey.Create().SetID("thirteen-char").SetName("x").Save(ctx)
	require.True(t, gen.IsValidationError(err), "MaxLen on the id: %v", err)
	_, err = client.StringKey.CreateBulk(client.StringKey.Create().SetID("thirteen-char").SetName("x")).Save(ctx)
	require.True(t, gen.IsValidationError(err), "MaxLen on the id in a bulk create: %v", err)

	n, err := client.StringKey.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
}

// Shape 8: the implicit auto-increment int id.
func TestKeys_IntAutoIncrement(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()

	a := client.IntKey.Create().SetName("a").SaveX(ctx)
	b := client.IntKey.Create().SetName("b").SaveX(ctx)
	bulk := client.IntKey.CreateBulk(client.IntKey.Create().SetName("c"), client.IntKey.Create().SetName("d")).SaveX(ctx)
	ids := []int{a.ID, b.ID, bulk[0].ID, bulk[1].ID}
	for i := 1; i < len(ids); i++ {
		assert.Equal(t, ids[i-1]+1, ids[i], "ids are assigned in order: %v", ids)
	}
	assert.Equal(t, "d", client.IntKey.GetX(ctx, bulk[1].ID).Name)
}

// Shape 9: Default, UpdateDefault (time and non-time), Immutable.
func TestDefaultShape_DefaultUpdateDefaultImmutable(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()
	start := time.Now().Add(-time.Second)

	created, err := client.DefaultShape.Create().SetName("d").SetCode("c1").Save(ctx)
	require.NoError(t, err)
	d := client.DefaultShape.GetX(ctx, created.ID)
	assert.Equal(t, 1, d.Revision)
	assert.Equal(t, "n/a", d.Note)
	assert.True(t, d.Active)
	assert.InDelta(t, 0.5, d.Ratio, 1e-9)
	assert.True(t, d.CreatedAt.After(start), "created_at default: %v", d.CreatedAt)
	assert.True(t, d.UpdatedAt.After(start), "updated_at default: %v", d.UpdatedAt)
	assert.Equal(t, created.Revision, d.Revision, "the returned entity carries the defaults")
	assert.Equal(t, created.Note, d.Note)

	// Backdate both timestamps so UpdateDefault is observable.
	old := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	require.NoError(t, client.DefaultShape.UpdateOneID(d.ID).SetUpdatedAt(old).SetRevision(5).Exec(ctx))
	d = client.DefaultShape.GetX(ctx, d.ID)
	require.True(t, d.UpdatedAt.Equal(old), "an explicit value wins over UpdateDefault: %v", d.UpdatedAt)
	require.Equal(t, 5, d.Revision)

	u, err := client.DefaultShape.UpdateOneID(d.ID).SetName("d2").Save(ctx)
	require.NoError(t, err)
	assert.True(t, u.UpdatedAt.After(start), "time UpdateDefault on UpdateOne.Save: %v", u.UpdatedAt)
	assert.Equal(t, schema.DefaultShapeRevision, u.Revision, "int UpdateDefault on UpdateOne.Save")
	assert.True(t, u.CreatedAt.Equal(d.CreatedAt), "created_at is untouched by an update")

	require.NoError(t, client.DefaultShape.UpdateOneID(d.ID).SetUpdatedAt(old).SetRevision(5).Exec(ctx))
	require.NoError(t, client.DefaultShape.UpdateOneID(d.ID).SetName("d3").Exec(ctx))
	d = client.DefaultShape.GetX(ctx, d.ID)
	assert.True(t, d.UpdatedAt.After(start), "time UpdateDefault on UpdateOne.Exec: %v", d.UpdatedAt)
	assert.Equal(t, schema.DefaultShapeRevision, d.Revision, "int UpdateDefault on UpdateOne.Exec")

	require.NoError(t, client.DefaultShape.UpdateOneID(d.ID).SetUpdatedAt(old).SetRevision(5).Exec(ctx))
	_, err = client.DefaultShape.Update().SetName("d4").Save(ctx)
	require.NoError(t, err)
	d = client.DefaultShape.GetX(ctx, d.ID)
	assert.True(t, d.UpdatedAt.After(start), "time UpdateDefault on bulk Update: %v", d.UpdatedAt)
	assert.Equal(t, schema.DefaultShapeRevision, d.Revision, "int UpdateDefault on bulk Update")

	// Immutable columns have no setter on any update builder.
	for _, b := range []any{client.DefaultShape.Update(), client.DefaultShape.UpdateOneID(d.ID)} {
		typ := reflect.TypeOf(b)
		for _, m := range []string{"SetCode", "SetCreatedAt"} {
			_, ok := typ.MethodByName(m)
			assert.False(t, ok, "%s has %s for an immutable field", typ, m)
		}
		_, ok := typ.MethodByName("SetName")
		require.True(t, ok, "%s lost SetName: the probe above proves nothing", typ)
	}
	assert.Equal(t, "c1", d.Code)
}

// Shape 10: validators run on Create, Update and UpdateOne, whether a field
// has one or several.
func TestValidated_ValidatorsRunOnCreateAndUpdate(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()

	valid := func() *entity.Validated {
		v, err := client.Validated.Create().
			SetOne("1").SetTwo("22").SetThree("abc").SetScore(50).SetRank(5).SetEven(2).SetRatio(0.5).Save(ctx)
		require.NoError(t, err)
		return v
	}
	row := valid()

	type set func(one, two, three string, score, rank, even int, ratio float64) error
	paths := map[string]set{
		"Create": func(one, two, three string, score, rank, even int, ratio float64) error {
			return client.Validated.Create().
				SetOne(one).SetTwo(two).SetThree(three).SetScore(score).SetRank(rank).SetEven(even).SetRatio(ratio).Exec(ctx)
		},
		"CreateBulk": func(one, two, three string, score, rank, even int, ratio float64) error {
			return client.Validated.CreateBulk(client.Validated.Create().
				SetOne(one).SetTwo(two).SetThree(three).SetScore(score).SetRank(rank).SetEven(even).SetRatio(ratio)).Exec(ctx)
		},
		"Update": func(one, two, three string, score, rank, even int, ratio float64) error {
			_, err := client.Validated.Update().Where(validated.IDField.EQ(row.ID)).
				SetOne(one).SetTwo(two).SetThree(three).SetScore(score).SetRank(rank).SetEven(even).SetRatio(ratio).Save(ctx)
			return err
		},
		"UpdateOne.Save": func(one, two, three string, score, rank, even int, ratio float64) error {
			_, err := client.Validated.UpdateOneID(row.ID).
				SetOne(one).SetTwo(two).SetThree(three).SetScore(score).SetRank(rank).SetEven(even).SetRatio(ratio).Save(ctx)
			return err
		},
		"UpdateOne.Exec": func(one, two, three string, score, rank, even int, ratio float64) error {
			return client.Validated.UpdateOneID(row.ID).
				SetOne(one).SetTwo(two).SetThree(three).SetScore(score).SetRank(rank).SetEven(even).SetRatio(ratio).Exec(ctx)
		},
	}
	cases := []struct {
		name              string
		one, two, three   string
		score, rank, even int
		ratio             float64
	}{
		{"one: NotEmpty", "", "22", "abc", 50, 5, 2, 0.5},
		{"two: first validator (NotEmpty)", "1", "", "abc", 50, 5, 2, 0.5},
		{"two: second validator (MaxLen)", "1", "123456", "abc", 50, 5, 2, 0.5},
		{"three: MinLen", "1", "22", "a", 50, 5, 2, 0.5},
		{"three: MaxLen", "1", "22", "abcdefg", 50, 5, 2, 0.5},
		{"three: Match", "1", "22", "ABC", 50, 5, 2, 0.5},
		{"score: Range low", "1", "22", "abc", -1, 5, 2, 0.5},
		{"score: Range high", "1", "22", "abc", 101, 5, 2, 0.5},
		{"rank: first validator (Min)", "1", "22", "abc", 50, 0, 2, 0.5},
		{"rank: second validator (Max)", "1", "22", "abc", 50, 11, 2, 0.5},
		{"even: custom Validate", "1", "22", "abc", 50, 5, 3, 0.5},
		{"ratio: Max on a float", "1", "22", "abc", 50, 5, 2, 1.5},
	}
	for path, run := range paths {
		for _, c := range cases {
			err := run(c.one, c.two, c.three, c.score, c.rank, c.even, c.ratio)
			require.Error(t, err, "%s / %s", path, c.name)
			require.True(t, gen.IsValidationError(err), "%s / %s: %v", path, c.name, err)
		}
	}
	err := paths["UpdateOne.Exec"]("1", "22", "abc", 50, 5, 3, 0.5)
	require.True(t, errors.Is(err, schema.ErrOdd), "the custom validator's error is wrapped: %v", err)

	// Nothing was written by any rejected mutation.
	all, err := client.Validated.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "abc", all[0].Three)
	assert.Equal(t, 5, all[0].Rank)
	// ...and every path accepts a valid row.
	for path, run := range paths {
		require.NoError(t, run("x", "yy", "zzz", 100, 10, 4, 1), path)
	}
}

// Shape 10: a field named err, with two validators, builds and validates.
func TestErrField_FieldNamedErrValidates(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()

	row, err := client.ErrField.Create().SetErr("ok").Save(ctx)
	require.NoError(t, err)
	_, err = client.ErrField.Create().SetErr("").Save(ctx)
	require.True(t, gen.IsValidationError(err), "first validator on create: %v", err)
	_, err = client.ErrField.Create().SetErr("123456").Save(ctx)
	require.True(t, gen.IsValidationError(err), "second validator on create: %v", err)
	err = client.ErrField.UpdateOneID(row.ID).SetErr("123456").Exec(ctx)
	require.True(t, gen.IsValidationError(err), "second validator on update: %v", err)
	err = client.ErrField.UpdateOneID(row.ID).SetErrs(10).Exec(ctx)
	require.True(t, gen.IsValidationError(err), "second validator of the int field: %v", err)
	require.NoError(t, client.ErrField.UpdateOneID(row.ID).SetErr("fine").SetErrs(9).Exec(ctx))
	got := client.ErrField.GetX(ctx, row.ID)
	assert.Equal(t, "fine", got.Err)
	assert.Equal(t, 9, got.Errs)
}
