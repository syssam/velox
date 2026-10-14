package collision

import "github.com/syssam/velox2"

type Order struct{ ID int64 }

func (x *Order) Schema(s *velox.Schema) {}

// Orders is the application's own, so the handles need another name.
var Orders = 1
