package shapes

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/syssam/velox/tests/orm/shapes/db/predicate"

	client "github.com/syssam/velox/tests/orm/shapes/db"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/orm"
	"github.com/syssam/velox/tests/orm/shapes/db/entity"
	"github.com/syssam/velox/tests/orm/shapes/db/scalar"
)

var (
	t0 = time.Date(2024, 2, 29, 13, 14, 15, 0, time.UTC)
	u1 = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	u2 = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	u3 = uuid.MustParse("33333333-3333-4333-8333-333333333333")
)

// scalarRequired are the setters of the fields Scalar requires.
func scalarRequired(seq int) []predicate.ScalarSetter {
	return []predicate.ScalarSetter{
		scalar.Seq.Set(seq),
		scalar.StringPtr.Set("p"),
		scalar.IntPtr.Set(seq),
		scalar.FloatPtr.Set(1.5),
		scalar.BoolPtr.Set(true),
		scalar.TimePtr.Set(t0),
		scalar.UUIDPtr.Set(u1),
		scalar.BytesPtr.Set([]byte{1, 2}),
	}
}

// scalarFull sets every optional field of Scalar to a value derived from n,
// every one different from the zero value.
func scalarFull(n int) []predicate.ScalarSetter {
	s := string(rune('a' + n))
	return []predicate.ScalarSetter{
		scalar.StringOpt.Set("so" + s), scalar.StringNil.Set("sn" + s),
		scalar.TextOpt.Set("to" + s), scalar.TextNil.Set("tn" + s),
		scalar.IntOpt.Set(n), scalar.IntNil.Set(-n),
		scalar.Int8Opt.Set(int8(n)), scalar.Int8Nil.Set(math.MinInt8),
		scalar.Int16Opt.Set(int16(n)), scalar.Int16Nil.Set(math.MinInt16),
		scalar.Int32Opt.Set(int32(n)), scalar.Int32Nil.Set(math.MinInt32),
		scalar.Int64Opt.Set(int64(n)), scalar.Int64Nil.Set(math.MinInt64),
		scalar.UintOpt.Set(uint(n)), scalar.UintNil.Set(uint(n) + 1),
		scalar.Uint8Opt.Set(uint8(n)), scalar.Uint8Nil.Set(math.MaxUint8),
		scalar.Uint16Opt.Set(uint16(n)), scalar.Uint16Nil.Set(math.MaxUint16),
		scalar.Uint32Opt.Set(uint32(n)), scalar.Uint32Nil.Set(math.MaxUint32),
		scalar.Uint64Opt.Set(uint64(n)), scalar.Uint64Nil.Set(math.MaxInt64),
		scalar.FloatOpt.Set(float64(n) + 0.25), scalar.FloatNil.Set(-0.5),
		scalar.Float32Opt.Set(float32(n) + 0.5), scalar.Float32Nil.Set(float32(-1.25)),
		scalar.BoolOpt.Set(true), scalar.BoolNil.Set(false),
		scalar.TimeOpt.Set(t0.Add(time.Duration(n) * time.Hour)), scalar.TimeNil.Set(t0.Add(-time.Hour)),
		scalar.UUIDOpt.Set(u2), scalar.UUIDNil.Set(u3),
		scalar.BytesOpt.Set([]byte("bo" + s)), scalar.BytesNil.Set([]byte("bn" + s)),
	}
}

