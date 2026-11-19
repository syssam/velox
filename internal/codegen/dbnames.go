package codegen

import (
	"cmp"
	"fmt"
	"hash/fnv"
	"slices"
	"strconv"
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/ddl"
	"github.com/syssam/velox/plugin"
)

// The names of what the graph puts in a database that velox derives. Build's
// rules and Tables derive them here and nowhere else. Those ent derives are
// ent's, uncapped, and each dialect caps them as ent does; the others are
// capped here.

// tableName is the default table of the entity named name: the snake_case
// of its plural, as ent's Type.Table names it. user gives users, Status
// gives status.
func tableName(name string) string { return plugin.Snake(plugin.Plural(name)) }

// pkName is the name PostgreSQL gives table's primary key, which ent leaves
// to it.
func pkName(table string) string { return ddl.PostgresName(table, "", "pkey") }

// seqName is the name PostgreSQL gives the sequence of table's identity
// column, which ent leaves to it.
func seqName(table, column string) string { return ddl.PostgresName(table, column, "seq") }

// indexName is ent's name of an index with no StorageKey (entc/gen/type.go):
// the entity's name in lower case, then the columns, joined by "_". It is
// not capped; each dialect caps it with ddl.Symbol.
func indexName(entity string, cols []string) string {
	return strings.Join(append([]string{strings.ToLower(entity)}, cols...), "_")
}

// partialIndexName is the name of a partial index with no StorageKey whose
// indexName another index of its entity has: indexName's, then "_" and the
// FNV-1a 32 hash of its Where.
func partialIndexName(entity string, cols []string, where string) string {
	h := fnv.New32a()
	h.Write([]byte(where))
	return fmt.Sprintf("%s_%08x", indexName(entity, cols), h.Sum32())
}

// uniqueName is the name of a Unique field's index on PostgreSQL and SQLite,
// as ent names it: PostgreSQL's own name for a UNIQUE column's constraint.
func uniqueName(table, column string) string { return table + "_" + column + "_key" }

// fkIndexName is the name of velox's index of a foreign-key column on
// PostgreSQL and SQLite, where ent makes none.
func fkIndexName(table, column string) string { return ddl.Cap(table + "_" + column) }

// fkName is ent's name of the foreign key of an O2O, O2M or M2O relation
// with no Symbol (entc/gen/graph.go): the table that holds it, the table it
// references and the assoc edge. It is not capped; each dialect caps it with
// ddl.Symbol.
func fkName(table, refTable, edge string) string { return table + "_" + refTable + "_" + edge }

// joinFKName is ent's name of a join table's foreign key with no Symbol: the
// join table and the column. It is not capped, as fkName is not.
func joinFKName(table, column string) string { return table + "_" + column }

// enumType is the PostgreSQL enum type of the enum field f: its
// Postgres SchemaType when that names a type that is not a string type.
// native is false otherwise: an enum is a string column, varchar on
// PostgreSQL, as veloxe and ent stored it (decision 0090).
func enumType(f *Field) (name string, native bool) {
	if st := f.SchemaType[velox.Postgres.String()]; st != "" && !ddl.StringType(velox.Postgres, st) {
		return st, true
	}
	return "", false
}

// symbols is the foreign-key names ed's relation is given: ed's Symbols, else
// its pair's; nil when neither gives any.
func symbols(g *Graph, e *Entity, ed *Edge) []string {
	if len(ed.Symbols) > 0 {
		return ed.Symbols
	}
	if ed.Ref == "" {
		return nil
	}
	t := e
	if ed.Target != e.Name {
		t = g.Entity(ed.Target)
	}
	if t == nil {
		return nil
	}
	if p := t.Edge(ed.Ref); p != nil && len(p.Symbols) > 0 {
		return p.Symbols
	}
	return nil
}

// joinTable is a many-to-many relation's table, once per relation.
type joinTable struct {
	Schema, Table string
	Columns       [2]string // the assoc edge's owner's column, then its target's
	Owner, Target *Entity   // of the assoc edge
	Edge          *Edge     // the assoc edge (the Bidi edge for a self M2M)
	Symbols       []string  // in the order of Columns
}

