package migrate

import (
	"slices"
	"strings"

	"github.com/syssam/velox2/internal/ddl"
)

// createType is the CREATE TYPE of the enum type ref.
func (r *renderer) createType(ref TableRef, values []string) string {
	lits := make([]string, len(values))
	for i, v := range values {
		lits[i] = ddl.Literal(r.d, v)
	}
	return "CREATE TYPE " + r.table(ref) + " AS ENUM (" + strings.Join(lits, ", ") + ")"
}

// addedValues is the values to adds to from, when from's keep their order
// in to; false when they do not, which takes a swap.
func addedValues(from, to []string) ([]string, bool) {
	j := 0
	var added []string
	for _, v := range to {
		if j < len(from) && from[j] == v {
			j++
		} else if !slices.Contains(from, v) {
			added = append(added, v)
		}
	}
	return added, j == len(from)
}

// addValues adds each value of added to the enum type of c in the enum
// script, a unit each, before the next value of to that the type already
// has. The type has its name in from, as the script runs before the main
// one renames it.
func (r *renderer) addValues(i int, c AlterEnum, added []string) {
	ref := c.To.ref()
	if f, ok := r.l.enumFrom[ref]; ok {
		ref = f
	}
	for _, v := range added {
		s := "ALTER TYPE " + r.table(ref) + " ADD VALUE IF NOT EXISTS " + ddl.Literal(r.d, v)
		at := slices.Index(c.To.Values, v)
		for _, w := range c.To.Values[at+1:] {
			if slices.Contains(c.From.Values, w) {
				s += " BEFORE " + ddl.Literal(r.d, w)
				break
			}
		}
		r.enum = append(r.enum, Statement{SQL: s, Change: i, Unit: len(r.enum), Review: r.review([]int{i}, nil)})
	}
}

// swapEnum replaces the enum type e by one of its values: the old type is
// renamed to a helper name, the new one created, every column of to that
// uses the type altered to it, its default dropped and set around the
// change, and the old type dropped. The partial indexes and checks that
// name the type or such a column hold casts to the old type, which
// PostgreSQL cannot rebuild against the new one: they are dropped before
// the columns change and made again after.
func (r *renderer) swapEnum(e Enum) []string {
	ref := e.ref()
	typ := enumTypeName(ref)
	old := TableRef{Schema: ref.Schema, Name: ddl.OldEnum(ref.Name)}
	out := []string{
		"ALTER TYPE " + r.table(ref) + " RENAME TO " + r.ident(old.Name),
		r.createType(ref, e.Values),
	}
	var alters, remake []string
	for _, t := range r.to.Tables {
		tref, tbl := t.Ref(), r.table(t.Ref())
		var cols, actions []string
		for _, c := range t.Columns {
			if c.Type != typ {
				continue
			}
			cols = append(cols, c.Name)
			col := r.ident(c.Name)
			if c.Default != "" {
				actions = append(actions, "ALTER COLUMN "+col+" DROP DEFAULT")
			}
			actions = append(actions, "ALTER COLUMN "+col+" TYPE "+typ+" USING "+col+"::text::"+typ)
			if c.Default != "" {
				actions = append(actions, "ALTER COLUMN "+col+" SET DEFAULT "+c.Default)
			}
		}
		if len(actions) > 0 {
			alters = append(alters, "ALTER TABLE "+tbl+" "+strings.Join(actions, ", "))
		}
		casts := func(expr string) bool {
			return expr != "" && (namesColumn(r.d, expr, ref.Name) || slices.ContainsFunc(cols, func(c string) bool { return namesColumn(r.d, expr, c) }))
		}
		for _, ck := range t.Checks {
			if casts(ck.Expr) && !r.addsOnline(tref, ck.Name) {
				out = append(out, "ALTER TABLE "+tbl+" DROP CONSTRAINT "+r.ident(ck.Name))
				remake = append(remake, "ALTER TABLE "+tbl+" ADD CONSTRAINT "+r.ident(ck.Name)+" CHECK ("+ck.Expr+")")
			}
		}
		var made []string
		for _, ix := range t.Indexes {
			if casts(ix.Where) && !r.addsOnline(tref, ix.Name) {
				out = append(out, "DROP INDEX "+r.indexName(tref, ix.Name))
				made = append(made, r.pgIndex(tref, ix, false))
			}
		}
		// An index the online script drops later still stands here.
		for _, c := range r.cs {
			if d, ok := c.(DropIndex); ok && r.afterRename(d.Table) == tref && casts(d.Index.Where) && r.dropOnline(d) {
				out = append(out, "DROP INDEX "+r.indexName(tref, d.Index.Name))
			}
		}
		remake = append(made, remake...)
	}
	out = append(out, alters...)
	out = append(out, remake...)
	return append(out, "DROP TYPE "+r.table(old))
}

// addsOnline reports whether the online script adds the index or check
// name to the existing table ref, so that it does not stand yet when the
// main script runs.
func (r *renderer) addsOnline(ref TableRef, name string) bool {
	if !r.l.existing(ref) {
		return false
	}
	return slices.ContainsFunc(r.cs, func(c Change) bool {
		switch c := c.(type) {
		case AddIndex:
			return c.Table == ref && c.Index.Name == name
		case AddCheck:
			return c.Table == ref && c.Check.Name == name
		}
		return false
	})
}
