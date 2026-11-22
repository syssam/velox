package apitest

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
)

// docRow is a projection of JDoc read by Scan: a column by its Go field's
// name, a JSON column decoded, a nullable JSON column into a pointer, and
// named terms by tag and by name.
type docRow struct {
	OwnerID int64
	Tags    []string
	Nums    *[]int
	N       int64
	Tier    *string `velox:"tier"`
	Ignored string  `velox:"-"`
}

func docRowQuery(db *velox.DB) velox.Query[fixture.JDoc] {
	J := fixture.JDocs
	return db.Query[fixture.JDoc]().
		Select(J.OwnerID, J.Tags, J.Nums, velox.CountAll[fixture.JDoc]().As("n"), J.Attrs.Text("tier").Term().As("tier")).
		GroupBy(J.OwnerID, J.ID).Limit(10)
}

// TestScanStruct: each result column goes to its field, decoded by its
// codec where it is a JSON column; NULL is a nil pointer.
func TestScanStruct(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{
		{int64(7), []byte(`["a","b"]`), []byte(`[1,2]`), int64(3), "pro"},
		{int64(8), `[]`, nil, int64(1), nil},
	}}
	db := openFake(velox.SQLite, e)
	got, err := velox.Scan[docRow](ctx, docRowQuery(db))
	if err != nil {
		t.Fatal(err)
	}
	pro := "pro"
	want := []docRow{
		{OwnerID: 7, Tags: []string{"a", "b"}, Nums: &[]int{1, 2}, N: 3, Tier: &pro},
		{OwnerID: 8, Tags: []string{}, N: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	if !strings.Contains(e.SQL, `COUNT(*) AS "n"`) {
		t.Errorf("SQL: %s", e.SQL)
	}
}

// TestScanWhole: a type that is no struct, or a struct with no field for the
// one JSON column read, takes the one term itself.
func TestScanWhole(t *testing.T) {
	J := fixture.JDocs
	e := &fakeconn.Exec{Rows: [][]any{{[]byte(`{"w":2,"h":3}`)}}}
	db := openFake(velox.SQLite, e)
	shapes, err := velox.Scan[fixture.JShape](ctx, db.Query[fixture.JDoc]().Select(J.Shape).Limit(5))
	if err != nil || !reflect.DeepEqual(shapes, []fixture.JShape{{W: 2, H: 3}}) {
		t.Errorf("a struct: %v %v", shapes, err)
	}
	e.Rows = [][]any{{[]byte(`["x"]`)}}
	tags, err := velox.Scan[[]string](ctx, db.Query[fixture.JDoc]().Select(J.Tags).Limit(5))
	if err != nil || !reflect.DeepEqual(tags, [][]string{{"x"}}) {
		t.Errorf("a slice: %v %v", tags, err)
	}
	e.Rows = [][]any{{int64(4)}}
	n, err := velox.ScanOne[int64](ctx, db.Query[fixture.JDoc]().Select(velox.CountAll[fixture.JDoc]()))
	if err != nil || n != 4 || !strings.Contains(e.SQL, "LIMIT 2") {
		t.Errorf("ScanOne: %v %v %s", n, err, e.SQL)
	}
	e.Rows = nil
	if none, err := velox.Scan[int64](ctx, db.Query[fixture.JDoc]().Select(J.ID).Limit(5)); err != nil || none != nil {
		t.Errorf("no row: %v %v", none, err)
	}
}

// TestScanOneCounts: ScanOne's error wraps ErrNotFound for no row and
// ErrNotSingular for two.
func TestScanOneCounts(t *testing.T) {
	J := fixture.JDocs
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	q := db.Query[fixture.JDoc]().Select(J.ID)
	if _, err := velox.ScanOne[int64](ctx, q); !errors.Is(err, velox.ErrNotFound) {
		t.Errorf("no row: %v", err)
	}
	e.Rows = [][]any{{int64(1)}, {int64(2)}}
	if _, err := velox.ScanOne[int64](ctx, q); !errors.Is(err, velox.ErrNotSingular) {
		t.Errorf("two rows: %v", err)
	}
}

// TestScanRefused: a result column with no name or no field, a field for
// two, two fields for one, a field of the wrong type, many terms into one
// value, and a query with no Select fail before anything is sent.
func TestScanRefused(t *testing.T) {
	J := fixture.JDocs
	e := &fakeconn.Exec{}
	db := openFake(velox.SQLite, e)
	q := func(ts ...velox.Selectable[fixture.JDoc]) velox.Query[fixture.JDoc] {
		return db.Query[fixture.JDoc]().Select(ts...).Limit(5)
	}
	type owner struct{ OwnerID int64 }
	type ownerText struct{ OwnerID string }
	type twice struct {
		A int64 `velox:"owner_id"`
		B int64 `velox:"OwnerID"`
	}
	type both struct {
		OwnerID  int64
		Owner_ID int64 //nolint:staticcheck,revive // the name a fold matches
	}
	type count struct{ N string }
	for name, run := range map[string]func() error{
		"unnamed": func() error { _, err := velox.Scan[owner](ctx, q(J.OwnerID, J.ID.Sum())); return err },
		"no field": func() error {
			_, err := velox.Scan[owner](ctx, q(J.OwnerID, J.ID))
			return err
		},
		"wrong type": func() error { _, err := velox.Scan[ownerText](ctx, q(J.OwnerID)); return err },
		"two fields": func() error { _, err := velox.Scan[twice](ctx, q(J.OwnerID)); return err },
		"ambiguous":  func() error { _, err := velox.Scan[both](ctx, q(J.OwnerID)); return err },
		"one field for two": func() error {
			_, err := velox.Scan[owner](ctx, q(J.OwnerID, velox.Calc[fixture.JDoc]("?", J.ID).As("owner_id")))
			return err
		},
		"count into a string": func() error { _, err := velox.Scan[count](ctx, q(velox.CountAll[fixture.JDoc]().As("n"))); return err },
		"many into one":       func() error { _, err := velox.Scan[int64](ctx, q(J.ID, J.OwnerID)); return err },
		"no select": func() error {
			_, err := velox.Scan[int64](ctx, db.Query[fixture.JDoc]().Limit(1))
			return err
		},
		"bad term": func() error { _, err := velox.Scan[int64](ctx, q(velox.Calc[fixture.JDoc]("x;"))); return err },
	} {
		err := run()
		want := map[string]string{
			"unnamed": "result column 2 has no name; name it with As", "no field": "no field takes result column 2 (id)",
			"wrong type": "result column 1 (owner_id) is a int64, which field OwnerID (string) cannot hold",
			"two fields": "fields A and B both take", "ambiguous": "fields OwnerID and Owner_ID both take",
			"one field for two":   "field OwnerID takes result columns 1 and 2",
			"count into a string": "result column 1 (n) is a int64",
			"many into one":       "reads one term, and the projection has 2", "no select": "add Select",
			"bad term": "Expr",
		}[name]
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", name, err, want)
		}
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}

// TestScanDecodeError: a JSON value that does not decode fails, naming the
// entity and the column.
func TestScanDecodeError(t *testing.T) {
	J := fixture.JDocs
	e := &fakeconn.Exec{Rows: [][]any{{[]byte(`{"not":"a list"}`)}}}
	db := openFake(velox.SQLite, e)
	_, err := velox.Scan[[]string](ctx, db.Query[fixture.JDoc]().Select(J.Tags).Limit(5))
	if err == nil || !strings.Contains(err.Error(), "column tags: decoding its value") {
		t.Errorf("got %v", err)
	}
}

// OwnerPart is embedded by pointer in embedded.
type OwnerPart struct{ OwnerID int64 }

// TestScanEmbedded: a promoted field takes its column, and a nil embedded
// pointer on the way to it gets a new struct.
func TestScanEmbedded(t *testing.T) {
	J := fixture.JDocs
	type embedded struct {
		*OwnerPart
		ID int64
	}
	e := &fakeconn.Exec{Rows: [][]any{{int64(9), int64(2)}}}
	db := openFake(velox.SQLite, e)
	got, err := velox.Scan[embedded](ctx, db.Query[fixture.JDoc]().Select(J.OwnerID, J.ID).Limit(5))
	if err != nil || len(got) != 1 || got[0].OwnerPart == nil || got[0].OwnerID != 9 || got[0].ID != 2 {
		t.Errorf("got %+v %v", got, err)
	}
}
