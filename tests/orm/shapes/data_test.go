package shapes

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/syssam/velox/tests/orm/shapes/db/predicate"

	client "github.com/syssam/velox/tests/orm/shapes/db"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
	"github.com/syssam/velox/tests/orm/shapes/db/blob"
	"github.com/syssam/velox/tests/orm/shapes/db/entity"
	"github.com/syssam/velox/tests/orm/shapes/db/int64key"
	"github.com/syssam/velox/tests/orm/shapes/db/intkey"
	"github.com/syssam/velox/tests/orm/shapes/db/manualkey"
	"github.com/syssam/velox/tests/orm/shapes/db/namedbytes"
	"github.com/syssam/velox/tests/orm/shapes/db/stringkey"
	"github.com/syssam/velox/tests/orm/shapes/db/uint16key"
	"github.com/syssam/velox/tests/orm/shapes/db/uuidkey"
	"github.com/syssam/velox/tests/orm/shapes/db/wide"
	"github.com/syssam/velox/tests/orm/shapes/types"
)

// Bytes and JSON in every nullability: a row of NULLs between two rows of
// values, and empty next to NULL.
func TestBlob_BytesAndJSON(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	addr := types.Address{Street: "1 Main", City: "Springfield", Zip: 12345}

	// data and req are required.
	_, err := client.Blob.Create(db).Set(blob.Seq.Set(0), blob.Req.Set(addr)).Save(ctx)
	invalid(t, err, "data")
	_, err = client.Blob.Create(db).Set(blob.Seq.Set(0), blob.Data.Set([]byte("d"))).Save(ctx)
	invalid(t, err, "req")

	full := func(seq int) *entity.BlobCreate {
		return client.Blob.Create(db).Set(
			blob.Seq.Set(seq), blob.Data.Set([]byte{0, 1, 2, 0xff}), blob.Opt.Set([]byte("opt")), blob.Optd.Set([]byte("set")),
			blob.Nilb.Set([]byte("nilb")), blob.Nils.Set("nils"),
			blob.Addr.Set(addr), blob.AddrPtr.Set(types.Address{City: "Ptr"}),
			blob.Tags.Set([]string{"x", "y"}), blob.Attrs.Set(map[string]int{"a": 1, "b": 2}),
			blob.Req.Set(types.Address{City: "Req"}), blob.Strs.Set([]string{"s"}), blob.Ints.Set([]int{1, -2}),
			blob.Floats.Set([]float64{1.5}), blob.Anything.Set(map[string]any{"k": []any{"v", 1.0, true, nil}}),
			blob.Free.Set(map[string]any{"n": 1.0, "s": "x"}),
		)
	}
	first := must(full(1).Save(ctx))
	middle := must(client.Blob.Create(db).Set(blob.Seq.Set(2), blob.Data.Set([]byte{}), blob.Req.Set(types.Address{})).Save(ctx))
	last := must(full(3).Save(ctx))
	// Empty, not NULL.
	empty := must(client.Blob.Create(db).Set(
		blob.Seq.Set(4), blob.Data.Set([]byte{}), blob.Req.Set(types.Address{}),
		blob.Opt.Set([]byte{}), blob.Optd.Set([]byte{}), blob.Nilb.Set([]byte{}), blob.Nils.Set(""),
		blob.Addr.Set(types.Address{}), blob.Tags.Set([]string{}), blob.Attrs.Set(map[string]int{}),
		blob.Strs.Set(nil), blob.Anything.Set(nil),
	).Save(ctx))

	checkFull := func(name string, r *entity.Blob) {
		t.Helper()
		assert.Equal(t, []byte{0, 1, 2, 0xff}, r.Data, name)
		assert.Equal(t, []byte("opt"), r.Opt, name)
		assert.Equal(t, []byte("set"), r.Optd, name)
		require.NotNil(t, r.Nilb, name)
		assert.Equal(t, []byte("nilb"), *r.Nilb, name)
		require.NotNil(t, r.Nils, name)
		assert.Equal(t, "nils", *r.Nils, name)
		assert.Equal(t, addr, r.Addr, name)
		assert.Equal(t, types.Address{City: "Ptr"}, r.AddrPtr, name)
		assert.Equal(t, []string{"x", "y"}, r.Tags, name)
		assert.Equal(t, map[string]int{"a": 1, "b": 2}, r.Attrs, name)
		assert.Equal(t, types.Address{City: "Req"}, r.Req, name)
		assert.Equal(t, []string{"s"}, r.Strs, name)
		assert.Equal(t, []int{1, -2}, r.Ints, name)
		assert.Equal(t, []float64{1.5}, r.Floats, name)
		assert.Equal(t, map[string]any{"k": []any{"v", 1.0, true, nil}}, r.Anything, name)
		assert.Equal(t, map[string]any{"n": 1.0, "s": "x"}, r.Free, name)
	}
	checkMinimal := func(name string, r *entity.Blob) {
		t.Helper()
		assert.Empty(t, r.Data, name)
		assert.Empty(t, r.Opt, name)
		assert.Equal(t, []byte("dflt"), r.Optd, name+": the default of an optional bytes field")
		assert.Nil(t, r.Nilb, name+": a NULL nillable bytes column is a nil pointer")
		assert.Nil(t, r.Nils, name)
		assert.Equal(t, types.Address{}, r.Addr, name)
		assert.Equal(t, types.Address{}, r.AddrPtr, name)
		assert.Nil(t, r.Tags, name)
		assert.Nil(t, r.Attrs, name)
		assert.Equal(t, types.Address{}, r.Req, name)
		assert.Nil(t, r.Strs, name)
		assert.Nil(t, r.Ints, name)
		assert.Nil(t, r.Floats, name)
		assert.Nil(t, r.Anything, name)
		assert.Nil(t, r.Free, name)
	}
	checkFull("created first", first)
	checkMinimal("created middle", middle)
	checkFull("created last", last)

	rows := must(client.Blob.Query(db).OrderBy(blob.Seq.Asc()).All(ctx))
	require.Len(t, rows, 4)
	checkFull("queried first", rows[0])
	checkMinimal("queried middle", rows[1])
	checkFull("queried last", rows[2])

	// The empty row: empty values, and none of them NULL in the database.
	for name, r := range map[string]*entity.Blob{"created": empty, "queried": rows[3]} {
		assert.Empty(t, r.Opt, name)
		assert.Empty(t, r.Optd, name+": an explicit empty value is not replaced by the default")
		require.NotNil(t, r.Nilb, name+": empty bytes is not NULL")
		assert.Empty(t, *r.Nilb, name)
		require.NotNil(t, r.Nils, name)
		assert.Empty(t, *r.Nils, name)
		assert.Empty(t, r.Tags, name)
		assert.Empty(t, r.Attrs, name)
	}
	for _, col := range []string{"opt", "optd", "nilb", "nils", "addr", "tags", "attrs"} {
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM blobs WHERE seq = 4 AND "+col+" IS NOT NULL"), col)
	}
	// The row that set nothing: NULL in the Nillable and in the JSON columns
	// (which are nullable on velox's tables); an unset Optional bytes column
	// holds empty bytes.
	for _, col := range []string{"nilb", "nils", "addr", "addr_ptr", "tags", "attrs", "strs", "ints", "floats", "anything", "free"} {
		assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM blobs WHERE seq = 2 AND "+col+" IS NULL"), col)
	}
	assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM blobs WHERE seq = 2 AND opt IS NOT NULL AND length(opt) = 0"))
	assert.JSONEq(t, `{"street":"1 Main","city":"Springfield","zip":12345}`, rawValue[string](t, db, "SELECT addr FROM blobs WHERE seq = 1"))
	assert.JSONEq(t, `["x","y"]`, rawValue[string](t, db, "SELECT tags FROM blobs WHERE seq = 1"))

	// Rows do not share memory.
	rows[0].Data[0] = 'X'
	rows[0].Tags[0] = "X"
	rows[0].Attrs["a"] = 99
	assert.Equal(t, byte(0), rows[2].Data[0])
	assert.Equal(t, "x", rows[2].Tags[0])
	assert.Equal(t, 1, rows[2].Attrs["a"])

	// Update: replace, clear, set again. JSON columns are nullable, so a
	// JSON field can be cleared; so can a Nillable one.
	got := must(client.Blob.Update(db).Set(
		blob.Data.Set([]byte("new")), blob.Opt.Set([]byte{}), blob.Nilb.Clear(), blob.Addr.Clear(), blob.Tags.Set([]string{"z"}),
		blob.Attrs.Clear(), blob.Anything.Set("just a string"), blob.Req.Set(types.Address{Zip: 1}), blob.Ints.Clear(),
	).Where(blob.Seq.EQ(1)).SaveOne(ctx))
	assert.Equal(t, []byte("new"), got.Data)
	assert.Empty(t, got.Opt)
	assert.Nil(t, got.Nilb)
	assert.Equal(t, types.Address{}, got.Addr)
	assert.Equal(t, []string{"z"}, got.Tags)
	assert.Nil(t, got.Attrs)
	assert.Equal(t, "just a string", got.Anything)
	assert.Equal(t, types.Address{Zip: 1}, got.Req)
	assert.Nil(t, got.Ints)
	assert.Equal(t, []float64{1.5}, got.Floats, "untouched")
	assert.Equal(t, 3, rawValue[int](t, db, "SELECT (addr IS NULL) + (attrs IS NULL) + (ints IS NULL) FROM blobs WHERE seq = 1"))
	n := must(client.Blob.Update(db).Set(blob.Nilb.Set([]byte("again")), blob.Attrs.Set(map[string]int{"z": 26})).Where(blob.Seq.In(1, 2)).Exec(ctx))
	assert.Equal(t, 2, n)
	for _, r := range must(client.Blob.Query(db).Where(blob.Seq.In(1, 2)).All(ctx)) {
		require.NotNil(t, r.Nilb)
		assert.Equal(t, []byte("again"), *r.Nilb)
		assert.Equal(t, map[string]int{"z": 26}, r.Attrs)
	}

	// Bytes predicates, and IsNil / NotNil on bytes and JSON columns.
	seqs := func(ps ...predicate.Blob) []int {
		var out []int
		for _, r := range must(client.Blob.Query(db).Where(ps...).OrderBy(blob.Seq.Asc()).All(ctx)) {
			out = append(out, r.Seq)
		}
		return out
	}
	assert.Equal(t, []int{1}, seqs(blob.Data.EQ([]byte("new"))))
	assert.Equal(t, []int{1, 2}, seqs(blob.Nilb.EQ([]byte("again"))))
	assert.Equal(t, []int{1, 2, 3, 4}, seqs(blob.Nilb.NotNil()))
	assert.Equal(t, []int{1, 2}, seqs(blob.Addr.IsNil()), "cleared, and never set")
	assert.Equal(t, []int{3, 4}, seqs(blob.Addr.NotNil()))
	assert.Equal(t, []int{1, 4}, seqs(blob.Ints.IsNil(), blob.Tags.NotNil()))
	assert.Equal(t, []int{2}, seqs(blob.Tags.IsNil(), blob.Free.IsNil(), blob.Anything.IsNil()))
}

