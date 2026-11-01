package migrate

import (
	"slices"

	"github.com/syssam/velox"
)

// createTables creates the tables of to that from lacks, sorted, each after
// the created tables it references. In a cycle the first table left is
// created first. On PostgreSQL and MySQL, its keys to a table not created
// yet, and a key to a column whose type changes, are added after the tables;
// a self-reference stays inline, and SQLite keeps every key inline.
func (d *differ) createTables(tables []*Table) {
	pending := map[TableRef]bool{}
	for _, t := range tables {
		pending[t.Ref()] = true
	}
	ready := func(t *Table) bool {
		return !slices.ContainsFunc(t.ForeignKeys, func(fk ForeignKey) bool { return fk.RefTable != t.Ref() && pending[fk.RefTable] })
	}
	for ord := 1; len(tables) > 0; ord++ {
		i := max(slices.IndexFunc(tables, ready), 0)
		t := tables[i]
		tables = slices.Delete(tables, i, i+1)
		ref := t.Ref()
		tb := *t
		tb.ForeignKeys = nil
		for _, fk := range t.ForeignKeys {
			later := fk.RefTable != ref && pending[fk.RefTable]
			if d.to.Dialect != velox.SQLite && (later || d.keyRetyped(ref, fk)) {
				d.add(addForeignKey{Table: ref, ForeignKey: fk}, 7, 2, ref, 0, fk.Name)
				continue
			}
			tb.ForeignKeys = append(tb.ForeignKeys, fk)
		}
		delete(pending, ref)
		d.out = append(d.out, planned{c: createTable{Table: tb}, step: 4, ord: ord, ref: ref})
	}
}

// dropTables drops the tables of from that to lacks, sorted, each before the
// dropped tables it references. In a cycle the first table left is dropped
// first; on PostgreSQL and MySQL the keys of the others to it are dropped
// first of all.
func (d *differ) dropTables(tables []*Table) {
	referenced := func(t *Table) bool {
		for _, r := range tables {
			if r != t && slices.ContainsFunc(r.ForeignKeys, func(fk ForeignKey) bool { return fk.RefTable == t.Ref() }) {
				return true
			}
		}
		return false
	}
	for ord := 1; len(tables) > 0; ord++ {
		i := slices.IndexFunc(tables, func(t *Table) bool { return !referenced(t) })
		if i < 0 {
			i = 0
			if d.to.Dialect != velox.SQLite {
				t := tables[0]
				for _, r := range tables[1:] {
					r.ForeignKeys = slices.DeleteFunc(r.ForeignKeys, func(fk ForeignKey) bool {
						if fk.RefTable != t.Ref() {
							return false
						}
						d.add(dropForeignKey{Table: r.Ref(), ForeignKey: *d.orig.table(r.Ref()).foreignKey(fk.Name)}, 1, 0, r.Ref(), 0, fk.Name)
						return true
					})
				}
			}
		}
		t := tables[i]
		tables = slices.Delete(tables, i, i+1)
		d.out = append(d.out, planned{c: dropTable{Table: *d.orig.table(t.Ref())}, step: 9, ord: ord, ref: t.Ref()})
	}
}
