package directive

import "github.com/syssam/velox2"

// Invoice's handles are named Bills.
//
//velox:handle Bills
type Invoice struct{ ID int64 }

func (x *Invoice) Schema(s *velox.Schema) {}
