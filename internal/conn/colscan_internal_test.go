package conn

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	_ "modernc.org/sqlite"
)

type (
	status  string
	cents   int64
	moment  time.Time // no methods
	scanned time.Time // its pointer has Scan, as kind.Moment's does
	pstr    *string
)

func (s *scanned) Scan(src any) error {
	t, ok := src.(time.Time)
	if !ok {
		return sql.ErrNoRows
	}
	*s = scanned(t)
	return nil
}

// scanCase is a destination type, always a pointer, and whether scanWraps
// says it is wrapped.
type scanCase struct {
	t    reflect.Type
	want bool
}

// scanCases lists the destinations velox2 supports and the ones it leaves
// alone.
var scanCases = []scanCase{
	{reflect.TypeFor[*int](), true},
	{reflect.TypeFor[*int8](), true},
	{reflect.TypeFor[*int16](), true},
	{reflect.TypeFor[*int32](), true},
	{reflect.TypeFor[*int64](), true},
	{reflect.TypeFor[*uint](), true},
	{reflect.TypeFor[*uint8](), true},
	{reflect.TypeFor[*uint16](), true},
	{reflect.TypeFor[*uint32](), true},
	{reflect.TypeFor[*uint64](), true},
	{reflect.TypeFor[*float32](), true},
	{reflect.TypeFor[*float64](), true},
	{reflect.TypeFor[*bool](), true},
	{reflect.TypeFor[*string](), true},
	{reflect.TypeFor[*[]byte](), true},
	{reflect.TypeFor[*time.Time](), true},
	{reflect.TypeFor[**string](), true},
	{reflect.TypeFor[**int64](), true},
	{reflect.TypeFor[**time.Time](), true},
	{reflect.TypeFor[**float64](), true},
	{reflect.TypeFor[**bool](), true},
	{reflect.TypeFor[*json.RawMessage](), true},
	{reflect.TypeFor[*status](), true},
	{reflect.TypeFor[**status](), true},
	{reflect.TypeFor[*cents](), true},
	{reflect.TypeFor[*moment](), true},

	{reflect.TypeFor[*sql.NullString](), false},
	{reflect.TypeFor[*scanned](), false},
	{reflect.TypeFor[**scanned](), false},
	{reflect.TypeFor[*[16]byte](), false},
	{reflect.TypeFor[*[]string](), false},
	{reflect.TypeFor[*map[string]int](), false},
	{reflect.TypeFor[*struct{ A int }](), false},
	{reflect.TypeFor[*any](), false},
	{reflect.TypeFor[*sql.RawBytes](), false},
	{reflect.TypeFor[*pgtype.Text](), false},
	{reflect.TypeFor[*pgtype.FlatArray[int32]](), false},
	{reflect.TypeFor[*pstr](), false},
	{reflect.TypeFor[string](), false},
	{nil, false},
}

func TestScanWrapsClassification(t *testing.T) {
	for _, c := range scanCases {
		name := "nil"
		if c.t != nil {
			name = c.t.String()
		}
		t.Run(name, func(t *testing.T) {
			if got := scanWrapsType(c.t); got != c.want {
				t.Errorf("scanWrapsType = %v, want %v", got, c.want)
			}
			// The second answer comes from the cache.
			for range 2 {
				if got := scanWraps(c.t); got != c.want {
					t.Errorf("scanWraps = %v, want %v", got, c.want)
				}
			}
		})
	}
}

func TestBeginKeepsColumnScan(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	for _, cols := range []bool{true, false} {
		ex, tx, err := NewSQLCols(db, cols).Begin(ctx, sql.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		r, err := ex.Query(ctx, "SELECT 1", nil)
		if err != nil {
			t.Fatal(err)
		}
		switch r.(type) {
		case *colRows:
			if !cols {
				t.Error("cols false: query gave colRows")
			}
		case sqlRows:
			if cols {
				t.Error("cols true: query gave sqlRows")
			}
		default:
			t.Errorf("query gave %T", r)
		}
		r.Close()
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
	}
}
