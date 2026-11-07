package migrate

import (
	"fmt"
	"slices"
	"strings"

	"github.com/syssam/velox/internal/ddl"
)

// sqlite renders the changes for SQLite. A table Lint finds rebuilt is
// rebuilt once, at its last change; its other changes write nothing, but
// for its rename, which runs first, its index drops and renames, which
// drop the index at once to free its name for another table's index (the
// rebuild drops the old indexes anyway, and makes the renamed one), and the
// rename of a column another table's foreign key references, which only
// ALTER TABLE carries to that key.
func (r *renderer) sqlite() error {
	last, name := r.sqliteRebuilds()
	r.fkOff = len(last) > 0 || r.dropsCycle()
	for i, c := range r.cs {
		k, ok := r.folds(c)
		if !ok {
			if err := r.sqliteChange(i, c); err != nil {
				return err
			}
			continue
		}
		if err := r.sqliteFolded(i, k, c); err != nil {
			return err
		}
		if last[k] != i {
			continue
		}
		ref := k
		if n, ok := name[k]; ok {
			ref = n
		}
		if err := r.rebuild(i, k, ref); err != nil {
			return err
		}
	}
	return nil
}

// folds returns the name in from of change c's table, and reports whether
// the change folds into that table's rebuild.
func (r *renderer) folds(c Change) (TableRef, bool) {
	switch c.(type) {
	case createTable, dropTable:
		return TableRef{}, false
	}
	k := r.l.canon(c.Target())
	return k, r.l.rebuilt[k]
}

// sqliteRebuilds returns each rebuilt table's last change and its name
// after its rename, by its name in from.
func (r *renderer) sqliteRebuilds() (last map[TableRef]int, name map[TableRef]TableRef) {
	last, name = map[TableRef]int{}, map[TableRef]TableRef{}
	for i, c := range r.cs {
		k, ok := r.folds(c)
		if !ok {
			continue
		}
		last[k] = i
		if rt, ok := c.(renameTable); ok {
			name[k] = rt.To
		}
	}
	return last, name
}

// sqliteFolded writes what change i, of the rebuilt table k, writes before
// the rebuild: its rename, an index drop or rename, or the rename of a
// column another table's key references.
func (r *renderer) sqliteFolded(i int, k TableRef, c Change) error {
	switch c := c.(type) {
	case renameTable:
		return r.sqliteChange(i, c)
	case dropIndex:
		// A UNIQUE constraint's index goes with the old table.
		if !tableUnique(c.Index) {
			return r.sqliteChange(i, c)
		}
	case renameIndex:
		if c.From != "" {
			r.emit("DROP INDEX "+r.indexName(c.Table, c.From), i)
		}
	case renameColumn:
		if r.referenced(k, c.From) {
			return r.sqliteChange(i, c)
		}
	}
	return nil
}

// referenced reports whether a foreign key of another table of from names
// the column col of its table k.
func (r *renderer) referenced(k TableRef, col string) bool {
	return slices.ContainsFunc(r.from.Tables, func(t Table) bool {
		return t.Ref() != k && slices.ContainsFunc(t.ForeignKeys, func(fk ForeignKey) bool {
			return fk.RefTable == k && slices.Contains(fk.RefColumns, col)
		})
	})
}

