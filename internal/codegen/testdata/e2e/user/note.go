package gen

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/scope"
)

// Org carries the organisation a request acts for.
var Org = scope.NewKey[int64]("org")

var noteScope = scope.By(Org, Notes.OrgID.EQ, func(x *Note) *int64 { return &x.OrgID })

// Policy confines notes to the context's organisation.
func (*Note) Policy() velox.Policy[Note] { return noteScope.Policy() }

// Hooks stamps org_id on inserted notes.
func (*Note) Hooks() []velox.Hook[Note] { return []velox.Hook[Note]{noteScope.Fill()} }
