package registered

import "github.com/syssam/velox2"

type Money struct{ Units int64 }

type Price struct {
	ID     int64
	Amount Money
	Tax    *Money
}

func (x *Price) Schema(s *velox.Schema) {}
