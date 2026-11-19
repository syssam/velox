package fixture

import "github.com/syssam/velox"

// Entities is the Descriptor.Entities of a hand-written entity: a table of
// links, built on first use, as velox-gen writes one for a package.
func Entities(links ...velox.EntityLink) func() *velox.EntityTable {
	var t velox.EntityTable
	return func() *velox.EntityTable { return t.Load(func() []velox.EntityLink { return links }) }
}
