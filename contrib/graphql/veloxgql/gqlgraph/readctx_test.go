package gqlgraph_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
)

// markerKey is the context key of a marker an application sets before it
// paginates, and a Filter reads.
type markerKey struct{}

func withMarker(ctx context.Context, m string) context.Context {
	return context.WithValue(ctx, markerKey{}, m)
}

// markedPeople is a Filter of people by the marker: "wide" sees every
// person, "narrow" person 3 alone, and no marker the people named b (3, 6
// and 8).
func markedPeople(ctx context.Context, _ *velox.DB, e velox.FilterEntity, _ velox.Op) (velox.Predicate, error) {
	if !e.HasColumn("age") {
		return velox.Allow(), nil
	}
	switch ctx.Value(markerKey{}) {
	case "wide":
		return velox.Allow(), nil
	case "narrow":
		return velox.FieldEQ("id", int64(3)), nil
	}
	return velox.FieldEQ("name", "b"), nil
}

// TestConnectionReadsWithPaginateValues is decision 0172: the page and the
// totalCount of a connection made in a context with a marker, read in a
// resolver's context without it, both honour the marker, widening or
// narrowing, and agree.
func TestConnectionReadsWithPaginateValues(t *testing.T) {
	db, _ := peopleDB(t, velox.WithFilter(markedPeople))
	resolver := context.Background()
	for _, c := range []struct {
		marker string
		want   []int64
	}{
		{"wide", []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
		{"narrow", []int64{3}},
		{"", []int64{3, 6, 8}},
	} {
		t.Run(c.marker, func(t *testing.T) {
			ctx := resolver
			if c.marker != "" {
				ctx = withMarker(resolver, c.marker)
			}
			conn, err := gqlgraph.NewConnectionOf(ctx, db.Query[person](), "Person", gqlgraph.Args{First: new(10)}, nil, personKey())
			if err != nil {
				t.Fatal(err)
			}
			p, err := conn.Page(resolver)
			if err != nil {
				t.Fatal(err)
			}
			n, err := conn.TotalCount(resolver)
			if err != nil {
				t.Fatal(err)
			}
			if got := ids(p.Rows); !reflect.DeepEqual(got, c.want) {
				t.Errorf("page %v, want %v", got, c.want)
			}
			if n != len(c.want) {
				t.Errorf("totalCount %d, want %d", n, len(c.want))
			}
		})
	}
}

// TestConnectionReadCancellation: the reading context's cancellation fails
// the reads, and Paginate's, cancelled before the reads (a resolver's
// defer cancel), does not.
func TestConnectionReadCancellation(t *testing.T) {
	db, _ := peopleDB(t)
	newConn := func(ctx context.Context) gqlgraph.ConnectionOf[person] {
		t.Helper()
		conn, err := gqlgraph.NewConnectionOf(ctx, db.Query[person](), "Person", gqlgraph.Args{First: new(3)}, nil, personKey())
		if err != nil {
			t.Fatal(err)
		}
		return conn
	}

	pctx, cancel := context.WithCancel(withMarker(context.Background(), "x"))
	conn := newConn(pctx)
	cancel()
	if p, err := conn.Page(context.Background()); err != nil || len(p.Rows) != 3 {
		t.Errorf("Paginate's context cancelled: page %v, %v; want 3 rows", p, err)
	}
	if n, err := conn.TotalCount(context.Background()); err != nil || n != 10 {
		t.Errorf("Paginate's context cancelled: totalCount %d, %v; want 10", n, err)
	}

	read, stop := context.WithCancel(context.Background())
	stop()
	conn = newConn(context.Background())
	if _, err := conn.Page(read); !errors.Is(err, context.Canceled) {
		t.Errorf("the reading context cancelled: page %v, want context.Canceled", err)
	}
	if _, err := conn.TotalCount(read); !errors.Is(err, context.Canceled) {
		t.Errorf("the reading context cancelled: totalCount %v, want context.Canceled", err)
	}
}

// TestConnectionReadsInPaginateTx: a transaction's mark is a value of
// Paginate's context. A connection on tx made in fn's context reads in the
// transaction under a reading context without the mark. One on db made in
// fn's context is refused with ErrOutsideTx under such a reading context,
// as the mark asks. One on db made under velox.WithoutTx(fn's) is not, read
// under fn's context: the reading context's mark is dropped.
func TestConnectionReadsInPaginateTx(t *testing.T) {
	db, _ := peopleDB(t)
	conn := func(ctx context.Context, on *velox.DB) gqlgraph.ConnectionOf[person] {
		t.Helper()
		c, err := gqlgraph.NewConnectionOf(ctx, on.Query[person](), "Person", gqlgraph.Args{First: new(20)}, nil, personKey())
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	err := db.Tx(context.Background(), func(tctx context.Context, tx *velox.DB) error {
		if _, err := tx.Exec(tctx, "test", `INSERT INTO people (id, name, age) VALUES (11, 'g', 1)`); err != nil {
			return err
		}
		// A read outside the transaction would wait for it, on a pool of
		// one connection: the reads are bounded.
		read, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if n, err := conn(tctx, tx).TotalCount(read); err != nil || n != 11 {
			t.Errorf("on tx: totalCount %d, %v; want 11", n, err)
		}
		if _, err := conn(tctx, db).TotalCount(read); !errors.Is(err, velox.ErrOutsideTx) {
			t.Errorf("on db in fn's context: %v, want ErrOutsideTx", err)
		}
		// It waits for the transaction, until its context ends.
		inTx, cancel := context.WithTimeout(tctx, 100*time.Millisecond)
		defer cancel()
		if _, err := conn(velox.WithoutTx(tctx), db).TotalCount(inTx); errors.Is(err, velox.ErrOutsideTx) {
			t.Errorf("on db under WithoutTx, read under fn's context: %v", err)
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatal(err)
	}
}

var errRollback = errors.New("rollback")

// markedChildren is a Filter of children by the marker: "narrow" sees the
// children titled a, and no marker every child.
func markedChildren(ctx context.Context, _ *velox.DB, e velox.FilterEntity, _ velox.Op) (velox.Predicate, error) {
	if e.HasColumn("title") && ctx.Value(markerKey{}) == "narrow" {
		return velox.FieldEQ("title", "a"), nil
	}
	return velox.Allow(), nil
}

// TestEdgeConnectionBatchOneContextEach: each owner's edge call has a
// context of its own, as gqlgen gives each row's field, holding the same
// marker; the batch stays one load and one count, each owner's part
// honouring the marker under a reading context without it.
func TestEdgeConnectionBatchOneContextEach(t *testing.T) {
	db, _, log := edgeDB(t, velox.WithFilter(markedChildren))
	ctx := operation(t, db, childrenQuery)
	xs := ownersPage(t, ctx, db, db.Query[owner](), gqlgraph.Args{})
	log.take("")
	want := map[int64][]int64{1: {2}, 2: {6}, 3: {}}
	for _, x := range xs {
		read := field(t, ctx, "owners", "edges", "node", "children")
		pctx := withMarker(read, "narrow")
		c, err := gqlgraph.NewConnectionOf(pctx, db.Query[child](), "Child", gqlgraph.Args{}, nil, childKey())
		if err != nil {
			t.Fatal(err)
		}
		c = gqlgraph.EdgeConnectionOf(pctx, c, x, owners.Children, "Owner.children", nil)
		p, err := c.Page(read)
		if err != nil {
			t.Fatal(err)
		}
		n, err := c.TotalCount(read)
		if err != nil {
			t.Fatal(err)
		}
		if got := childIDs(p.Rows); !reflect.DeepEqual(got, want[x.ID]) || n != len(want[x.ID]) {
			t.Errorf("owner %d: page %v, totalCount %d; want %v", x.ID, got, n, want[x.ID])
		}
	}
	var loads, counts int
	for _, s := range log.take("children") {
		if strings.Contains(s, "COUNT(") {
			counts++
		} else {
			loads++
		}
	}
	if loads != 1 || counts != 1 {
		t.Errorf("%d loads and %d counts, want one of each", loads, counts)
	}
}
