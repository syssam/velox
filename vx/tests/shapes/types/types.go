// Package types holds the custom Go types the shapes schema binds to fields
// with GoType(). It is a sibling of the schema package on purpose: generated
// code must import a GoType's package by its own path, and a type declared
// inside the schema package would hide a wrong import.
package types

import (
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Tier is a custom enum type. field.Enum(...).GoType requires Values().
type Tier string

// Tier values.
const (
	TierFree Tier = "free"
	TierPro  Tier = "pro"
)

// Values implements field.EnumValues.
func (Tier) Values() []string { return []string{string(TierFree), string(TierPro)} }

// Label is a custom string type.
type Label string

// Weight is a custom int type.
type Weight int

// Ratio is a custom float type.
type Ratio float64

// Flag is a custom bool type.
type Flag bool

// Address is stored as a JSON object.
type Address struct {
	Street string `json:"street"`
	City   string `json:"city"`
	Zip    int    `json:"zip,omitempty"`
}

// Decimal is a fixed-point number with two decimals. It is a struct, so it
// reaches the database only through its Valuer and comes back only through
// its Scanner: a runtime that converts by kind cannot handle it.
type Decimal struct {
	Cents int64
}

// Value implements driver.Valuer: "12.34".
func (d Decimal) Value() (driver.Value, error) { return d.String(), nil }

// String renders the decimal.
func (d Decimal) String() string {
	sign := ""
	c := d.Cents
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

// Pair is the Go type of an Other field: two ints stored as "a:b".
type Pair struct {
	A, B int
}

// Value implements driver.Valuer.
func (p Pair) Value() (driver.Value, error) { return fmt.Sprintf("%d:%d", p.A, p.B), nil }

// Scan implements sql.Scanner.
func (p *Pair) Scan(src any) error {
	var s string
	switch v := src.(type) {
	case nil:
		*p = Pair{}
		return nil
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("types: cannot scan %T into a Pair", src)
	}
	a, b, ok := strings.Cut(s, ":")
	if !ok {
		return fmt.Errorf("types: bad pair %q", s)
	}
	var err error
	if p.A, err = strconv.Atoi(a); err != nil {
		return err
	}
	p.B, err = strconv.Atoi(b)
	return err
}

// Stamp is a time under a type of its own: it reaches the database through
// its Valuer, and comes back through its Scanner.
type Stamp struct{ T time.Time }

// Value implements driver.Valuer.
func (s Stamp) Value() (driver.Value, error) { return s.T, nil }

// Scan implements sql.Scanner.
func (s *Stamp) Scan(v any) error {
	switch t := v.(type) {
	case time.Time:
		s.T = t
		return nil
	case string:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999"} {
			if at, err := time.Parse(layout, t); err == nil {
				s.T = at
				return nil
			}
		}
	case nil:
		s.T = time.Time{}
		return nil
	}
	return fmt.Errorf("types: cannot scan %T into a Stamp", v)
}
