package matrix

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"
	"unsafe"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect/sql"

	"example.com/matrix/types"
	gen "example.com/matrix/velox"
	"example.com/matrix/velox/blob"
	wideclient "example.com/matrix/velox/client/wide"
	"example.com/matrix/velox/entity"
	mtime "example.com/matrix/velox/time"
	"example.com/matrix/velox/wide"
)

// blobRow is what row seq must read back as. Rows 2 and 4 (the middle and
// the last of five) carry NULL in every nullable column.
type blobRow struct {
	data, opt []byte
	optd      []byte // set on the rows that are not NULL rows, defaulted otherwise
	nilb      []byte // nil = NULL
	nils      *string
	addr      types.Address
	tags      []string
	attrs     map[string]int
}

func wantBlob(seq int) blobRow {
	r := blobRow{
		data: []byte(fmt.Sprintf("data-%d", seq)),
		opt:  []byte(fmt.Sprintf("opt-%d", seq)),
	}
	if seq == 2 || seq == 4 {
		r.optd = []byte("dflt")
		return r
	}
	r.optd = []byte(fmt.Sprintf("optd-%d", seq))
	r.nilb = []byte(fmt.Sprintf("nilb-%d", seq))
	r.nils = gen.Ptr(fmt.Sprintf("nils-%d", seq))
	r.addr = types.Address{Street: fmt.Sprintf("%d Main St", seq), City: "X", Zip: 1000 + seq}
	r.tags = []string{fmt.Sprintf("t%d", seq), "shared"}
	r.attrs = map[string]int{"seq": seq}
	return r
}

func assertBlob(t *testing.T, got *entity.Blob) {
	t.Helper()
	want := wantBlob(got.Seq)
	assert.Equal(t, want.data, got.Data, "seq %d data", got.Seq)
	assert.Equal(t, want.opt, got.Opt, "seq %d opt", got.Seq)
	assert.Equal(t, want.optd, got.Optd, "seq %d optd", got.Seq)
	if want.nilb == nil {
		assert.True(t, nilBytes(got.Nilb), "seq %d nilb must be NULL, got %q", got.Seq, got.Nilb)
		assert.Nil(t, got.Nils, "seq %d nils must be NULL", got.Seq)
		assert.Empty(t, got.Tags, "seq %d tags", got.Seq)
		assert.Empty(t, got.Attrs, "seq %d attrs", got.Seq)
		assert.Equal(t, types.Address{}, got.Addr, "seq %d addr", got.Seq)
		assert.Equal(t, types.Address{}, got.AddrPtr, "seq %d addr_ptr", got.Seq)
		return
	}
	if assert.NotNil(t, got.Nilb, "seq %d nilb", got.Seq) {
		assert.Equal(t, want.nilb, *got.Nilb, "seq %d nilb", got.Seq)
	}
	if assert.NotNil(t, got.Nils, "seq %d nils", got.Seq) {
		assert.Equal(t, *want.nils, *got.Nils, "seq %d nils", got.Seq)
	}
	assert.Equal(t, want.addr, got.Addr, "seq %d addr", got.Seq)
	assert.Equal(t, want.addr, got.AddrPtr, "seq %d addr_ptr", got.Seq)
	assert.Equal(t, want.tags, got.Tags, "seq %d tags", got.Seq)
	assert.Equal(t, want.attrs, got.Attrs, "seq %d attrs", got.Seq)
}

// nilBytes reports whether a nillable bytes column read back as NULL. A NULL
// currently scans as a non-nil pointer to a nil slice rather than a nil
// pointer; that is pinned in known_gaps_test.go, and accepted here so this
// test keeps guarding the values.
func nilBytes(p *[]byte) bool { return p == nil || *p == nil }

func sliceData[T any](s []T) unsafe.Pointer { return unsafe.Pointer(unsafe.SliceData(s)) }

