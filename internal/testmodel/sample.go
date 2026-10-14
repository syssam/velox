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

// Sample has a field of every handle kind, and both kinds of relation, declared.
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

// Schema names Sample's table and declares both relations' keys.
func (x *Sample) Schema(s *velox.Schema) {
	s.Table("samples")
	s.Many(&x.Lines).ForeignKey(func(l *Line) *int64 { return &l.SampleID })
	s.One(&x.Parent).References(&x.ParentID)
}

// Note returns the unexported note, which is never a column.
func (x *Sample) Note() string { return x.note }

// Line is the to-many side of Sample.Lines.
type Line struct {
	ID       int64
	SampleID int64
}

// Schema declares nothing: Line's table is "line".
func (x *Line) Schema(s *velox.Schema) {}