func assertScalarFull(t *testing.T, n int, r *entity.Scalar) {
	t.Helper()
	s := string(rune('a' + n))
	assert.Equal(t, "so"+s, r.StringOpt)
	require.NotNil(t, r.StringNil)
	assert.Equal(t, "sn"+s, *r.StringNil)
	assert.Equal(t, "to"+s, r.TextOpt)
	require.NotNil(t, r.TextNil)
	assert.Equal(t, "tn"+s, *r.TextNil)
	assert.Equal(t, n, r.IntOpt)
	require.NotNil(t, r.IntNil)
	assert.Equal(t, -n, *r.IntNil)
	assert.Equal(t, int8(n), r.Int8Opt)
	require.NotNil(t, r.Int8Nil)
	assert.Equal(t, int8(math.MinInt8), *r.Int8Nil)
	assert.Equal(t, int16(n), r.Int16Opt)
	require.NotNil(t, r.Int16Nil)
	assert.Equal(t, int16(math.MinInt16), *r.Int16Nil)
	assert.Equal(t, int32(n), r.Int32Opt)
	require.NotNil(t, r.Int32Nil)
	assert.Equal(t, int32(math.MinInt32), *r.Int32Nil)
	assert.Equal(t, int64(n), r.Int64Opt)
	require.NotNil(t, r.Int64Nil)
	assert.Equal(t, int64(math.MinInt64), *r.Int64Nil)
	assert.Equal(t, uint(n), r.UintOpt)
	require.NotNil(t, r.UintNil)
	assert.Equal(t, uint(n)+1, *r.UintNil)
	assert.Equal(t, uint8(n), r.Uint8Opt)
	require.NotNil(t, r.Uint8Nil)
	assert.Equal(t, uint8(math.MaxUint8), *r.Uint8Nil)
	assert.Equal(t, uint16(n), r.Uint16Opt)
	require.NotNil(t, r.Uint16Nil)
	assert.Equal(t, uint16(math.MaxUint16), *r.Uint16Nil)
	assert.Equal(t, uint32(n), r.Uint32Opt)
	require.NotNil(t, r.Uint32Nil)
	assert.Equal(t, uint32(math.MaxUint32), *r.Uint32Nil)
	assert.Equal(t, uint64(n), r.Uint64Opt)
	require.NotNil(t, r.Uint64Nil)
	assert.Equal(t, uint64(math.MaxInt64), *r.Uint64Nil)
	assert.Equal(t, float64(n)+0.25, r.FloatOpt)
	require.NotNil(t, r.FloatNil)
	assert.Equal(t, -0.5, *r.FloatNil)
	assert.Equal(t, float32(n)+0.5, r.Float32Opt)
	require.NotNil(t, r.Float32Nil)
	assert.Equal(t, float32(-1.25), *r.Float32Nil)
	assert.True(t, r.BoolOpt)
	require.NotNil(t, r.BoolNil, "a false in a nillable bool is a pointer to false, not nil")
	assert.False(t, *r.BoolNil)
	assert.True(t, r.TimeOpt.Equal(t0.Add(time.Duration(n)*time.Hour)), "time_opt = %v", r.TimeOpt)
	require.NotNil(t, r.TimeNil)
	assert.True(t, r.TimeNil.Equal(t0.Add(-time.Hour)), "time_nil = %v", r.TimeNil)
	assert.Equal(t, u2, r.UUIDOpt)
	require.NotNil(t, r.UUIDNil)
	assert.Equal(t, u3, *r.UUIDNil)
	assert.Equal(t, []byte("bo"+s), r.BytesOpt)
	require.NotNil(t, r.BytesNil)
	assert.Equal(t, []byte("bn"+s), *r.BytesNil)
}

func assertScalarRequired(t *testing.T, seq int, r *entity.Scalar) {
	t.Helper()
	assert.Equal(t, seq, r.Seq)
	require.NotNil(t, r.StringPtr)
	assert.Equal(t, "p", *r.StringPtr)
	require.NotNil(t, r.IntPtr)
	assert.Equal(t, seq, *r.IntPtr)
	require.NotNil(t, r.FloatPtr)
	assert.Equal(t, 1.5, *r.FloatPtr)
	require.NotNil(t, r.BoolPtr)
	assert.True(t, *r.BoolPtr)
	require.NotNil(t, r.TimePtr)
	assert.True(t, r.TimePtr.Equal(t0))
	require.NotNil(t, r.UUIDPtr)
	assert.Equal(t, u1, *r.UUIDPtr)
	require.NotNil(t, r.BytesPtr)
	assert.Equal(t, []byte{1, 2}, *r.BytesPtr)
}

// assertScalarEmpty asserts every optional field reads as unset: the zero
// value for an Optional field, nil for a Nillable one.
func assertScalarEmpty(t *testing.T, r *entity.Scalar) {
	t.Helper()
	assert.Zero(t, r.StringOpt)
	assert.Nil(t, r.StringNil)
	assert.Zero(t, r.TextOpt)
	assert.Nil(t, r.TextNil)
	assert.Zero(t, r.IntOpt)
	assert.Nil(t, r.IntNil)
	assert.Zero(t, r.Int8Opt)
	assert.Nil(t, r.Int8Nil)
	assert.Zero(t, r.Int16Opt)
	assert.Nil(t, r.Int16Nil)
	assert.Zero(t, r.Int32Opt)
	assert.Nil(t, r.Int32Nil)
	assert.Zero(t, r.Int64Opt)
	assert.Nil(t, r.Int64Nil)
	assert.Zero(t, r.UintOpt)
	assert.Nil(t, r.UintNil)
	assert.Zero(t, r.Uint8Opt)
	assert.Nil(t, r.Uint8Nil)
	assert.Zero(t, r.Uint16Opt)
	assert.Nil(t, r.Uint16Nil)
	assert.Zero(t, r.Uint32Opt)
	assert.Nil(t, r.Uint32Nil)
	assert.Zero(t, r.Uint64Opt)
	assert.Nil(t, r.Uint64Nil)
	assert.Zero(t, r.FloatOpt)
	assert.Nil(t, r.FloatNil)
	assert.Zero(t, r.Float32Opt)
	assert.Nil(t, r.Float32Nil)
	assert.False(t, r.BoolOpt)
	assert.Nil(t, r.BoolNil)
	assert.True(t, r.TimeOpt.IsZero(), "time_opt = %v", r.TimeOpt)
	assert.Nil(t, r.TimeNil)
	assert.Equal(t, uuid.Nil, r.UUIDOpt)
	assert.Nil(t, r.UUIDNil)
	assert.Empty(t, r.BytesOpt)
	assert.Nil(t, r.BytesNil, "a NULL nillable bytes column is a nil pointer, not a pointer to a nil slice")
}

