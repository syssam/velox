package migrate

import (
	"fmt"
	"slices"
	"strings"

	"github.com/syssam/velox2/internal/ddl"
)

// sqlite renders the changes for SQLite. A table Lint finds rebuilt is
// rebuilt once, at its last change; its other changes write nothing, but
// for its rename, which runs first, and its index drops, which free their
// names at once (the rebuild drops the old indexes anyway).
func (r *renderer) sqlite() error {
	last := map[TableRef]int{}      // a rebuilt table's last change, by its name in from
	name := map[TableRef]TableRef{} // a rebuilt table's name after its rename
	folds := func(c Change) (TableRef, bool) {
		switch c.(type) {
		case CreateTable, DropTable:
			return TableRef{}, false
		}
		k := r.l.canon(c.Target())
		return k, r.l.rebuilt[k]
	}
	for i, c := range r.cs {
		k, ok := folds(c)
		if !ok {
			continue
		}
		last[k] = i
		if rt, ok := c.(RenameTable); ok {
			name[k] = rt.To
		}
	}
	r.fkOff = len(last) > 0 || r.dropsCycle()
	for i, c := range r.cs {
		k, ok := folds(c)
		if !ok {
			if err := r.sqliteChange(i, c); err != nil {
				return err
			}
			continue
		}
		switch c.(type) {
		case RenameTable, DropIndex:
			if err := r.sqliteChange(i, c); err != nil {
				return err
			}
		}
		if last[k] == i {
			ref := k
			if n, ok := name[k]; ok {
				ref = n
			}
			if err := r.rebuild(i, k, ref); err != nil {
				return err
			}
		}
	}
	return nil
}

// dropsCycle reports whether a dropped table is referenced by one dropped
// after it, which only a cycle does: the first drop then runs while a key
// still points at the table.
func (r *renderer) dropsCycle() bool {
	for i, c := range r.cs {
		d, ok := c.(DropTable)
		if !ok {
			continue
		}
		for _, o := range r.cs[i+1:] {
			if e, ok := o.(DropTable); ok && slices.ContainsFunc(e.Table.ForeignKeys, func(fk ForeignKey) bool {
				return fk.RefTable == d.Table.Ref() && e.Table.Ref() != d.Table.Ref()
			}) {
				return true
			}
		}
	}
	return false
}

// sqliteChange writes change i, of a table that is not rebuilt.
func (r *renderer) sqliteChange(i int, c Change) error {
	switch c := c.(type) {
	case CreateTable:
		r.emit(r.sqliteCreate(c.Table.Ref(), &c.Table), i)
		for _, ix := range c.Table.Indexes {
			r.emit(r.sqliteIndex(c.Table.Ref(), ix), i)
		}
	case DropTable:
		r.emit("DROP TABLE "+r.table(c.Table.Ref()), i)
	case RenameTable:
		if c.From.Schema != c.To.Schema {
			return r.refuse(i, "SQLite moves no table between schemas")
		}
		r.emit("ALTER TABLE "+r.table(c.From)+" RENAME TO "+r.ident(c.To.Name), i)
	case AddColumn:
		r.emit("ALTER TABLE "+r.table(c.Table)+" ADD COLUMN "+r.sqliteColumn(c.Column, false), i)
	case DropColumn:
		r.emit("ALTER TABLE "+r.table(c.Table)+" DROP COLUMN "+r.ident(c.Column.Name), i)
	case RenameColumn:
		r.emit("ALTER TABLE "+r.table(c.Table)+" RENAME COLUMN "+r.ident(c.From)+" TO "+r.ident(c.To), i)
	case AddIndex:
		r.emit(r.sqliteIndex(c.Table, c.Index), i)
	case DropIndex:
		r.emit("DROP INDEX "+r.indexName(c.Table, c.Index.Name), i)
	case RenameIndex:
		// SQLite renames no index: it is dropped and made again.
		t, err := r.toTable(i, c.Table)
		if err != nil {
			return err
		}
		ix := t.index(c.To)
		if ix == nil {
			return r.refuse(i, fmt.Sprintf("table %v of to has no index %s", c.Table, c.To))
		}
		r.emit("DROP INDEX "+r.indexName(c.Table, c.From), i)
		r.emit(r.sqliteIndex(c.Table, *ix), i)
	case AlterColumn, AddForeignKey, DropForeignKey, AddCheck, DropCheck, RenameConstraint:
		return r.refuse(i, fmt.Sprintf("SQLite makes a %s only by rebuilding table %v, and Lint finds no rebuild", c.Kind(), c.Target()))
	default:
		return r.refuse(i, fmt.Sprintf("SQLite has no %s", c.Kind()))
	}
	return nil
}

