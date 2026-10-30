package apitest

import (
	"errors"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/fakeconn"
)

// rtree is a tree whose parent edge is Required, its key NULL-able, as
// velox-gen writes a Desc for an edge to the entity's own schema; rlink's
// Required edge has a NOT NULL key, which the database enforces.
type rtree struct {
	ID       int64
	ParentID *int64
}

var rtrees = struct {
	ID       velox.NumberCol[rtree, int64]
	ParentID velox.NullNumberCol[rtree, int64]
}{velox.NumberCol[rtree, int64]{I: 0}, velox.NullNumberCol[rtree, int64]{I: 1}}

var rtreeMeta velox.MetaOnce[rtree]

func (*rtree) VeloxMeta() *velox.Meta[rtree] {
	return rtreeMeta.Load(func() velox.Desc[rtree] {
		return velox.Desc[rtree]{
			Table:      "rtrees",
			Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "ParentID", Name: "parent_id", RequiredEdge: "parent"}},
			PrimaryKey: []int{0},
			Tenant:     -1,
			Addr: func(x *rtree, i int) any {
				if i == 0 {
					return &x.ID
				}
				return &x.ParentID
			},
		}
	})
}

// rbad says RequiredEdge on a column that cannot be NULL.
type rbad struct {
	ID       int64
	ParentID int64
}

var rbadMeta velox.MetaOnce[rbad]

func (*rbad) VeloxMeta() *velox.Meta[rbad] {
	return rbadMeta.Load(func() velox.Desc[rbad] {
		return velox.Desc[rbad]{
			Table:      "rbads",
			Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "ParentID", Name: "parent_id", RequiredEdge: "parent"}},
			PrimaryKey: []int{0},
			Tenant:     -1,
			Addr: func(x *rbad, i int) any {
				if i == 0 {
					return &x.ID
				}
				return &x.ParentID
			},
		}
	})
}

// TestRequiredEdge: a Required edge whose key is NULL-able is refused unset,
// as ent's check() refuses it, by Insert, InsertAll, an upsert, Update and
// Save, before anything is sent.
func TestRequiredEdge(t *testing.T) {
	T := rtrees
	one := int64(1)
	const want = `velox: apitest.rtree: ParentID: missing required edge "rtree.parent"`
	refused := func(t *testing.T, e *fakeconn.Exec, err error) {
		t.Helper()
		var ve *velox.ValidationError
		if !errors.As(err, &ve) || err.Error() != want || ve.Field != "ParentID" {
			t.Errorf("got %v, want %s", err, want)
		}
		if len(e.Log) != 0 {
			t.Errorf("sent %q", e.Log)
		}
	}
	t.Run("insert", func(t *testing.T) {
		e := &fakeconn.Exec{}
		refused(t, e, openFake(velox.Postgres, e).Insert(&rtree{ID: 2}).Exec(ctx))
	})
	t.Run("insert all", func(t *testing.T) {
		e := &fakeconn.Exec{}
		refused(t, e, openFake(velox.Postgres, e).InsertAll([]rtree{{ID: 2, ParentID: &one}, {ID: 3}}).Exec(ctx))
	})
	t.Run("upsert", func(t *testing.T) {
		e := &fakeconn.Exec{}
		refused(t, e, openFake(velox.Postgres, e).Insert(&rtree{ID: 2}).OnConflict(T.ID).Update(T.ParentID).Exec(ctx))
	})
	t.Run("update", func(t *testing.T) {
		e := &fakeconn.Exec{}
		_, err := openFake(velox.Postgres, e).Update[rtree]().Set(T.ParentID.SetNull()).Where(T.ID.EQ(2)).Exec(ctx)
		refused(t, e, err)
	})
	t.Run("save", func(t *testing.T) {
		e := &fakeconn.Exec{}
		refused(t, e, openFake(velox.Postgres, e).Save(&rtree{ID: 2}, T.ParentID).Exec(ctx))
	})
	t.Run("set passes", func(t *testing.T) {
		e := &fakeconn.Exec{}
		db := openFake(velox.Postgres, e)
		if err := db.Insert(&rtree{ID: 2, ParentID: &one}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Update[rtree]().Set(T.ParentID.Set(1)).Where(T.ID.EQ(2)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if len(e.Log) != 2 {
			t.Errorf("sent %q", e.Log)
		}
	})
	t.Run("a column that cannot be NULL", func(t *testing.T) {
		err := velox.CheckEntity[rbad]()
		var se *velox.SchemaError
		if !errors.As(err, &se) {
			t.Fatalf("got %v", err)
		}
	})
}