// joinTables lists the join tables of g, sorted by (Schema, Table).
func joinTables(g *Graph) []joinTable {
	var out []joinTable
	for _, e := range g.Entities {
		for _, ed := range e.Edges {
			if ed.Rel.Kind != "M2M" || ed.Inverse || len(ed.Rel.Columns) != 2 {
				continue
			}
			t := g.Entity(ed.Target)
			if t == nil {
				continue
			}
			out = append(out, joinTable{
				Schema: e.TableSchema, Table: ed.Rel.Table, Columns: [2]string{ed.Rel.Columns[0], ed.Rel.Columns[1]},
				Owner: e, Target: t, Edge: ed, Symbols: symbols(g, e, ed),
			})
		}
	}
	slices.SortStableFunc(out, func(x, y joinTable) int {
		return cmp.Or(strings.Compare(x.Schema, y.Schema), strings.Compare(x.Table, y.Table))
	})
	return out
}

// foreignKey is one foreign key of a relation that holds one, or one of a
// join table's two.
type foreignKey struct {
	Schema, Table, Column string // where the key is
	Name                  string // a Symbol, else fkName or joinFKName, uncapped
	Derived               bool   // Name is derived: each dialect caps it with ddl.Symbol
	Ref                   *Entity
	OnDelete              string  // "", "CASCADE", "SET NULL" or "RESTRICT", migrate's spelling: "" is NO ACTION; a join table's are "CASCADE"
	Owner                 *Entity // the schema that declares the edge, for errors
	Edge                  *Edge
}

// foreignKeys lists the foreign keys of g, sorted by (Schema, Table, Column).
// A relation that holds a key is listed once, by the edge whose owner holds
// it, or by an O2M edge with no inverse.
func foreignKeys(g *Graph) []foreignKey {
	var out []foreignKey
	for _, e := range g.Entities {
		for _, ed := range e.Edges {
			var holder, ref *Entity
			switch {
			case ed.Rel.Kind == "" || ed.Rel.Kind == "M2M" || len(ed.Rel.Columns) == 0:
				continue
			case ownFK(ed):
				holder, ref = e, g.Entity(ed.Target)
			case ed.Rel.Kind == "O2M" && ed.Ref == "":
				holder, ref = g.Entity(ed.Target), e
			}
			if holder == nil || ref == nil {
				continue
			}
			assoc := ed.Name
			if ed.Inverse {
				assoc = ed.Ref
			}
			act := ed.Rel.OnDelete
			if act == "NO ACTION" {
				act = "" // migrate's NO ACTION, which renders no clause
			}
			fk := foreignKey{
				Schema: holder.TableSchema, Table: ed.Rel.Table, Column: ed.Rel.Columns[0],
				Name: fkName(ed.Rel.Table, ref.Table, assoc), Derived: true,
				Ref: ref, OnDelete: act, Owner: e, Edge: ed,
			}
			if s := symbols(g, e, ed); len(s) > 0 {
				fk.Name, fk.Derived = s[0], false
			}
			out = append(out, fk)
		}
	}
	for _, jt := range joinTables(g) {
		for i, ref := range []*Entity{jt.Owner, jt.Target} {
			fk := foreignKey{
				Schema: jt.Schema, Table: jt.Table, Column: jt.Columns[i],
				Name: joinFKName(jt.Table, jt.Columns[i]), Derived: true,
				Ref: ref, OnDelete: "CASCADE", Owner: jt.Owner, Edge: jt.Edge,
			}
			if i < len(jt.Symbols) {
				fk.Name, fk.Derived = jt.Symbols[i], false
			}
			out = append(out, fk)
		}
	}
	slices.SortStableFunc(out, func(x, y foreignKey) int {
		return cmp.Or(strings.Compare(x.Schema, y.Schema), strings.Compare(x.Table, y.Table), strings.Compare(x.Column, y.Column))
	})
	return out
}

// fkIndexed reports whether column leads the primary key or one of the
// non-partial indexes of a table (declared, unique-field, or a join table's
// primary key), so that it needs no foreign-key index.
func fkIndexed(pk []string, indexes [][]string, column string) bool {
	if len(pk) > 0 && pk[0] == column {
		return true
	}
	for _, ix := range indexes {
		if len(ix) > 0 && ix[0] == column {
			return true
		}
	}
	return false
}

// fkIndexNames are the names of velox's foreign-key indexes of e's table on
// PostgreSQL and SQLite, given the foreign keys of the graph: those of the
// keys in the table that neither its primary key, a non-partial index the
// index rules do not refuse, nor a Unique field's index leads.
func fkIndexNames(e *Entity, fks []foreignKey, refused map[*Index]bool) []string {
	var keys [][]string
	for _, ix := range e.Indexes {
		if !refused[ix] && ix.Where == "" {
			keys = append(keys, ix.Columns)
		}
	}
	ixs, _ := fieldIndexes(e, refused, velox.Postgres)
	for _, ix := range ixs {
		keys = append(keys, ix.Columns)
	}
	var out []string
	for _, fk := range fks {
		if fk.Schema == e.TableSchema && fk.Table == e.Table && !fkIndexed([]string{e.Fields[0].Column}, keys, fk.Column) {
			out = append(out, fkIndexName(fk.Table, fk.Column))
		}
	}
	return out
}