// assertNoAliasing fails when two rows share a pointer or a backing array.
func assertNoAliasing(t *testing.T, rows []*entity.Blob) {
	t.Helper()
	for i, a := range rows {
		for _, b := range rows[i+1:] {
			pair := fmt.Sprintf("seq %d and seq %d", a.Seq, b.Seq)
			assert.NotSame(t, a, b, pair)
			if len(a.Data) > 0 && len(b.Data) > 0 {
				assert.NotEqual(t, sliceData(a.Data), sliceData(b.Data), "%s share data", pair)
			}
			if len(a.Opt) > 0 && len(b.Opt) > 0 {
				assert.NotEqual(t, sliceData(a.Opt), sliceData(b.Opt), "%s share opt", pair)
			}
			if len(a.Optd) > 0 && len(b.Optd) > 0 {
				assert.NotEqual(t, sliceData(a.Optd), sliceData(b.Optd), "%s share optd", pair)
			}
			if a.Nilb != nil && b.Nilb != nil {
				assert.NotSame(t, a.Nilb, b.Nilb, "%s share the nilb pointer", pair)
				if len(*a.Nilb) > 0 && len(*b.Nilb) > 0 {
					assert.NotEqual(t, sliceData(*a.Nilb), sliceData(*b.Nilb), "%s share nilb bytes", pair)
				}
			}
			if a.Nils != nil && b.Nils != nil {
				assert.NotSame(t, a.Nils, b.Nils, "%s share the nils pointer", pair)
			}
			if len(a.Tags) > 0 && len(b.Tags) > 0 {
				assert.NotEqual(t, sliceData(a.Tags), sliceData(b.Tags), "%s share tags", pair)
			}
		}
	}
}

// Shape 11: bytes (plain, Optional, Optional+Nillable) and JSON (struct,
// slice, map). Every row keeps its own value and no two rows alias, with a
// NULL in the middle and at the end.
func TestBlob_RowsKeepTheirOwnValuesAndDoNotAlias(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()

	for seq := range 5 {
		w := wantBlob(seq)
		c := client.Blob.Create().SetSeq(seq).SetData(w.data).SetOpt(w.opt).SetNillableNils(w.nils)
		if w.nilb != nil {
			c.SetNilb(w.nilb).SetOptd(w.optd).SetAddr(w.addr).SetAddrPtr(w.addr).SetTags(w.tags).SetAttrs(w.attrs)
		}
		_, err := c.Save(ctx)
		require.NoError(t, err, "seq %d", seq)
	}

	read := map[string]func() ([]*entity.Blob, error){
		"All": func() ([]*entity.Blob, error) {
			return client.Blob.Query().Order(blob.BySeq()).All(ctx)
		},
		"All, descending": func() ([]*entity.Blob, error) {
			return client.Blob.Query().Order(blob.BySeq(sql.OrderDesc())).All(ctx)
		},
		"Select every column": func() ([]*entity.Blob, error) {
			return client.Blob.Query().Order(blob.BySeq()).Select(blob.Columns...).All(ctx)
		},
	}
	for name, run := range read {
		t.Run(name, func(t *testing.T) {
			rows, err := run()
			require.NoError(t, err)
			require.Len(t, rows, 5)
			for _, r := range rows {
				assertBlob(t, r)
			}
			assertNoAliasing(t, rows)

			// Scribbling over one row must not reach another. This is the
			// check that still fails when rows alias a driver buffer the
			// pointer comparison above cannot see.
			for _, r := range rows {
				for _, b := range [][]byte{r.Data, r.Opt, r.Optd} {
					for i := range b {
						b[i] = '!'
					}
				}
				if r.Nilb != nil {
					*r.Nilb = bytes.Repeat([]byte{'!'}, len(*r.Nilb))
				}
				if r.Nils != nil {
					*r.Nils = "!"
				}
				for i := range r.Tags {
					r.Tags[i] = "!"
				}
			}
			again, err := run()
			require.NoError(t, err)
			for _, r := range again {
				assertBlob(t, r)
			}
		})
	}

	// Optional() without Default() or Nillable() is still a NOT NULL column
	// (CLAUDE.md: "Optional() does NOT add DB DEFAULT"): leaving it out is a
	// constraint error, not a silent empty value.
	err := client.Blob.Create().SetSeq(99).SetData([]byte("x")).Exec(ctx)
	require.ErrorContains(t, err, "NOT NULL", "omitting an Optional bytes column with no default")

	// A projection that leaves the nullable columns out, and one that keeps
	// only them.
	some, err := client.Blob.Query().Order(blob.BySeq()).Select(blob.FieldSeq, blob.FieldNilb).All(ctx)
	require.NoError(t, err)
	require.Len(t, some, 5)
	for _, r := range some {
		want := wantBlob(r.Seq)
		if want.nilb == nil {
			assert.True(t, nilBytes(r.Nilb), "seq %d", r.Seq)
			continue
		}
		require.NotNil(t, r.Nilb, "seq %d", r.Seq)
		assert.Equal(t, want.nilb, *r.Nilb, "seq %d", r.Seq)
	}
	assertNoAliasing(t, some)

	// NULL predicates see what was written.
	nulls, err := client.Blob.Query().Where(blob.NilbField.IsNull()).Order(blob.BySeq()).Select(blob.FieldSeq).Ints(ctx)
	require.NoError(t, err)
	assert.Equal(t, []int{2, 4}, nulls)

	// Clearing and setting through UpdateOne lands on the right row only.
	one := must(client.Blob.Query().Where(blob.SeqField.EQ(1)).Only(ctx))
	require.NoError(t, client.Blob.UpdateOneID(one.ID).ClearNilb().ClearNils().Exec(ctx))
	four := must(client.Blob.Query().Where(blob.SeqField.EQ(4)).Only(ctx))
	require.NoError(t, client.Blob.UpdateOneID(four.ID).SetNilb([]byte("late")).Exec(ctx))
	nulls, err = client.Blob.Query().Where(blob.NilbField.IsNull()).Order(blob.BySeq()).Select(blob.FieldSeq).Ints(ctx)
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2}, nulls)
	four = client.Blob.GetX(ctx, four.ID)
	require.NotNil(t, four.Nilb)
	assert.Equal(t, []byte("late"), *four.Nilb)
}

