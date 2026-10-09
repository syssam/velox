// Package types holds the custom Go type the schema binds to its decimal
// fields. It is a sibling of the schema package on purpose: a type declared in
// the schema package would make the generated entity code import the schema,
// and every generated package would rebuild whenever the schema does.
package types

import (
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"
)

// Decimal is a fixed-point number with two decimals. It is a struct, so it
// reaches the database only through its Valuer and comes back only through
// its Scanner.
type Decimal struct{ Cents int64 }

// Value implements driver.Valuer.
func (d Decimal) Value() (driver.Value, error) { return d.String(), nil }

// String renders the decimal.
func (d Decimal) String() string {
	sign, c := "", d.Cents
	if c < 0 {
		sign, c = "-", -c
	}
	return fmt.Sprintf("%s%d.%02d", sign, c/100, c%100)
}

// Scan implements sql.Scanner.
func (d *Decimal) Scan(src any) error {
	var s string
	switch v := src.(type) {
	case nil:
		d.Cents = 0
		return nil
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("types: cannot scan %T into a Decimal", src)
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	frac = (frac + "00")[:2]
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return fmt.Errorf("types: bad decimal %q: %w", s, err)
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return fmt.Errorf("types: bad decimal %q: %w", s, err)
	}
	d.Cents = w*100 + f
	if neg {
		d.Cents = -d.Cents
	}
	return nil
}