// fieldIndexes are the unique indexes of e's Unique fields but the ID on the
// dialect d, each with its field, one per column, named as ent names them:
// uniqueName on PostgreSQL, cut at 63 bytes as PostgreSQL cuts it, and on
// SQLite; the column on MySQL. As ent does, a field's index is left out
// when a declared Unique index not in refused has the name the dialect
// would give it (implicitName); unlike ent, only when that index is full and
// on the field's column alone, so no uniqueness is lost. One of that name
// over other columns is a clash of names, which rule 44 or Tables refuses.
func fieldIndexes(e *Entity, refused map[*Index]bool, d velox.Dialect) ([]*Index, []*Field) {
	var ixs []*Index
	var fs []*Field
	for _, f := range e.Fields[1:] {
		if !f.Unique || slices.ContainsFunc(fs, func(g *Field) bool { return g.Column == f.Column }) {
			continue // a column two fields share is rule 2's
		}
		if slices.ContainsFunc(e.Indexes, func(ix *Index) bool {
			return !refused[ix] && ix.Unique && ix.Where == "" && slices.Equal(ix.Columns, []string{f.Column}) && implicitName(d, ix.Name, e.Table, f.Column)
		}) {
			continue
		}
		name := uniqueName(e.Table, f.Column)
		switch d {
		case velox.Postgres:
			name = ddl.Clip(name, ddl.MaxName) // as PostgreSQL truncates it
		case velox.MySQL:
			name = f.Column
		}
		ixs, fs = append(ixs, &Index{Name: name, Columns: []string{f.Column}, Unique: true}), append(fs, f)
	}
	return ixs, fs
}

// implicitName reports whether name is one the dialect d gives the index of
// a UNIQUE column of table, as ent tells (dialect/sql/schema): on PostgreSQL
// <table>_<column>_key, or that followed by a number above 0; on MySQL
// <column>, or <column>_<n> with n above 1; on SQLite <column>, or
// sqlite_autoindex_<table>_<n> with n above 0, or <table>_<column>_key, the
// name ent and velox give it there.
func implicitName(d velox.Dialect, name, table, column string) bool {
	numbered := func(prefix string, above int64) bool {
		rest, ok := strings.CutPrefix(name, prefix)
		if !ok {
			return false
		}
		n, err := strconv.ParseInt(rest, 10, 64)
		return err == nil && n > above
	}
	switch d {
	case velox.Postgres:
		return name == uniqueName(table, column) || numbered(uniqueName(table, column), 0)
	case velox.MySQL:
		return name == column || numbered(column+"_", 1)
	}
	return name == column || name == uniqueName(table, column) || numbered("sqlite_autoindex_"+table+"_", 0)
}

// object is one name the graph puts in a database, on any dialect.
type object struct {
	space  nameSpace // relations, types or constraints
	schema string    // the database schema (TableSchema)
	name   string
	owner  string // the schema that declares it
	at     loc
	what   string   // as an error names it: "table", "index 0", "enum type of field \"role\""…
	values []string // an enum type's values; nil for other objects
	table  string   // the table it belongs to, or is
	helper bool     // an index's new-index helper, which clashes when the index does
}

// nameSpace is a set of names a database keeps apart from the others'.
type nameSpace uint8

const (
	spaceRelations   nameSpace = iota // PostgreSQL's pg_class: tables, indexes, sequences
	spaceTypes                        // PostgreSQL's pg_type: tables and types
	spaceConstraints                  // foreign keys, per MySQL database
	// spaceTableConstraints holds checks and not-null checks, which
	// PostgreSQL keeps per table (pg_constraint is unique by table and name)
	// and SQLite does not compare. MySQL compares check names per database,
	// which Tables checks on MySQL; not-null checks are PostgreSQL's alone.
	spaceTableConstraints
)

// isTable reports whether o is a table, an entity's or a join table.
func (o *object) isTable() bool { return o.what == "table" || o.what == "join table" }

// tableLoc is where an error about e's table points: no element, and the
// position of its first declared field, else of its first edge.
func tableLoc(e *Entity) loc {
	for _, f := range e.Fields {
		if isDeclared(f) {
			return loc{pos: f.Pos}
		}
	}
	if len(e.Edges) > 0 {
		return loc{pos: e.Edges[0].Pos}
	}
	return loc{}
}