// rebuild writes the rebuild of the table from names k and the plan names
// ref, as change i: a new table of to's definition, the rows copied into
// it, the AUTOINCREMENT counter kept, the old table dropped, the new one
// renamed, and its indexes made.
func (r *renderer) rebuild(i int, k, ref TableRef) error {
	t, err := r.toTable(i, ref)
	if err != nil {
		return err
	}
	renamed := map[string]string{} // a column's name in from, by its new name
	added := map[string]bool{}
	var folded []int
	for j, c := range r.cs {
		switch c.(type) {
		case CreateTable, DropTable:
			continue
		}
		if r.l.canon(c.Target()) != k {
			continue
		}
		switch c := c.(type) {
		case RenameColumn:
			renamed[c.To] = c.From
		case AddColumn:
			added[c.Column.Name] = true
		}
		switch c.(type) {
		case RenameTable, DropIndex:
		default:
			folded = append(folded, j)
		}
	}
	ft := r.from.table(k)
	var cols, src []string
	for _, c := range t.Columns {
		s := c.Name
		if f, ok := renamed[s]; ok {
			s = f
		}
		if added[c.Name] || ft == nil || ft.column(s) == nil {
			continue
		}
		cols, src = append(cols, r.ident(c.Name)), append(src, r.ident(s))
	}
	tmp := TableRef{Schema: ref.Schema, Name: ddl.Rebuild(ref.Name)}
	r.emitAt(r.sqliteCreate(tmp, t), i, folded)
	if len(cols) > 0 {
		r.emit(fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM %s", r.table(tmp), strings.Join(cols, ", "), strings.Join(src, ", "), r.table(ref)), i)
	}
	if autoIncrement(t) {
		lit := func(s string) string { return ddl.Literal(r.d, s) }
		r.emit("DELETE FROM sqlite_sequence WHERE name = "+lit(tmp.Name), i)
		r.emit("INSERT INTO sqlite_sequence (name, seq) SELECT "+lit(tmp.Name)+", seq FROM sqlite_sequence WHERE name = "+lit(ref.Name), i)
	}
	r.emit("DROP TABLE "+r.table(ref), i)
	r.emit("ALTER TABLE "+r.table(tmp)+" RENAME TO "+r.ident(ref.Name), i)
	for _, ix := range t.Indexes {
		r.emit(r.sqliteIndex(ref, ix), i)
	}
	return nil
}

// autoIncrement reports whether t's key is one identity column, which
// SQLite makes an INTEGER PRIMARY KEY AUTOINCREMENT.
func autoIncrement(t *Table) bool {
	if len(t.PrimaryKey) != 1 {
		return false
	}
	c := t.column(t.PrimaryKey[0])
	return c != nil && c.Identity
}

// sqliteColumn is c's definition: "c" <type> [NOT NULL] [PRIMARY KEY
// AUTOINCREMENT] [DEFAULT x]; key says c is the table's AUTOINCREMENT key.
func (r *renderer) sqliteColumn(c Column, key bool) string {
	s := r.ident(c.Name) + " " + c.Type
	if !c.Nullable {
		s += " NOT NULL"
	}
	if key {
		s += " PRIMARY KEY AUTOINCREMENT"
	}
	if c.Default != "" {
		s += " DEFAULT " + c.Default
	}
	return s
}

// sqliteCreate is the CREATE TABLE of t under the name ref, with its
// foreign keys and checks; a key other than one identity column is an
// unnamed table constraint.
func (r *renderer) sqliteCreate(ref TableRef, t *Table) string {
	auto := autoIncrement(t)
	var defs []string
	for _, c := range t.Columns {
		defs = append(defs, r.sqliteColumn(c, auto && c.Name == t.PrimaryKey[0]))
	}
	if !auto && len(t.PrimaryKey) > 0 {
		defs = append(defs, "PRIMARY KEY ("+r.idents(t.PrimaryKey)+")")
	}
	for _, fk := range t.ForeignKeys {
		defs = append(defs, "CONSTRAINT "+r.ident(fk.Name)+" "+r.foreignKey(fk))
	}
	for _, ck := range t.Checks {
		defs = append(defs, "CONSTRAINT "+r.ident(ck.Name)+" CHECK ("+ck.Expr+")")
	}
	return "CREATE TABLE " + r.table(ref) + " (\n  " + strings.Join(defs, ",\n  ") + "\n)"
}

// indexName is the index name of table, with the table's schema.
func (r *renderer) indexName(table TableRef, name string) string {
	return r.table(TableRef{Schema: table.Schema, Name: name})
}

// sqliteIndex is the CREATE INDEX of ix on table.
func (r *renderer) sqliteIndex(table TableRef, ix Index) string {
	s := "CREATE INDEX "
	if ix.Unique {
		s = "CREATE UNIQUE INDEX "
	}
	s += r.indexName(table, ix.Name) + " ON " + r.ident(table.Name) + " (" + r.idents(ix.Columns) + ")"
	if ix.Where != "" {
		s += " WHERE " + ix.Where
	}
	return s
}
