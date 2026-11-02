package engine_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
)

// ovOwner has notes, and picks of notes through ovPick; ovNote's Read policy
// is an In of the values ovReading puts in the context: the fixtures of a
// load whose statement overflows the parameter limit once the policy's
// conditions are added.
type ovOwner struct {
	ID    int64
	Notes velox.Many[ovNote]
	Picks velox.Many[ovNote]
}

var ovOwners = struct {
	ID    velox.NumberCol[ovOwner, int64]
	Notes velox.Rel[ovOwner, ovNote]
	Picks velox.Rel[ovOwner, ovNote]
}{velox.NumberCol[ovOwner, int64]{I: 0}, velox.Rel[ovOwner, ovNote]{I: 0}, velox.Rel[ovOwner, ovNote]{I: 1}}

var ovOwnerMeta velox.MetaOnce[ovOwner]

func (*ovOwner) VeloxMeta() *velox.Meta[ovOwner] {
	return ovOwnerMeta.Load(func() velox.Descriptor[ovOwner] {
		return velox.Descriptor[ovOwner]{
			Table:      "ov_owners",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}},
			PrimaryKey: []int{0},
			Tenant:     -1,
			Edges:      []velox.EdgeDescriptor{velox.EdgeTo[ovNote]("Notes", 0, 1), velox.EdgeThrough[ovPick, ovNote]("Picks", 0, 0, 1, 0)},
			Addr:       func(x *ovOwner, i int) any { return &x.ID },
			EdgeAddr: func(x *ovOwner, i int) any {
				if i == 0 {
					return &x.Notes
				}
				return &x.Picks
			},
		}
	})
}

type ovNote struct {
	ID      int64
	OwnerID int64
	Rank    int64
}

var ovNotes = struct {
	ID      velox.NumberCol[ovNote, int64]
	OwnerID velox.NumberCol[ovNote, int64]
	Rank    velox.NumberCol[ovNote, int64]
}{velox.NumberCol[ovNote, int64]{I: 0}, velox.NumberCol[ovNote, int64]{I: 1}, velox.NumberCol[ovNote, int64]{I: 2}}

// ovKey's value, a []int64, is what ovNote's Read policy's In holds.
type ovKey struct{}

// ovReading is a context under which ovNote's Read policy is an In of n
// values, n parameters.
func ovReading(n int) context.Context {
	vs := make([]int64, n)
	for i := range vs {
		vs[i] = int64(i)
	}
	return context.WithValue(context.Background(), ovKey{}, vs)
}

func (*ovNote) Policy() velox.Policy[ovNote] {
	return velox.Policy[ovNote]{Read: func(ctx context.Context) (velox.Pred[ovNote], error) {
		if vs, ok := ctx.Value(ovKey{}).([]int64); ok {
			return ovNotes.Rank.In(vs...), nil
		}
		return velox.Allow[ovNote](), nil
	}}
}

var ovNoteMeta velox.MetaOnce[ovNote]

func (*ovNote) VeloxMeta() *velox.Meta[ovNote] {
	return ovNoteMeta.Load(func() velox.Descriptor[ovNote] {
		return velox.Descriptor[ovNote]{
			Policy:     func() velox.BoundPolicy { return velox.BindPolicy((*ovNote)(nil).Policy()) },
			Table:      "ov_notes",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "OwnerID", Name: "owner_id"}, {Field: "Rank", Name: "rank"}},
			PrimaryKey: []int{0},
			Tenant:     -1,
			Addr:       func(x *ovNote, i int) any { return [...]any{&x.ID, &x.OwnerID, &x.Rank}[i] },
		}
	})
}

// ovPick is the join entity of ovOwner.Picks.
type ovPick struct{ OwnerID, NoteID int64 }

var ovPickMeta velox.MetaOnce[ovPick]

func (*ovPick) VeloxMeta() *velox.Meta[ovPick] {
	return ovPickMeta.Load(func() velox.Descriptor[ovPick] {
		return velox.Descriptor[ovPick]{
			Table:      "ov_picks",
			Columns:    []velox.ColumnDescriptor{{Field: "OwnerID", Name: "owner_id"}, {Field: "NoteID", Name: "note_id"}},
			PrimaryKey: []int{0, 1},
			Tenant:     -1,
			Addr:       func(x *ovPick, i int) any { return [...]any{&x.OwnerID, &x.NoteID}[i] },
		}
	})
}

// ovSent records the parameters of every statement the Around hooks see.
type ovSent struct{ args []int }

func (s *ovSent) around(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
	s.args = append(s.args, len(st.Args))
	return next(ctx)
}

// ovRun opens a fake PostgreSQL database answering with results, whose
// Around hook records into the ovSent returned.
func ovRun(results [][][]any) (*velox.DB, *fakeconn.Exec, *ovSent) {
	e := &fakeconn.Exec{Results: results}
	s := &ovSent{}
	return openFake(velox.Postgres, e, velox.WithAround(s.around)), e, s
}

// ovOwnerRows are owners 1–n.
func ovOwnerRows(n int) [][]any {
	var rows [][]any
	for id := int64(1); id <= int64(n); id++ {
		rows = append(rows, []any{id})
	}
	return rows
}

// checkSent fails t unless every statement the fake received was one the
// Around hooks saw, of at most 100 parameters, and the statements had want
// parameters each.
func checkSent(t *testing.T, e *fakeconn.Exec, s *ovSent, want []int) {
	t.Helper()
	if e.Calls != len(s.args) {
		t.Errorf("%d statements reached the database, the Around hooks saw %d", e.Calls, len(s.args))
	}
	for i, n := range s.args {
		if n > 100 {
			t.Errorf("statement %d has %d parameters", i, n)
		}
	}
	if !reflect.DeepEqual(s.args, want) {
		t.Errorf("the statements' parameters %v, want %v", s.args, want)
	}
}
