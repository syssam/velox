package shapes

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/vx/orm"
	client "github.com/syssam/velox/vx/tests/shapes/db"
	"github.com/syssam/velox/vx/tests/shapes/db/defaultshape"
	"github.com/syssam/velox/vx/tests/shapes/db/entity"
)

// opt is an optional as an API layer has one: absent, present and null, or
// present with a value. Fill recognizes it by its ValueOK method.
type opt[T any] struct {
	v   T
	set bool
}

func (o opt[T]) ValueOK() (T, bool) { return o.v, o.set }

func some[T any](v T) opt[T] { return opt[T]{v: v, set: true} }

// Create(...).Fill(input): what the input gives is written, by the type of
// each of its fields.
func TestFill_Create(t *testing.T) {
	ctx := context.Background()
	db := open(t)

	type input struct {
		Name    string        // a value: written
		Code    string        //
		Note    *string       // nil: not given
		Active  *bool         //
		OptNote opt[*string]  // absent, null, or a value
		NilNote opt[*string]  //
		Tags    opt[[]string] //
		Tiny    int           // converted to the field's int8
		Comment string        // not a field of DefaultShape
	}
	in := input{Name: "a", Code: "x", Active: ptr(false), NilNote: some(ptr("")), Tiny: 5, Comment: "for the audit log"}

	// A field that goes nowhere is an error, until the caller says so.
	_, err := client.DefaultShape.Create(db).Fill(in).Save(ctx)
	require.ErrorContains(t, err, "has a field Comment that DefaultShape does not")
	a := must(client.DefaultShape.Create(db).Fill(in, "Comment").Save(ctx))
	assert.Equal(t, "a", a.Name)
	assert.False(t, a.Active, "a given false was taken for not given")
	assert.Equal(t, "n/a", a.Note, "a nil pointer is not given: the default")
	assert.Equal(t, "opt", a.OptNote, "an absent optional is not given: the default")
	require.NotNil(t, a.NilNote)
	assert.Equal(t, "", *a.NilNote, "a present empty string is a value")
	assert.Equal(t, []string{"a", "b"}, a.Tags)
	assert.Equal(t, int8(5), a.Tiny)

	// Present and null on a create is not given.
	b := must(client.DefaultShape.Create(db).Fill(input{Name: "b", Code: "y", NilNote: some[*string](nil), Tags: some[[]string](nil), Tiny: 1}, "Comment").Save(ctx))
	require.NotNil(t, b.NilNote)
	assert.Equal(t, "nil", *b.NilNote)
	assert.Equal(t, []string{"a", "b"}, b.Tags)

	// A pointer to the input, and setters after it.
	c := must(client.DefaultShape.Create(db).Fill(&in, "Comment").Set(defaultshape.Name.Set("c")).Save(ctx))
	assert.Equal(t, "c", c.Name)

	// A value that does not fit the field is refused, not truncated.
	_, err = client.DefaultShape.Create(db).Fill(input{Name: "d", Code: "z", Tiny: 300}, "Comment").Save(ctx)
	require.ErrorContains(t, err, "cannot use int(300) as a int8")
	_, err = client.DefaultShape.Create(db).Fill(struct{ Name int }{Name: 1}).Save(ctx)
	require.Error(t, err)
	// Nor is a skip that names nothing, or something that is not a struct.
	_, err = client.DefaultShape.Create(db).Fill(in, "Comment", "Commment").Save(ctx)
	require.ErrorContains(t, err, "skip names Commment")
	_, err = client.DefaultShape.Create(db).Fill("name").Save(ctx)
	require.ErrorContains(t, err, "takes a struct")
	var none *input
	_, err = client.DefaultShape.Create(db).Fill(none).Save(ctx)
	require.ErrorContains(t, err, "takes a struct")

	// An entity is not an input: its fields cannot say "not given" (false is
	// both "false" and "the default, true"), so it is refused, not guessed at.
	_, err = client.DefaultShape.Create(db).Fill(&entity.DefaultShape{Name: "e", Code: "x"}).Save(ctx)
	require.ErrorContains(t, err, "takes an input struct, not a DefaultShape")
	_, err = client.DefaultShape.Update(db).Fill(entity.DefaultShape{Name: "e"}).Exec(ctx)
	require.ErrorContains(t, err, "takes an input struct, not a DefaultShape")
	assert.Equal(t, 3, must(client.DefaultShape.Query(db).Count(ctx)), "a refused create wrote")

	// A user-defined id is a field like another; validators see the values;
	// several inputs make one statement, and one that cannot be built stops
	// all of them.
	k := must(client.ManualKey.Create(db).Fill(struct{ ID, Name string }{"key9", "n"}).Save(ctx))
	assert.Equal(t, "key9", k.ID)
	_, err = client.Validated.Create(db).Fill(struct{ One string }{""}).Save(ctx)
	require.Error(t, err, "the validators did not see the input's values")
	type row struct {
		Name, Code string
		Note       *string
	}
	rows := must(client.DefaultShape.CreateBulk(db,
		client.DefaultShape.Create(nil).Fill(row{Name: "b1", Code: "p"}),
		client.DefaultShape.Create(nil).Fill(row{Name: "b2", Code: "q", Note: ptr("two")}),
	).Save(ctx))
	require.Len(t, rows, 2)
	assert.Equal(t, "n/a", rows[0].Note)
	assert.Equal(t, "two", rows[1].Note)
	_, err = client.DefaultShape.CreateBulk(db, client.DefaultShape.Create(nil).Fill(row{Name: "ok", Code: "x"}), client.DefaultShape.Create(nil).Fill(7)).Save(ctx)
	require.ErrorContains(t, err, "takes a struct")
	assert.Equal(t, 5, must(client.DefaultShape.Query(db).Count(ctx)))
}

