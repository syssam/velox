package migrate

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/ddl"
)

// renameError is a refusal of a rename.
func renameError(object, format string, args ...any) error {
	return &Error{Object: object, Code: "rename", Reason: fmt.Sprintf(format, args...)}
}

func compareRefs(a, b TableRef) int {
	return cmp.Or(cmp.Compare(a.Schema, b.Schema), cmp.Compare(a.Name, b.Name))
}

// renameTables checks and applies the table renames.
func (d *differ) renameTables(m map[TableRef]TableRef) error {
	srcs := make([]TableRef, 0, len(m))
	for src := range m {
		srcs = append(srcs, src)
	}
	slices.SortFunc(srcs, compareRefs)
	by := map[TableRef]TableRef{} // target → source
	for _, src := range srcs {
		dst := m[src]
		switch _, ok := by[dst]; {
		case d.orig.table(src) == nil:
			return renameError(src.String(), "the table to rename is not in from")
		case d.to.table(dst) == nil:
			return renameError(src.String(), "the table is renamed to %v, which is not in to", dst)
		case d.orig.table(dst) != nil:
			return renameError(src.String(), "the table is renamed to %v, which from already has", dst)
		case ok:
			return renameError(src.String(), "the table is renamed to %v, as %v is", dst, by[dst])
		}
		by[dst] = src
	}
	for _, src := range srcs {
		dst := m[src]
		t := d.mid.table(src)
		t.Schema, t.Name = dst.Schema, dst.Name
		for i := range d.mid.Tables {
			for j := range d.mid.Tables[i].ForeignKeys {
				if fk := &d.mid.Tables[i].ForeignKeys[j]; fk.RefTable == src {
					fk.RefTable = dst
				}
			}
		}
		d.fromRef[dst] = src
		d.add(RenameTable{From: src, To: dst}, 2, 0, src, 0, "")
	}
	return nil
}

// renameColumns checks and applies the column renames; tables maps a table
// of from to its name in to.
func (d *differ) renameColumns(tables map[TableRef]TableRef, m map[ColumnRef]string) error {
	keys := make([]ColumnRef, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b ColumnRef) int {
		return cmp.Or(compareRefs(a.Table, b.Table), cmp.Compare(a.Column, b.Column))
	})
	type target struct {
		ref TableRef
		col string
	}
	by := map[target]string{}
	for _, k := range keys {
		dst, obj := m[k], k.Table.String()+"."+k.Column
		ref := k.Table
		if r, ok := tables[ref]; ok {
			ref = r
		}
		ft, tt := d.orig.table(k.Table), d.to.table(ref)
		switch prev, ok := by[target{ref, dst}]; {
		case ft == nil || ft.column(k.Column) == nil:
			return renameError(obj, "the column to rename is not in from")
		case tt == nil || tt.column(dst) == nil:
			return renameError(obj, "the column is renamed to %s, which %v does not have", dst, ref)
		case ft.column(dst) != nil:
			return renameError(obj, "the column is renamed to %s, which %v already has in from", dst, k.Table)
		case ok:
			return renameError(obj, "the column is renamed to %s, as %s is", dst, prev)
		}
		by[target{ref, dst}] = obj
	}
	for _, k := range keys {
		dst := m[k]
		ref := k.Table
		if r, ok := tables[ref]; ok {
			ref = r
		}
		t := d.mid.table(ref)
		t.column(k.Column).Name = dst
		if d.colFrom[ref] == nil {
			d.colFrom[ref] = map[string]string{}
		}
		d.colFrom[ref][dst] = k.Column
		rename := func(names []string) {
			for i := range names {
				if names[i] == k.Column {
					names[i] = dst
				}
			}
		}
		rename(t.PrimaryKey)
		for i := range t.Indexes {
			rename(t.Indexes[i].Columns)
			t.Indexes[i].Where = renameIdent(d.mid.Dialect, t.Indexes[i].Where, k.Column, dst)
		}
		// MySQL refuses to rename a column a check uses, so there the
		// check keeps its expression: it is dropped before the rename and
		// added after.
		for i := range t.Checks {
			if d.mid.Dialect != velox.MySQL {
				t.Checks[i].Expr = renameIdent(d.mid.Dialect, t.Checks[i].Expr, k.Column, dst)
			}
		}
		for i := range t.ForeignKeys {
			rename(t.ForeignKeys[i].Columns)
		}
		for i := range d.mid.Tables {
			for j := range d.mid.Tables[i].ForeignKeys {
				if fk := &d.mid.Tables[i].ForeignKeys[j]; fk.RefTable == ref {
					rename(fk.RefColumns)
				}
			}
		}
		u := d.to.table(ref)
		d.add(RenameColumn{Table: ref, From: k.Column, To: dst}, 2, 2, ref, slices.IndexFunc(u.Columns, func(c Column) bool { return c.Name == dst }), "")
	}
	return nil
}

// primaryKeys refuses a table whose primary key's columns change, or whose
// primary key gains or loses its name, which no statement can rename.
func (d *differ) primaryKeys() error {
	for i := range d.mid.Tables {
		t := &d.mid.Tables[i]
		u := d.to.table(t.Ref())
		if u == nil {
			continue
		}
		if t.PrimaryKeyName != u.PrimaryKeyName && (t.PrimaryKeyName == "" || u.PrimaryKeyName == "") {
			return &Error{
				Pos: u.Pos, Object: u.Ref().String(), Code: "primary-key",
				Reason: fmt.Sprintf("the primary key's name changes from %q to %q; only a name can be renamed to another", t.PrimaryKeyName, u.PrimaryKeyName),
			}
		}
		if slices.Equal(t.PrimaryKey, u.PrimaryKey) {
			continue
		}
		return &Error{
			Pos: u.Pos, Object: u.Ref().String(), Code: "primary-key",
			Reason: fmt.Sprintf("the primary key changes from (%s) to (%s); write this migration by hand",
				strings.Join(t.PrimaryKey, ", "), strings.Join(u.PrimaryKey, ", ")),
		}
	}
	return nil
}

