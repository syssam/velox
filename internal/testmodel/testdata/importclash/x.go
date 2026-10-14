package importclash

import (
	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel/testdata/importclash/notes"
)

// note's handles variable would be notes, the name of an import above.
type note struct {
	ID   int64
	Kind notes.Kind
}

func (x *note) Schema(s *velox.Schema) {}
