package veloxgql_test

import (
	_ "modernc.org/sqlite"

	"github.com/syssam/velox2"
)

// person is a hand-written entity, as velox-gen would write it, for the
// handler's tests: a key, a name and an age. gqlimpl's paginate_test.go
// declares the same entity; the two test packages are separate and cannot
// share a declaration without a non-test package only tests would import.
type person struct {
	ID   int64
	Name string
	Age  int64
}

var people = struct {
	ID   velox.NumberCol[person, int64]
	Name velox.StringCol[person, string]
	Age  velox.NumberCol[person, int64]
}{
	ID:   velox.NumberCol[person, int64]{I: 0},
	Name: velox.StringCol[person, string]{I: 1},
	Age:  velox.NumberCol[person, int64]{I: 2},
}

var personMeta velox.MetaOnce[person]

func (*person) VeloxMeta() *velox.Meta[person] { return personMeta.Load(personDesc) }

func personDesc() velox.Desc[person] {
	return velox.Desc[person]{
		Table:      "people",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"}, {Field: "Age", Name: "age"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Addr: func(x *person, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.Name
			case 2:
				return &x.Age
			}
			return nil
		},
	}
}
