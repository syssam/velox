package generic

import "github.com/syssam/velox2"

type Box[T any] struct{ ID int64 }

func (x *Box[T]) Schema(s *velox.Schema) {}