// wideAt is the instant every Wide row's timestamps derive from.
var wideAt = time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)

// setWide fills every column of row i from (i, rev). Odd rows leave every
// nullable column NULL, so a bulk statement mixes set and unset columns.
func setWide(c *wideclient.WideCreate, i, rev int) *wideclient.WideCreate {
	n := i*100 + rev
	c.SetKey(fmt.Sprintf("k%d", i)).
		SetS1(fmt.Sprintf("s1-%d", n)).
		SetS2(fmt.Sprintf("s2-%d", n)).
		SetT1(fmt.Sprintf("text-%d", n)).
		SetB1(n%3 == 0).
		SetI1(n).
		SetI8(int8(i + rev)).
		SetI16(int16(1000 + n)).
		SetI32(int32(100000 + n)).
		SetI64(int64(1)<<40 + int64(n)).
		SetU1(uint(n)).
		SetU8(uint8(200 + i + rev)).
		SetU16(uint16(60000 + n)).
		SetU32(uint32(4000000000) + uint32(n)).
		SetU64(uint64(1)<<50 + uint64(n)).
		SetF64(float64(n) + 0.25).
		SetF32(float32(n) + 0.5).
		SetAt(wideAt.Add(time.Duration(n) * time.Minute)).
		SetUID(wideUUID(n)).
		SetRaw([]byte(fmt.Sprintf("raw-%d", n))).
		SetKind([]wide.Kind{wide.KindA, wide.KindB, wide.KindC}[n%3]).
		SetList([]int{n, n + 1}).
		SetDict(map[string]string{"n": fmt.Sprint(n)}).
		SetAddr(types.Address{Street: fmt.Sprintf("st-%d", n), City: "c", Zip: n}).
		SetLabel(types.Label(fmt.Sprintf("label-%d", n))).
		SetWeight(types.Weight(n)).
		SetDflt(fmt.Sprintf("dflt-%d", n))
	if i%2 == 0 {
		c.SetS3(fmt.Sprintf("s3-%d", n)).
			SetB2(n%2 == 0).
			SetI2(-n).
			SetF3(float64(n) + 0.75).
			SetAt2(wideAt.Add(time.Duration(n) * time.Hour)).
			SetUid2(wideUUID(n + 7)).
			SetRaw2([]byte(fmt.Sprintf("raw2-%d", n))).
			SetKind2([]wide.Kind2{wide.Kind2X, wide.Kind2Y}[n%2])
	}
	return c
}

func wideUUID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", n))
}