// 40 columns of mixed types: a misaligned column shows up as a wrong value.
func wideRow(db velox.Executor, n int, optional bool) *entity.WideCreate {
	b := client.Wide.Create(db).Set(
		wide.Key.Set(fmt.Sprintf("k%d", n)), wide.S1.Set(fmt.Sprintf("s1-%d", n)), wide.T1.Set(fmt.Sprintf("t1-%d", n)),
		wide.B1.Set(n%2 == 0), wide.I1.Set(n), wide.I8.Set(int8(-n)), wide.I16.Set(int16(n*100)), wide.I32.Set(int32(n*1000)),
		wide.I64.Set(int64(n)<<40), wide.U1.Set(uint(n)), wide.U8.Set(uint8(n)), wide.U16.Set(uint16(n*200)),
		wide.U32.Set(uint32(n)<<20), wide.U64.Set(uint64(n)<<50), wide.F64.Set(float64(n)/4), wide.F32.Set(float32(n)/2),
		wide.At.Set(t0.Add(time.Duration(n)*time.Minute)), wide.UID.Set(uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", n))),
		wide.Raw.Set([]byte{byte(n), 0, byte(n)}), wide.Kind.Set(wide.KindB), wide.List.Set([]int{n, n + 1}),
		wide.Label.Set(types.Label(fmt.Sprintf("l%d", n))), wide.Weight.Set(types.Weight(n*7)),
		wide.Price.Set(types.Decimal{Cents: int64(n * 101)}), wide.Tier.Set(types.TierPro), wide.Tail.Set(fmt.Sprintf("tail-%d", n)),
	)
	if optional {
		b.Set(
			wide.S2.Set(fmt.Sprintf("s2-%d", n)), wide.S3.Set(fmt.Sprintf("s3-%d", n)), wide.B2.Set(n%2 == 1), wide.I2.Set(-n),
			wide.F3.Set(float64(n)*1.5), wide.At2.Set(t0.Add(-time.Duration(n)*time.Minute)), wide.Uid2.Set(u2),
			wide.Raw2.Set([]byte{byte(n)}), wide.Kind2.Set(wide.Kind2Y), wide.Dict.Set(map[string]string{"n": fmt.Sprint(n)}),
			wide.Addr.Set(types.Address{Zip: n}), wide.Dflt.Set(fmt.Sprintf("d-%d", n)), wide.N1.Set(n*3), wide.N2.Set(n*5),
		)
	}
	return b
}

func assertWide(t *testing.T, name string, n int, optional bool, r *entity.Wide) {
	t.Helper()
	assert.Equal(t, fmt.Sprintf("k%d", n), r.Key, name)
	assert.Equal(t, fmt.Sprintf("s1-%d", n), r.S1, name)
	assert.Equal(t, fmt.Sprintf("t1-%d", n), r.T1, name)
	assert.Equal(t, n%2 == 0, r.B1, name)
	assert.Equal(t, n, r.I1, name)
	assert.Equal(t, int8(-n), r.I8, name)
	assert.Equal(t, int16(n*100), r.I16, name)
	assert.Equal(t, int32(n*1000), r.I32, name)
	assert.Equal(t, int64(n)<<40, r.I64, name)
	assert.Equal(t, uint(n), r.U1, name)
	assert.Equal(t, uint8(n), r.U8, name)
	assert.Equal(t, uint16(n*200), r.U16, name)
	assert.Equal(t, uint32(n)<<20, r.U32, name)
	assert.Equal(t, uint64(n)<<50, r.U64, name)
	assert.Equal(t, float64(n)/4, r.F64, name)
	assert.Equal(t, float32(n)/2, r.F32, name)
	assert.True(t, r.At.Equal(t0.Add(time.Duration(n)*time.Minute)), name)
	assert.Equal(t, uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", n)), r.UID, name)
	assert.Equal(t, []byte{byte(n), 0, byte(n)}, r.Raw, name)
	assert.Equal(t, wide.KindB, r.Kind, name)
	assert.Equal(t, []int{n, n + 1}, r.List, name)
	assert.Equal(t, types.Label(fmt.Sprintf("l%d", n)), r.Label, name)
	assert.Equal(t, types.Weight(n*7), r.Weight, name)
	assert.Equal(t, types.Decimal{Cents: int64(n * 101)}, r.Price, name)
	assert.Equal(t, types.TierPro, r.Tier, name)
	assert.Equal(t, fmt.Sprintf("tail-%d", n), r.Tail, name)
	if !optional {
		assert.Zero(t, r.S2, name)
		assert.Nil(t, r.S3, name)
		assert.Nil(t, r.B2, name)
		assert.Nil(t, r.I2, name)
		assert.Nil(t, r.F3, name)
		assert.Nil(t, r.At2, name)
		assert.Nil(t, r.Uid2, name)
		assert.Nil(t, r.Raw2, name)
		assert.Nil(t, r.Kind2, name)
		assert.Nil(t, r.Dict, name)
		assert.Equal(t, types.Address{}, r.Addr, name)
		assert.Equal(t, "d", r.Dflt, name)
		assert.Equal(t, 1, r.N1, name)
		assert.Zero(t, r.N2, name)
		return
	}
	assert.Equal(t, fmt.Sprintf("s2-%d", n), r.S2, name)
	require.NotNil(t, r.S3, name)
	assert.Equal(t, fmt.Sprintf("s3-%d", n), *r.S3, name)
	require.NotNil(t, r.B2, name)
	assert.Equal(t, n%2 == 1, *r.B2, name)
	require.NotNil(t, r.I2, name)
	assert.Equal(t, -n, *r.I2, name)
	require.NotNil(t, r.F3, name)
	assert.Equal(t, float64(n)*1.5, *r.F3, name)
	require.NotNil(t, r.At2, name)
	assert.True(t, r.At2.Equal(t0.Add(-time.Duration(n)*time.Minute)), name)
	require.NotNil(t, r.Uid2, name)
	assert.Equal(t, u2, *r.Uid2, name)
	require.NotNil(t, r.Raw2, name)
	assert.Equal(t, []byte{byte(n)}, *r.Raw2, name)
	require.NotNil(t, r.Kind2, name)
	assert.Equal(t, wide.Kind2Y, *r.Kind2, name)
	assert.Equal(t, map[string]string{"n": fmt.Sprint(n)}, r.Dict, name)
	assert.Equal(t, types.Address{Zip: n}, r.Addr, name)
	assert.Equal(t, fmt.Sprintf("d-%d", n), r.Dflt, name)
	assert.Equal(t, n*3, r.N1, name)
	assert.Equal(t, n*5, r.N2, name)
}

func TestWide_SingleAndBulkAlignment(t *testing.T) {
	ctx := context.Background()
	db := open(t)

	one := must(wideRow(db, 1, true).Save(ctx))
	assertWide(t, "created", 1, true, one)
	assertWide(t, "queried", 1, true, must(client.Wide.Query(db).Where(wide.ID.EQ(one.ID)).Only(ctx)))

	// A bulk create whose rows set different columns: with, without, with,
	// without, with. The statement has one column list for all of them.
	want := []bool{true, false, true, false, true}
	var bs []*entity.WideCreate
	for i, opt := range want {
		bs = append(bs, wideRow(db, 10+i, opt))
	}
	rows := must(client.Wide.CreateBulk(db, bs...).Save(ctx))
	require.Len(t, rows, len(want))
	for i, r := range rows {
		name := fmt.Sprintf("bulk row %d", i)
		assertWide(t, name+" created", 10+i, want[i], r)
		require.NotZero(t, r.ID, name)
		if i > 0 {
			assert.Equal(t, rows[i-1].ID+1, r.ID, name+": ids follow the order of the builders")
		}
		// The id an entity carries is the id of the row holding its values.
		assertWide(t, name+" queried", 10+i, want[i], must(client.Wide.Query(db).Where(wide.ID.EQ(r.ID)).Only(ctx)))
	}
	assert.Equal(t, 6, must(client.Wide.Query(db).Count(ctx)))

	// A constraint violation in the middle of a bulk fails the whole batch.
	_, err := client.Wide.CreateBulk(db, wideRow(db, 20, false), wideRow(db, 10, false), wideRow(db, 21, true)).Save(ctx)
	require.True(t, velox.IsConstraintError(err), "got %v", err)
	assert.Equal(t, 6, must(client.Wide.Query(db).Count(ctx)))

	// Exec, and the empty bulk.
	require.NoError(t, client.Wide.CreateBulk(db, wideRow(db, 30, true), wideRow(db, 31, false)).Exec(ctx))
	assert.Equal(t, 8, must(client.Wide.Query(db).Count(ctx)))
	none := must(client.Wide.CreateBulk(db).Save(ctx))
	assert.Empty(t, none)
	require.NoError(t, client.Wide.CreateBulk(db).Exec(ctx))

	// Missing a required field in one row names the field.
	_, err = client.Wide.CreateBulk(db, wideRow(db, 40, true), client.Wide.Create(db).Set(wide.Key.Set("k41"))).Save(ctx)
	invalid(t, err, "s1")
	assert.Equal(t, 8, must(client.Wide.Query(db).Count(ctx)))

	// Update of every required column at once.
	_, err = wideRow(db, 50, false).Save(ctx)
	require.NoError(t, err)
	got := must(client.Wide.Update(db).Set(
		wide.S1.Set("s1-60"), wide.T1.Set("t1-60"), wide.B1.Set(true), wide.I1.Set(60), wide.I8.Set(-60), wide.I16.Set(6000),
		wide.I32.Set(60000), wide.I64.Set(60<<40), wide.U1.Set(60), wide.U8.Set(60), wide.U16.Set(12000), wide.U32.Set(60<<20),
		wide.U64.Set(60<<50), wide.F64.Set(15), wide.F32.Set(30), wide.At.Set(t0.Add(time.Hour)),
		wide.UID.Set(uuid.MustParse("00000000-0000-4000-8000-000000000060")), wide.Raw.Set([]byte{60, 0, 60}),
		wide.List.Set([]int{60, 61}), wide.Label.Set("l60"), wide.Weight.Set(420), wide.Price.Set(types.Decimal{Cents: 6060}),
		wide.Tail.Set("tail-60"), wide.Key.Set("k60"),
	).Where(wide.Key.EQ("k50")).SaveOne(ctx))
	assertWide(t, "updated", 60, false, got)
}

func TestKeys_EveryIDKind(t *testing.T) {
	ctx := context.Background()
	db := open(t)

	t.Run("int", func(t *testing.T) {
		use(t)
		a := must(client.IntKey.Create(db).Set(intkey.Name.Set("a")).Save(ctx))
		b := must(client.IntKey.Create(db).Set(intkey.Name.Set("b")).Save(ctx))
		assert.Equal(t, a.ID+1, b.ID)
		c := must(client.IntKey.Create(db).Set(intkey.ID.Set(100), intkey.Name.Set("c")).Save(ctx))
		assert.Equal(t, 100, c.ID)
		assert.Equal(t, []int{a.ID, b.ID, 100}, must(client.IntKey.Query(db).OrderBy(intkey.ID.Asc()).IDs(ctx)))
		_, err := client.IntKey.Create(db).Set(intkey.ID.Set(100), intkey.Name.Set("dup")).Save(ctx)
		require.True(t, velox.IsConstraintError(err), "got %v", err)
		got := must(client.IntKey.Query(db).Where(intkey.ID.In(a.ID, 100)).OrderBy(intkey.ID.Desc()).All(ctx))
		require.Len(t, got, 2)
		assert.Equal(t, "c", got[0].Name)
		n := must(client.IntKey.Delete(db).Where(intkey.ID.EQ(a.ID)).Exec(ctx))
		assert.Equal(t, 1, n)
		n = must(client.IntKey.Delete(db).Where(intkey.ID.EQ(a.ID)).Exec(ctx))
		assert.Zero(t, n)
	})

	t.Run("int64", func(t *testing.T) {
		use(t)
		a := must(client.Int64Key.Create(db).Set(int64key.Name.Set("a")).Save(ctx))
		require.NotZero(t, a.ID)
		big := int64(1) << 40
		b := must(client.Int64Key.Create(db).Set(int64key.ID.Set(big), int64key.Name.Set("b")).Save(ctx))
		assert.Equal(t, big, b.ID)
		assert.Equal(t, []int64{a.ID, big}, must(client.Int64Key.Query(db).OrderBy(int64key.ID.Asc()).IDs(ctx)))
		got := must(client.Int64Key.Update(db).Set(int64key.Name.Set("b2")).Where(int64key.ID.EQ(big)).SaveOne(ctx))
		assert.Equal(t, big, got.ID)
		rows := must(client.Int64Key.CreateBulk(db, client.Int64Key.Create(db).Set(int64key.Name.Set("x")), client.Int64Key.Create(db).Set(int64key.Name.Set("y"))).Save(ctx))
		assert.Equal(t, rows[0].ID+1, rows[1].ID)
		assert.Equal(t, "y", must(client.Int64Key.Query(db).Where(int64key.ID.EQ(rows[1].ID)).Only(ctx)).Name)
	})

	t.Run("uint16", func(t *testing.T) {
		use(t)
		a := must(client.Uint16Key.Create(db).Set(uint16key.Name.Set("a")).Save(ctx))
		require.NotZero(t, a.ID)
		b := must(client.Uint16Key.Create(db).Set(uint16key.ID.Set(65535), uint16key.Name.Set("b")).Save(ctx))
		assert.Equal(t, uint16(65535), b.ID)
		assert.Equal(t, []uint16{a.ID, 65535}, must(client.Uint16Key.Query(db).OrderBy(uint16key.ID.Asc()).IDs(ctx)))
		assert.Equal(t, "b", must(client.Uint16Key.Query(db).Where(uint16key.ID.GT(a.ID)).Only(ctx)).Name)
	})

	t.Run("string with a default func and validators", func(t *testing.T) {
		use(t)
		a := must(client.StringKey.Create(db).Set(stringkey.Name.Set("a")).Save(ctx))
		b := must(client.StringKey.Create(db).Set(stringkey.Name.Set("b")).Save(ctx))
		assert.Contains(t, a.ID, "sk-")
		assert.NotEqual(t, a.ID, b.ID)
		c := must(client.StringKey.Create(db).Set(stringkey.ID.Set("mine"), stringkey.Name.Set("c")).Save(ctx))
		assert.Equal(t, "mine", c.ID)
		assert.ElementsMatch(t, []string{a.ID, b.ID, "mine"}, must(client.StringKey.Query(db).IDs(ctx)))
		_, err := client.StringKey.Create(db).Set(stringkey.ID.Set("thirteen-char"), stringkey.Name.Set("x")).Save(ctx)
		invalid(t, err, "id")
		_, err = client.StringKey.Create(db).Set(stringkey.ID.Set(""), stringkey.Name.Set("x")).Save(ctx)
		invalid(t, err, "id")
		_, err = client.StringKey.Create(db).Set(stringkey.ID.Set("mine"), stringkey.Name.Set("dup")).Save(ctx)
		require.True(t, velox.IsConstraintError(err), "got %v", err)
		got := must(client.StringKey.Update(db).Set(stringkey.Name.Set("c2")).Where(stringkey.ID.EQ("mine")).SaveOne(ctx))
		assert.Equal(t, "c2", got.Name)
		assert.Equal(t, "mine", got.ID)
		assert.Equal(t, "c2", must(client.StringKey.Query(db).Where(stringkey.ID.GT("m"), stringkey.ID.LT("n")).Only(ctx)).Name)
		rows := must(client.StringKey.CreateBulk(db, client.StringKey.Create(db).Set(stringkey.Name.Set("x")), client.StringKey.Create(db).Set(stringkey.ID.Set("y-id"), stringkey.Name.Set("y"))).Save(ctx))
		assert.Contains(t, rows[0].ID, "sk-")
		assert.Equal(t, "y-id", rows[1].ID)
		assert.Equal(t, "x", must(client.StringKey.Query(db).Where(stringkey.ID.EQ(rows[0].ID)).Only(ctx)).Name)
	})

	t.Run("uuid with a default func", func(t *testing.T) {
		use(t)
		a := must(client.UUIDKey.Create(db).Set(uuidkey.Name.Set("a")).Save(ctx))
		assert.NotEqual(t, uuid.Nil, a.ID)
		assert.Equal(t, a.ID, must(client.UUIDKey.Query(db).Where(uuidkey.Name.EQ("a")).Only(ctx)).ID, "the id returned is the id stored")
		b := must(client.UUIDKey.Create(db).Set(uuidkey.ID.Set(u1), uuidkey.Name.Set("b")).Save(ctx))
		assert.Equal(t, u1, b.ID)
		assert.ElementsMatch(t, []uuid.UUID{a.ID, u1}, must(client.UUIDKey.Query(db).IDs(ctx)))
		assert.Equal(t, "a", must(client.UUIDKey.Query(db).Where(uuidkey.ID.EQ(a.ID)).Only(ctx)).Name)
		assert.Equal(t, "b", must(client.UUIDKey.Query(db).Where(uuidkey.ID.In(u1, u2)).Only(ctx)).Name)
		assert.Equal(t, "a", must(client.UUIDKey.Query(db).Where(uuidkey.ID.NEQ(u1)).Only(ctx)).Name)
		got := must(client.UUIDKey.Update(db).Set(uuidkey.Name.Set("b2")).Where(uuidkey.ID.EQ(u1)).SaveOne(ctx))
		assert.Equal(t, u1, got.ID)
		assert.Equal(t, "b2", got.Name)
		got = must(client.UUIDKey.Update(db).Set(uuidkey.Name.Set("b3")).Where(uuidkey.Name.EQ("b2")).SaveOne(ctx))
		assert.Equal(t, u1, got.ID)
		_, err := client.UUIDKey.Create(db).Set(uuidkey.ID.Set(u1), uuidkey.Name.Set("dup")).Save(ctx)
		require.True(t, velox.IsConstraintError(err), "got %v", err)
		rows := must(client.UUIDKey.CreateBulk(db, client.UUIDKey.Create(db).Set(uuidkey.Name.Set("x")), client.UUIDKey.Create(db).Set(uuidkey.ID.Set(u3), uuidkey.Name.Set("y")), client.UUIDKey.Create(db).Set(uuidkey.Name.Set("z"))).Save(ctx))
		require.Len(t, rows, 3)
		assert.NotEqual(t, uuid.Nil, rows[0].ID)
		assert.Equal(t, u3, rows[1].ID)
		assert.NotEqual(t, rows[0].ID, rows[2].ID)
		for _, r := range rows {
			assert.Equal(t, r.Name, must(client.UUIDKey.Query(db).Where(uuidkey.ID.EQ(r.ID)).Only(ctx)).Name)
		}
		n := must(client.UUIDKey.Delete(db).Where(uuidkey.ID.NotIn(u1)).Exec(ctx))
		assert.Equal(t, 4, n)
	})

	t.Run("string the caller must set", func(t *testing.T) {
		use(t)
		a := must(client.ManualKey.Create(db).Set(manualkey.ID.Set("m1"), manualkey.Name.Set("a")).Save(ctx))
		assert.Equal(t, "m1", a.ID)
		_, err := client.ManualKey.Create(db).Set(manualkey.ID.Set("nine-char"), manualkey.Name.Set("x")).Save(ctx)
		invalid(t, err, "id")
		// Without an id there is nothing the database could generate.
		_, err = client.ManualKey.Create(db).Set(manualkey.Name.Set("no id")).Save(ctx)
		require.Error(t, err)
		assert.Equal(t, 1, must(client.ManualKey.Query(db).Count(ctx)))
	})
}

// A bytes field with a named Go type.
func TestNamedBytes_RoundTrip(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	ip1, ip2 := net.ParseIP("10.0.0.1").To4(), net.ParseIP("192.168.1.2").To4()
	a := must(client.NamedBytes.Create(db).Set(namedbytes.IP.Set(ip1), namedbytes.IPOpt.Set(ip2), namedbytes.IPNil.Set(ip1)).Save(ctx))
	b := must(client.NamedBytes.Create(db).Set(namedbytes.IP.Set(ip2)).Save(ctx))
	assert.Empty(t, b.IPOpt)
	assert.Equal(t, 1, rawValue[int](t, db, "SELECT COUNT(*) FROM named_bytes WHERE ip_opt IS NOT NULL AND length(ip_opt) = 0 AND ip_nil IS NULL"))
	c := must(client.NamedBytes.Create(db).Set(namedbytes.IP.Set(ip1), namedbytes.IPOpt.Set(ip1), namedbytes.IPNil.Set(ip2)).Save(ctx))
	_, _, _ = a, b, c
	rows := must(client.NamedBytes.Query(db).OrderBy(namedbytes.ID.Asc()).All(ctx))
	require.Len(t, rows, 3)
	assert.Equal(t, ip1, rows[0].IP)
	assert.Equal(t, ip2, rows[0].IPOpt)
	require.NotNil(t, rows[0].IPNil)
	assert.Equal(t, ip1, *rows[0].IPNil)
	assert.Equal(t, ip2, rows[1].IP)
	assert.Empty(t, rows[1].IPOpt)
	assert.Nil(t, rows[1].IPNil)
	assert.Equal(t, ip1, rows[2].IP)
	assert.Equal(t, ip1, rows[2].IPOpt)
	require.NotNil(t, rows[2].IPNil)
	assert.Equal(t, ip2, *rows[2].IPNil)
	// Rows do not share memory with each other or with the driver.
	rows[0].IP[0] = 99
	assert.Equal(t, ip1, rows[2].IP)
	assert.Equal(t, 2, must(client.NamedBytes.Query(db).Where(namedbytes.IP.EQ(ip1)).Count(ctx)))
}

// The entity a Create returns holds what a query of the row returns, for
// every scalar kind (the returned entity is built from the setters, not read
// back).
func TestCreate_ReturnedEntityEqualsQueried(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	created := must(client.Scalar.Create(db).Set(scalarRequired(1)...).Set(scalarFull(1)...).Save(ctx))
	queried := must(client.Scalar.Query(db).Only(ctx))
	// Times compare by instant, everything else exactly.
	for _, p := range []*time.Time{&created.TimeOpt, created.TimeNil, created.TimePtr} {
		*p = p.UTC()
	}
	for _, p := range []*time.Time{&queried.TimeOpt, queried.TimeNil, queried.TimePtr} {
		*p = p.UTC()
	}
	assert.Equal(t, queried, created)
}
