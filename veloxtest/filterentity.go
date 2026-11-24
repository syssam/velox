package veloxtest

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/engine"
)

// FilterEntity is a stand-in for the entity a velox.Filter is called
// for: its Name is name, and HasColumn is true for exactly the given
// columns. A filter is tested with it alone, with no schema and no
// database:
//
//	e := veloxtest.FilterEntity("Invoice", "id", "org_id", "deleted_at")
//	p, err := filter(ctx, nil, e, velox.OpSelect)
func FilterEntity(name string, columns ...string) velox.FilterEntity {
	return engine.WrapFilterEntity(engine.StandInInfo(name, columns...)).(velox.FilterEntity)
}
