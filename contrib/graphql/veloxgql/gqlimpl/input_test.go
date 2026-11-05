package gqlimpl_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

// item is a hand-written entity, as velox-gen would write it, for Update's
// tests: a key, a name its validator refuses empty and an age it refuses
// below 0.
type item struct {
	ID   int64
	Name string
	Age  int64
}

var items = struct {
	ID   velox.NumberCol[item, int64]
	Name velox.StringCol[item, string]
	Age  velox.NumberCol[item, int64]
}{
	ID:   velox.NumberCol[item, int64]{I: 0},
	Name: velox.StringCol[item, string]{I: 1},
	Age:  velox.NumberCol[item, int64]{I: 2},
}

var itemMeta velox.MetaOnce[item]

func (*item) VeloxMeta() *velox.Meta[item] { return itemMeta.Load(itemDescriptor) }

func itemDescriptor() velox.Descriptor[item] {
	return velox.Descriptor[item]{
		Table:      "items",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"}, {Field: "Age", Name: "age"}},
		PrimaryKey: []int{0},
		FieldPtr:   func(x *item, i int) any { return [...]any{&x.ID, &x.Name, &x.Age}[i] },
		Check: func(col int, v any) error {
			switch {
			case col == 1 && v.(string) == "":
				return errors.New("must not be empty")
			case col == 2 && v.(int64) < 0:
				return errors.New("must not be negative")
			}
			return nil
		},
	}
}

// itemInputGQL names the input fields of UpdateItemInput, which sets both
// fields.
func itemInputGQL(goField string) string {
	switch goField {
	case "Name":
		return "name"
	case "Age":
		return "age"
	}
	return ""
}

// updateItem is what an UpdateItemInput's generated Update does with the
// fields name and age, when given, and the application's setters set.
func updateItem(ctx context.Context, name *string, age *int64, set ...velox.Setter[item]) (*item, error) {
	u, key := gqlimpl.NewUpdate[int64](ctx, gqlimpl.InputConn[item]("Item", int(items.ID.I), itemInputGQL), veloxgql.EncodeID("Item", int64(1)))
	if name != nil {
		u.Set(int(items.Name.I), *name)
	}
	if age != nil {
		u.Set(int(items.Age.I), *age)
	}
	p, err := u.Exec(key, velox.Assignments(set))
	return gqlimpl.As[*item](p, err)
}

// TestUpdateAppSetters: an application's setter of a column the input sets
// too wins, and the input's is dropped before it is validated; an
// application's setter that its validator refuses fails with velox's own
// error, uncoded, as the server's bug, while the input's own invalid field
// is still BAD_USER_INPUT naming it.
func TestUpdateAppSetters(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	db := velox.Open(velox.SQLite, sqlDB)
	ctx := veloxgql.NewExtension(db).NewContext(context.Background())
	if err := db.Insert(&item{ID: 1, Name: "a", Age: 1}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	ptr := func(s string) *string { return &s }
	age := func(n int64) *int64 { return &n }

	// The client's name, and the server's: the server's is written.
	x, err := updateItem(ctx, ptr("client"), age(2), items.Name.Set("server"))
	if err != nil || x.Name != "server" || x.Age != 2 {
		t.Fatalf("update with the server's name = %+v, %v; want server, 2", x, err)
	}
	// The client's name is invalid, but dropped: the server's wins.
	if x, err := updateItem(ctx, ptr(""), nil, items.Name.Set("server 2")); err != nil || x.Name != "server 2" {
		t.Fatalf("update with an invalid client name the server's replaces = %+v, %v; want server 2", x, err)
	}
	// The server's own setter is invalid: velox's error, as it is.
	_, err = updateItem(ctx, ptr("ok"), nil, items.Age.Set(-1))
	var ve *velox.ValidationError
	if !errors.As(err, &ve) || codeOf(err) != "" {
		t.Fatalf("update with an invalid server setter = %v (code %q); want a *velox.ValidationError, uncoded", err, codeOf(err))
	}
	// The client's invalid field, beside a valid server setter of another.
	_, err = updateItem(ctx, nil, age(-1), items.Name.Set("fine"))
	if codeOf(err) != veloxgql.CodeBadUserInput || err.Error() != "veloxgql: UpdateItemInput.age: invalid value: must not be negative" {
		t.Fatalf("update with an invalid client age = %v (code %q); want BAD_USER_INPUT naming UpdateItemInput.age", err, codeOf(err))
	}
	x, err = db.Query[item]().Where(items.ID.EQ(1)).One(ctx)
	if err != nil || x.Name != "server 2" || x.Age != 2 {
		t.Fatalf("the row = %+v, %v; want server 2, 2: the failed updates wrote nothing", x, err)
	}
}