// A struct says in its tags what Fill does with a field: orm:"-" keeps it
// out of the row, orm:"column" names the column of a field called otherwise.
func TestFill_Tags(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	type input struct {
		Title   string `orm:"name"`
		Code    string
		Remark  *string `orm:"note"`
		TraceID string  `orm:"-"`
		Reason  string  `json:"reason" orm:"-"`
	}
	a := must(client.DefaultShape.Create(db).Fill(input{Title: "a", Code: "x", Remark: ptr("r"), TraceID: "t", Reason: "why"}).Save(ctx))
	assert.Equal(t, "a", a.Name)
	assert.Equal(t, "r", a.Note)
	// A tag that names no column is the same error as a field that names none.
	_, err := client.DefaultShape.Create(db).Fill(struct {
		Title string `orm:"no_such_column"`
	}{"b"}).Save(ctx)
	require.ErrorContains(t, err, "has a field Title that DefaultShape does not")
	// Naming a tagged-out field in skip is allowed: the caller may not know.
	must(client.DefaultShape.Create(db).Fill(input{Title: "c", Code: "y"}, "TraceID").Save(ctx))
}

// The zero value of a builder is not a builder of anything: an error, not a
// nil dereference.
func TestZeroBuilder(t *testing.T) {
	ctx := context.Background()
	_, err := new(entity.DefaultShapeQuery).All(ctx)
	require.ErrorIs(t, err, orm.ErrNoEntity)
	_, err = new(entity.DefaultShapeQuery).Count(ctx)
	require.ErrorIs(t, err, orm.ErrNoEntity)
	_, err = new(entity.DefaultShapeCreate).Save(ctx)
	require.ErrorIs(t, err, orm.ErrNoEntity)
	_, err = new(entity.DefaultShapeUpdate).Exec(ctx)
	require.ErrorIs(t, err, orm.ErrNoEntity)
	_, err = new(entity.DefaultShapeDelete).Exec(ctx)
	require.ErrorIs(t, err, orm.ErrNoEntity)
}

