package apitest

import (
	"errors"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
)

// vaddr is a row whose AddressCity field has a StorageKey: its schema name,
// address_city, is not its column, city. ParentID is a Required edge's
// NULL-able key whose schema field, parent_id, is stored in parent_ref.
// velox-gen writes SchemaName only where the two differ.
type vaddr struct {
	ID          int64
	Title       string
	AddressCity string
	ParentID    *int64
}

var vaddrs = struct {
	ID          velox.NumberCol[vaddr, int64]
	Title       velox.StringCol[vaddr, string]
	AddressCity velox.StringCol[vaddr, string]
	ParentID    velox.NullNumberCol[vaddr, int64]
}{velox.NumberCol[vaddr, int64]{I: 0}, velox.StringCol[vaddr, string]{I: 1}, velox.StringCol[vaddr, string]{I: 2}, velox.NullNumberCol[vaddr, int64]{I: 3}}

var vaddrMeta velox.MetaOnce[vaddr]

func (*vaddr) VeloxMeta() *velox.Meta[vaddr] {
	return vaddrMeta.Load(func() velox.Descriptor[vaddr] {
		return velox.Descriptor[vaddr]{
			Table: "vaddrs",
			Columns: []velox.ColumnDescriptor{
				{Field: "ID", Name: "id"}, {Field: "Title", Name: "title"},
				{Field: "AddressCity", Name: "city", SchemaName: "address_city"},
				{Field: "ParentID", Name: "parent_ref", SchemaName: "parent_id", RequiredEdge: "parent"},
			},
			PrimaryKey: []int{0},
			FieldPtr: func(x *vaddr, i int) any {
				return [...]any{&x.ID, &x.Title, &x.AddressCity, &x.ParentID}[i]
			},
			Check: func(col int, v any) error {
				if (col == 1 || col == 2) && v.(string) == "" {
					return errEmpty
				}
				return nil
			},
		}
	})
}

var errEmpty = errors.New("must not be empty")

// TestValidationErrorName: a *ValidationError's Name is the schema field's
// name, as ent's ValidationError.Name is: the column's name for a plain
// field, the schema's name for a field with a StorageKey, and the key
// field's for a Required edge. Error's text names the Go field, as before.
func TestValidationErrorName(t *testing.T) {
	T := vaddrs
	one := int64(1)
	for _, c := range []struct {
		name, field, schema, text string
		run                       func(db *velox.DB) error
	}{
		{"plain insert", "Title", "title", "velox: apitest.vaddr: Title: must not be empty", func(db *velox.DB) error {
			return db.Insert(&vaddr{ID: 1, AddressCity: "x", ParentID: &one}).Exec(ctx)
		}},
		{"storage key insert", "AddressCity", "address_city", "velox: apitest.vaddr: AddressCity: must not be empty", func(db *velox.DB) error {
			return db.Insert(&vaddr{ID: 1, Title: "x", ParentID: &one}).Exec(ctx)
		}},
		{"storage key update", "AddressCity", "address_city", "velox: apitest.vaddr: AddressCity: must not be empty", func(db *velox.DB) error {
			_, err := db.Update[vaddr]().Set(T.AddressCity.Set("")).Where(T.ID.EQ(1)).Exec(ctx)
			return err
		}},
		{"required edge", "ParentID", "parent_id", `velox: apitest.vaddr: ParentID: missing required edge "vaddr.parent"`, func(db *velox.DB) error {
			return db.Insert(&vaddr{ID: 1, Title: "x", AddressCity: "x"}).Exec(ctx)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := &fakeconn.Exec{}
			err := c.run(openFake(velox.Postgres, e))
			ve, ok := errors.AsType[*velox.ValidationError](err)
			if !ok {
				t.Fatalf("got %v, want a *ValidationError", err)
			}
			if ve.Entity != "apitest.vaddr" || ve.Field != c.field || ve.Name != c.schema || err.Error() != c.text {
				t.Errorf("got %+v (%q), want Field %s, Name %s, text %q", *ve, err, c.field, c.schema, c.text)
			}
			if len(e.Log) != 0 {
				t.Errorf("sent %q", e.Log)
			}
		})
	}
}
