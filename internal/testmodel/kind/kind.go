// Package kind holds the named types of the test model's fields. They are in
// a package of their own because the schema package names them in GoType and
// the generated package imports them, so neither can import the other.
// testmodel re-exports them as aliases.
package kind

import (
	"database/sql/driver"
	"fmt"
	"strconv"
	"time"
)

// Dec is a decimal held as its text, standing in for a decimal library's
// type such as shopspring's decimal.Decimal: an Other field of it in a
// numeric column has a NumberCol, which sums and adds.
type Dec string

// Value writes the decimal as its text, which every database converts to its
// numeric column.
func (d Dec) Value() (driver.Value, error) { return string(d), nil }

// Scan reads the text PostgreSQL and MySQL hand over for a numeric, and the
// number SQLite stores for one.
func (d *Dec) Scan(src any) error {
	switch v := src.(type) {
	case string:
		*d = Dec(v)
	case []byte:
		*d = Dec(v)
	case int64:
		*d = Dec(strconv.FormatInt(v, 10))
	case float64:
		*d = Dec(strconv.FormatFloat(v, 'f', -1, 64))
	default:
		return fmt.Errorf("kind.Dec: cannot scan a %T", src)
	}
	return nil
}

// Status is a named string type: its handle is a StringCol.
type Status string

// Cents is a named integer type: its handle is a NumberCol.
type Cents int64

// Score is a named integer type: its handle is a NumberCol.
type Score int64

// Moment is a time type defined on time.Time: a time default, a func() time.Time,
// converts to it. Value and Scan let every driver write and read it.
type Moment time.Time

// Value returns the moment as a time.Time.
func (m Moment) Value() (driver.Value, error) { return time.Time(m), nil }

// Scan reads a time.Time, as every driver hands a DATETIME or timestamptz over.
func (m *Moment) Scan(src any) error {
	t, ok := src.(time.Time)
	if !ok {
		return fmt.Errorf("kind.Moment: cannot scan a %T", src)
	}
	*m = Moment(t)
	return nil
}
