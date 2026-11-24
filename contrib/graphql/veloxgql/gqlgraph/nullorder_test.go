package gqlgraph_test

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
)

// pet is a hand-written entity with a nullable rank, many of them NULL and
// the others tied, as velox-gen would write a Nillable field.
type pet struct {
	ID   int64
	Rank *int64
}

var pets = struct {
	ID   velox.NumberCol[pet, int64]
	Rank velox.NullNumberCol[pet, int64]
}{ID: velox.NumberCol[pet, int64]{I: 0}, Rank: velox.NullNumberCol[pet, int64]{I: 1}}

var petMeta velox.MetaOnce[pet]

func (*pet) VeloxMeta() *velox.Meta[pet] { return petMeta.Load(petDescriptor) }

func petDescriptor() velox.Descriptor[pet] {
	return velox.Descriptor[pet]{
		Table:      "pets",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Rank", Name: "rank"}},
		PrimaryKey: []int{0},
		FieldPtr: func(x *pet, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.Rank
			}
			return nil
		},
	}
}

func rankTerm(dir veloxgql.OrderDirection) gqlgraph.OrderTerm[pet] {
	return gqlgraph.TermOf[pet]("RANK", dir, int(pets.Rank.I))
}

func petKey(dir veloxgql.OrderDirection) []gqlgraph.OrderTerm[pet] {
	return []gqlgraph.OrderTerm[pet]{gqlgraph.TermOf[pet]("id", dir, int(pets.ID.I))}
}

// The ranks of pets 1 to 12: six NULLs, and three ties of 1 and of 2.
var ranks = []any{nil, int64(2), nil, int64(1), nil, int64(2), nil, int64(1), int64(2), nil, int64(1), nil}

// TestPaginateNullable: a connection ordered by a nullable field pages exactly,
// forward and backward, at every page size and from every cursor, ASC with the
// NULLs last and DESC with them first; a cursor carries the NULL and reads it
// back from the wire.
func TestPaginateNullable(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec(`CREATE TABLE pets (id INTEGER PRIMARY KEY, rank INTEGER)`); err != nil {
		t.Fatal(err)
	}
	for i, r := range ranks {
		if _, err := sqlDB.Exec(`INSERT INTO pets (id, rank) VALUES (?, ?)`, i+1, r); err != nil {
			t.Fatal(err)
		}
	}
	db := velox.Open(velox.SQLite, sqlDB)
	for _, c := range []struct {
		rank, key veloxgql.OrderDirection
		want      []int64
	}{
		{veloxgql.OrderDirectionAsc, veloxgql.OrderDirectionAsc, []int64{4, 8, 11, 2, 6, 9, 1, 3, 5, 7, 10, 12}},
		// One term DESC: the key breaks its ties DESC, as entgql's does.
		{veloxgql.OrderDirectionDesc, veloxgql.OrderDirectionAsc, []int64{12, 10, 7, 5, 3, 1, 9, 6, 2, 11, 8, 4}},
	} {
		t.Run(string(c.rank)+" "+string(c.key), func(t *testing.T) {
			page := func(args gqlgraph.Args) *gqlgraph.PageOf[pet] {
				t.Helper()
				conn, err := gqlgraph.NewConnectionOf(ctx, db.Query[pet](), "Pet", args, []gqlgraph.OrderTerm[pet]{rankTerm(c.rank)}, petKey(c.key))
				if err != nil {
					t.Fatalf("%+v: %v", args, err)
				}
				p, err := conn.Page(ctx)
				if err != nil {
					t.Fatalf("%+v: %v", args, err)
				}
				return p
			}
			petIDs := func(rows []*pet) []int64 {
				out := make([]int64, len(rows))
				for i, r := range rows {
					out[i] = r.ID
				}
				return out
			}
			all := page(gqlgraph.Args{First: new(len(c.want))})
			if got := petIDs(all.Rows); !slices.Equal(got, c.want) {
				t.Fatalf("all: %v, want %v", got, c.want)
			}
			for i := range c.want {
				cur := wire(t, all.Cursors[i])
				if null := ranks[c.want[i]-1] == nil; null != (cur.Terms[0].Value == nil) {
					t.Fatalf("cursor of %d: %+v", c.want[i], cur)
				}
				for n := 1; n <= 4; n++ {
					after := page(gqlgraph.Args{First: new(n), After: cur})
					end := min(i+1+n, len(c.want))
					if got := petIDs(after.Rows); !slices.Equal(got, c.want[i+1:end]) || after.Info.HasNextPage != (end < len(c.want)) {
						t.Errorf("first %d after %d: %v next %v, want %v", n, c.want[i], got, after.Info.HasNextPage, c.want[i+1:end])
					}
					before := page(gqlgraph.Args{Last: new(n), Before: cur})
					start := max(i-n, 0)
					if got := petIDs(before.Rows); !slices.Equal(got, c.want[start:i]) || before.Info.HasPreviousPage != (start > 0) {
						t.Errorf("last %d before %d: %v prev %v, want %v", n, c.want[i], got, before.Info.HasPreviousPage, c.want[start:i])
					}
				}
			}
		})
	}
}
