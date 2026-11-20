package apitest

import (
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
)

// dEnum is an order whose status is an enum with a declared database
// default ('new'): its validator refuses the empty string.
type dEnum struct {
	ID     int64
	Name   string
	Status string
}

var dEnums = struct {
	ID     velox.NumberCol[dEnum, int64]
	Name   velox.StringCol[dEnum, string]
	Status velox.StringCol[dEnum, string]
}{velox.NumberCol[dEnum, int64]{I: 0}, velox.StringCol[dEnum, string]{I: 1}, velox.StringCol[dEnum, string]{I: 2}}

var dEnumMeta velox.MetaOnce[dEnum]

func (*dEnum) VeloxMeta() *velox.Meta[dEnum] {
	return dEnumMeta.Load(func() velox.Descriptor[dEnum] {
		return velox.Descriptor[dEnum]{
			Table: "denums",
			Columns: []velox.ColumnDescriptor{
				{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"},
				{Field: "Status", Name: "status", DBDefault: allDialects},
			},
			PrimaryKey: []int{0},
			FieldPtr: func(x *dEnum, i int) any {
				return [...]any{&x.ID, &x.Name, &x.Status}[i]
			},
			Check: func(col int, v any) error {
				if col != 2 {
					return nil
				}
				switch v.(string) {
				case "new", "paid", "shipped":
					return nil
				}
				return errors.New("is not one of new, paid, shipped")
			},
		}
	})
}

func enumDB(t *testing.T) (*velox.DB, *sql.DB) {
	t.Helper()
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "enums.db"))
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	exec(t, raw, `CREATE TABLE denums (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, status TEXT NOT NULL DEFAULT 'new')`)
	return velox.Open(velox.SQLite, raw), raw
}

func isInvalidStatus(err error) bool {
	var ve *velox.ValidationError
	return errors.As(err, &ve) && strings.Contains(err.Error(), "is not one of")
}

// TestEnumSchemaDefaultZero: an enum's zero value in a column with a
// declared database default is left to the database, so its validators do
// not run on it (D10): Insert, InsertAll and an upsert's inserted row store
// 'new'. A value given is validated; so is the zero when the upsert's
// conflict clause writes the column (it is sent as held), and on update.
func TestEnumSchemaDefaultZero(t *testing.T) {
	db, raw := enumDB(t)
	E := dEnums
	a := dEnum{Name: "a"}
	if err := db.Insert(&a).Exec(ctx); err != nil {
		t.Fatalf("Insert of the zero status: %v", err)
	}
	if a.Status != "new" {
		t.Errorf("read back status %q, want new", a.Status)
	}
	xs := []dEnum{{Name: "b"}, {Name: "c", Status: "paid"}}
	if err := db.InsertAll(xs).Exec(ctx); err != nil {
		t.Fatalf("InsertAll: %v", err)
	}
	if xs[0].Status != "new" || xs[1].Status != "paid" {
		t.Errorf("InsertAll read back %+v", xs)
	}
	if err := db.Insert(&dEnum{Name: "d"}).OnConflict(E.Name).Update(E.Name).Exec(ctx); err != nil {
		t.Fatalf("upsert leaving status to the database: %v", err)
	}
	var status string
	if err := raw.QueryRow("SELECT status FROM denums WHERE name = 'd'").Scan(&status); err != nil || status != "new" {
		t.Errorf("stored %q, %v; want new", status, err)
	}

	if err := db.Insert(&dEnum{Name: "e", Status: "lost"}).Exec(ctx); !isInvalidStatus(err) {
		t.Errorf("Insert of an invalid status: %v, want a ValidationError", err)
	}
	if err := db.InsertAll([]dEnum{{Name: "f"}, {Name: "g", Status: "lost"}}).Exec(ctx); !isInvalidStatus(err) {
		t.Errorf("InsertAll with an invalid status: %v, want a ValidationError", err)
	}
	if err := db.Insert(&dEnum{Name: "a"}).OnConflict(E.Name).Update(E.Status).Exec(ctx); !isInvalidStatus(err) {
		t.Errorf("upsert that writes the zero status: %v, want a ValidationError", err)
	}
	if err := db.Insert(&dEnum{Name: "h"}).OnConflict(E.Status).DoNothing().Exec(ctx); !isInvalidStatus(err) {
		t.Errorf("upsert whose conflict target is the zero status: %v, want a ValidationError", err)
	}
	if _, err := db.Update[dEnum]().Set(E.Status.Set("")).Where(E.ID.EQ(a.ID)).Exec(ctx); !isInvalidStatus(err) {
		t.Errorf("Update to the zero status: %v, want a ValidationError", err)
	}
}

// TestEnumSchemaDefaultZeroDefaultCell: in a mixed InsertAll on PostgreSQL
// and MySQL, a row's zero enum left to its declared default is a DEFAULT
// cell in the one statement, and passes validation; the other row's value
// is sent and validated.
func TestEnumSchemaDefaultZeroDefaultCell(t *testing.T) {
	for _, c := range []struct {
		d    velox.Dialect
		e    *fakeconn.Exec
		want []string
	}{
		{velox.Postgres, &fakeconn.Exec{Rows: [][]any{{int64(1), "new"}, {int64(2), "paid"}}}, []string{
			`INSERT INTO "denums" ("name", "status") VALUES ($1, DEFAULT), ($2, $3) RETURNING "id", "status"`,
		}},
		{velox.MySQL, &fakeconn.Exec{Affected: 2, LastID: 1, HasLastID: true, Rows: [][]any{{int64(1), "new"}, {int64(2), "paid"}}}, []string{
			"INSERT INTO `denums` (`name`, `status`) VALUES (?, DEFAULT), (?, ?)",
			"SELECT `id`, `status` FROM `denums` WHERE `id` IN (?, ?)",
		}},
	} {
		xs := []dEnum{{Name: "b"}, {Name: "c", Status: "paid"}}
		if err := openFake(c.d, c.e).InsertAll(xs).Exec(ctx); err != nil {
			t.Fatalf("%s: %v", c.d, err)
		}
		if !slices.Equal(c.e.Log, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.d, c.e.Log, c.want)
		}
		if xs[0].ID != 1 || xs[0].Status != "new" || xs[1].Status != "paid" {
			t.Errorf("%s: read back %+v", c.d, xs)
		}
	}
}
