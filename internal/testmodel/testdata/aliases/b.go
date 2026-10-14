package aliases

import (
	gotime "time"

	"github.com/syssam/velox2"
)

type Event struct {
	ID int64
	At gotime.Time
}

func (x *Event) Schema(s *velox.Schema) {}
