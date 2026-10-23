package migrate

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/syssam/velox2"
)

// Diff returns the changes that turn from, the snapshot, into to, the desired
// schema, in the order they run. Both must be of one dialect.
//
// Tables match by name, after opts.RenameTables, and columns by name within a
// table, after opts.RenameColumns; nothing guesses a table or column rename.
// An index, foreign key, check or enum type matches by name, else by its
// definition, which makes it a rename: so a table rename carries the names
// derived from the table's. A column whose type is a renamed enum type is not
// altered.
//
// The order is: drop the foreign keys, checks and indexes that must go first;
// rename tables, sequences, columns, indexes, constraints and enum types;
// create enum types and add their values; create tables, a referenced table
// first; add, alter and comment columns and tables; drop columns; add
// indexes, then checks, then foreign keys; drop the other indexes; drop
// tables, children first, and swap or drop enum types. Within a step the
// changes are sorted by table, then by object name or column position.
//
// Each change names tables and objects as they are when it runs: the drops
// before the renames use from's names, and the changes after them to's. An
// object dropped before the renames is not renamed. A column rename carries
// the checks and partial indexes that name the column.
//
// Its errors are *Error values: either schema is nil ("nil"), has a dialect
// it does not know or the dialects differ ("dialect"), either schema lists a
// table, an enum type, or a table's column, index or constraint twice
// ("schema"), a primary key's columns change ("primary-key"), or a rename
// names a table or column from does not have, or one to does not have or
// from already has ("rename").
// Diff does not change from or to.
func Diff(from, to *Schema, opts DiffOptions) ([]Change, error) {
	if from == nil || to == nil {
		return nil, &Error{Code: "nil", Reason: "Diff needs two schemas"}
	}
	if err := from.validate("from"); err != nil {
		return nil, err
	}
	if err := to.validate("to"); err != nil {
		return nil, err
	}
	if from.Dialect != to.Dialect {
		return nil, &Error{Code: "dialect", Reason: fmt.Sprintf("from is a %s schema and to a %s one; Diff compares two schemas of one dialect", from.Dialect, to.Dialect)}
	}
	d := &differ{orig: from.clone(), mid: from.clone(), to: to.clone(), fromRef: map[TableRef]TableRef{}}
	for _, s := range [...]*Schema{d.orig, d.mid, d.to} {
		s.sort()
	}
	if err := d.renameTables(opts.RenameTables); err != nil {
		return nil, err
	}
	if err := d.renameColumns(opts.RenameTables, opts.RenameColumns); err != nil {
		return nil, err
	}
	d.mid.sort()
	if err := d.primaryKeys(); err != nil {
		return nil, err
	}
	d.renameEnums()
	d.findRetyped()
	d.renameObjects()
	d.mid.sort()
	d.enums()
	d.tables()
	if d.to.Dialect == velox.MySQL {
		d.keepKeysIndexed()
	}
	return d.changes(), nil
}

// differ holds the state of one Diff. mid is from with each rename applied
// as it is found, so that after the renames it matches to by name.
type differ struct {
	orig, mid, to *Schema
	fromRef       map[TableRef]TableRef        // a renamed table's name in from, by its name in to
	retyped       map[TableRef]map[string]bool // the columns whose type changes, by table
	out           []planned
}

// planned is a change and its place in the order: its step, its rank within
// the step, the order of a created or dropped table, then its table, column
// position and object name. keyRename marks a foreign key's rename.
type planned struct {
	c         Change
	step, sub int
	ord       int
	ref       TableRef
	pos       int
	name      string
	keyRename bool
}

func (d *differ) add(c Change, step, sub int, ref TableRef, pos int, name string) {
	d.out = append(d.out, planned{c: c, step: step, sub: sub, ref: ref, pos: pos, name: name})
}

