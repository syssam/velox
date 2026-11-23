package apitest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/untyped"
)

// shareRule is a rule whose column and table names come from configuration,
// as an application's shared rule package takes them (ADR 0137).
type shareRule struct {
	IDColumn, NumberColumn string
	ShareTable, ShareFK    string
	ShareQtyColumn         string
}

func (c shareRule) cond(numbers ...any) velox.Predicate {
	return velox.Or(
		velox.FieldIn(c.NumberColumn, numbers...),
		velox.FieldInSelect(c.IDColumn, c.ShareTable, c.ShareFK, velox.FieldGTE(c.ShareQtyColumn, 5)),
	)
}

// TestRunTimeColumnNames: conditions whose column, table and key names are
// known only at run time are resolved when the statement runs, against the
// entity and the package's entity table; an unknown name fails the statement,
// naming the entity and the column, and nothing is sent.
func TestRunTimeColumnNames(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	cfg := shareRule{IDColumn: "id", NumberColumn: "number", ShareTable: "items", ShareFK: "invoice_id", ShareQtyColumn: "qty"}
	q := func(p velox.Predicate) (string, []any, error) {
		return untyped.Query(db, untyped.TableOf[testmodel.Invoice]()).Columns(0).Where(p).Limit(1).SQL(ctx)
	}
	s, args, err := q(cfg.cond("a", "b"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, `"number" = ANY($1)`) || !strings.Contains(s, `"invoice_id"`) || !strings.Contains(s, `"qty" >= $2`) {
		t.Errorf("SQL %s", s)
	}
	if len(args) != 2 {
		t.Errorf("args %v", args)
	}
	for _, bad := range []struct {
		rule shareRule
		want string
	}{
		{shareRule{IDColumn: "id", NumberColumn: "nope", ShareTable: "items", ShareFK: "invoice_id", ShareQtyColumn: "qty"}, `no column "nope"`},
		{shareRule{IDColumn: "id", NumberColumn: "number", ShareTable: "items", ShareFK: "invoice_id", ShareQtyColumn: "nope"}, `no column "nope"`},
		{shareRule{IDColumn: "id", NumberColumn: "number", ShareTable: "nope", ShareFK: "invoice_id", ShareQtyColumn: "qty"}, `"nope"`},
	} {
		sent := len(e.Log)
		_, _, err := q(bad.rule.cond("a"))
		if err == nil || !strings.Contains(err.Error(), "Invoice") || !strings.Contains(err.Error(), bad.want) {
			t.Errorf("%+v: %v, want the entity and %s", bad.rule, err, bad.want)
		}
		_, err = db.Query[testmodel.Invoice]().Where(velox.Pred[testmodel.Invoice](bad.rule.cond("a"))).Limit(1).All(ctx)
		if err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Errorf("%+v: All: %v", bad.rule, err)
		}
		if len(e.Log) != sent {
			t.Errorf("%+v: sent %q", bad.rule, e.Log[sent:])
		}
	}
}

// TestIsZero: IsZero holds for the zero Pred and the zero Predicate, and for
// no condition made by a handle, Allow, None or And.
func TestIsZero(t *testing.T) {
	if !velox.IsZero(velox.Pred[testmodel.Invoice]{}) || !velox.IsZero(velox.Predicate{}) {
		t.Error("the zero Pred is not zero")
	}
	for name, p := range map[string]velox.Pred[testmodel.Invoice]{
		"EQ":      I.Paid.EQ(true),
		"And":     velox.And[testmodel.Invoice](),
		"Allow":   velox.Pred[testmodel.Invoice](velox.Allow()),
		"None":    velox.Pred[testmodel.Invoice](velox.None()),
		"FieldEQ": velox.Pred[testmodel.Invoice](velox.FieldEQ("paid", true)),
	} {
		if velox.IsZero(p) {
			t.Errorf("%s is zero", name)
		}
	}
}

