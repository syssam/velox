package velox_test

import (
	"errors"
	"net"
	"testing"

	"github.com/syssam/velox"
	_ "modernc.org/sqlite"
)

type (
	// nilSlice and nilMap have no Scan: a NULL is their nil.
	nilSlice []byte
	nilMap   map[string]string
	// nilScanned has its own Scan, which is told of the NULL.
	nilScanned []byte
)

func (s *nilScanned) Scan(src any) error {
	if src == nil {
		*s = nilScanned("was null")
		return nil
	}
	return errors.New("not null")
}

// nilRow is an entity whose columns have a nil-able Go kind that is not []byte, as
// Bytes().GoType(net.IP{}).Optional().Nillable() gives.
type nilRow struct {
	ID      int64
	IP      net.IP
	Slice   nilSlice
	Map     nilMap
	Scanned nilScanned
	Plain   []byte
}

var nilRowMeta velox.MetaOnce[nilRow]

func (*nilRow) VeloxMeta() *velox.Meta[nilRow] {
	return nilRowMeta.Load(func() velox.Descriptor[nilRow] {
		return velox.Descriptor[nilRow]{
			Table: "nil_rows",
			Columns: []velox.ColumnDescriptor{
				{Field: "ID", Name: "id"},
				{Field: "IP", Name: "ip", Nullable: true},
				{Field: "Slice", Name: "slice", Nullable: true},
				{Field: "Map", Name: "map", Nullable: true},
				{Field: "Scanned", Name: "scanned", Nullable: true},
				{Field: "Plain", Name: "plain", Nullable: true},
			},
			PrimaryKey: []int{0},
			Tenant:     -1,
			Addr: func(x *nilRow, i int) any {
				switch i {
				case 0:
					return &x.ID
				case 1:
					return &x.IP
				case 2:
					return &x.Slice
				case 3:
					return &x.Map
				case 4:
					return &x.Scanned
				case 5:
					return &x.Plain
				}
				return nil
			},
		}
	})
}

// TestNullIntoNilKinds: a NULL reads as nil in a field of every kind that holds nil,
// where database/sql refuses any but a []byte, which failed each page holding the row.
// A value reads as it did, its bytes the row's own, and a type's Scan is still asked.
func TestNullIntoNilKinds(t *testing.T) {
	db, err := velox.Connect("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := velox.Pool(db).Exec(`CREATE TABLE nil_rows (id integer PRIMARY KEY, ip blob, slice blob, map blob, scanned blob, plain blob);
		INSERT INTO nil_rows (id) VALUES (1);
		INSERT INTO nil_rows (id, ip, slice, plain) VALUES (2, x'01020304', x'05', x'06')`); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query[nilRow]().Limit(2).All(t.Context())
	if err != nil {
		t.Fatalf("a NULL in the page: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("%d rows", len(rows))
	}
	if r := rows[0]; r.IP != nil || r.Slice != nil || r.Map != nil || r.Plain != nil {
		t.Errorf("row 1: %+v, want nil", r)
	}
	if got := string(rows[0].Scanned); got != "was null" {
		t.Errorf("a type's Scan: %q", got)
	}
	if r := rows[1]; !r.IP.Equal(net.IP{1, 2, 3, 4}) || string(r.Slice) != "\x05" || string(r.Plain) != "\x06" {
		t.Errorf("row 2: %+v", r)
	}
}
