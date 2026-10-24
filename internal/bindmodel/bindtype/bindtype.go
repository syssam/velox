// Package bindtype holds the named types of the binding model's fields. They
// are in a package of their own because the schema package names them and the
// generated package imports them, so neither can import the other.
package bindtype

import (
	"database/sql/driver"
	"encoding/hex"
	"fmt"
)

// UUID is a test-local UUID type: it binds and scans its canonical
// 36-character text, as google/uuid and gofrs/uuid do, so the module gains no
// dependency.
type UUID [16]byte

// String is the canonical text, xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx.
func (u UUID) String() string {
	var b [36]byte
	hex.Encode(b[0:8], u[0:4])
	b[8] = '-'
	hex.Encode(b[9:13], u[4:6])
	b[13] = '-'
	hex.Encode(b[14:18], u[6:8])
	b[18] = '-'
	hex.Encode(b[19:23], u[8:10])
	b[23] = '-'
	hex.Encode(b[24:], u[10:])
	return string(b[:])
}

// Value binds the canonical text.
func (u UUID) Value() (driver.Value, error) { return u.String(), nil }

// Scan reads the canonical text, as a string or as bytes.
func (u *UUID) Scan(src any) error {
	var s string
	switch v := src.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("bindtype.UUID: cannot scan a %T", src)
	}
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return fmt.Errorf("bindtype.UUID: %q is not a canonical UUID", s)
	}
	h := s[0:8] + s[9:13] + s[14:18] + s[19:23] + s[24:]
	if _, err := hex.Decode(u[:], []byte(h)); err != nil {
		return fmt.Errorf("bindtype.UUID: %q: %w", s, err)
	}
	return nil
}

// Level is an enum type: EnumOf names it.
type Level string

// Values lists Level's values, in their order.
func (Level) Values() []string { return []string{"low", "high"} }

// Doc is a struct stored as JSON.
type Doc struct {
	Title string   `json:"title"`
	Pages int      `json:"pages"`
	Tags  []string `json:"tags"`
}
