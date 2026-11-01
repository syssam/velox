package migrate

import (
	"fmt"
	"slices"
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/ddl"
)

// The steps of Diff's order whose MySQL actions on one table join one
// ALTER TABLE when their changes are consecutive.
const (
	phaseDrop = iota + 1
	phaseRename
	phaseColumn
	phaseDropColumn
	phaseAdd
)

// alter is an ALTER TABLE being built from consecutive changes.
type alter struct {
	table   string
	phase   int
	actions []string
	changes []int
}

// mysql renders the changes for MySQL.
func (r *renderer) mysql() error {
	at, absorbed := r.mysqlIndexes()
	keys, drop, add, err := r.mysqlKeyRenames()
	if err != nil {
		return err
	}
	var pending *alter
	flush := func() {
		if pending != nil {
			r.emit("ALTER TABLE "+pending.table+" "+strings.Join(pending.actions, ", "), pending.changes...)
			pending = nil
		}
	}
	for i := 0; i <= len(r.cs); i++ {
		for _, k := range keys {
			switch i {
			case drop:
				flush()
				r.emit("ALTER TABLE "+r.table(r.l.canon(k.c.Table))+" DROP FOREIGN KEY "+r.ident(k.c.From), k.i)
			case add:
				flush()
				r.emit("ALTER TABLE "+r.table(k.c.Table)+" ADD CONSTRAINT "+r.ident(k.c.To)+" "+r.foreignKey(k.fk), k.i)
			}
		}
		if i == len(r.cs) {
			break
		}
		if g, ok := at[i]; ok {
			flush()
			r.emitAt(g.sql(r), i, g.changes)
			continue
		}
		if absorbed[i] || slices.ContainsFunc(keys, func(k keyRename) bool { return k.i == i }) {
			continue
		}
		table, phase, action, stmts, err := r.mysqlChange(i, r.cs[i])
		if err != nil {
			return err
		}
		if action != "" {
			if pending == nil || pending.table != table || pending.phase != phase {
				flush()
				pending = &alter{table: table, phase: phase}
			}
			pending.actions = append(pending.actions, action)
			pending.changes = append(pending.changes, i)
			continue
		}
		flush()
		for _, s := range stmts {
			r.emit(s, i)
		}
	}
	flush()
	return nil
}

// keyRename is a foreign key's renameConstraint, change i.
type keyRename struct {
	i  int
	c  renameConstraint
	fk ForeignKey // as to has it
}

// mysqlKeyRenames finds the renames of foreign keys. MySQL renames no
// constraint, and a foreign key's drop and add in one ALTER TABLE would
// need it to build in place, which a checked add does not; so the key is
// dropped, under from's names, where the drops before the renames end
// (place drop), and added after the other adds, where the index drops
// after them or the table drops begin (place add).
func (r *renderer) mysqlKeyRenames() (keys []keyRename, drop, add int, err error) {
	for i, c := range r.cs {
		rc, ok := c.(renameConstraint)
		if !ok {
			continue
		}
		if r.primaryKeyName(rc) {
			return nil, 0, 0, &Error{Object: rc.Target().String(), Code: "render",
				Reason: fmt.Sprintf("change %d: MySQL's primary key is always named PRIMARY; it cannot be renamed", i)}
		}
		t, err := r.toTable(i, rc.Table)
		if err != nil {
			return nil, 0, 0, err
		}
		if fk := t.foreignKey(rc.To); fk != nil {
			keys = append(keys, keyRename{i: i, c: rc, fk: *fk})
		}
	}
	if len(keys) == 0 {
		return nil, -1, -1, nil
	}
	drop = slices.IndexFunc(r.cs, func(c Change) bool {
		switch c.(type) {
		case dropForeignKey, dropCheck, dropIndex:
			return false
		}
		return true
	})
	last := keys[len(keys)-1].i
	add = len(r.cs)
	for j := last + 1; j < len(r.cs); j++ {
		switch r.cs[j].(type) {
		case dropIndex, dropTable:
			add = j
		}
		if add < len(r.cs) {
			break
		}
	}
	return keys, drop, add, nil
}

// primaryKeyName reports whether rc renames a primary key: from or to
// names PRIMARY, or the primary key's name of its table in from or to.
func (r *renderer) primaryKeyName(rc renameConstraint) bool {
	pk := func(t *Table) bool {
		return t != nil && t.PrimaryKeyName != "" && (rc.From == t.PrimaryKeyName || rc.To == t.PrimaryKeyName)
	}
	return rc.From == "PRIMARY" || rc.To == "PRIMARY" ||
		pk(r.to.table(rc.Table)) || pk(r.from.table(rc.Table)) || pk(r.from.table(r.l.canon(rc.Table)))
}