// objects lists every object of g: tables (relations and types), indexes
// (declared, unique-field, foreign-key), primary keys and identity sequences
// (relations), enum types (types), foreign keys and checks (constraints), and
// the helpers velox_new_<table> and <index>_velox_new (relations),
// <type>_velox_old (types) and <table>_<col>_not_null for each NOT NULL column
// (constraints). The indexes
// in refused are left out.
//
// The tables and their rebuild tables come first, then each table's other
// objects, then the foreign keys and their indexes. A column two fields share
// (rule 2) and an enum type fields share give their objects once.
func objects(g *Graph, refused map[*Index]bool) []object {
	jts := distinctJoinTables(g)
	ol := &objectLister{tables: map[tableKey]*tableKeys{}, oldEnums: map[[2]string]bool{}}
	for _, e := range g.Entities {
		at := tableLoc(e)
		ol.out = append(ol.out,
			object{space: spaceRelations, schema: e.TableSchema, name: e.Table, owner: e.Name, at: at, what: "table", table: e.Table},
			object{space: spaceTypes, schema: e.TableSchema, name: e.Table, owner: e.Name, at: at, what: "table", table: e.Table},
			object{space: spaceRelations, schema: e.TableSchema, name: ddl.Rebuild(e.Table), owner: e.Name, at: at, what: "rebuild table", table: e.Table},
		)
	}
	for _, jt := range jts {
		at := edgeLoc(jt.Edge)
		ol.out = append(ol.out,
			object{space: spaceRelations, schema: jt.Schema, name: jt.Table, owner: jt.Owner.Name, at: at, what: "join table", table: jt.Table},
			object{space: spaceTypes, schema: jt.Schema, name: jt.Table, owner: jt.Owner.Name, at: at, what: "join table", table: jt.Table},
			object{space: spaceRelations, schema: jt.Schema, name: ddl.Rebuild(jt.Table), owner: jt.Owner.Name, at: at, what: "rebuild table of join table " + jt.Table, table: jt.Table},
		)
	}
	for _, e := range g.Entities {
		ol.entity(e, refused)
	}
	for _, jt := range jts {
		ol.joinTable(jt)
	}
	ol.foreignKeyObjects(g)
	return ol.out
}

// distinctJoinTables returns the join tables of g, each edge's once.
func distinctJoinTables(g *Graph) []joinTable {
	var jts []joinTable
	for _, jt := range joinTables(g) {
		if !slices.ContainsFunc(jts, func(j joinTable) bool { return j.Owner == jt.Owner && j.Edge.Name == jt.Edge.Name }) {
			jts = append(jts, jt) // an edge declared twice is rule 3's
		}
	}
	return jts
}

// tableKey is a table, by database schema and name.
type tableKey struct{ schema, table string }

// tableKeys is the leading columns of one table's keys.
type tableKeys struct {
	pk      []string
	indexes [][]string
}

// objectLister collects the objects that objects returns.
type objectLister struct {
	out      []object
	tables   map[tableKey]*tableKeys // the leading columns of each table's keys, for fkIndexed
	oldEnums map[[2]string]bool
}

// add appends an object of e's table.
func (ol *objectLister) add(e *Entity, space nameSpace, name, what string, at loc) {
	ol.out = append(ol.out, object{space: space, schema: e.TableSchema, name: name, owner: e.Name, at: at, what: what, table: e.Table})
}

// addHelper appends the new-index helper of the index of e's table.
func (ol *objectLister) addHelper(e *Entity, index, what string, at loc) {
	ol.out = append(ol.out, object{space: spaceRelations, schema: e.TableSchema, name: ddl.NewIndex(index), owner: e.Name, at: at, what: "new-index helper of " + what, table: e.Table, helper: true})
}

// entity appends the objects of e's table other than the table itself, and
// records the leading columns of its keys.
func (ol *objectLister) entity(e *Entity, refused map[*Index]bool) {
	id := e.Fields[0]
	k := &tableKeys{pk: []string{id.Column}}
	ol.tables[tableKey{e.TableSchema, e.Table}] = k
	ol.add(e, spaceRelations, pkName(e.Table), "primary key", tableLoc(e))
	if integer(id.Type) {
		ol.add(e, spaceRelations, seqName(e.Table, id.Column), "identity sequence", fieldLoc(id))
	}
	ol.indexes(e, refused, k)
	seen := map[string]bool{}
	for _, f := range e.Fields {
		if seen[f.Column] {
			continue // rules 1 and 2
		}
		seen[f.Column] = true
		ol.column(e, f)
	}
	// A bad annotation is declaredChecks's error.
	declared, _ := checksOf(e)
	for i, c := range declared { // sorted; a repeated name is declaredChecks's error
		if i == 0 || declared[i-1].Name != c.Name {
			ol.add(e, spaceTableConstraints, c.Name, "check of table "+e.Table, tableLoc(e))
		}
	}
}

