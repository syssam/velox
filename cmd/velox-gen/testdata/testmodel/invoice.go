// Package testmodel is velox's test model as it was on the old path, with
// Schema methods, before internal/testmodel moved to the Desc path (schema
// plan 2). It is velox-gen's fixture until schema plan 4 removes the
// old generator. Its *_velox.go files are written by velox-gen; velox-gen's
// golden test fails when they differ from what it writes.
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

// Mixin sets CreatedAt when an Invoice is inserted, and UpdatedAt on every
// insert and update.
func (t *Timestamps) Mixin(s *velox.Schema) {
	s.Column(&t.CreatedAt).DefaultFunc(time.Now)
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
	Items   velox.Many[Item]
	Display string `velox:"-"`
}

// Schema declares Invoice's table, unique key and tenant column.
func (x *Invoice) Schema(s *velox.Schema) {
	s.Table("invoices")
	s.Unique(&x.OrgID, &x.Number)
	s.Tenant(&x.OrgID)
}
