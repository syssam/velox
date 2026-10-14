package emptyembed

import (
	"time"

	"github.com/syssam/velox2"
)

type Order struct {
	time.Time
	ID int64
}

func (x *Order) Schema(s *velox.Schema) {}