func (d *differ) changes() []Change {
	slices.SortStableFunc(d.out, func(a, b planned) int {
		return cmp.Or(cmp.Compare(a.step, b.step), cmp.Compare(a.sub, b.sub), cmp.Compare(a.ord, b.ord),
			cmp.Compare(a.ref.Schema, b.ref.Schema), cmp.Compare(a.ref.Name, b.ref.Name),
			cmp.Compare(a.pos, b.pos), cmp.Compare(a.name, b.name))
	})
	out := make([]Change, len(d.out))
	for i, p := range d.out {
		out[i] = p.c
	}
	return out
}

// enums creates, alters and drops the enum types, renames applied.
func (d *differ) enums() {
	for _, u := range d.to.Enums {
		t := d.mid.enum(u.ref())
		switch {
		case t == nil:
			d.add(CreateEnum{Enum: u}, 3, 0, u.ref(), 0, "")
		case !slices.Equal(t.Values, u.Values):
			d.alterEnum(*t, u)
		}
	}
	for _, t := range d.mid.Enums {
		if d.to.enum(t.ref()) == nil {
			d.add(DropEnum{Enum: t}, 9, 2, t.ref(), 0, "")
		}
	}
}

// alterEnum changes from's values to to's. Added values that keep the
// others' order are one change; otherwise the values to adds are appended
// first, and the swap to to's order runs after the columns are dropped.
func (d *differ) alterEnum(from, to Enum) {
	j := 0
	for _, v := range to.Values {
		if j < len(from.Values) && from.Values[j] == v {
			j++
		}
	}
	if j == len(from.Values) {
		d.add(AlterEnum{From: from, To: to}, 3, 1, to.ref(), 0, "")
		return
	}
	mid := from
	mid.Values = slices.Clone(from.Values)
	for _, v := range to.Values {
		if !slices.Contains(from.Values, v) {
			mid.Values = append(mid.Values, v)
		}
	}
	if len(mid.Values) > len(from.Values) {
		d.add(AlterEnum{From: from, To: mid}, 3, 1, to.ref(), 0, "")
	}
	d.add(AlterEnum{From: mid, To: to}, 9, 1, to.ref(), 0, "")
}

// tables creates, alters and drops the tables, renames applied.
func (d *differ) tables() {
	var created, dropped []*Table
	for i := range d.to.Tables {
		if u := &d.to.Tables[i]; d.mid.table(u.Ref()) == nil {
			created = append(created, u)
		}
	}
	for i := range d.mid.Tables {
		if t := &d.mid.Tables[i]; d.to.table(t.Ref()) == nil {
			dropped = append(dropped, t)
			d.freeNames(t)
		}
	}
	for i := range d.to.Tables {
		if t := d.mid.table(d.to.Tables[i].Ref()); t != nil {
			d.alterTable(t, &d.to.Tables[i])
		}
	}
	d.createTables(created)
	d.dropTables(dropped)
}

// keepKeysIndexed, on MySQL, drops in step 1 a foreign key whose every
// index step 1 drops, as MySQL refuses to drop a key's last index (error
// 1553), and adds it back in step 7, after the indexes. A key renamed in
// step 2 is dropped under its from name instead.
func (d *differ) keepKeysIndexed() {
	for i := range d.to.Tables {
		u := &d.to.Tables[i]
		t := d.mid.table(u.Ref())
		if t == nil {
			continue
		}
		ref, oref := u.Ref(), u.Ref()
		if r, ok := d.fromRef[ref]; ok {
			oref = r
		}
		dropped, keys := map[string]bool{}, map[string]bool{}
		for _, p := range d.out {
			switch c := p.c.(type) {
			case DropIndex:
				if p.step == 1 && c.Table == oref {
					dropped[c.Index.Name] = true
				}
			case DropForeignKey:
				if p.step == 1 && c.Table == oref {
					keys[c.ForeignKey.Name] = true
				}
			}
		}
		for _, fk := range t.ForeignKeys {
			name := fk.Name
			rename := d.keyRename(ref, fk.Name)
			if rename != nil {
				name = rename.c.(RenameConstraint).From
			}
			leads := func(cols []string) bool {
				return len(cols) >= len(fk.Columns) && slices.Equal(cols[:len(fk.Columns)], fk.Columns)
			}
			if keys[name] || leads(t.PrimaryKey) || slices.ContainsFunc(t.Indexes, func(ix Index) bool {
				return !dropped[ix.Name] && leads(ix.Columns)
			}) {
				continue
			}
			if rename != nil {
				rename.c = nil
			}
			d.add(DropForeignKey{Table: oref, ForeignKey: *d.orig.table(oref).foreignKey(name)}, 1, 0, oref, 0, name)
			if uf := u.foreignKey(fk.Name); uf != nil {
				d.add(AddForeignKey{Table: ref, ForeignKey: *uf}, 7, 2, ref, 0, uf.Name)
			}
		}
	}
	d.out = slices.DeleteFunc(d.out, func(p planned) bool { return p.c == nil })
}

