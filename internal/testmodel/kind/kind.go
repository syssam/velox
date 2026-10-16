// Package kind holds the named types of the test model's fields. They are in
// a package of their own because the schema package names them in GoType and
// the generated package imports them, so neither can import the other.
// testmodel re-exports them as aliases.
package kind

// Status is a named string type: its handle is a StringCol.
type Status string

// Cents is a named integer type: its handle is a NumberCol.
type Cents int64

// Score is a named integer type: its handle is a NumberCol.
type Score int64
