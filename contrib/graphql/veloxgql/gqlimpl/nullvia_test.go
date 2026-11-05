package gqlimpl_test

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

// toy belongs to a pet, whose nullable rank orders the toys: an OrderVia of a
// Nillable field, as velox-gen and velox-graphql write it.
type toy struct {
	ID    int64
	PetID int64
	Pet   velox.One[pet]
}

var toys = struct {
	ID    velox.NumberCol[toy, int64]
	PetID velox.NumberCol[toy, int64]
	Pet   velox.Rel[toy, pet]
}{velox.NumberCol[toy, int64]{I: 0}, velox.NumberCol[toy, int64]{I: 1}, velox.Rel[toy, pet]{I: 0}}

var toyMeta velox.MetaOnce[toy]

func (*toy) VeloxMeta() *velox.Meta[toy] { return toyMeta.Load(toyDescriptor) }

func toyDescriptor() velox.Descriptor[toy] {
	return velox.Descriptor[toy]{
		Table:        "toys",
		Columns:      []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "PetID", Name: "pet_id"}},
		PrimaryKey:   []int{0},
		Edges:        []velox.EdgeDescriptor{velox.EdgeTo[pet]("Pet", 1, 0)},
		FieldPtr:     func(x *toy, i int) any { return [...]any{&x.ID, &x.PetID}[i] },
		EdgeFieldPtr: func(x *toy, i int) any { return &x.Pet },
	}
}

// TestPaginateNullableVia: a connection ordered by a nullable column of a
// joined row pages exactly, forward and backward, at every page size and from
// every cursor, ASC with the NULLs last and DESC with them first. A cursor
// holds the NULL as null and reads it back from the wire.
func TestPaginateNullableVia(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	for _, s := range []string{
		`CREATE TABLE pets (id INTEGER PRIMARY KEY, rank INTEGER)`,
		`CREATE TABLE toys (id INTEGER PRIMARY KEY, pet_id INTEGER NOT NULL)`,
	} {
		if _, err := sqlDB.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	for i, r := range ranks {
		if _, err := sqlDB.Exec(`INSERT INTO pets (id, rank) VALUES (?, ?)`, i+1, r); err != nil {
			t.Fatal(err)
		}
	}
	// Toy i belongs to pet i, so toy i's rank is ranks[i-1].
	for i := range ranks {
		if _, err := sqlDB.Exec(`INSERT INTO toys (id, pet_id) VALUES (?, ?)`, i+1, i+1); err != nil {
			t.Fatal(err)
		}
	}
	db := velox.Open(velox.SQLite, sqlDB)
	key := []gqlimpl.OrderTerm[toy]{gqlimpl.TermOf[toy]("id", veloxgql.OrderDirectionAsc, int(toys.ID.I))}
	for _, c := range []struct {
		rank veloxgql.OrderDirection
		want []int64
	}{
		{veloxgql.OrderDirectionAsc, []int64{4, 8, 11, 2, 6, 9, 1, 3, 5, 7, 10, 12}},
		{veloxgql.OrderDirectionDesc, []int64{1, 3, 5, 7, 10, 12, 2, 6, 9, 4, 8, 11}},
	} {
		t.Run(string(c.rank), func(t *testing.T) {
			order := []gqlimpl.OrderTerm[toy]{gqlimpl.JoinedTermOf[toy, pet]("PET_RANK", c.rank, int(toys.Pet.I), int(pets.Rank.I))}
			page := func(args gqlimpl.Args) *gqlimpl.PageOf[toy] {
				t.Helper()
				conn, err := gqlimpl.NewConnectionOf(ctx, db.Query[toy](), "Toy", args, order, key)
				if err != nil {
					t.Fatalf("%+v: %v", args, err)
				}
				p, err := conn.Page(ctx)
				if err != nil {
					t.Fatalf("%+v: %v", args, err)
				}
				return p
			}
			toyIDs := func(rows []*toy) []int64 {
				out := make([]int64, len(rows))
				for i, r := range rows {
					out[i] = r.ID
				}
				return out
			}
			all := page(gqlimpl.Args{First: new(len(c.want))})
			if got := toyIDs(all.Rows); !slices.Equal(got, c.want) {
				t.Fatalf("all: %v, want %v", got, c.want)
			}
			for i := range c.want {
				cur := wire(t, all.Cursors[i])
				if null := ranks[c.want[i]-1] == nil; null != (cur.Terms[0].Value == nil) {
					t.Fatalf("cursor of %d: %+v", c.want[i], cur)
				}
				for n := 1; n <= 4; n++ {
					after := page(gqlimpl.Args{First: new(n), After: cur})
					end := min(i+1+n, len(c.want))
					if got := toyIDs(after.Rows); !slices.Equal(got, c.want[i+1:end]) || after.Info.HasNextPage != (end < len(c.want)) {
						t.Errorf("first %d after %d: %v next %v, want %v", n, c.want[i], got, after.Info.HasNextPage, c.want[i+1:end])
					}
					before := page(gqlimpl.Args{Last: new(n), Before: cur})
					start := max(i-n, 0)
					if got := toyIDs(before.Rows); !slices.Equal(got, c.want[start:i]) || before.Info.HasPreviousPage != (start > 0) {
						t.Errorf("last %d before %d: %v prev %v, want %v", n, c.want[i], got, before.Info.HasPreviousPage, c.want[start:i])
					}
				}
			}
		})
	}
}