// assertWide checks every column of a row against setWide(i, rev).
func assertWide(t *testing.T, got *entity.Wide, i, rev int) {
	t.Helper()
	n := i*100 + rev
	row := fmt.Sprintf("row %d rev %d", i, rev)
	assert.Equal(t, fmt.Sprintf("k%d", i), got.Key, row)
	assert.Equal(t, fmt.Sprintf("s1-%d", n), got.S1, row)
	assert.Equal(t, fmt.Sprintf("s2-%d", n), got.S2, row)
	assert.Equal(t, fmt.Sprintf("text-%d", n), got.T1, row)
	assert.Equal(t, n%3 == 0, got.B1, row)
	assert.Equal(t, n, got.I1, row)
	assert.Equal(t, int8(i+rev), got.I8, row)
	assert.Equal(t, int16(1000+n), got.I16, row)
	assert.Equal(t, int32(100000+n), got.I32, row)
	assert.Equal(t, int64(1)<<40+int64(n), got.I64, row)
	assert.Equal(t, uint(n), got.U1, row)
	assert.Equal(t, uint8(200+i+rev), got.U8, row)
	assert.Equal(t, uint16(60000+n), got.U16, row)
	assert.Equal(t, uint32(4000000000)+uint32(n), got.U32, row)
	assert.Equal(t, uint64(1)<<50+uint64(n), got.U64, row)
	assert.Equal(t, float64(n)+0.25, got.F64, row)
	assert.Equal(t, float32(n)+0.5, got.F32, row)
	assert.True(t, wideAt.Add(time.Duration(n)*time.Minute).Equal(got.At), "%s at: %v", row, got.At)
	assert.Equal(t, wideUUID(n), got.UID, row)
	assert.Equal(t, []byte(fmt.Sprintf("raw-%d", n)), got.Raw, row)
	assert.Equal(t, []wide.Kind{wide.KindA, wide.KindB, wide.KindC}[n%3], got.Kind, row)
	assert.Equal(t, []int{n, n + 1}, got.List, row)
	assert.Equal(t, map[string]string{"n": fmt.Sprint(n)}, got.Dict, row)
	assert.Equal(t, types.Address{Street: fmt.Sprintf("st-%d", n), City: "c", Zip: n}, got.Addr, row)
	assert.Equal(t, types.Label(fmt.Sprintf("label-%d", n)), got.Label, row)
	assert.Equal(t, types.Weight(n), got.Weight, row)
	assert.Equal(t, fmt.Sprintf("dflt-%d", n), got.Dflt, row)

	if i%2 != 0 {
		assert.Nil(t, got.S3, row)
		assert.Nil(t, got.B2, row)
		assert.Nil(t, got.I2, row)
		assert.Nil(t, got.F3, row)
		assert.Nil(t, got.At2, row)
		assert.Nil(t, got.Uid2, row)
		assert.True(t, nilBytes(got.Raw2), row)
		assert.Nil(t, got.Kind2, row)
		return
	}
	if assert.NotNil(t, got.S3, row) {
		assert.Equal(t, fmt.Sprintf("s3-%d", n), *got.S3, row)
	}
	if assert.NotNil(t, got.B2, row) {
		assert.Equal(t, n%2 == 0, *got.B2, row)
	}
	if assert.NotNil(t, got.I2, row) {
		assert.Equal(t, -n, *got.I2, row)
	}
	if assert.NotNil(t, got.F3, row) {
		assert.Equal(t, float64(n)+0.75, *got.F3, row)
	}
	if assert.NotNil(t, got.At2, row) {
		assert.True(t, wideAt.Add(time.Duration(n)*time.Hour).Equal(*got.At2), "%s at2: %v", row, *got.At2)
	}
	if assert.NotNil(t, got.Uid2, row) {
		assert.Equal(t, wideUUID(n+7), *got.Uid2, row)
	}
	if assert.NotNil(t, got.Raw2, row) {
		assert.Equal(t, []byte(fmt.Sprintf("raw2-%d", n)), *got.Raw2, row)
	}
	if assert.NotNil(t, got.Kind2, row) {
		assert.Equal(t, []wide.Kind2{wide.Kind2X, wide.Kind2Y}[n%2], *got.Kind2, row)
	}
}

// Shape 12: a wide entity round-trips every column of every row through
// CreateBulk.
func TestWide_CreateBulkRoundTripsEveryColumn(t *testing.T) {
	require.GreaterOrEqual(t, len(wide.Columns), 31, "Wide must stay wide")
	client := openClient(t)
	ctx := context.Background()

	const rows = 6
	builders := make([]*wideclient.WideCreate, rows)
	for i := range builders {
		builders[i] = setWide(client.Wide.Create(), i, 0)
	}
	saved, err := client.Wide.CreateBulk(builders...).Save(ctx)
	require.NoError(t, err)
	require.Len(t, saved, rows)
	for i, s := range saved {
		assertWide(t, s, i, 0) // what Save returned
	}

	got, err := client.Wide.Query().Order(wide.ByKey()).All(ctx)
	require.NoError(t, err)
	require.Len(t, got, rows)
	for i, g := range got {
		assert.Equal(t, saved[i].ID, g.ID)
		assertWide(t, g, i, 0) // what the database holds
	}
}