// keyRename is the planned rename of table ref's foreign key to name, or
// nil.
func (d *differ) keyRename(ref TableRef, name string) *planned {
	for i := range d.out {
		if rc, ok := d.out[i].c.(RenameConstraint); ok && d.out[i].keyRename && rc.Table == ref && rc.To == name {
			return &d.out[i]
		}
	}
	return nil
}

// findRetyped finds the columns of the tables both have whose type changes,
// renames applied.
func (d *differ) findRetyped() {
	d.retyped = map[TableRef]map[string]bool{}
	for i := range d.to.Tables {
		u := &d.to.Tables[i]
		t := d.mid.table(u.Ref())
		if t == nil {
			continue
		}
		for _, uc := range u.Columns {
			if tc := t.column(uc.Name); tc != nil && tc.Type != uc.Type {
				if d.retyped[u.Ref()] == nil {
					d.retyped[u.Ref()] = map[string]bool{}
				}
				d.retyped[u.Ref()][uc.Name] = true
			}
		}
	}
}

// freeNames drops first the objects of t, a table dropped at the end, whose
// names to gives another object in t's database schema: indexes on
// PostgreSQL and SQLite, and foreign keys and checks on PostgreSQL and
// MySQL, where the names are per schema.
func (d *differ) freeNames(t *Table) {
	ref := t.Ref()
	if d.to.Dialect != velox.MySQL {
		for _, ix := range t.Indexes {
			if d.nameTaken(ref, ix.Name) {
				d.add(DropIndex{Table: ref, Index: ix}, 1, 2, ref, 0, ix.Name)
			}
		}
	}
	if d.to.Dialect == velox.SQLite {
		return
	}
	t.ForeignKeys = slices.DeleteFunc(t.ForeignKeys, func(fk ForeignKey) bool {
		if !d.toHas(ref.Schema, func(u *Table) bool { return u.foreignKey(fk.Name) != nil }) {
			return false
		}
		d.add(DropForeignKey{Table: ref, ForeignKey: *d.orig.table(ref).foreignKey(fk.Name)}, 1, 0, ref, 0, fk.Name)
		return true
	})
	for _, ck := range t.Checks {
		if d.toHas(ref.Schema, func(u *Table) bool { return u.check(ck.Name) != nil }) {
			d.add(DropCheck{Table: ref, Check: ck}, 1, 1, ref, 0, ck.Name)
		}
	}
}

// toHas reports whether a table of to in the database schema schema has
// what has looks for.
func (d *differ) toHas(schema string, has func(*Table) bool) bool {
	for i := range d.to.Tables {
		if u := &d.to.Tables[i]; u.Schema == schema && has(u) {
			return true
		}
	}
	return false
}

