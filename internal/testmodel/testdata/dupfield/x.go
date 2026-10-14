package dupfield

import "github.com/syssam/velox2"

type Base struct{ ID int64 }

type Order struct {
	Base
	ID int64
}

func (x *Order) Schema(s *velox.Schema) {}
