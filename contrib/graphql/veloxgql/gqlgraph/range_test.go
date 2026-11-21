package gqlgraph_test

import (
	"context"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
)

// dial is a hand-written entity whose level is an int8, filtered by a
// GraphQL Int (an int64).
type dial struct {
	ID    int64
	Level int8
}

var dialMeta velox.MetaOnce[dial]

func (*dial) VeloxMeta() *velox.Meta[dial] { return dialMeta.Load(dialDescriptor) }

func dialDescriptor() velox.Descriptor[dial] {
	return velox.Descriptor[dial]{
		Table:      "dials",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Level", Name: "level"}},
		PrimaryKey: []int{0},
		FieldPtr: func(x *dial, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.Level
			}
			return nil
		},
	}
}

// TestOutOfRangeIsUserInput: a where value that the column's Go type cannot
// hold is BAD_USER_INPUT naming the field, not a value wrapped round; one at
// the bound is taken.
func TestOutOfRangeIsUserInput(t *testing.T) {
	ctx := context.Background()
	filter := func(o gqlgraph.Ops[int64]) error {
		f := gqlgraph.NewFilter(ctx, gqlgraph.TableType[dial]("Dial"))
		gqlgraph.Field(f, "level", 1, o)
		_, err := f.Pred()
		return err
	}
	for name, c := range map[string]struct {
		field string
		o     gqlgraph.Ops[int64]
	}{
		"over":      {"level", gqlgraph.Ops[int64]{EQ: new(int64(128))}},
		"under":     {"level", gqlgraph.Ops[int64]{EQ: new(int64(-129))}},
		"in a list": {"levelIn", gqlgraph.Ops[int64]{In: []int64{1, 300}}},
	} {
		err := filter(c.o)
		if codeOf(err) != "BAD_USER_INPUT" || !strings.Contains(err.Error(), "DialWhereInput."+c.field) {
			t.Errorf("%s: %v (code %q), want BAD_USER_INPUT naming DialWhereInput.%s", name, err, codeOf(err), c.field)
		}
	}
	if err := filter(gqlgraph.Ops[int64]{EQ: new(int64(127))}); err != nil {
		t.Errorf("127: %v", err)
	}
	if err := filter(gqlgraph.Ops[int64]{EQ: new(int64(-128))}); err != nil {
		t.Errorf("-128: %v", err)
	}
}

// TestFirstWhereErrorStays: a filter keeps its first error. A value out of
// the column's range that also spends the last of the budget is the
// range's error, and a foreign key's NotNil after a refused ID list adds
// nothing.
func TestFirstWhereErrorStays(t *testing.T) {
	ctx := gqlgenx.NewExtension(&velox.DB{}, veloxgql.WithLimits(veloxgql.Limits{MaxFilterNodes: 1})).NewContext(context.Background())
	f := gqlgraph.NewFilter(ctx, gqlgraph.TableType[dial]("Dial"))
	gqlgraph.Field(f, "level", 1, gqlgraph.Ops[int64]{EQ: new(int64(300))})
	if _, err := f.Pred(); err == nil || !strings.Contains(err.Error(), "out of the field's range") {
		t.Errorf("out of range and over the budget: %v, want the range's error", err)
	}
	f = gqlgraph.NewFilter(context.Background(), gqlgraph.TableType[dial]("Dial"))
	yes := true
	gqlgraph.EdgeIDField(f, "dialID", 0, 0, gqlgraph.TableType[dial]("Dial"), gqlgraph.Ops[int64]{In: make([]int64, veloxgql.DefaultLimits().MaxFilterWidth+1), NotNil: &yes})
	if _, err := f.Pred(); err == nil || !strings.Contains(err.Error(), "DialWhereInput.dialIDIn:") {
		t.Errorf("a refused ID list, then NotNil: %v, want the list's error", err)
	}
}
