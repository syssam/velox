package aliasclash

import (
	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel/testdata/aliasclash/mix"
)

// event's handles variable is events, and its file does not import the
// package events, which only the mixin's field type needs.
type event struct {
	mix.M
	ID int64
}

func (x *event) Schema(s *velox.Schema) {}