// scalarOptCols and scalarNilCols are the columns of Scalar's Optional
// (zero value when unset) and Optional+Nillable (NULL when unset) fields.
var (
	scalarOptCols = []string{"string_opt", "text_opt", "int_opt", "int8_opt", "int16_opt", "int32_opt", "int64_opt", "uint_opt", "uint8_opt",
		"uint16_opt", "uint32_opt", "uint64_opt", "float_opt", "float32_opt", "bool_opt", "time_opt", "uuid_opt", "bytes_opt"}
	scalarNilCols = []string{"string_nil", "text_nil", "int_nil", "int8_nil", "int16_nil", "int32_nil", "int64_nil", "uint_nil", "uint8_nil",
		"uint16_nil", "uint32_nil", "uint64_nil", "float_nil", "float32_nil", "bool_nil", "time_nil", "uuid_nil", "bytes_nil"}
)

// nullOptionals stores NULL in every Optional column of the Scalar row seq,
// with raw SQL: the ORM itself never leaves such a column NULL on create.
func nullOptionals(t *testing.T, db *orm.DB, seq int) {
	t.Helper()
	for _, col := range append(append([]string{}, scalarOptCols...), scalarNilCols...) {
		rawExec(t, db, "UPDATE scalars SET "+col+" = NULL WHERE seq = ?", seq)
	}
}

