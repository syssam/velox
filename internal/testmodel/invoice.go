// Package testmodel holds entities for velox's own tests. Its *_velox.go
// files are written by hand in exactly the form velox-gen writes them.
package testmodel

import (
	"time"

	"github.com/syssam/velox2"
)

// Timestamps is a mixin: embedded in an entity, its fields are columns.
type Timestamps struct {
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Mixin makes every update set UpdatedAt.
func (t *Timestamps) Mixin(s *velox.Schema) {
	s.Column(&t.UpdatedAt).OnUpdate(time.Now)
}

// Invoice is the entity velox's tests query.
type Invoice struct {
	Timestamps
	ID      int64
	OrgID   int64
	Number  string
	Total   float64
	Paid    bool
	PaidAt  *time.Time
	Note    *string
	Data    []byte
	Display string `velox:"-"`
}

// Schema declares Invoice's table, unique key and tenant column.
func (x *Invoice) Schema(s *velox.Schema) {
	s.Table("invoices")
	s.Unique(&x.OrgID, &x.Number)
	s.Tenant(&x.OrgID)
}
