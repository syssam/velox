package codegen

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/syssam/velox/internal/codegen/loadjson"
)

// The soft-delete mixin, by its type, and the column it adds.
const (
	softDeletePkg    = "github.com/syssam/velox/schema/mixin/softdelete"
	softDeleteMixin  = "Mixin"
	softDeleteColumn = "deleted_at"
)

// SoftDeleteWarnings returns a warning for each unique key over every row of
// an entity that lists softdelete.Mixin: a Unique field, a one-to-one edge's
// key, or a unique index with no Where. Its delete only sets deleted_at, so
// a deleted row keeps its value and a new row with the same one fails. An
// index that covers deleted_at is left alone. Each warning is
// "<pos>: schema X: <element>: reason", as an Error prints.
func SoftDeleteWarnings(g *Graph) []string {
	var out []string
	for _, e := range g.Entities {
		if !slices.ContainsFunc(e.Mixins, func(m loadjson.Mixin) bool {
			return m.PkgPath == softDeletePkg && m.Name == softDeleteMixin
		}) {
			continue
		}
		for _, f := range e.Fields[1:] {
			if f.Unique {
				out = append(out, softDeleteFieldWarning(e, f).Error())
			}
		}
		for i, ix := range e.Indexes {
			if ix.Unique && ix.Where == "" && !slices.Contains(ix.Columns, softDeleteColumn) {
				out = append(out, (&Error{Pos: ix.Pos, Schema: e.Name, Where: indexAt(i),
					Reason: "a unique index over every row, deleted ones included: softdelete.Mixin's delete keeps the row, so its values block a new row. " +
						`On PostgreSQL and SQLite, add .Where("deleted_at IS NULL") to cover the live rows alone; on MySQL, which has no partial index, delete under softdelete.Skip(ctx) to remove the row for good, or check uniqueness in a hook`}).Error())
			}
		}
	}
	return out
}

// softDeleteFieldWarning is the warning of f, a Unique field of e, which
// lists softdelete.Mixin.
func softDeleteFieldWarning(e *Entity, f *Field) *Error {
	w := &Error{Pos: f.Pos, Schema: e.Name, Where: fieldAt(f.Name)}
	edge := f.Implicit
	if edge == "" {
		if i := slices.IndexFunc(e.Edges, func(ed *Edge) bool { return ed.Field == f.Name }); i >= 0 {
			edge = e.Edges[i].Name
		}
	}
	if edge != "" {
		if w.Pos == "" {
			if i := slices.IndexFunc(e.Edges, func(ed *Edge) bool { return ed.Name == edge }); i >= 0 {
				w.Pos = e.Edges[i].Pos
			}
		}
		w.Reason = fmt.Sprintf("the key of one-to-one edge %q is unique over every row, deleted ones included: softdelete.Mixin's delete keeps the row, so linking the same row again fails. "+
			"Delete under softdelete.Skip(ctx) to remove the row for good, or restore the deleted row instead of inserting one", edge)
		return w
	}
	w.Reason = "a Unique field is unique over every row, deleted ones included: softdelete.Mixin's delete keeps the row, so its value blocks a new row. " +
		"On PostgreSQL and SQLite, drop Unique and declare index.Fields(" + strconv.Quote(f.Name) + `).Unique().Where("deleted_at IS NULL"); on MySQL, which has no partial index, delete under softdelete.Skip(ctx) to remove the row for good, or check uniqueness in a hook`
	return w
}
