// Package kind holds the named types of the test model's fields. They are in
// a package of their own because the schema package names them in GoType and
// the generated package imports them, so neither can import the other.
// testmodel re-exports them as aliases.
package kind

import (
	"database/sql/driver"
	"fmt"
	"time"
)

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
