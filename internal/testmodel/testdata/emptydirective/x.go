package emptydirective

import "github.com/syssam/velox2"

//velox:handle
type Invoice struct{ ID int64 }

func (x *Invoice) Schema(s *velox.Schema) {}
