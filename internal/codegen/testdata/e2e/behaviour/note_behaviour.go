//go:build ignore

package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/scope"

	"example.com/tmp/gen/entity"
)

// Org carries the organisation a request acts for.
var Org = scope.NewKey[int64]("org")

// NoteEarly touches Note while the schema package initializes, before gen
// binds the schema: the not-linked failure it sees is not kept, and Note
// works once gen has bound it.
var NoteEarly = velox.CheckEntity[entity.Note]()

var noteScope = scope.By(Org, entity.Notes.OrgID.EQ, func(x *entity.Note) *int64 { return &x.OrgID })

// Policy confines notes to the context's organisation.
func (Note) Policy() velox.Policy[entity.Note] { return noteScope.Policy() }

// Hooks stamps org_id on inserted notes.
func (Note) Hooks() []velox.Hook[entity.Note] { return []velox.Hook[entity.Note]{noteScope.Fill()} }
