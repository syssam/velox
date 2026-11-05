package gqlimpl_test

import (
	"context"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

// dial is a hand-written entity whose level is an int8, bound to a GraphQL
// Int (an int64) by an input that does not check it.
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

func dialInputGQL(goField string) string {
	if goField == "Level" {
		return "level"
	}
	return ""
}

// TestOutOfRangeIsUserInput: a where value or an update value that the
// column's Go type cannot hold is BAD_USER_INPUT naming the field, not a
// value wrapped round; one at the bound is taken.
func TestOutOfRangeIsUserInput(t *testing.T) {
	ctx := context.Background()
	filter := func(field string, v any, list bool) error {
		f := gqlimpl.NewFilter(ctx, gqlimpl.TableConn[dial]("Dial"))
		if list {
			gqlimpl.List(f, field, v, 1, velox.PredIn)
		} else {
			gqlimpl.Value(f, field, v, 1, velox.PredEQ)
		}
		_, err := f.Pred()
		return err
	}
	for name, c := range map[string]struct {
		field string
		v     any
		list  bool
	}{
		"over":      {"level", new(int64(128)), false},
		"under":     {"level", new(int64(-129)), false},
		"in a list": {"levelIn", []int64{1, 300}, true},
	} {
		err := filter(c.field, c.v, c.list)
		if codeOf(err) != "BAD_USER_INPUT" || !strings.Contains(err.Error(), "DialWhereInput."+c.field) {
			t.Errorf("%s: %v (code %q), want BAD_USER_INPUT naming DialWhereInput.%s", name, err, codeOf(err), c.field)
		}
	}
	if err := filter("level", new(int64(127)), false); err != nil {
		t.Errorf("127: %v", err)
	}
	if err := filter("level", new(int64(-128)), false); err != nil {
		t.Errorf("-128: %v", err)
	}

	u, _ := gqlimpl.NewUpdate[int64](ctx, gqlimpl.InputConn[dial]("Dial", 0, dialInputGQL), veloxgql.EncodeID("Dial", int64(1)))
	u.Set(1, int64(300))
	_, err := u.Exec(int64(1), nil)
	if codeOf(err) != "BAD_USER_INPUT" || !strings.Contains(err.Error(), "UpdateDialInput.level") {
		t.Errorf("update with 300: %v (code %q), want BAD_USER_INPUT naming UpdateDialInput.level", err, codeOf(err))
	}
}