// A row that sets nothing optional between two rows of values, for every
// scalar type, on velox's own tables: an unset Optional field is stored as
// its zero value (the column is NOT NULL), an unset Nillable one as NULL. A
// scan holder reused across rows, or a destination that keeps the previous
// row's value, shows up in the middle row.
func TestScalar_UnsetBetweenValues(t *testing.T) {
	ctx := context.Background()
	db := open(t)

	first := must(client.Scalar.Create(db).Set(scalarRequired(1)...).Set(scalarFull(1)...).Save(ctx))
	middle := must(client.Scalar.Create(db).Set(scalarRequired(2)...).Save(ctx))
	last := must(client.Scalar.Create(db).Set(scalarRequired(3)...).Set(scalarFull(3)...).Save(ctx))

	// What Create returns.
	assertScalarRequired(t, 1, first)
	assertScalarFull(t, 1, first)
	assertScalarRequired(t, 2, middle)
	assertScalarEmpty(t, middle)
	assertScalarFull(t, 3, last)

	// What a query reads back.
	rows := must(client.Scalar.Query(db).OrderBy(scalar.Seq.Asc()).All(ctx))
	require.Len(t, rows, 3)
	assertScalarRequired(t, 1, rows[0])
	assertScalarFull(t, 1, rows[0])
	assertScalarRequired(t, 2, rows[1])
	assertScalarEmpty(t, rows[1])
	assertScalarRequired(t, 3, rows[2])
	assertScalarFull(t, 3, rows[2])

	// The middle row holds zero values in the Optional columns and NULL in
	// the Nillable ones.
	for _, col := range scalarOptCols {
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM scalars WHERE seq = 2 AND "+col+" IS NOT NULL"), "column %s", col)
	}
	for _, col := range scalarNilCols {
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM scalars WHERE seq = 2 AND "+col+" IS NULL"), "column %s", col)
	}
	assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM scalars WHERE seq = 2 AND string_opt = '' AND int_opt = 0 AND uint64_opt = 0 AND float_opt = 0 AND bool_opt = 0 AND length(bytes_opt) = 0"))
	// ...which is what the predicates see. (IsNil does not exist on the
	// Optional columns: TestCompileTimeChecks.)
	seqs := func(ps ...predicate.Scalar) []int {
		t.Helper()
		var out []int
		for _, r := range must(client.Scalar.Query(db).Where(ps...).OrderBy(scalar.Seq.Asc()).All(ctx)) {
			out = append(out, r.Seq)
		}
		return out
	}
	assert.Equal(t, []int{2}, seqs(scalar.IntNil.IsNil(), scalar.StringNil.IsNil(), scalar.TimeNil.IsNil(), scalar.UUIDNil.IsNil(), scalar.BytesNil.IsNil(), scalar.BoolNil.IsNil()))
	assert.Equal(t, []int{2}, seqs(scalar.IntOpt.EQ(0), scalar.StringOpt.EQ(""), scalar.BoolOpt.EQ(false), scalar.UUIDOpt.EQ(uuid.Nil), scalar.FloatOpt.EQ(0), scalar.Uint8Opt.EQ(0)))
	assert.Equal(t, []int{2}, seqs(scalar.TimeOpt.EQ(time.Time{})), "the zero time written is the zero time compared")
	assert.Equal(t, []int{2}, seqs(scalar.TimeOpt.LT(t0)))

	// The rows do not share memory.
	(*rows[0].BytesNil)[0] = 'X'
	rows[0].BytesOpt[0] = 'X'
	assert.Equal(t, []byte("bnd"), *rows[2].BytesNil)
	assert.Equal(t, []byte("bod"), rows[2].BytesOpt)
	again := must(client.Scalar.Query(db).Where(scalar.Seq.EQ(1)).Only(ctx))
	assert.Equal(t, []byte("bnb"), *again.BytesNil)

	// A bulk create whose rows set different optional columns: the unset ones
	// are zero-filled per row, not taken from a neighbor.
	bulk := must(client.Scalar.CreateBulk(db,
		client.Scalar.Create(db).Set(scalarRequired(10)...).Set(scalarFull(4)...),
		client.Scalar.Create(db).Set(scalarRequired(11)...),
		client.Scalar.Create(db).Set(scalarRequired(12)...).Set(scalar.IntOpt.Set(7), scalar.StringNil.Set("only")),
		client.Scalar.Create(db).Set(scalarRequired(13)...).Set(scalarFull(5)...),
	).Save(ctx))
	require.Len(t, bulk, 4)
	for name, rs := range map[string][]*entity.Scalar{"created": bulk, "queried": must(client.Scalar.Query(db).Where(scalar.Seq.GTE(10)).OrderBy(scalar.Seq.Asc()).All(ctx))} {
		require.Len(t, rs, 4, name)
		assertScalarFull(t, 4, rs[0])
		assertScalarRequired(t, 11, rs[1])
		assertScalarEmpty(t, rs[1])
		assert.Equal(t, 7, rs[2].IntOpt, name)
		require.NotNil(t, rs[2].StringNil, name)
		assert.Equal(t, "only", *rs[2].StringNil, name)
		assert.Zero(t, rs[2].StringOpt, name)
		assert.Nil(t, rs[2].IntNil, name)
		assert.Empty(t, rs[2].BytesOpt, name)
		assert.Nil(t, rs[2].BytesNil, name)
		assert.True(t, rs[2].TimeOpt.IsZero(), name)
		assert.Equal(t, uuid.Nil, rs[2].UUIDOpt, name)
		assertScalarFull(t, 5, rs[3])
	}

	// An update does not zero-fill: the columns it does not name keep their
	// values.
	got := must(client.Scalar.Update(db).Set(scalar.IntOpt.Set(99)).Where(scalar.Seq.EQ(1)).SaveOne(ctx))
	assert.Equal(t, 99, got.IntOpt)
	got.IntOpt = 1
	got.BytesOpt[0], (*got.BytesNil)[0] = 'b', 'b'
	assertScalarFull(t, 1, got)
}

// A NULL in the column of an Optional, non-Nillable field — which the runtime never
// writes on create, but a database migrated by something else may hold —
// reads as the zero value, between two rows of values.
func TestScalar_NullInOptionalColumnReadsAsZero(t *testing.T) {
	ctx := context.Background()
	db := openDDL(t, ddlNullableOptional)
	for n := 1; n <= 3; n++ {
		must(client.Scalar.Create(db).Set(scalarRequired(n)...).Set(scalarFull(n)...).Save(ctx))
	}
	nullOptionals(t, db, 2)
	for _, col := range scalarOptCols {
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM scalars WHERE seq = 2 AND "+col+" IS NULL"), "column %s", col)
	}
	rows := must(client.Scalar.Query(db).OrderBy(scalar.Seq.Asc()).All(ctx))
	require.Len(t, rows, 3)
	assertScalarFull(t, 1, rows[0])
	assertScalarRequired(t, 2, rows[1])
	assertScalarEmpty(t, rows[1])
	assertScalarFull(t, 3, rows[2])
	// Through First, Only and an update's read-back too.
	assertScalarEmpty(t, must(client.Scalar.Query(db).Where(scalar.Seq.EQ(2)).Only(ctx)))
	assertScalarEmpty(t, must(client.Scalar.Query(db).Where(scalar.IntNil.IsNil()).First(ctx)))
	got := must(client.Scalar.Update(db).Set(scalar.Seq.Set(20)).Where(scalar.Seq.EQ(2)).SaveOne(ctx))
	assertScalarEmpty(t, got)
}

