package valuerecv

import "github.com/syssam/velox2"

type Order struct{ ID int64 }

func (x Order) Schema(s *velox.Schema) {}
