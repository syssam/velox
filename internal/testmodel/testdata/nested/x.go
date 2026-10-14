package nested

import "github.com/syssam/velox2"

type Inner struct{ UpdatedAt int64 }

func (t *Inner) Mixin(s *velox.Schema) {}

type Outer struct {
	Inner
	CreatedAt int64
}

type Order struct {
	Outer
	ID int64
}

func (x *Order) Schema(s *velox.Schema) {}
