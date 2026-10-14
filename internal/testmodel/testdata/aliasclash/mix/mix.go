package mix

import (
	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel/testdata/aliasclash/events"
)

type M struct{ Kind events.Kind }

func (m *M) Mixin(s *velox.Schema) {}