// An Optional field set to its zero value is written as that value, not left
// NULL, and a Nillable one comes back as a pointer to the zero value.
func TestScalar_ZeroValueIsNotNull(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	r := must(client.Scalar.Create(db).Set(scalarRequired(1)...).Set(
		scalar.StringOpt.Set(""), scalar.StringNil.Set(""),
		scalar.IntOpt.Set(0), scalar.IntNil.Set(0),
		scalar.Uint8Opt.Set(0), scalar.Uint8Nil.Set(0),
		scalar.FloatOpt.Set(0), scalar.FloatNil.Set(0),
		scalar.BoolOpt.Set(false), scalar.BoolNil.Set(false),
		scalar.UUIDOpt.Set(uuid.Nil), scalar.UUIDNil.Set(uuid.Nil),
		scalar.BytesOpt.Set([]byte{}), scalar.BytesNil.Set([]byte{}),
	).Save(ctx))

	for _, col := range []string{"string_opt", "string_nil", "int_opt", "int_nil", "uint8_opt", "uint8_nil", "float_opt", "float_nil", "bool_opt", "bool_nil", "uuid_opt", "uuid_nil", "bytes_opt", "bytes_nil"} {
		n := rawValue[int](t, db, "SELECT COUNT(*) FROM scalars WHERE "+col+" IS NOT NULL")
		assert.Equal(t, 1, n, "column %s was set to its zero value", col)
	}
	got := must(client.Scalar.Query(db).Where(scalar.ID.EQ(r.ID)).Only(ctx))
	for name, r := range map[string]*entity.Scalar{"created": r, "queried": got} {
		require.NotNil(t, r.StringNil, name)
		assert.Equal(t, "", *r.StringNil, name)
		require.NotNil(t, r.IntNil, name)
		assert.Equal(t, 0, *r.IntNil, name)
		require.NotNil(t, r.Uint8Nil, name)
		assert.Equal(t, uint8(0), *r.Uint8Nil, name)
		require.NotNil(t, r.FloatNil, name)
		assert.Equal(t, 0.0, *r.FloatNil, name)
		require.NotNil(t, r.BoolNil, name)
		assert.False(t, *r.BoolNil, name)
		require.NotNil(t, r.UUIDNil, name)
		assert.Equal(t, uuid.Nil, *r.UUIDNil, name)
		require.NotNil(t, r.BytesNil, name)
		assert.Empty(t, *r.BytesNil, name)
	}

	// IsNil / NotNil see the difference.
	n := must(client.Scalar.Query(db).Where(scalar.StringNil.NotNil(), scalar.IntNil.NotNil(), scalar.BoolNil.NotNil(), scalar.BytesNil.NotNil()).Count(ctx))
	assert.Equal(t, 1, n)
	n = must(client.Scalar.Query(db).Where(scalar.StringNil.IsNil()).Count(ctx))
	assert.Zero(t, n)
	n = must(client.Scalar.Query(db).Where(scalar.TimeNil.IsNil(), scalar.Int64Nil.IsNil()).Count(ctx))
	assert.Equal(t, 1, n)
}

// A required field is required whether or not it is a pointer in Go.
func TestScalar_RequiredNillableMustBeSet(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	all := scalarRequired(1)
	for skip := 1; skip < len(all); skip++ {
		sets := append(append([]predicate.ScalarSetter(nil), all[:skip]...), all[skip+1:]...)
		_, err := client.Scalar.Create(db).Set(sets...).Save(ctx)
		var verr *orm.ValidationError
		require.ErrorAs(t, err, &verr, "setter %d left out", skip)
		assert.Equal(t, "Scalar", verr.Entity)
	}
	assert.Zero(t, must(client.Scalar.Query(db).Count(ctx)))
}

