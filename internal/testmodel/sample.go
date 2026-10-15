package testmodel

import (
	"time"

	"github.com/syssam/velox2"
)

// Status is a named string type: its handle is a StringCol.
type Status string

// Cents is a named integer type: its handle is a NumberCol.
type Cents int64

// Score is embedded in Sample: an embedded type that is not a struct is a column named after the type.
type Score int64

// Sample has a field of every handle kind, and both kinds of relation.
type Sample struct {
	ID        int64
	Name      string
	Status    Status
	Prev      *Status
	Price     Cents
	Ratio     *float64
	Flag      bool
	MaybeFlag *bool
	When      time.Time
	MaybeWhen *time.Time
	Blob      []byte
	ParentID  *int64
	Score
	note   string
	Lines  velox.Many[Line]
	Parent velox.One[Sample]
	Hidden string `velox:"-"`
}

// Note returns the unexported note, which is never a column.
func (x *Sample) Note() string { return x.note }

// Line is the to-many side of Sample.Lines. Its table is "line".
type Line struct {
	ID       int64
	SampleID int64
}