// alterTable changes t, a table of from with the renames applied, into u.
// The drops of step 1 run before the renames, so they name the table and the
// object as from does.
func (d *differ) alterTable(t, u *Table) {
	ref := u.Ref()
	oref := ref
	if r, ok := d.fromRef[ref]; ok {
		oref = r
	}
	// ot holds from's names for the drops of step 1. An object renamed by
	// definition is never dropped in step 1, so every object those drops
	// name has its from name in mid, and ot has it.
	ot := d.orig.table(oref)
	gone := map[string]bool{}
	for i, uc := range u.Columns {
		tc := t.column(uc.Name)
		if tc == nil {
			d.add(AddColumn{Table: ref, Column: uc}, 5, 0, ref, i, "")
			continue
		}
		if w := columnChange(*tc, uc); w != 0 {
			d.add(AlterColumn{Table: ref, From: *tc, To: uc, What: w}, 5, 1, ref, i, "")
		}
	}
	for i, tc := range t.Columns {
		if u.column(tc.Name) == nil {
			gone[tc.Name] = true
			d.add(DropColumn{Table: ref, Column: tc}, 6, 0, ref, i, "")
		}
	}
	if t.Comment != u.Comment {
		d.add(SetTableComment{Table: ref, From: t.Comment, To: u.Comment}, 5, 2, ref, 0, "")
	}
	if t.Collation != u.Collation {
		d.add(SetTableCollation{Table: ref, From: t.Collation, To: u.Collation}, 5, 3, ref, 0, "")
	}

	for _, ui := range u.Indexes {
		ti := t.index(ui.Name)
		if ti != nil && !sameIndex(*ti, ui) {
			d.add(DropIndex{Table: oref, Index: *ot.index(ti.Name)}, 1, 2, oref, 0, ti.Name)
		}
		if ti == nil || !sameIndex(*ti, ui) {
			d.add(AddIndex{Table: ref, Index: ui}, 7, 0, ref, 0, ui.Name)
		}
	}
	for _, ti := range t.Indexes {
		if u.index(ti.Name) != nil {
			continue
		}
		// An index on a dropped column goes before it, and one whose name
		// another table's index takes goes before that one is made; the
		// others go after the adds, so a replacement exists first. MySQL
		// drops an index with its last column, and shrinks one that keeps
		// others, which then goes after the adds.
		// A unique one it would keep unique over the others, so that goes
		// first too.
		onGone := slices.ContainsFunc(ti.Columns, func(c string) bool { return gone[c] })
		if d.to.Dialect == velox.MySQL && !ti.Unique {
			onGone = !slices.ContainsFunc(ti.Columns, func(c string) bool { return !gone[c] })
		}
		if onGone || d.nameTaken(ref, ti.Name) {
			d.add(DropIndex{Table: oref, Index: *ot.index(ti.Name)}, 1, 2, oref, 0, ti.Name)
		} else {
			d.add(DropIndex{Table: ref, Index: ti}, 8, 0, ref, 0, ti.Name)
		}
	}

	for _, uf := range u.ForeignKeys {
		tf := t.foreignKey(uf.Name)
		if tf != nil && (!sameForeignKey(*tf, uf) || d.keyRetyped(ref, uf)) {
			d.add(DropForeignKey{Table: oref, ForeignKey: *ot.foreignKey(tf.Name)}, 1, 0, oref, 0, tf.Name)
			tf = nil
		}
		if tf == nil {
			d.add(AddForeignKey{Table: ref, ForeignKey: uf}, 7, 2, ref, 0, uf.Name)
		}
	}
	for _, tf := range t.ForeignKeys {
		if u.foreignKey(tf.Name) == nil {
			d.add(DropForeignKey{Table: oref, ForeignKey: *ot.foreignKey(tf.Name)}, 1, 0, oref, 0, tf.Name)
		}
	}

	for _, uc := range u.Checks {
		tc := t.check(uc.Name)
		if tc != nil && tc.Expr != uc.Expr {
			d.add(DropCheck{Table: oref, Check: *ot.check(tc.Name)}, 1, 1, oref, 0, tc.Name)
		}
		if tc == nil || tc.Expr != uc.Expr {
			d.add(AddCheck{Table: ref, Check: uc}, 7, 1, ref, 0, uc.Name)
		}
	}
	for _, tc := range t.Checks {
		if u.check(tc.Name) == nil {
			d.add(DropCheck{Table: oref, Check: *ot.check(tc.Name)}, 1, 1, oref, 0, tc.Name)
		}
	}
}