// indexes appends e's declared and unique-field indexes, but those in
// refused, with their helpers, and adds their columns to k.
func (ol *objectLister) indexes(e *Entity, refused map[*Index]bool, k *tableKeys) {
	for i, ix := range e.Indexes {
		if refused[ix] {
			continue
		}
		ol.add(e, spaceRelations, ix.Name, indexAt(i), indexLoc(i, ix))
		ol.addHelper(e, ix.Name, indexAt(i), indexLoc(i, ix))
		if ix.Where == "" {
			k.indexes = append(k.indexes, ix.Columns)
		}
	}
	ixs, fs := fieldIndexes(e, refused, velox.Postgres)
	for i, ix := range ixs {
		ol.add(e, spaceRelations, ix.Name, "unique index of "+fieldAt(fs[i].Name), fieldLoc(fs[i]))
		ol.addHelper(e, ix.Name, "the unique index of "+fieldAt(fs[i].Name), fieldLoc(fs[i]))
		k.indexes = append(k.indexes, ix.Columns)
	}
}

// column appends the objects of f's column: an enum's type, old type and
// check, and a NOT NULL column's not-null check.
func (ol *objectLister) column(e *Entity, f *Field) {
	of := fieldAt(f.Name)
	if f.Type == "enum" {
		if name, native := enumType(f); native {
			values := make([]string, len(f.Enums))
			for i, v := range f.Enums {
				values[i] = v.Value
			}
			ol.out = append(ol.out, object{space: spaceTypes, schema: e.TableSchema, name: name, owner: e.Name, at: fieldLoc(f), what: "enum type of " + of, values: values, table: e.Table})
			if ok := [2]string{strings.ToLower(e.TableSchema), strings.ToLower(name)}; !ol.oldEnums[ok] {
				ol.oldEnums[ok] = true
				ol.add(e, spaceTypes, ddl.OldEnum(name), "old enum type of "+of, fieldLoc(f))
			}
		}
	}
	if !columnNullable(f) {
		ol.add(e, spaceTableConstraints, ddl.NotNull(e.Table, f.Column), "not-null check of "+of, fieldLoc(f))
	}
}

// joinTable appends the primary key and not-null checks of jt, and records
// its key's columns.
func (ol *objectLister) joinTable(jt joinTable) {
	ol.tables[tableKey{jt.Schema, jt.Table}] = &tableKeys{pk: jt.Columns[:]}
	at := edgeLoc(jt.Edge)
	add := func(space nameSpace, name, what string) {
		ol.out = append(ol.out, object{space: space, schema: jt.Schema, name: name, owner: jt.Owner.Name, at: at, what: what + " of join table " + jt.Table, table: jt.Table})
	}
	add(spaceRelations, pkName(jt.Table), "primary key")
	for i, c := range jt.Columns {
		if i == 0 || c != jt.Columns[0] { // rule 35
			add(spaceTableConstraints, ddl.NotNull(jt.Table, c), "not-null check of column "+c)
		}
	}
}

// foreignKeyObjects appends the foreign keys of g, and the index and helper of
// each one no key of its table leads with.
func (ol *objectLister) foreignKeyObjects(g *Graph) {
	seen := map[[3]string]bool{}
	for _, fk := range foreignKeys(g) {
		c := [3]string{fk.Schema, fk.Table, fk.Column}
		if seen[c] {
			continue // rule 35: a join table's two columns are one
		}
		seen[c] = true
		at, of := edgeLoc(fk.Edge), edgeAt(fk.Edge.Name)
		obj := object{schema: fk.Schema, owner: fk.Owner.Name, at: at, table: fk.Table}
		if k := ol.tables[tableKey{fk.Schema, fk.Table}]; k == nil || !fkIndexed(k.pk, k.indexes, fk.Column) {
			ix := obj
			ix.space, ix.name, ix.what = spaceRelations, fkIndexName(fk.Table, fk.Column), "foreign-key index of "+of
			helper := ix
			helper.name, helper.what, helper.helper = ddl.NewIndex(ix.name), "new-index helper of the foreign-key index of "+of, true
			ol.out = append(ol.out, ix, helper)
		}
		obj.space, obj.name, obj.what = spaceConstraints, fk.Name, "foreign key of "+of
		ol.out = append(ol.out, obj)
	}
}