// Update: set, clear and set-again of every kind. Clear exists on the
// Nillable fields only; an Optional field goes back to its zero value by
// being set to it.
func TestScalar_UpdateSetAndClear(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	a := must(client.Scalar.Create(db).Set(scalarRequired(1)...).Set(scalarFull(1)...).Save(ctx))
	b := must(client.Scalar.Create(db).Set(scalarRequired(2)...).Set(scalarFull(2)...).Save(ctx))

	got := must(client.Scalar.Update(db).Set(
		scalar.StringOpt.Set(""), scalar.StringNil.Clear(), scalar.TextOpt.Set(""), scalar.TextNil.Clear(),
		scalar.IntOpt.Set(0), scalar.IntNil.Clear(), scalar.Int8Opt.Set(0), scalar.Int8Nil.Clear(),
		scalar.Int16Opt.Set(0), scalar.Int16Nil.Clear(), scalar.Int32Opt.Set(0), scalar.Int32Nil.Clear(),
		scalar.Int64Opt.Set(0), scalar.Int64Nil.Clear(), scalar.UintOpt.Set(0), scalar.UintNil.Clear(),
		scalar.Uint8Opt.Set(0), scalar.Uint8Nil.Clear(), scalar.Uint16Opt.Set(0), scalar.Uint16Nil.Clear(),
		scalar.Uint32Opt.Set(0), scalar.Uint32Nil.Clear(), scalar.Uint64Opt.Set(0), scalar.Uint64Nil.Clear(),
		scalar.FloatOpt.Set(0), scalar.FloatNil.Clear(), scalar.Float32Opt.Set(0), scalar.Float32Nil.Clear(),
		scalar.BoolOpt.Set(false), scalar.BoolNil.Clear(), scalar.TimeOpt.Set(time.Time{}), scalar.TimeNil.Clear(),
		scalar.UUIDOpt.Set(uuid.Nil), scalar.UUIDNil.Clear(), scalar.BytesOpt.Set([]byte{}), scalar.BytesNil.Clear(),
	).Where(scalar.ID.EQ(a.ID)).SaveOne(ctx))
	assertScalarRequired(t, 1, got)
	assertScalarEmpty(t, got)
	for _, col := range scalarNilCols {
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM scalars WHERE seq = 1 AND "+col+" IS NULL"), "column %s", col)
	}
	for _, col := range scalarOptCols {
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM scalars WHERE seq = 1 AND "+col+" IS NOT NULL"), "column %s", col)
	}

	// The other row is untouched.
	other := must(client.Scalar.Query(db).Where(scalar.ID.EQ(b.ID)).Only(ctx))
	assertScalarFull(t, 2, other)

	// Set again, through Exec.
	n := must(client.Scalar.Update(db).Set(scalarFull(5)...).Where(scalar.ID.EQ(a.ID)).Exec(ctx))
	assert.Equal(t, 1, n)
	got = must(client.Scalar.Query(db).Where(scalar.ID.EQ(a.ID)).Only(ctx))
	assertScalarFull(t, 5, got)

	// Numeric Add on every numeric kind, including a nillable one.
	got = must(client.Scalar.Update(db).Set(
		scalar.IntOpt.Add(10), scalar.IntNil.Add(10), scalar.Int8Opt.Add(1), scalar.Int16Opt.Add(-1),
		scalar.Int32Opt.Add(2), scalar.Int64Opt.Add(1<<40), scalar.UintOpt.Add(3), scalar.Uint8Opt.Add(4),
		scalar.Uint16Opt.Add(5), scalar.Uint32Opt.Add(6), scalar.Uint64Opt.Add(7),
		scalar.FloatOpt.Add(0.5), scalar.Float32Opt.Add(0.5),
	).Where(scalar.ID.EQ(a.ID)).SaveOne(ctx))
	assert.Equal(t, 15, got.IntOpt)
	assert.Equal(t, 5, *got.IntNil)
	assert.Equal(t, int8(6), got.Int8Opt)
	assert.Equal(t, int16(4), got.Int16Opt)
	assert.Equal(t, int32(7), got.Int32Opt)
	assert.Equal(t, int64(5+1<<40), got.Int64Opt)
	assert.Equal(t, uint(8), got.UintOpt)
	assert.Equal(t, uint8(9), got.Uint8Opt)
	assert.Equal(t, uint16(10), got.Uint16Opt)
	assert.Equal(t, uint32(11), got.Uint32Opt)
	assert.Equal(t, uint64(12), got.Uint64Opt)
	assert.Equal(t, 5.75, got.FloatOpt)
	assert.Equal(t, float32(6), got.Float32Opt)

	// Clear on create leaves NULL even next to a later To of another column.
	c := must(client.Scalar.Create(db).Set(scalarRequired(3)...).Set(scalar.StringNil.Set("x"), scalar.StringNil.Clear(), scalar.IntNil.Clear(), scalar.IntNil.Set(4)).Save(ctx))
	assert.Nil(t, c.StringNil, "Clear after To wins")
	require.NotNil(t, c.IntNil, "To after Clear wins")
	assert.Equal(t, 4, *c.IntNil)
	c = must(client.Scalar.Query(db).Where(scalar.ID.EQ(c.ID)).Only(ctx))
	assert.Nil(t, c.StringNil)
	require.NotNil(t, c.IntNil)
	assert.Equal(t, 4, *c.IntNil)
}

