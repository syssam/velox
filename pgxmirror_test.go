package velox

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// pgxIsSQLScanDestination is a copy of isSQLScanDestination from
// stdlib/sql_go1.27.go of github.com/jackc/pgx/v5 v5.11.0, renamed. pgx is
// licensed under the MIT license, Copyright (c) 2013-2021 Jack Christensen.
// scanWrapsType wraps only destinations this function accepts, so the copy is
// what the tests below check it against; TestPgxMirrorIsCurrent compares the
// copy with the pgx the module uses.
func pgxIsSQLScanDestination(t reflect.Type) bool {
	var namedPointers map[reflect.Type]bool
	for {
		if t.Implements(reflect.TypeFor[sql.Scanner]()) {
			return true
		}
		// These interfaces distinguish native byte slices, such as
		// FlatArray[byte] and PreallocBytes, from ordinary []byte destinations.
		if t.Implements(reflect.TypeFor[pgtype.ArraySetter]()) || t.Implements(reflect.TypeFor[pgtype.BytesScanner]()) {
			return false
		}
		if t.Kind() != reflect.Pointer {
			break
		}
		// Only defined pointer types can form a cycle without reaching a
		// non-pointer type. Leave those to pgx's bounded scan planning.
		if t.Name() != "" {
			if namedPointers[t] {
				return false
			}
			if namedPointers == nil {
				namedPointers = make(map[reflect.Type]bool)
			}
			namedPointers[t] = true
		}
		t = t.Elem()
	}

	switch t.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64, reflect.String, reflect.Interface:
		return true
	case reflect.Slice:
		return t.Elem().Kind() == reflect.Uint8
	case reflect.Struct:
		return t.ConvertibleTo(reflect.TypeFor[time.Time]())
	default:
		return false
	}
}

// pgxScanColumnText is a copy of (*Rows).ScanColumn from the same file. The
// scan destination colScanner converts as the ScanColumn branch for a
// sql.Scanner does: ConvertAssign on the value that valueFuncs gives. The copy
// is not run; TestPgxMirrorIsCurrent compares it with the pgx the module uses,
// so that a change to that branch, or to what runs before it, is noticed.
const pgxScanColumnText = `func (r *Rows) ScanColumn(scanCtx driver.ScanContext, index int, dest any) error {
	if dest == nil {
		return errors.New("destination not a pointer")
	}
	rv := reflect.ValueOf(dest)
	if rv.Kind() == reflect.Pointer && rv.IsNil() {
		return errors.New("destination pointer is nil")
	}

	src := r.rows.RawValues()[index]
	if isSQLScanDestination(rv.Type()) {
		var value driver.Value
		if src != nil {
			r.initValueFuncs()
			var err error
			value, err = r.valueFuncs[index](src)
			if err != nil {
				return err
			}
		}
		return sql.ConvertAssign(scanCtx, dest, value)
	}

	m := r.conn.conn.TypeMap()
	fd := r.rows.FieldDescriptions()[index]
	return m.Scan(fd.DataTypeOID, fd.Format, src, dest)
}`

// funcText is the text of the function that starts with the line head, up to
// the first line that is exactly "}".
func funcText(t *testing.T, src, head string) string {
	t.Helper()
	i := strings.Index(src, "\n"+head)
	if i < 0 {
		t.Fatalf("no %q in the source", head)
	}
	rest := src[i+1:]
	j := strings.Index(rest, "\n}\n")
	if j < 0 {
		t.Fatalf("%q has no closing brace line", head)
	}
	return rest[:j+2]
}

func TestScanWrapsMirrorsPgx(t *testing.T) {
	scanner := reflect.TypeFor[sql.Scanner]()
	types := []reflect.Type{reflect.TypeFor[***string](), reflect.TypeFor[*time.Location]()}
	for _, c := range scanCases {
		types = append(types, c.t)
	}
	wrapped := 0
	for _, typ := range types {
		if typ == nil || !scanWraps(typ) {
			continue
		}
		wrapped++
		if !pgxIsSQLScanDestination(typ) {
			t.Errorf("%s: scanWraps, but pgx would not accept it as a destination", typ)
		}
		if typ.Implements(scanner) {
			t.Errorf("%s: scanWraps, but it is a sql.Scanner already", typ)
		}
	}
	if wrapped == 0 {
		t.Fatal("no type is wrapped")
	}
}

func TestPgxMirrorIsCurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/jackc/pgx/v5").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	dir := strings.TrimSpace(string(out))
	file := filepath.Join(dir, "stdlib", "sql_go1.27.go")
	pgx, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("pgx's isSQLScanDestination changed: %s cannot be read (%v): update the copy and re-check scanWrapsType", file, err)
	}
	mine, err := os.ReadFile("pgxmirror_test.go")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(funcText(t, string(pgx), "func isSQLScanDestination("), "func isSQLScanDestination(", "func pgxIsSQLScanDestination(", 1)
	if got := funcText(t, string(mine), "func pgxIsSQLScanDestination("); got != want {
		t.Error("pgx's isSQLScanDestination changed: update the copy and re-check scanWrapsType")
	}
	if got := funcText(t, string(pgx), "func (r *Rows) ScanColumn("); got != pgxScanColumnText {
		t.Error("pgx's ScanColumn changed: update the copy and re-check that colScanner still converts as its Scanner branch does")
	}
}
