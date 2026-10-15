// Package testmodel holds entities for velox's own tests. They are on the
// Desc path: each *_desc.go file is written by hand in the shape velox-gen
// will generate (schema plan 3).
package testmodel

import (
	"time"

	"github.com/syssam/velox2"
)

// Timestamps is embedded in Invoice: its fields are columns. Invoice's Desc
// sets CreatedAt when an Invoice is inserted, and UpdatedAt on every insert
// and update.
type Timestamps struct {
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Invoice is the entity velox's tests query. Its OrgID and Number are
// unique together, and OrgID is its tenant.
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