// TestFilterEntityHasColumn: a Filter's FilterEntity answers HasColumn
// from the entity's own metadata, so a filter narrows the entities that
// have a column without a list of them. Two packages' entities of one name
// are told apart: testmodel's Invoice has paid, this package's does not.
// The process-wide HasColumn it replaces answered by name, true for both.
func TestFilterEntityHasColumn(t *testing.T) {
	e := &fakeconn.Exec{}
	var asked []string
	db := openFake(velox.Postgres, e, velox.WithFilter(func(_ context.Context, _ *velox.DB, fe velox.FilterEntity, _ velox.Op) (velox.Predicate, error) {
		asked = append(asked, fe.Name())
		if !fe.HasColumn("paid") {
			return velox.Allow(), nil
		}
		return velox.FieldEQ("paid", false), nil
	}))
	s, _, err := db.Query[testmodel.Invoice]().Limit(1).SQL(ctx)
	if err != nil || !strings.Contains(s, `NOT "paid"`) && !strings.Contains(s, `"paid" = $`) {
		t.Errorf("Invoice: %s, %v; want the paid condition", s, err)
	}
	s, _, err = db.Query[testmodel.Item]().Limit(1).SQL(ctx)
	if err != nil || strings.Contains(s, "paid") {
		t.Errorf("Item: %s, %v; want no condition", s, err)
	}
	s, _, err = db.Query[Invoice]().Limit(1).SQL(ctx)
	if err != nil || strings.Contains(s, "paid") {
		t.Errorf("apitest's Invoice: %s, %v; want no condition", s, err)
	}
	if !slices.Contains(asked, "Invoice") || !slices.Contains(asked, "Item") {
		t.Errorf("filter asked about %q", asked)
	}
	var zero velox.FilterEntity
	if zero.Name() != "" || zero.HasColumn("id") {
		t.Error("the zero FilterEntity names an entity")
	}
}

// Invoice is an entity of the name of testmodel's, without its paid column.
type Invoice struct {
	ID     int64
	Number string
}

var invoiceMeta velox.MetaOnce[Invoice]

// VeloxMeta returns Invoice's metadata.
func (*Invoice) VeloxMeta() *velox.Meta[Invoice] {
	return invoiceMeta.Load(func() velox.Descriptor[Invoice] {
		return velox.Descriptor[Invoice]{
			Table:      "other_invoices",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Number", Name: "number"}},
			PrimaryKey: []int{0},
			FieldPtr:   func(x *Invoice, i int) any { return [...]any{&x.ID, &x.Number}[i] },
		}
	})
}

// TestTxFromJoins: under a transaction's context, TxFrom gives a DB that
// runs in that transaction, with the caller's own policy bypass; db.Tx
// there fails. It is false outside a transaction, after it ended, and for
// another DB's.
func TestTxFromJoins(t *testing.T) {
	e := &fakeconn.Exec{}
	policied := openPolicied(velox.Postgres, e)
	db := policied.WithoutPolicy("velox tests")
	if _, ok := velox.TxFrom(ctx, db); ok {
		t.Error("TxFrom outside a transaction")
	}
	var ended context.Context
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		ended = ctx
		if err := db.Tx(ctx, touch); !errors.Is(err, velox.ErrOutsideTx) {
			t.Errorf("db.Tx under the mark: %v", err)
		}
		joined, ok := velox.TxFrom(ctx, db)
		if !ok {
			t.Fatal("TxFrom in a transaction: false")
		}
		if joined.PolicyBypass() != "velox tests" {
			t.Errorf("bypass %q", joined.PolicyBypass())
		}
		if same, ok := velox.TxFrom(ctx, tx); !ok || same != tx {
			t.Error("TxFrom of the transaction's own DB is not it")
		}
		if p, ok := velox.TxFrom(ctx, policied); !ok || p.PolicyBypass() != "" {
			t.Errorf("TxFrom(policied): %v, bypass %q; want no bypass", ok, p.PolicyBypass())
		}
		if _, ok := velox.TxFrom(ctx, openFake(velox.Postgres, &fakeconn.Exec{})); ok {
			t.Error("TxFrom of another DB")
		}
		if err := touch(ctx, joined); err != nil {
			return err
		}
		return joined.Tx(ctx, func(ctx context.Context, sp *velox.DB) error {
			inner, ok := velox.TxFrom(ctx, db)
			if !ok {
				t.Error("TxFrom in a savepoint: false")
				return nil
			}
			return touch(ctx, inner)
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"BEGIN", touchSQL, "SAVEPOINT velox_1", touchSQL, "RELEASE SAVEPOINT velox_1", "COMMIT"}; !slices.Equal(e.Log, want) {
		t.Errorf("log %q\nwant %q", e.Log, want)
	}
	if _, ok := velox.TxFrom(ended, db); ok {
		t.Error("TxFrom after the transaction ended")
	}
}
