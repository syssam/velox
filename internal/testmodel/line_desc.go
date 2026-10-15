// Written by hand in the shape velox-gen will generate (schema plan 3).

package testmodel

import (
	"github.com/syssam/velox2"
)

// Lines holds the typed handles of Line's columns and relations.
var Lines = struct {
	ID       velox.NumberCol[Line, int64]
	SampleID velox.NumberCol[Line, int64]
}{
	ID:       velox.NumberCol[Line, int64]{I: 0},
	SampleID: velox.NumberCol[Line, int64]{I: 1},
}

var lineMeta velox.MetaOnce[Line]

// VeloxMeta is called by velox, not by applications.
func (*Line) VeloxMeta() *velox.Meta[Line] { return lineMeta.Load(lineDesc) }

func lineDesc() velox.Desc[Line] {
	return velox.Desc[Line]{
		Table:      "line",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "SampleID", Name: "sample_id"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Addr: func(x *Line, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.SampleID
			}
			return nil
		},
	}
}