// mysqlChange is change i as an action of an ALTER TABLE on table, in
// phase, or as whole statements.
func (r *renderer) mysqlChange(i int, c Change) (table string, phase int, action string, stmts []string, err error) {
	switch c := c.(type) {
	case createTable:
		return "", 0, "", []string{r.mysqlCreate(&c.Table)}, nil
	case dropTable:
		return "", 0, "", []string{"DROP TABLE " + r.table(c.Table.Ref())}, nil
	case renameTable:
		return "", 0, "", []string{"RENAME TABLE " + r.table(c.From) + " TO " + r.table(c.To)}, nil
	case addColumn:
		return r.table(c.Table), phaseColumn, "ADD COLUMN " + r.mysqlColumn(c.Column), nil, nil
	case dropColumn:
		return r.table(c.Table), phaseDropColumn, "DROP COLUMN " + r.ident(c.Column.Name), nil, nil
	case renameColumn:
		return r.table(c.Table), phaseRename, "RENAME COLUMN " + r.ident(c.From) + " TO " + r.ident(c.To), nil, nil
	case alterColumn:
		return r.table(c.Table), phaseColumn, r.mysqlAlterColumn(c), nil, nil
	case renameIndex:
		return r.table(c.Table), phaseRename, "RENAME INDEX " + r.ident(c.From) + " TO " + r.ident(c.To), nil, nil
	case addForeignKey:
		return r.table(c.Table), phaseAdd, "ADD CONSTRAINT " + r.ident(c.ForeignKey.Name) + " " + r.foreignKey(c.ForeignKey), nil, nil
	case dropForeignKey:
		return r.table(c.Table), phaseDrop, "DROP FOREIGN KEY " + r.ident(c.ForeignKey.Name), nil, nil
	case addCheck:
		return r.table(c.Table), phaseAdd, "ADD CONSTRAINT " + r.ident(c.Check.Name) + " CHECK (" + c.Check.Expr + ")", nil, nil
	case dropCheck:
		return r.table(c.Table), phaseDrop, "DROP CHECK " + r.ident(c.Check.Name), nil, nil
	case renameConstraint:
		// MySQL renames no constraint: the old check is dropped and the
		// new one added. A foreign key's is mysqlKeyRenames'.
		t, err := r.toTable(i, c.Table)
		if err != nil {
			return "", 0, "", nil, err
		}
		if ck := t.check(c.To); ck != nil {
			return r.table(c.Table), phaseRename, "DROP CHECK " + r.ident(c.From) + ", ADD CONSTRAINT " + r.ident(c.To) + " CHECK (" + ck.Expr + ")", nil, nil
		}
		return "", 0, "", nil, r.refuse(i, fmt.Sprintf("table %v of to has no check %s", c.Table, c.To))
	case setTableComment:
		return r.table(c.Table), phaseColumn, "COMMENT = " + ddl.Literal(velox.MySQL, c.To), nil, nil
	case setTableCollation:
		if !strings.HasPrefix(c.To, "utf8mb4_") {
			return "", 0, "", nil, &Error{Object: c.Table.String(), Code: "render",
				Reason: fmt.Sprintf("the collation %q is not one of utf8mb4, the character set the plan converts to, so CONVERT TO would fail every time the file ran; use a utf8mb4 collation, or convert the table by hand and write a baseline", c.To)}
		}
		return r.table(c.Table), phaseColumn, "CONVERT TO CHARACTER SET utf8mb4 COLLATE " + c.To, nil, nil
	}
	return "", 0, "", nil, r.refuse(i, fmt.Sprintf("MySQL has no %s", c.Kind()))
}

// mysqlAlterColumn is the action that alters a column: a default alone
// changes in place when it is a literal or an expression in parentheses,
// and anything else rewrites the whole definition, as MySQL requires.
func (r *renderer) mysqlAlterColumn(c alterColumn) string {
	if c.What == columnDefault {
		switch def := c.To.Default; {
		case def == "":
			return "ALTER COLUMN " + r.ident(c.To.Name) + " DROP DEFAULT"
		case literalDefault(velox.MySQL, def) || strings.HasPrefix(def, "("):
			return "ALTER COLUMN " + r.ident(c.To.Name) + " SET DEFAULT " + def
		}
	}
	return "MODIFY COLUMN " + r.mysqlColumn(c.To)
}

// mysqlColumn is c's definition: `c` <type> NULL|NOT NULL [AUTO_INCREMENT]
// [DEFAULT x] [COMMENT '…'].
func (r *renderer) mysqlColumn(c Column) string {
	s := r.ident(c.Name) + " " + c.Type
	if c.Nullable {
		s += " NULL"
	} else {
		s += " NOT NULL"
	}
	if c.Identity {
		s += " AUTO_INCREMENT"
	}
	if c.Default != "" {
		s += " DEFAULT " + c.Default
	}
	if c.Comment != "" {
		s += " COMMENT " + ddl.Literal(velox.MySQL, c.Comment)
	}
	return s
}