// dropsCycle reports whether a dropped table is referenced by one dropped
// after it, which only a cycle does: the first drop then runs while a key
// still points at the table.
func (r *renderer) dropsCycle() bool {
	for i, c := range r.cs {
		d, ok := c.(dropTable)
		if !ok {
			continue
		}
		for _, o := range r.cs[i+1:] {
			if e, ok := o.(dropTable); ok && slices.ContainsFunc(e.Table.ForeignKeys, func(fk ForeignKey) bool {
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
	case createTable:
		r.emit(r.sqliteCreate(c.Table.Ref(), &c.Table), i)
		for _, ix := range c.Table.Indexes {
			if !tableUnique(ix) {
				r.emit(r.sqliteIndex(c.Table.Ref(), ix), i)
			}
		}
	case dropTable:
		r.emit("DROP TABLE "+r.table(c.Table.Ref()), i)
	case renameTable:
		if c.From.Schema != c.To.Schema {
			return r.refuse(i, "SQLite moves no table between schemas")
		}
		r.emit("ALTER TABLE "+r.table(c.From)+" RENAME TO "+r.ident(c.To.Name), i)
	case addColumn:
		r.emit("ALTER TABLE "+r.table(c.Table)+" ADD COLUMN "+r.sqliteColumn(c.Column, false), i)
	case dropColumn:
		r.emit("ALTER TABLE "+r.table(c.Table)+" DROP COLUMN "+r.ident(c.Column.Name), i)
	case renameColumn:
		r.emit("ALTER TABLE "+r.table(c.Table)+" RENAME COLUMN "+r.ident(c.From)+" TO "+r.ident(c.To), i)
	case addIndex:
		r.emit(r.sqliteIndex(c.Table, c.Index), i)
	case dropIndex:
		r.emit("DROP INDEX "+r.indexName(c.Table, c.Index.Name), i)
	case renameIndex:
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
	case alterColumn, addForeignKey, dropForeignKey, addCheck, dropCheck, renameConstraint:
		return r.refuse(i, fmt.Sprintf("SQLite makes a %s only by rebuilding table %v, and Lint finds no rebuild", c.Kind(), c.Target()))
	default:
		return r.refuse(i, fmt.Sprintf("SQLite has no %s", c.Kind()))
	}
	return nil
}

// rebuild writes the rebuild of the table from names k and the plan names
// ref, as change i: a new table of to's definition, the rows copied into
// it with their rowids, the AUTOINCREMENT counter kept, the old table dropped,
// the new one renamed, and its indexes made. What the plan takes away of the
// table is added to r.drops, for Up, which compares the table before and after.
func (r *renderer) rebuild(i int, k, ref TableRef) error {
	t, err := r.toTable(i, ref)
	if err != nil {
		return err
	}
	renamed := map[string]string{} // a column's name in from, by its new name
	inPlace := map[string]bool{}   // a new name the table has before the copy
	added := map[string]bool{}
	var folded []int
	for j, c := range r.cs {
		switch c.(type) {
		case createTable, dropTable:
			continue
		}
		if r.l.canon(c.Target()) != k {
			continue
		}
		switch c := c.(type) {
		case renameColumn:
			renamed[c.To] = c.From
			inPlace[c.To] = r.referenced(k, c.From)
		case addColumn:
			added[c.Column.Name] = true
		}
		switch c.(type) {
		case renameTable, dropIndex, renameIndex:
			if r.l.rebuild[j] {
				folded = append(folded, j) // a UNIQUE constraint's index
			}
		default:
			folded = append(folded, j)
		}
		r.dropsOf(c, ref, renamed)
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
		if inPlace[c.Name] {
			s = c.Name
		}
		cols, src = append(cols, r.ident(c.Name)), append(src, r.ident(s))
	}
	tmp := TableRef{Schema: ref.Schema, Name: ddl.Rebuild(ref.Name)}
	r.out = append(r.out, Statement{SQL: r.sqliteCreate(tmp, t), Change: i, Review: r.review(folded, &ref)})
	if id := rowidName(ft, t); id != "" && len(cols) > 0 {
		cols, src = append([]string{id}, cols...), append([]string{id}, src...)
	}
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
		if !tableUnique(ix) {
			r.emit(r.sqliteIndex(ref, ix), i)
		}
	}
	return nil
}

// rowidName is the name that reads the rowid of the rebuild of ft into t, one
// neither has as a column; "" when t's key is its rowid, an INTEGER PRIMARY KEY,
// whose copy carries it, or every name is taken. External-content FTS5, and
// anything else that holds a rowid, depends on it.
func rowidName(ft, t *Table) string {
	if len(t.PrimaryKey) == 1 {
		if c := t.column(t.PrimaryKey[0]); c != nil && strings.EqualFold(c.Type, "integer") {
			return ""
		}
	}
	taken := func(s *Table, name string) bool {
		return s != nil && slices.ContainsFunc(s.Columns, func(c Column) bool { return strings.EqualFold(c.Name, name) })
	}
	for _, name := range [...]string{"rowid", "_rowid_", "oid"} {
		if !taken(ft, name) && !taken(t, name) {
			return name
		}
	}
	return ""
}

// dropsOf adds to r.drops what change c takes away of the table the rebuild
// names ref, under the names Up reads after the rename: renamed holds a column's
// old name by its new one.
func (r *renderer) dropsOf(c Change, ref TableRef, renamed map[string]string) {
	cols := func(names []string) []string {
		out := slices.Clone(names)
		for i, n := range out {
			for to, from := range renamed {
				if from == n {
					out[i] = to
				}
			}
		}
		return out
	}
	switch c := c.(type) {
	case alterColumn:
		col := colTarget(ref, c.To.Name)
		add := func(on bool, attr string) {
			if on {
				r.drops = append(r.drops, col+":"+attr)
			}
		}
		add(c.What&columnType != 0, "type="+c.From.Type)
		add(c.What&columnNullable != 0 && !c.From.Nullable, "not-null")
		add(c.What&columnDefault != 0 && c.From.Default != "", "default="+c.From.Default)
		add(c.What&columnIdentity != 0 && c.From.Identity, "identity")
	case dropForeignKey:
		fk := c.ForeignKey
		fk.Columns = cols(fk.Columns)
		object := keyObject(ref, fk)
		var added []Change
		for _, o := range r.cs {
			if a, ok := o.(addForeignKey); ok && r.l.canon(a.Table) == r.l.canon(c.Table) {
				added = append(added, o)
			}
		}
		to := addedKey(added, fk)
		if to == nil {
			r.drops = append(r.drops, object)
			return
		}
		for _, attr := range keyChanges(*to, fk) {
			r.drops = append(r.drops, object+":"+attr)
		}
	case dropCheck:
		ck := c.Check
		for to, from := range renamed {
			ck.Expr = renameIdent(r.d, ck.Expr, from, to)
		}
		r.drops = append(r.drops, checkObject(ref, ck))
	case dropIndex:
		if tableUnique(c.Index) {
			ix := c.Index
			ix.Columns = cols(ix.Columns)
			r.drops = append(r.drops, indexObject(ref, ix))
		}
	}
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
// unnamed table constraint, and so is an unnamed unique index.
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
		defs = append(defs, r.constraint(fk.Name)+r.foreignKey(fk))
	}
	for _, ck := range t.Checks {
		defs = append(defs, r.constraint(ck.Name)+"CHECK ("+ck.Expr+")")
	}
	for _, ix := range t.Indexes {
		if tableUnique(ix) {
			defs = append(defs, "UNIQUE ("+r.idents(ix.Columns)+")")
		}
	}
	return "CREATE TABLE " + r.table(ref) + " (\n  " + strings.Join(defs, ",\n  ") + "\n)"
}

// constraint is "CONSTRAINT <name> ", or "" for a constraint SQLite keeps
// no name for.
func (r *renderer) constraint(name string) string {
	if name == "" {
		return ""
	}
	return "CONSTRAINT " + r.ident(name) + " "
}

// tableUnique reports whether ix is a UNIQUE constraint's index, unnamed as
// SQLite keeps it: sqliteCreate writes it as UNIQUE (…), no CREATE INDEX
// makes it, and no DROP INDEX drops it, so dropping or renaming it rebuilds
// its table.
func tableUnique(ix Index) bool { return ix.Name == "" && ix.Unique && ix.Where == "" }

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