// Predicates of every operator on every scalar kind.
func TestScalar_Predicates(t *testing.T) {
	ctx := context.Background()
	// Nullable Optional columns, and a row of NULLs written by raw SQL: the
	// predicates must treat NULL as SQL does.
	db := openDDL(t, ddlNullableOptional)
	for n := 1; n <= 4; n++ {
		must(client.Scalar.Create(db).Set(scalarRequired(n)...).Set(scalarFull(n)...).Save(ctx))
	}
	nullOptionals(t, db, 3) // row 3 holds nothing optional
	seqs := func(ps ...predicate.Scalar) []int {
		t.Helper()
		rows := must(client.Scalar.Query(db).Where(ps...).OrderBy(scalar.Seq.Asc()).All(ctx))
		out := make([]int, len(rows))
		for i, r := range rows {
			out[i] = r.Seq
		}
		return out
	}
	none := []int{}

	// Comparison operators.
	assert.Equal(t, []int{2}, seqs(scalar.IntOpt.EQ(2)))
	assert.Equal(t, []int{1, 4}, seqs(scalar.IntOpt.NEQ(2)), "NEQ does not match NULL")
	assert.Equal(t, []int{2, 4}, seqs(scalar.IntOpt.GT(1)))
	assert.Equal(t, []int{1, 2, 4}, seqs(scalar.IntOpt.GTE(1)))
	assert.Equal(t, []int{1}, seqs(scalar.IntOpt.LT(2)))
	assert.Equal(t, []int{1, 2}, seqs(scalar.IntOpt.LTE(2)))
	assert.Equal(t, []int{1, 4}, seqs(scalar.IntOpt.In(1, 4, 99)))
	assert.Equal(t, []int{2}, seqs(scalar.IntOpt.NotIn(1, 4, 99)))
	assert.Equal(t, []int{3}, seqs(scalar.IntNil.IsNil()))
	assert.Equal(t, []int{1, 2, 4}, seqs(scalar.IntNil.NotNil()))

	// Each kind, with the Go type of its field.
	assert.Equal(t, []int{2}, seqs(scalar.Int8Opt.EQ(2)))
	assert.Equal(t, []int{2, 4}, seqs(scalar.Int16Opt.GT(1)))
	assert.Equal(t, []int{1}, seqs(scalar.Int32Opt.LT(2)))
	assert.Equal(t, []int{4}, seqs(scalar.Int64Opt.In(4)))
	assert.Equal(t, []int{1, 2, 4}, seqs(scalar.Int64Nil.EQ(math.MinInt64)))
	assert.Equal(t, []int{2}, seqs(scalar.UintOpt.EQ(2)))
	assert.Equal(t, []int{1, 2, 4}, seqs(scalar.Uint8Nil.EQ(math.MaxUint8)))
	assert.Equal(t, []int{4}, seqs(scalar.Uint16Opt.GTE(4)))
	assert.Equal(t, []int{1, 2, 4}, seqs(scalar.Uint32Nil.EQ(math.MaxUint32)))
	assert.Equal(t, []int{1, 2, 4}, seqs(scalar.Uint64Nil.EQ(math.MaxInt64)))
	assert.Equal(t, []int{2}, seqs(scalar.FloatOpt.EQ(2.25)))
	assert.Equal(t, []int{2, 4}, seqs(scalar.FloatOpt.GT(1.25)))
	assert.Equal(t, []int{2}, seqs(scalar.Float32Opt.EQ(2.5)))
	assert.Equal(t, []int{1, 2, 4}, seqs(scalar.BoolOpt.EQ(true)))
	assert.Equal(t, none, seqs(scalar.BoolOpt.EQ(false)), "NULL is not false")
	assert.Equal(t, []int{1, 2, 4}, seqs(scalar.BoolNil.EQ(false)))
	assert.Equal(t, []int{1, 2, 4}, seqs(scalar.BoolNil.NEQ(true)))
	assert.Equal(t, []int{2}, seqs(scalar.TimeOpt.EQ(t0.Add(2*time.Hour))))
	assert.Equal(t, []int{2, 4}, seqs(scalar.TimeOpt.GT(t0.Add(time.Hour))))
	assert.Equal(t, []int{1, 2}, seqs(scalar.TimeOpt.LTE(t0.Add(2*time.Hour))))
	assert.Equal(t, []int{1, 4}, seqs(scalar.TimeOpt.In(t0.Add(time.Hour), t0.Add(4*time.Hour))))
	assert.Equal(t, []int{1, 2, 4}, seqs(scalar.UUIDOpt.EQ(u2)))
	assert.Equal(t, none, seqs(scalar.UUIDOpt.EQ(u3)))
	assert.Equal(t, []int{1, 2, 4}, seqs(scalar.UUIDNil.In(u1, u3)))
	assert.Equal(t, none, seqs(scalar.UUIDNil.NotIn(u3)))
	assert.Equal(t, []int{1, 2, 3, 4}, seqs(scalar.UUIDPtr.EQ(u1)))
	assert.Equal(t, []int{2}, seqs(scalar.BytesOpt.EQ([]byte("boc"))))
	assert.Equal(t, []int{1, 4}, seqs(scalar.BytesNil.NEQ([]byte("bnc"))))
	assert.Equal(t, []int{3}, seqs(scalar.BytesNil.IsNil()))

	// String operators.
	assert.Equal(t, []int{2}, seqs(scalar.StringOpt.EQ("soc")))
	assert.Equal(t, []int{1, 2, 4}, seqs(scalar.StringOpt.HasPrefix("so")))
	assert.Equal(t, []int{4}, seqs(scalar.StringOpt.HasSuffix("oe")))
	assert.Equal(t, []int{1, 2, 4}, seqs(scalar.StringNil.Contains("n")))
	assert.Equal(t, []int{2}, seqs(scalar.StringOpt.EqualFold("SOC")))
	assert.Equal(t, []int{1, 2, 4}, seqs(scalar.StringOpt.ContainsFold("SO")))
	assert.Equal(t, []int{2, 4}, seqs(scalar.StringOpt.GT("sob")))
	assert.Equal(t, []int{1, 4}, seqs(scalar.StringOpt.In("sob", "soe")))
	assert.Equal(t, []int{2}, seqs(scalar.TextOpt.EQ("toc")))

	// LIKE metacharacters in the operand match literally.
	must(client.Scalar.Update(db).Set(scalar.StringOpt.Set("100%_done")).Where(scalar.Seq.EQ(3)).Exec(ctx))
	assert.Equal(t, []int{3}, seqs(scalar.StringOpt.Contains("%")))
	assert.Equal(t, []int{3}, seqs(scalar.StringOpt.Contains("_")))
	assert.Equal(t, []int{3}, seqs(scalar.StringOpt.HasPrefix("100%")))
	assert.Equal(t, none, seqs(scalar.StringOpt.HasPrefix("1_0")))
	assert.Equal(t, []int{3}, seqs(scalar.StringOpt.HasSuffix("%_done")))
	rawExec(t, db, "UPDATE scalars SET string_opt = NULL WHERE seq = 3")

	// In and NotIn with an empty list: nothing is in the empty set.
	assert.Equal(t, none, seqs(scalar.IntOpt.In()))
	assert.Equal(t, none, seqs(scalar.StringOpt.In()))
	assert.Equal(t, none, seqs(scalar.UUIDOpt.In()))
	assert.Equal(t, []int{1, 2, 3, 4}, seqs(scalar.Seq.NotIn()))
	assert.Equal(t, none, seqs(scalar.Seq.GT(0), scalar.IntOpt.In()), "an empty In next to another predicate")
	assert.Equal(t, []int{1, 2, 3, 4}, seqs(scalar.Not(scalar.Seq.In())))

	// Combinators.
	assert.Equal(t, []int{1, 4}, seqs(scalar.Or(scalar.IntOpt.EQ(1), scalar.IntOpt.EQ(4))))
	assert.Equal(t, []int{2}, seqs(scalar.And(scalar.IntOpt.GT(1), scalar.IntOpt.LT(4))))
	assert.Equal(t, []int{1, 4}, seqs(scalar.Not(scalar.IntOpt.EQ(2)), scalar.IntNil.NotNil()))
	// Not of several predicates negates their conjunction.
	assert.Equal(t, []int{1, 2, 3},
		seqs(scalar.Not(scalar.And(scalar.Seq.GT(1), scalar.Seq.GT(3)), scalar.Or(scalar.Seq.EQ(4), scalar.Seq.EQ(9)))))
	assert.Equal(t, []int{1, 3},
		seqs(scalar.Or(
			scalar.And(scalar.Seq.EQ(1), scalar.Not(scalar.IntNil.IsNil())),
			scalar.And(scalar.IntNil.IsNil(), scalar.Not(scalar.Or(scalar.Seq.EQ(1), scalar.Seq.EQ(2)))),
		)))
	// Where's arguments and repeated Where calls are a conjunction.
	rows := must(client.Scalar.Query(db).Where(scalar.Seq.GT(1)).Where(scalar.Seq.LT(4), scalar.IntNil.NotNil()).All(ctx))
	require.Len(t, rows, 1)
	assert.Equal(t, 2, rows[0].Seq)
	// A single-operand And / Or is that operand.
	assert.Equal(t, []int{2}, seqs(scalar.And(scalar.Seq.EQ(2))))
	assert.Equal(t, []int{2}, seqs(scalar.Or(scalar.Seq.EQ(2))))

	// The same predicates restrict Update and Delete.
	n := must(client.Scalar.Update(db).Set(scalar.StringOpt.Set("hit")).
		Where(scalar.Or(scalar.IntOpt.In(), scalar.And(scalar.IntNil.NotNil(), scalar.Not(scalar.Seq.In(1, 2))))).Exec(ctx))
	assert.Equal(t, 1, n)
	assert.Equal(t, []int{4}, seqs(scalar.StringOpt.EQ("hit")))
	n = must(client.Scalar.Delete(db).Where(scalar.IntNil.IsNil()).Exec(ctx))
	assert.Equal(t, 1, n)
	n = must(client.Scalar.Delete(db).Where(scalar.IntOpt.In()).Exec(ctx))
	assert.Zero(t, n, "a delete restricted by an empty In deletes nothing")
	assert.Equal(t, []int{1, 2, 4}, seqs())
}
