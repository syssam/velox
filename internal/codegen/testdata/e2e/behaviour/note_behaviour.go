//go:build ignore

package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/scope"

	"example.com/tmp/gen"
)

// Org carries the organisation a request acts for.
var Org = scope.NewKey[int64]("org")

// NoteEarly touches Note while the schema package initializes, before package
// runtime binds the schema: the not-linked failure it sees is not kept, and Note
// works once runtime has bound it.
var NoteEarly = velox.CheckEntity[gen.Note]()

// Policy confines notes to the context's organisation.
func (Note) Policy() velox.Policy[gen.Note] { return scope.By[gen.Note](Org, "org_id").Policy() }

// Hooks stamps org_id on inserted notes.
func (Note) Hooks() []velox.Hook[gen.Note] {
	return []velox.Hook[gen.Note]{scope.By[gen.Note](Org, "org_id").Fill()}
}
