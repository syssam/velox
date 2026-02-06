package fakevelox

import (
	// This import creates a cycle back to the parent package.
	_ "github.com/syssam/velox/compiler/load/testdata/cycle"
)

// Enum is a fake enum type.
type Enum string

// Used is a fake used type.
type Used struct{}