// mysqlCreate is the CREATE TABLE of t, with its indexes, foreign keys and
// checks.
func (r *renderer) mysqlCreate(t *Table) string {
	var defs []string
	for _, c := range t.Columns {
		defs = append(defs, r.mysqlColumn(c))
	}
	if len(t.PrimaryKey) > 0 {
		defs = append(defs, "PRIMARY KEY ("+r.idents(t.PrimaryKey)+")")
	}
	for _, ix := range t.Indexes {
		defs = append(defs, r.mysqlIndex(ix))
	}
	for _, fk := range t.ForeignKeys {
		defs = append(defs, "CONSTRAINT "+r.ident(fk.Name)+" "+r.foreignKey(fk))
	}
	for _, ck := range t.Checks {
		defs = append(defs, "CONSTRAINT "+r.ident(ck.Name)+" CHECK ("+ck.Expr+")")
	}
	collation := t.Collation
	if collation == "" {
		collation = "utf8mb4_bin"
	}
	charset, _, _ := strings.Cut(collation, "_") // a collation names its character set
	s := "CREATE TABLE " + r.table(t.Ref()) + " (\n  " + strings.Join(defs, ",\n  ") +
		"\n) ENGINE=InnoDB DEFAULT CHARSET=" + charset + " COLLATE=" + collation
	if t.Comment != "" {
		s += " COMMENT=" + ddl.Literal(velox.MySQL, t.Comment)
	}
	return s
}

// mysqlIndex is ix as written in CREATE TABLE and after ADD.
func (r *renderer) mysqlIndex(ix Index) string {
	s := "INDEX " + r.ident(ix.Name) + " (" + r.idents(ix.Columns) + ")"
	if ix.Unique {
		s = "UNIQUE " + s
	}
	return s
}

// indexGroup is the index changes of one table that one ALTER TABLE makes
// in place.
type indexGroup struct {
	table   TableRef // by its name when the statement runs
	changes []int
	adds    []Index
	drops   []string // the dropped indexes whose names no add takes
	reused  []string // the dropped indexes whose names an add takes
}

func (g *indexGroup) sql(r *renderer) string {
	var actions []string
	for _, n := range g.reused {
		actions = append(actions, "DROP INDEX "+r.ident(n))
	}
	for _, ix := range g.adds {
		actions = append(actions, "ADD "+r.mysqlIndex(ix))
	}
	for _, n := range g.drops {
		actions = append(actions, "DROP INDEX "+r.ident(n))
	}
	return "ALTER TABLE " + r.table(g.table) + " " + strings.Join(actions, ", ") + ", ALGORITHM=INPLACE, LOCK=NONE"
}

// mysqlIndexes groups each table's index adds and drops into one statement,
// at the place of its first add, or of its first drop when it adds none. A
// drop whose name an add takes joins the add. A drop before the drop of one
// of its columns stays where it is, alone: MySQL drops or shrinks an index
// with its column, so the drop could not come later. at holds each group by
// its place; absorbed are the other changes the groups make.
func (r *renderer) mysqlIndexes() (at map[int]*indexGroup, absorbed map[int]bool) {
	dropsColumn := map[TableRef]map[string]int{} // a dropped column's change, by table and column
	for i, c := range r.cs {
		if dc, ok := c.(dropColumn); ok {
			k := r.l.canon(dc.Table)
			if dropsColumn[k] == nil {
				dropsColumn[k] = map[string]int{}
			}
			dropsColumn[k][dc.Column.Name] = i
		}
	}
	pinned := func(i int, d dropIndex) bool {
		return slices.ContainsFunc(d.Index.Columns, func(col string) bool {
			j, ok := dropsColumn[r.l.canon(d.Table)][col]
			return ok && j > i
		})
	}
	at, absorbed = map[int]*indexGroup{}, map[int]bool{}
	type member struct {
		i  int
		ix Index
		to TableRef
		up bool // an add
	}
	var order []TableRef
	members := map[TableRef][]member{}
	for i, c := range r.cs {
		switch c := c.(type) {
		case addIndex:
			k := r.l.canon(c.Table)
			if members[k] == nil {
				order = append(order, k)
			}
			members[k] = append(members[k], member{i: i, ix: c.Index, to: c.Table, up: true})
		case dropIndex:
			if pinned(i, c) {
				at[i] = &indexGroup{table: c.Table, changes: []int{i}, drops: []string{c.Index.Name}}
				continue
			}
			k := r.l.canon(c.Table)
			if members[k] == nil {
				order = append(order, k)
			}
			members[k] = append(members[k], member{i: i, ix: c.Index, to: c.Table})
		}
	}
	for _, k := range order {
		ms := members[k]
		added := map[string]bool{}
		for _, m := range ms {
			if m.up {
				added[m.ix.Name] = true
			}
		}
		g := &indexGroup{}
		pos := -1
		for _, m := range ms {
			g.changes = append(g.changes, m.i)
			switch {
			case m.up:
				g.adds = append(g.adds, m.ix)
			case added[m.ix.Name]:
				g.reused = append(g.reused, m.ix.Name)
				absorbed[m.i] = true
				continue
			default:
				g.drops = append(g.drops, m.ix.Name)
			}
			if pos < 0 {
				pos, g.table = m.i, m.to
			} else {
				absorbed[m.i] = true
			}
		}
		at[pos] = g
	}
	return at, absorbed
}
