// Written by hand in the shape velox-gen will generate (schema plan 3).

package testmodel

import (
	"time"

	"github.com/syssam/velox2"
)

// Samples holds the typed handles of Sample's columns and relations.
var Samples = struct {
	ID        velox.NumberCol[Sample, int64]
	Name      velox.StringCol[Sample, string]
	Status    velox.StringCol[Sample, Status]
	Prev      velox.NullStringCol[Sample, Status]
	Price     velox.NumberCol[Sample, Cents]
	Ratio     velox.NullNumberCol[Sample, float64]
	Flag      velox.Col[Sample, bool]
	MaybeFlag velox.NullCol[Sample, bool]
	When      velox.OrderedCol[Sample, time.Time]
	MaybeWhen velox.NullOrderedCol[Sample, time.Time]
	Blob      velox.Col[Sample, []byte]
	ParentID  velox.NullNumberCol[Sample, int64]
	Score     velox.NumberCol[Sample, Score]
	Lines     velox.Rel[Sample, Line]
	Parent    velox.Rel[Sample, Sample]
}{
	ID:        velox.NumberCol[Sample, int64]{I: 0},
	Name:      velox.StringCol[Sample, string]{I: 1},
	Status:    velox.StringCol[Sample, Status]{I: 2},
	Prev:      velox.NullStringCol[Sample, Status]{I: 3},
	Price:     velox.NumberCol[Sample, Cents]{I: 4},
	Ratio:     velox.NullNumberCol[Sample, float64]{I: 5},
	Flag:      velox.Col[Sample, bool]{I: 6},
	MaybeFlag: velox.NullCol[Sample, bool]{I: 7},
	When:      velox.OrderedCol[Sample, time.Time]{I: 8},
	MaybeWhen: velox.NullOrderedCol[Sample, time.Time]{I: 9},
	Blob:      velox.Col[Sample, []byte]{I: 10},
	ParentID:  velox.NullNumberCol[Sample, int64]{I: 11},
	Score:     velox.NumberCol[Sample, Score]{I: 12},
	Lines:     velox.Rel[Sample, Line]{I: 0},
	Parent:    velox.Rel[Sample, Sample]{I: 1},
}

var sampleMeta velox.MetaOnce[Sample]

// VeloxMeta is called by velox, not by applications.
func (*Sample) VeloxMeta() *velox.Meta[Sample] { return sampleMeta.Load(sampleDesc) }

func sampleDesc() velox.Desc[Sample] {
	return velox.Desc[Sample]{
		Table: "samples",
		Columns: []velox.ColumnDesc{
			{Field: "ID", Name: "id"},
			{Field: "Name", Name: "name"},
			{Field: "Status", Name: "status"},
			{Field: "Prev", Name: "prev"},
			{Field: "Price", Name: "price"},
			{Field: "Ratio", Name: "ratio"},
			{Field: "Flag", Name: "flag"},
			{Field: "MaybeFlag", Name: "maybe_flag"},
			{Field: "When", Name: "when"},
			{Field: "MaybeWhen", Name: "maybe_when"},
			{Field: "Blob", Name: "blob"},
			{Field: "ParentID", Name: "parent_id"},
			{Field: "Score", Name: "score"},
		},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Relations: []velox.Relation{
			velox.EdgeTo[Line]("Lines", 0, 1),
			velox.EdgeTo[Sample]("Parent", 11, 0),
		},
		Addr: func(x *Sample, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.Name
			case 2:
				return &x.Status
			case 3:
				return &x.Prev
			case 4:
				return &x.Price
			case 5:
				return &x.Ratio
			case 6:
				return &x.Flag
			case 7:
				return &x.MaybeFlag
			case 8:
				return &x.When
			case 9:
				return &x.MaybeWhen
			case 10:
				return &x.Blob
			case 11:
				return &x.ParentID
			case 12:
				return &x.Score
			}
			return nil
		},
		RelAddr: func(x *Sample, i int) any {
			switch i {
			case 0:
				return &x.Lines
			case 1:
				return &x.Parent
			}
			return nil
		},
	}
}