// enumTypeName is the column type of the PostgreSQL enum type ref.
func enumTypeName(ref TableRef) string { return ddl.EnumType(ref.Schema, ref.Name) }

// usage is the columns of s whose type is the enum type ref, sorted.
func usage(s *Schema, ref TableRef) []string {
	typ := enumTypeName(ref)
	var out []string
	for _, t := range s.Tables {
		for _, c := range t.Columns {
			if c.Type == typ {
				out = append(out, t.Ref().String()+"."+c.Name)
			}
		}
	}
	slices.Sort(out)
	return out
}

// renameEnums pairs an enum type of from that to lacks with one of to that
// from lacks, of the same values and used by the same columns, and renames
// it.
func (d *differ) renameEnums() {
	var fresh []*Enum
	for i := range d.to.Enums {
		if d.mid.enum(d.to.Enums[i].ref()) == nil {
			fresh = append(fresh, &d.to.Enums[i])
		}
	}
	for i := range d.mid.Enums {
		e := &d.mid.Enums[i]
		if d.to.enum(e.ref()) != nil {
			continue
		}
		for j, f := range fresh {
			if f == nil || !slices.Equal(e.Values, f.Values) || !slices.Equal(usage(d.mid, e.ref()), usage(d.to, f.ref())) {
				continue
			}
			from, to := enumTypeName(e.ref()), enumTypeName(f.ref())
			for k := range d.mid.Tables {
				for l := range d.mid.Tables[k].Columns {
					if c := &d.mid.Tables[k].Columns[l]; c.Type == from {
						c.Type = to
					}
				}
			}
			d.add(RenameEnum{From: e.ref(), To: f.ref()}, 2, 5, e.ref(), 0, "")
			e.Schema, e.Name = f.Schema, f.Name
			fresh[j] = nil
			break
		}
	}
}

// renameObjects renames, within each table both have, the primary key, the
// sequences, and the indexes, foreign keys and checks that match one of to
// by definition but not by name; on SQLite such a foreign key takes to's
// name with no change.
func (d *differ) renameObjects() {
	for i := range d.to.Tables {
		u := &d.to.Tables[i]
		t := d.mid.table(u.Ref())
		if t == nil {
			continue
		}
		ref := u.Ref()
		if t.PrimaryKeyName != u.PrimaryKeyName && t.PrimaryKeyName != "" && u.PrimaryKeyName != "" {
			d.add(RenameConstraint{Table: ref, From: t.PrimaryKeyName, To: u.PrimaryKeyName}, 2, 4, ref, 0, t.PrimaryKeyName)
			t.PrimaryKeyName = u.PrimaryKeyName
		}
		for j, uc := range u.Columns {
			tc := t.column(uc.Name)
			if tc != nil && tc.Identity == uc.Identity && tc.Sequence != "" && uc.Sequence != "" && tc.Sequence != uc.Sequence {
				d.add(RenameSequence{Table: ref, Column: uc.Name, From: tc.Sequence, To: uc.Sequence}, 2, 1, ref, j, "")
				tc.Sequence = uc.Sequence
			}
		}
		// What pairs by name, or unnamed by definition, keeps its name; the
		// rest of t is renamed to the first unpaired one of u, named, of its
		// definition.
		tu, ut := pairIndexes(t.Indexes, u.Indexes)
		for j := range t.Indexes {
			ti := &t.Indexes[j]
			if tu[j] >= 0 {
				continue
			}
			for k, ui := range u.Indexes {
				if ut[k] < 0 && ui.Name != "" && sameIndex(*ti, ui) {
					d.add(RenameIndex{Table: ref, From: ti.Name, To: ui.Name, def: indexLabel(Index{Columns: ti.Columns})}, 2, 3, ref, 0, ti.Name)
					ti.Name, ut[k] = ui.Name, j
					break
				}
			}
		}
		tu, ut = pairKeys(t.ForeignKeys, u.ForeignKeys)
		for j := range t.ForeignKeys {
			tf := &t.ForeignKeys[j]
			if tu[j] >= 0 {
				continue
			}
			for k, uf := range u.ForeignKeys {
				// A key dropped around a type change is not renamed.
				if ut[k] < 0 && uf.Name != "" && sameForeignKey(*tf, uf) && !d.keyRetyped(ref, uf) {
					if d.to.Dialect == velox.SQLite {
						// SQLite cannot rename a constraint and never uses
						// its name: a key that differs only in it is no
						// change.
						tf.Name, ut[k] = uf.Name, j
						break
					}
					d.out = append(d.out, planned{c: RenameConstraint{Table: ref, From: tf.Name, To: uf.Name, def: keyLabel(ForeignKey{Columns: tf.Columns})}, step: 2, sub: 4, ref: ref, name: tf.Name, keyRename: true})
					tf.Name, ut[k] = uf.Name, j
					break
				}
			}
		}
		tu, ut = pairChecks(t.Checks, u.Checks)
		for j := range t.Checks {
			tc := &t.Checks[j]
			if tu[j] >= 0 {
				continue
			}
			for k, uc := range u.Checks {
				if ut[k] < 0 && uc.Name != "" && tc.Expr == uc.Expr {
					d.add(RenameConstraint{Table: ref, From: tc.Name, To: uc.Name, def: checkLabel(Check{Expr: tc.Expr})}, 2, 4, ref, 0, tc.Name)
					tc.Name, ut[k] = uc.Name, j
					break
				}
			}
		}
	}
}
