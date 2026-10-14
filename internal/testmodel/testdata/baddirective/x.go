package baddirective

import "github.com/syssam/velox2"

//velox:handle not-an-identifier
type Invoice struct{ ID int64 }

func (x *Invoice) Schema(s *velox.Schema) {}
