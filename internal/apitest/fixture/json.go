package fixture

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox2"
)

// JShape is a struct stored as JSON.
type JShape struct {
	W int `json:"w"`
	H int `json:"h"`
}

// JDoc has a column of each kind of JSON value: a slice, a map, a struct,
// and a nullable pointer to a slice.
type JDoc struct {
	ID      int64
	OwnerID int64
	Tags    []string
	Attrs   map[string]any
	Shape   JShape
	Nums    *[]int
}

// JDocs are jDoc's handles as velox-gen writes them: JSONCol and NullJSONCol
// for the encoded columns, which set them and compare or order by nothing.
var JDocs = struct {
	ID      velox.NumberCol[JDoc, int64]
	OwnerID velox.NumberCol[JDoc, int64]
	Tags    velox.JSONCol[JDoc, []string]
	Attrs   velox.JSONCol[JDoc, map[string]any]
	Shape   velox.JSONCol[JDoc, JShape]
	Nums    velox.NullJSONCol[JDoc, []int]
}{
	velox.NumberCol[JDoc, int64]{I: 0}, velox.NumberCol[JDoc, int64]{I: 1},
	velox.JSONCol[JDoc, []string]{I: 2}, velox.JSONCol[JDoc, map[string]any]{I: 3},
	velox.JSONCol[JDoc, JShape]{I: 4}, velox.NullJSONCol[JDoc, []int]{I: 5},
}

var JDocMeta velox.MetaOnce[JDoc]

func (*JDoc) VeloxMeta() *velox.Meta[JDoc] { return JDocMeta.Load(JDocDesc) }

func JDocDesc() velox.Desc[JDoc] {
	return velox.Desc[JDoc]{
		Table: "j_docs",
		Columns: []velox.ColumnDesc{
			{Field: "ID", Name: "id"},
			{Field: "OwnerID", Name: "owner_id"},
			{Field: "Tags", Name: "tags", Encode: json.Marshal, Decode: json.Unmarshal},
			{Field: "Attrs", Name: "attrs", Encode: json.Marshal, Decode: json.Unmarshal},
			{Field: "Shape", Name: "shape", Encode: json.Marshal, Decode: json.Unmarshal},
			{Field: "Nums", Name: "nums", Encode: json.Marshal, Decode: json.Unmarshal},
		},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Addr: func(x *JDoc, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.OwnerID
			case 2:
				return &x.Tags
			case 3:
				return &x.Attrs
			case 4:
				return &x.Shape
			case 5:
				return &x.Nums
			}
			return nil
		},
	}
}

func Ints(xs ...int) *[]int { return &xs }

// JFull is a jDoc with every JSON column set, and the bytes each binds.
func JFull() (JDoc, []any) {
	x := JDoc{ID: 1, OwnerID: 2, Tags: []string{"a", "b"}, Attrs: map[string]any{"k": 1}, Shape: JShape{3, 4}, Nums: Ints(5, 6)}
	return x, []any{int64(1), int64(2), []byte(`["a","b"]`), []byte(`{"k":1}`), []byte(`{"w":3,"h":4}`), []byte(`[5,6]`)}
}

// Bound is args as SQLite and MySQL bind them: each []byte, an encoded value
// that is valid UTF-8, as a string.
func Bound(args []any) []any {
	out := make([]any, len(args))
	for i, a := range args {
		if b, ok := a.([]byte); ok {
			a = string(b)
		}
		out[i] = a
	}
	return out
}

func SameArgs(t *testing.T, what string, got, want []any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: args\n%s\nwant\n%s", what, ShowArgs(got), ShowArgs(want))
	}
}

// ShowArgs prints arguments with their types, and []byte ones as text.
func ShowArgs(args []any) string {
	parts := make([]string, len(args))
	for i, a := range args {
		if bs, ok := a.([]byte); ok {
			parts[i] = "[]byte(" + string(bs) + ")"
		} else {
			parts[i] = fmt.Sprintf("%T(%v)", a, a)
		}
	}
	return strings.Join(parts, ", ")
}

// JList's Words column has a codec of its own, not JSON: the words joined
// by commas inside angle brackets.
type JList struct {
	ID    int64
	Words []string
}

var JListMeta velox.MetaOnce[JList]

func (*JList) VeloxMeta() *velox.Meta[JList] {
	return JListMeta.Load(func() velox.Desc[JList] {
		return velox.Desc[JList]{
			Table: "j_lists",
			Columns: []velox.ColumnDesc{
				{Field: "ID", Name: "id"},
				{Field: "Words", Name: "words", Encode: WrapWords, Decode: UnwrapWords},
			},
			PrimaryKey: []int{0},
			Tenant:     -1,
			Addr: func(x *JList, i int) any {
				if i == 0 {
					return &x.ID
				}
				return &x.Words
			},
		}
	})
}

func WrapWords(v any) ([]byte, error) {
	ws, ok := v.([]string)
	if !ok {
		return nil, fmt.Errorf("not words: %T", v)
	}
	return []byte("<" + strings.Join(ws, ",") + ">"), nil
}

func UnwrapWords(data []byte, v any) error {
	s, pre := strings.CutPrefix(string(data), "<")
	s, suf := strings.CutSuffix(s, ">")
	if !pre || !suf {
		return errors.New("not wrapped")
	}
	*v.(*[]string) = strings.Split(s, ",")
	return nil
}