// orm.Opt is the three-state field for a caller that has none of its own: it
// decodes from JSON as the body of a PATCH request needs.
func TestFill_Opt(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	a := must(client.DefaultShape.Create(db).Set(defaultshape.Name.Set("a"), defaultshape.Code.Set("x"), defaultshape.NilNote.Set("nil note")).Save(ctx))

	type patch struct {
		Name    orm.Opt[string] `json:"name,omitzero"`
		Note    orm.Opt[string] `json:"note,omitzero"`
		NilNote orm.Opt[string] `json:"nil_note,omitzero"`
		Big     orm.Opt[int64]  `json:"big,omitzero"`
	}
	var in patch
	require.NoError(t, json.Unmarshal([]byte(`{"note": "", "nil_note": null, "big": 0}`), &in))
	got := must(client.DefaultShape.UpdateByID(db, a.ID).Fill(in).SaveOne(ctx))
	assert.Equal(t, "a", got.Name, "a missing key changed the column")
	assert.Equal(t, "", got.Note, "an empty string is a value")
	assert.Nil(t, got.NilNote, "null did not clear the column")
	assert.Equal(t, int64(0), got.Big)

	got = must(client.DefaultShape.UpdateByID(db, a.ID).Fill(patch{Name: orm.Some("b"), NilNote: orm.Some("x"), Note: orm.Null[string]()}).SaveOne(ctx))
	assert.Equal(t, "b", got.Name)
	require.NotNil(t, got.NilNote)
	assert.Equal(t, "x", *got.NilNote)
	assert.Equal(t, "", got.Note, "null for a column that is NOT NULL changed it")

	// And back to JSON: an absent Opt is a missing key.
	out, err := json.Marshal(patch{Name: orm.Some("n"), NilNote: orm.Null[string]()})
	require.NoError(t, err)
	assert.JSONEq(t, `{"name": "n", "nil_note": null}`, string(out))
	require.Error(t, json.Unmarshal([]byte(`{"big": "not a number"}`), &in))
}

// Update(...).Fill(input): what the input does not give is left alone.
func TestFill_Update(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	a := must(client.DefaultShape.Create(db).Set(defaultshape.Name.Set("a"), defaultshape.Code.Set("x"), defaultshape.Note.Set("note"), defaultshape.NilNote.Set("nil note"), defaultshape.Big.Set(9)).Save(ctx))

	type patch struct {
		Name         opt[*string]
		Note         opt[*string]
		NilNote      opt[*string]
		Big          *int64
		Active       *bool
		ClearNilNote bool
	}
	// Nothing given: nothing written but the update defaults.
	got := must(client.DefaultShape.UpdateByID(db, a.ID).Fill(patch{}).SaveOne(ctx))
	assert.Equal(t, "a", got.Name)
	assert.Equal(t, "note", got.Note)
	assert.Equal(t, int64(9), got.Big)
	assert.True(t, got.Active)

	// Values, zero included.
	got = must(client.DefaultShape.UpdateByID(db, a.ID).Fill(patch{Name: some(ptr("b")), Note: some(ptr("")), Big: ptr(int64(0)), Active: ptr(false)}).SaveOne(ctx))
	assert.Equal(t, "b", got.Name)
	assert.Equal(t, "", got.Note)
	assert.Equal(t, int64(0), got.Big)
	assert.False(t, got.Active)
	require.NotNil(t, got.NilNote)

	// Present and null: NULL where the column has one, nothing where it has not.
	got = must(client.DefaultShape.UpdateByID(db, a.ID).Fill(patch{Name: some[*string](nil), NilNote: some[*string](nil)}).SaveOne(ctx))
	assert.Equal(t, "b", got.Name, "a null for a column that is NOT NULL changed it")
	assert.Nil(t, got.NilNote)
	must(client.DefaultShape.UpdateByID(db, a.ID).Set(defaultshape.NilNote.Set("back")).SaveOne(ctx))
	got = must(client.DefaultShape.UpdateByID(db, a.ID).Fill(patch{ClearNilNote: true}).SaveOne(ctx))
	assert.Nil(t, got.NilNote)

	// A field an update may not write is an error when the input gives it,
	// as with a setter, and not when it does not.
	type withCode struct{ Code *string }
	must(client.DefaultShape.UpdateByID(db, a.ID).Fill(withCode{}).SaveOne(ctx))
	_, err := client.DefaultShape.UpdateByID(db, a.ID).Fill(withCode{Code: ptr("changed")}).SaveOne(ctx)
	require.Error(t, err)
	assert.Equal(t, "x", must(client.DefaultShape.Get(ctx, db, a.ID)).Code)

	// Every matching row, like any update.
	must(client.DefaultShape.Create(db).Set(defaultshape.Name.Set("z"), defaultshape.Code.Set("y")).Save(ctx))
	assert.Equal(t, 2, must(client.DefaultShape.Update(db).Fill(patch{Note: some(ptr("all"))}).Exec(ctx)))
}
