package groupdirective

import "github.com/syssam/velox2"

// The directive below belongs to the group, not to Invoice or Bill.
//
//velox:handle Group
type (
	Invoice struct{ ID int64 }

	Bill struct{ ID int64 }
)

func (x *Invoice) Schema(s *velox.Schema) {}
func (x *Bill) Schema(s *velox.Schema)    {}
