package velox

import (
	"context"
	"testing"
)

type pagedRow struct{}

type pagedCall struct {
	q             Query[pagedRow]
	after, before *string
	first, last   *int
	opts          []byte
}

// PaginateQuery records what Paginate passed it.
func (*pagedRow) PaginateQuery(_ context.Context, q Query[pagedRow], after *string, first *int, before *string, last *int, opts []byte) (*pagedCall, error) {
	return &pagedCall{q: q, after: after, first: first, before: before, last: last, opts: opts}, nil
}

// TestPaginate: Paginate infers the cursor, option and connection types from
// the entity's PaginateQuery and passes it the query and every argument.
func TestPaginate(t *testing.T) {
	a, b, f, l := "a", "b", 1, 2
	q := Query[pagedRow]{}.Limit(5)
	c, err := q.Paginate(context.Background(), &a, &f, &b, &l, 'x', 'y')
	if err != nil {
		t.Fatal(err)
	}
	if c.q.s.St.Limit != 5 || c.after != &a || c.first != &f || c.before != &b || c.last != &l || string(c.opts) != "xy" {
		t.Errorf("PaginateQuery got %+v", c)
	}
	if c, _ = q.Paginate(context.Background(), nil, nil, nil, nil); c.opts != nil {
		t.Errorf("no options: %v", c.opts)
	}
}
