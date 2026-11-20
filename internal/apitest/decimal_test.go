package apitest

import (
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// TestDecimalAddSQL: on MySQL, Add and Sub on a decimal column cast their
// argument to the column's type, so that an argument the driver interpolates
// as text ('0.0001') does not make the sum a DOUBLE. PostgreSQL and SQLite
// send what they always sent.
func TestDecimalAddSQL(t *testing.T) {
	L := testmodel.Ledgers
	for _, c := range []struct {
		d    velox.Dialect
		want string
	}{
		{velox.MySQL, "UPDATE `ledgers` SET `amount` = `amount` + CAST(? AS DECIMAL(19,4)), `fee` = `fee` - CAST(? AS DECIMAL(19,4)) WHERE `id` = ?"},
		{velox.Postgres, `UPDATE "ledgers" SET "amount" = "amount" + $1, "fee" = "fee" - $2 WHERE "id" = $3`},
		{velox.SQLite, `UPDATE "ledgers" SET "amount" = "amount" + ?, "fee" = "fee" - ? WHERE "id" = ?`},
	} {
		t.Run(c.d.String(), func(t *testing.T) {
			e := &fakeconn.Exec{Affected: 1}
			db := openFake(c.d, e)
			if _, err := db.Update[testmodel.Ledger]().Where(L.ID.EQ(7)).Set(L.Amount.Add("0.0001"), L.Fee.Sub("0.05")).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			wantLog(t, e, []string{c.want})
			if !reflect.DeepEqual(e.Args, []any{testmodel.Dec("0.0001"), testmodel.Dec("0.05"), int64(7)}) {
				t.Errorf("args %v", e.Args)
			}
		})
	}
}

// A Decimal MySQL's decimal cannot hold is refused with the descriptor.
func TestDecimalDescriptorRefused(t *testing.T) {
	for _, dec := range [][2]uint8{{0, 2}, {66, 2}, {40, 31}, {4, 5}} {
		decCol = dec
		decMeta = new(velox.MetaOnce[decRow])
		err := velox.CheckEntity[decRow]()
		if err == nil || !strings.Contains(err.Error(), "has Decimal") {
			t.Errorf("Decimal %v: %v", dec, err)
		}
	}
}

type decRow struct {
	ID     int64
	Amount testmodel.Dec
}

var (
	decMeta = new(velox.MetaOnce[decRow])
	decCol  [2]uint8
)

func (*decRow) VeloxMeta() *velox.Meta[decRow] {
	return decMeta.Load(func() velox.Descriptor[decRow] {
		return velox.Descriptor[decRow]{
			Table:      "dec_rows",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Amount", Name: "amount", Decimal: decCol}},
			PrimaryKey: []int{0},
			FieldPtr: func(x *decRow, i int) any {
				if i == 0 {
					return &x.ID
				}
				return &x.Amount
			},
		}
	})
}
