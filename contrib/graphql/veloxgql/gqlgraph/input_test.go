package gqlgraph_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

// item is a hand-written entity, as velox-gen would write it, for Checked's
// tests: a key, a name its validator refuses empty, an age it refuses below
// 0 and an immutable code it refuses empty.
type item struct {
	ID   int64
	Name string
	Age  int64
	Code string
}

var items = struct {
	ID   velox.NumberCol[item, int64]
	Name velox.StringCol[item, string]
	Age  velox.NumberCol[item, int64]
	Code velox.StringCol[item, string]
}{
	ID:   velox.NumberCol[item, int64]{I: 0},
	Name: velox.StringCol[item, string]{I: 1},
	Age:  velox.NumberCol[item, int64]{I: 2},
	Code: velox.StringCol[item, string]{I: 3},
}

var itemMeta velox.MetaOnce[item]

func (*item) VeloxMeta() *velox.Meta[item] { return itemMeta.Load(itemDescriptor) }

func itemDescriptor() velox.Descriptor[item] {
	return velox.Descriptor[item]{
		Table: "items",
		Columns: []velox.ColumnDescriptor{
			{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"}, {Field: "Age", Name: "age"},
			{Field: "Code", Name: "code", Immutable: true},
		},
		PrimaryKey: []int{0},
		FieldPtr:   func(x *item, i int) any { return [...]any{&x.ID, &x.Name, &x.Age, &x.Code}[i] },
		Check: func(col int, v any) error {
			switch {
			case (col == 1 || col == 3) && v.(string) == "":
				return errors.New("must not be empty")
			case col == 2 && v.(int64) < 0:
				return errors.New("must not be negative")
			}
			return nil
		},
	}
}

// itemInputGQL is, by column, the inputs' fields.
var itemInputGQL = []string{1: "name", 2: "age", 3: "code"}

// itemDB is an in-memory database with the items table and the item 1, and
// a context of the Extension on it.
func itemDB(t *testing.T) (*velox.DB, context.Context) {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER NOT NULL, code TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	db := velox.Open(velox.SQLite, sqlDB)
	ctx := context.Background()
	if err := db.Insert(&item{ID: 1, Name: "a", Age: 1, Code: "c"}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return db, ctx
}

// TestCheckedCreate: a create input's setters are checked as an insert
// checks its row, so an immutable column may be set; a validator's error is
// BAD_USER_INPUT naming the input's field, by CreateInputs when it is set;
// the setters that pass go to the insert.
func TestCheckedCreate(t *testing.T) {
	db, ctx := itemDB(t)
	c := gqlgraph.InputType[item]("Item", int(items.ID.I), itemInputGQL)
	set, err := gqlgraph.Validate("CreateItemInput", c, []velox.Setter[item]{items.Name.Set("b"), items.Age.Set(2), items.Code.Set("d")})
	if err != nil || len(set) != 3 {
		t.Fatalf("Checked = %d setters, %v; want 3", len(set), err)
	}
	x := &item{ID: 2}
	if err := db.Insert(x).Set(set...).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := db.Query[item]().Where(items.ID.EQ(2)).One(ctx); err != nil || *got != (item{ID: 2, Name: "b", Age: 2, Code: "d"}) {
		t.Fatalf("the row = %+v, %v", got, err)
	}

	_, err = gqlgraph.Validate("CreateItemInput", c, []velox.Setter[item]{items.Name.Set("ok"), items.Code.Set("")})
	if _, ok := errors.AsType[*velox.ValidationError](err); codeOf(err) != veloxgql.CodeBadUserInput || err.Error() != "veloxgql: CreateItemInput.code: invalid value: must not be empty" || !ok {
		t.Errorf("an empty code = %v (code %q); want BAD_USER_INPUT naming CreateItemInput.code", err, codeOf(err))
	}
	c.CreateInputs = []string{1: "title"}
	_, err = gqlgraph.Validate("CreateItemInput", c, []velox.Setter[item]{items.Name.Set("")})
	if want := "veloxgql: CreateItemInput.title: invalid value: must not be empty"; err == nil || err.Error() != want {
		t.Errorf("by CreateInputs = %v, want %s", err, want)
	}
	// A column the input does not name: velox's error, as it is.
	_, err = gqlgraph.Validate("CreateItemInput", c, []velox.Setter[item]{items.Age.Set(-1)})
	if _, ok := errors.AsType[*velox.ValidationError](err); !ok || codeOf(err) != "" {
		t.Errorf("an unnamed column = %v (code %q); want a *velox.ValidationError, uncoded", err, codeOf(err))
	}
}

// TestCheckedUpdate: an update input's setters are checked as an update
// checks its setters; a validator's error is BAD_USER_INPUT naming the
// input's field, by UpdateInputs when it is set. A setter the application
// adds after Checked is the write's to check: its error is velox's,
// uncoded, as the server's bug.
func TestCheckedUpdate(t *testing.T) {
	db, ctx := itemDB(t)
	c := gqlgraph.InputType[item]("Item", int(items.ID.I), itemInputGQL)
	in := []velox.Setter[item]{items.Name.Set("client"), items.Age.Set(2)}
	set, err := gqlgraph.Validate("UpdateItemInput", c, in)
	if err != nil || !slices.Equal(untyped.Assignments(set), untyped.Assignments(in)) {
		t.Fatalf("Checked = %v, %v; want the setters", set, err)
	}
	if n, err := db.Update[item]().Where(items.ID.EQ(1)).Set(set...).Exec(ctx); err != nil || n != 1 {
		t.Fatalf("update = %d, %v", n, err)
	}

	_, err = gqlgraph.Validate("UpdateItemInput", c, []velox.Setter[item]{items.Age.Set(-1)})
	if codeOf(err) != veloxgql.CodeBadUserInput || err.Error() != "veloxgql: UpdateItemInput.age: invalid value: must not be negative" {
		t.Errorf("an invalid age = %v (code %q); want BAD_USER_INPUT naming UpdateItemInput.age", err, codeOf(err))
	}
	c.UpdateInputs = []string{2: "years"}
	_, err = gqlgraph.Validate("UpdateItemInput", c, []velox.Setter[item]{items.Age.Set(-1)})
	if want := "veloxgql: UpdateItemInput.years: invalid value: must not be negative"; err == nil || err.Error() != want {
		t.Errorf("by UpdateInputs = %v, want %s", err, want)
	}

	set, err = gqlgraph.Validate("UpdateItemInput", c, []velox.Setter[item]{items.Age.Set(3)})
	if err != nil {
		t.Fatal(err)
	}
	set = append(set, items.Name.Set(""))
	_, err = db.Update[item]().Where(items.ID.EQ(1)).Set(set...).Exec(ctx)
	if _, ok := errors.AsType[*velox.ValidationError](err); !ok || codeOf(err) != "" {
		t.Errorf("an invalid setter of the application's = %v (code %q); want a *velox.ValidationError, uncoded", err, codeOf(err))
	}
	if got, err := db.Query[item]().Where(items.ID.EQ(1)).One(ctx); err != nil || got.Name != "client" || got.Age != 2 {
		t.Errorf("the row = %+v, %v; want client, 2: the failed update wrote nothing", got, err)
	}
}

// TestCheckedSkipsSetPtrNil: a SetPtr of a nil pointer sets nothing, so a
// create input does not store and validate the zero value, and an update
// input does not refuse it on an immutable column.
func TestCheckedSkipsSetPtrNil(t *testing.T) {
	c := gqlgraph.InputType[item]("Item", int(items.ID.I), itemInputGQL)
	if _, err := gqlgraph.Validate("CreateItemInput", c, []velox.Setter[item]{items.Name.Set("ok"), items.Code.SetPtr(nil)}); err != nil {
		t.Errorf("create with a nil SetPtr: %v", err)
	}
	if _, err := gqlgraph.Validate("UpdateItemInput", c, []velox.Setter[item]{items.Code.SetPtr(nil), items.Age.Set(1)}); err != nil {
		t.Errorf("update with a nil SetPtr of an immutable column: %v", err)
	}
	code := "x"
	if _, err := gqlgraph.Validate("UpdateItemInput", c, []velox.Setter[item]{items.Code.SetPtr(&code)}); !errors.Is(err, velox.ErrImmutable) {
		t.Errorf("update with a SetPtr of an immutable column: %v", err)
	}
}
