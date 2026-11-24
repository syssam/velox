package engine

import (
	"errors"
	"fmt"
	"reflect"
)

// ColumnRead makes st, a read's prepared statement, read column col alone,
// for the root's Values and IDs. what names the caller in errors. A read
// that joins is refused: its rows would carry the joined columns too, and
// an inner join would drop rows that Has or HasWith keep. So is an encoded
// column, whose stored bytes are not its value.
func ColumnRead(st *Statement, col int, what string) error {
	if len(st.Joins) > 0 {
		return fmt.Errorf("velox: %s: %s reads one column and cannot join; test the relation with Has or HasWith", st.Entity.Name, what)
	}
	if col < 0 || col >= len(st.Entity.Columns) {
		return fmt.Errorf("velox: %s: %s: no column %d", st.Entity.Name, what, col)
	}
	if st.Entity.Columns[col].Codec {
		return fmt.Errorf("velox: %s: %s cannot read column %s, an encoded column, whose stored bytes are not its value; read the entities", st.Entity.Name, what, st.Entity.Columns[col].Name)
	}
	st.Columns, st.columnRead = []int{col}, true
	return nil
}

// KeyColumn returns the column of in's one-column primary key, for IDs.
func KeyColumn(in *EntityInfo) (int, error) {
	if in == nil {
		return 0, errors.New("velox: IDs: no entity")
	}
	if len(in.PrimaryKey) != 1 {
		return 0, fmt.Errorf("velox: %s: IDs reads a one-column primary key, and the key has %d columns; read them with Select and Rows", in.Name, len(in.PrimaryKey))
	}
	return in.PrimaryKey[0], nil
}

// KeyTypeError is IDs' error for a type argument that is not the type of
// the key's field: want is a nil *K, and got the field's address.
func KeyTypeError(entity string, want, got any) error {
	return fmt.Errorf("velox: %s: IDs[%v] of a key whose field is %v", entity, reflect.TypeOf(want).Elem(), reflect.TypeOf(got).Elem())
}