// Shape 12, via upsert: a bulk OnConflict over a mix of existing and new
// rows leaves every column of every row at its new value.
func TestWide_BulkUpsertRoundTripsEveryColumn(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()

	// Rows 1 and 2 exist at rev 0: one with NULLs in the nullable columns,
	// one without.
	old := client.Wide.CreateBulk(setWide(client.Wide.Create(), 1, 0), setWide(client.Wide.Create(), 2, 0)).SaveX(ctx)

	const rows = 5
	builders := make([]*wideclient.WideCreate, rows)
	for i := range builders {
		builders[i] = setWide(client.Wide.Create(), i, 1)
	}
	err := client.Wide.CreateBulk(builders...).
		OnConflict(sql.ConflictColumns(wide.FieldKey), sql.ResolveWithNewValues()).
		Exec(ctx)
	require.NoError(t, err)

	got, err := client.Wide.Query().Order(wide.ByKey()).All(ctx)
	require.NoError(t, err)
	require.Len(t, got, rows, "two rows updated, three inserted")
	for i, g := range got {
		assertWide(t, g, i, 1)
	}
	assert.Equal(t, old[0].ID, got[1].ID, "an upserted row keeps its id")
	assert.Equal(t, old[1].ID, got[2].ID, "an upserted row keeps its id")

	// The single-row form, on an existing and on a new key.
	id, err := setWide(client.Wide.Create(), 3, 2).OnConflictColumns(wide.FieldKey).UpdateNewValues().ID(ctx)
	require.NoError(t, err)
	assert.Equal(t, got[3].ID, id)
	require.NoError(t, setWide(client.Wide.Create(), 5, 2).OnConflictColumns(wide.FieldKey).UpdateNewValues().Exec(ctx))
	assertWide(t, must(client.Wide.Query().Where(wide.KeyField.EQ("k3")).Only(ctx)), 3, 2)
	assertWide(t, must(client.Wide.Query().Where(wide.KeyField.EQ("k5")).Only(ctx)), 5, 2)
	assertWide(t, must(client.Wide.Query().Where(wide.KeyField.EQ("k4")).Only(ctx)), 4, 1)
}

// Shape 13: entities named like packages the generated code imports. That
// this file compiles is half the test; the other half is that each one
// creates and reads back.
func TestNameCollisions_CreateAndQuery(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()

	var tm *entity.Time = client.Time.Create().SetLabel("time").SaveX(ctx)
	var fl *entity.Field = client.Field.Create().SetLabel("field").SaveX(ctx)
	var er *entity.Errors = client.Errors.Create().SetLabel("errors").SaveX(ctx)
	var st *entity.Strings = client.Strings.Create().SetLabel("strings").SaveX(ctx)
	var ma *entity.Math = client.Math.Create().SetLabel("math").SaveX(ctx)
	var rt *entity.Runtime = client.Runtime.Create().SetLabel("runtime").SaveX(ctx)

	assert.Equal(t, "time", client.Time.GetX(ctx, tm.ID).Label)
	assert.Equal(t, "field", client.Field.GetX(ctx, fl.ID).Label)
	assert.Equal(t, "errors", client.Errors.GetX(ctx, er.ID).Label)
	assert.Equal(t, "strings", client.Strings.GetX(ctx, st.ID).Label)
	assert.Equal(t, "math", client.Math.GetX(ctx, ma.ID).Label)
	assert.Equal(t, "runtime", client.Runtime.GetX(ctx, rt.ID).Label)

	// The per-entity packages are importable under an alias and their
	// predicates and update builders work.
	require.NoError(t, client.Time.UpdateOneID(tm.ID).SetLabel("later").Exec(ctx))
	n, err := client.Time.Query().Where(mtime.LabelField.EQ("later")).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	_, err = client.Errors.Get(ctx, er.ID+1)
	require.True(t, gen.IsNotFound(err))
	del, err := client.Runtime.Delete().Exec(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, del)
}