// columnChange is what differs between a and b.
func columnChange(a, b Column) ColumnChange {
	var w ColumnChange
	if a.Type != b.Type {
		w |= ColumnType
	}
	if a.Nullable != b.Nullable {
		w |= ColumnNullable
	}
	if a.Default != b.Default {
		w |= ColumnDefault
	}
	if a.Identity != b.Identity || a.Sequence != b.Sequence {
		w |= ColumnIdentity
	}
	if a.Comment != b.Comment {
		w |= ColumnComment
	}
	return w
}

// indexNameTaken reports whether to gives name, in database schema sch, to
// a table, a sequence, a primary key, or an index of a table other than the
// index's own, which own reports by its name in to: the names an index
// shares on PostgreSQL and SQLite. MySQL names indexes per table. Such an
// index is dropped in the main script's first step, to free the name.
func indexNameTaken(to *Schema, sch, name string, own func(TableRef) bool) bool {
	if to.Dialect == velox.MySQL {
		return false
	}
	for i := range to.Tables {
		u := &to.Tables[i]
		if u.Schema != sch {
			continue
		}
		if u.Name == name || u.PrimaryKeyName == name || !own(u.Ref()) && u.index(name) != nil ||
			slices.ContainsFunc(u.Columns, func(c Column) bool { return c.Sequence == name }) {
			return true
		}
	}
	return false
}

// nameTaken is indexNameTaken for an index of the table ref.
func (d *differ) nameTaken(ref TableRef, name string) bool {
	return indexNameTaken(d.to, ref.Schema, name, func(u TableRef) bool { return u == ref })
}

// keyRetyped reports whether a column of fk, of table ref, or one it
// references changes type: the key is dropped around the change.
func (d *differ) keyRetyped(ref TableRef, fk ForeignKey) bool {
	return slices.ContainsFunc(fk.Columns, func(c string) bool { return d.retyped[ref][c] }) ||
		slices.ContainsFunc(fk.RefColumns, func(c string) bool { return d.retyped[fk.RefTable][c] })
}

func sameIndex(a, b Index) bool {
	return a.Unique == b.Unique && a.Where == b.Where && slices.Equal(a.Columns, b.Columns)
}

func sameForeignKey(a, b ForeignKey) bool {
	return a.RefTable == b.RefTable && a.OnDelete == b.OnDelete && slices.Equal(a.Columns, b.Columns) && slices.Equal(a.RefColumns, b.RefColumns)
}

// table is the table ref of s, to change.
func (s *Schema) table(ref TableRef) *Table {
	for i := range s.Tables {
		if t := &s.Tables[i]; t.Schema == ref.Schema && t.Name == ref.Name {
			return t
		}
	}
	return nil
}

func (s *Schema) enum(ref TableRef) *Enum {
	for i := range s.Enums {
		if e := &s.Enums[i]; e.Schema == ref.Schema && e.Name == ref.Name {
			return e
		}
	}
	return nil
}

func (t *Table) column(name string) *Column {
	for i := range t.Columns {
		if t.Columns[i].Name == name {
			return &t.Columns[i]
		}
	}
	return nil
}

func (t *Table) index(name string) *Index {
	for i := range t.Indexes {
		if t.Indexes[i].Name == name {
			return &t.Indexes[i]
		}
	}
	return nil
}

func (t *Table) foreignKey(name string) *ForeignKey {
	for i := range t.ForeignKeys {
		if t.ForeignKeys[i].Name == name {
			return &t.ForeignKeys[i]
		}
	}
	return nil
}

func (t *Table) check(name string) *Check {
	for i := range t.Checks {
		if t.Checks[i].Name == name {
			return &t.Checks[i]
		}
	}
	return nil
}
