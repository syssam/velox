package migrate

import (
	"fmt"
	"strings"
)

// A Change is one step of a migration. A caller tells them apart by Kind, and
// reads what each acts on through Target and String.
type Change interface {
	Kind() Kind
	// Target is the object it acts on, by its name when the change runs: a
	// table, or an enum type's ref for the enum changes.
	Target() TableRef
	// String is the change in one line: its kind and the object it acts
	// on, as in "add-column users.email" or "rename-table users → accounts".
	String() string
	isChange()
}

// A Kind names a change in text and in file names: "create-table",
// "drop-table", "rename-table", "add-column", "drop-column", "rename-column",
// "alter-column", "rename-sequence", "add-index", "drop-index",
// "rename-index", "add-foreign-key", "drop-foreign-key", "add-check",
// "drop-check", "rename-constraint", "set-table-comment",
// "set-table-collation", "create-enum", "rename-enum", "alter-enum" and
// "drop-enum".
type Kind string

// A columnMask is the set of what an alterColumn changes.
type columnMask uint8

// The columnMask bits, one per part of a column an alterColumn changes.
const (
	columnType columnMask = 1 << iota
	columnNullable
	columnDefault
	columnIdentity // Identity, and Sequence with it
	columnComment
)

// String is the names of the set bits, joined by "|" in the order
// "type|nullable|default|identity|comment"; "0" for none.
func (c columnMask) String() string {
	if c == 0 {
		return "0"
	}
	var parts []string
	for i, name := range [...]string{"type", "nullable", "default", "identity", "comment"} {
		if bit := columnMask(1) << i; c&bit != 0 {
			parts = append(parts, name)
			c &^= bit
		}
	}
	if c != 0 {
		parts = append(parts, fmt.Sprintf("%#x", uint8(c)))
	}
	return strings.Join(parts, "|")
}

// createTable creates a table, with its indexes, checks and foreign keys.
type createTable struct{ Table Table }

// dropTable drops a table; Table is as in from.
type dropTable struct{ Table Table }

// renameTable renames a table.
type renameTable struct{ From, To TableRef }

// addColumn adds a column.
type addColumn struct {
	Table  TableRef
	Column Column
}

// dropColumn drops a column.
type dropColumn struct {
	Table  TableRef
	Column Column
}

// renameColumn renames a column.
type renameColumn struct {
	Table    TableRef
	From, To string
}

// alterColumn changes what What names of a column, From, to To's.
type alterColumn struct {
	Table    TableRef
	From, To Column
	What     columnMask
}

// renameSequence renames the PostgreSQL sequence a column owns.
type renameSequence struct {
	Table            TableRef
	Column, From, To string
}

// addIndex adds an index.
type addIndex struct {
	Table TableRef
	Index Index
}

// dropIndex drops an index.
type dropIndex struct {
	Table TableRef
	Index Index
}

// renameIndex renames an index.
type renameIndex struct {
	Table    TableRef
	From, To string
	def      string // the label of an unnamed source, From "", by its definition
}

// addForeignKey adds a foreign key.
type addForeignKey struct {
	Table      TableRef
	ForeignKey ForeignKey
}

// dropForeignKey drops a foreign key.
type dropForeignKey struct {
	Table      TableRef
	ForeignKey ForeignKey
}

// addCheck adds a check.
type addCheck struct {
	Table TableRef
	Check Check
}

// dropCheck drops a check.
type dropCheck struct {
	Table TableRef
	Check Check
}

// renameConstraint renames a primary key, a foreign key or a check.
type renameConstraint struct {
	Table    TableRef
	From, To string
	def      string // the label of an unnamed source, From "", by its definition
}

// setTableComment changes a table's comment.
type setTableComment struct {
	Table    TableRef
	From, To string
}

// setTableCollation changes a MySQL table's collation.
type setTableCollation struct {
	Table    TableRef
	From, To string
}

// createEnum creates a PostgreSQL enum type.
type createEnum struct{ Enum Enum }

// dropEnum drops a PostgreSQL enum type.
type dropEnum struct{ Enum Enum }

// renameEnum renames a PostgreSQL enum type; the refs are the type's schema
// and name.
type renameEnum struct{ From, To TableRef }

// alterEnum changes a PostgreSQL enum type's values: it adds values, or
// reorders or removes them.
type alterEnum struct{ From, To Enum }

// DiffOptions are the renames Diff cannot see.
type DiffOptions struct {
	RenameTables  map[TableRef]TableRef // a table of from → its name in to
	RenameColumns map[ColumnRef]string  // a column of from (its table by from's name) → its name in to
}

func (e *Enum) ref() TableRef { return TableRef{Schema: e.Schema, Name: e.Name} }

// Kind is "create-table".
func (createTable) Kind() Kind { return "create-table" }

// Kind is "drop-table".
func (dropTable) Kind() Kind { return "drop-table" }

// Kind is "rename-table".
func (renameTable) Kind() Kind { return "rename-table" }

// Kind is "add-column".
func (addColumn) Kind() Kind { return "add-column" }

// Kind is "drop-column".
func (dropColumn) Kind() Kind { return "drop-column" }

// Kind is "rename-column".
func (renameColumn) Kind() Kind { return "rename-column" }

// Kind is "alter-column".
func (alterColumn) Kind() Kind { return "alter-column" }

// Kind is "rename-sequence".
func (renameSequence) Kind() Kind { return "rename-sequence" }

// Kind is "add-index".
func (addIndex) Kind() Kind { return "add-index" }

// Kind is "drop-index".
func (dropIndex) Kind() Kind { return "drop-index" }

// Kind is "rename-index".
func (renameIndex) Kind() Kind { return "rename-index" }

// Kind is "add-foreign-key".
func (addForeignKey) Kind() Kind { return "add-foreign-key" }

// Kind is "drop-foreign-key".
func (dropForeignKey) Kind() Kind { return "drop-foreign-key" }

// Kind is "add-check".
func (addCheck) Kind() Kind { return "add-check" }

// Kind is "drop-check".
func (dropCheck) Kind() Kind { return "drop-check" }

// Kind is "rename-constraint".
func (renameConstraint) Kind() Kind { return "rename-constraint" }

// Kind is "set-table-comment".
func (setTableComment) Kind() Kind { return "set-table-comment" }

// Kind is "set-table-collation".
func (setTableCollation) Kind() Kind { return "set-table-collation" }

// Kind is "create-enum".
func (createEnum) Kind() Kind { return "create-enum" }

// Kind is "drop-enum".
func (dropEnum) Kind() Kind { return "drop-enum" }

// Kind is "rename-enum".
func (renameEnum) Kind() Kind { return "rename-enum" }

// Kind is "alter-enum".
func (alterEnum) Kind() Kind { return "alter-enum" }

// Target is the table it creates.
func (c createTable) Target() TableRef { return c.Table.Ref() }

// Target is the table it drops.
func (c dropTable) Target() TableRef { return c.Table.Ref() }

// Target is the table's new name.
func (c renameTable) Target() TableRef { return c.To }

// Target is the table it adds the column to.
func (c addColumn) Target() TableRef { return c.Table }

// Target is the table it drops the column from.
func (c dropColumn) Target() TableRef { return c.Table }

// Target is the column's table.
func (c renameColumn) Target() TableRef { return c.Table }

// Target is the column's table.
func (c alterColumn) Target() TableRef { return c.Table }

// Target is the column's table.
func (c renameSequence) Target() TableRef { return c.Table }

// Target is the index's table.
func (c addIndex) Target() TableRef { return c.Table }

// Target is the index's table.
func (c dropIndex) Target() TableRef { return c.Table }

// Target is the index's table.
func (c renameIndex) Target() TableRef { return c.Table }

// Target is the foreign key's table.
func (c addForeignKey) Target() TableRef { return c.Table }

// Target is the foreign key's table.
func (c dropForeignKey) Target() TableRef { return c.Table }

// Target is the check's table.
func (c addCheck) Target() TableRef { return c.Table }

// Target is the check's table.
func (c dropCheck) Target() TableRef { return c.Table }

// Target is the constraint's table.
func (c renameConstraint) Target() TableRef { return c.Table }

// Target is the table whose comment it sets.
func (c setTableComment) Target() TableRef { return c.Table }

// Target is the table whose collation it sets.
func (c setTableCollation) Target() TableRef { return c.Table }

// Target is the enum type's ref.
func (c createEnum) Target() TableRef { return c.Enum.ref() }

// Target is the enum type's ref.
func (c dropEnum) Target() TableRef { return c.Enum.ref() }

// Target is the enum type's new ref.
func (c renameEnum) Target() TableRef { return c.To }

// Target is the enum type's ref.
func (c alterEnum) Target() TableRef { return c.To.ref() }

func (createTable) isChange()       {}
func (dropTable) isChange()         {}
func (renameTable) isChange()       {}
func (addColumn) isChange()         {}
func (dropColumn) isChange()        {}
func (renameColumn) isChange()      {}
func (alterColumn) isChange()       {}
func (renameSequence) isChange()    {}
func (addIndex) isChange()          {}
func (dropIndex) isChange()         {}
func (renameIndex) isChange()       {}
func (addForeignKey) isChange()     {}
func (dropForeignKey) isChange()    {}
func (addCheck) isChange()          {}
func (dropCheck) isChange()         {}
func (renameConstraint) isChange()  {}
func (setTableComment) isChange()   {}
func (setTableCollation) isChange() {}
func (createEnum) isChange()        {}
func (dropEnum) isChange()          {}
func (renameEnum) isChange()        {}
func (alterEnum) isChange()         {}

// String is "<kind> <object>": the object is the table, "<table>.<column>",
// "<table>.<index or constraint>" or the enum type, as named when the change
// runs. A rename adds " → <new name>", an alter-column " (<What>)", a
// sequence rename " (<from> → <to>)" after its column, and an alter-enum
// " (<from values> → <to values>)".
func (c createTable) String() string { return kindOf(c, c.Table.Ref().String()) }

// String is "drop-table <table>".
func (c dropTable) String() string { return kindOf(c, c.Table.Ref().String()) }

// String is "rename-table <from> → <to>".
func (c renameTable) String() string { return kindOf(c, c.From.String()+" → "+c.To.String()) }

// String is "add-column <table>.<column>".
func (c addColumn) String() string { return kindOf(c, objectOn(c.Table, c.Column.Name)) }

// String is "drop-column <table>.<column>".
func (c dropColumn) String() string { return kindOf(c, objectOn(c.Table, c.Column.Name)) }

// String is "rename-column <table>.<from> → <to>".
func (c renameColumn) String() string { return kindOf(c, objectOn(c.Table, c.From)+" → "+c.To) }

// String is "alter-column <table>.<column> (<What>)".
func (c alterColumn) String() string {
	return kindOf(c, objectOn(c.Table, c.To.Name)+" ("+c.What.String()+")")
}

// String is "add-index <table>.<index>".
func (c addIndex) String() string { return kindOf(c, indexObject(c.Table, c.Index)) }

// String is "drop-index <table>.<index>".
func (c dropIndex) String() string { return kindOf(c, indexObject(c.Table, c.Index)) }

// String is "rename-index <table>.<from> → <to>".
func (c renameIndex) String() string {
	return kindOf(c, objectOn(c.Table, source(c.From, c.def))+" → "+c.To)
}

// String is "add-foreign-key <table>.<constraint>".
func (c addForeignKey) String() string { return kindOf(c, keyObject(c.Table, c.ForeignKey)) }

// String is "drop-foreign-key <table>.<constraint>".
func (c dropForeignKey) String() string { return kindOf(c, keyObject(c.Table, c.ForeignKey)) }

// String is "add-check <table>.<constraint>".
func (c addCheck) String() string { return kindOf(c, checkObject(c.Table, c.Check)) }

// String is "drop-check <table>.<constraint>".
func (c dropCheck) String() string { return kindOf(c, checkObject(c.Table, c.Check)) }

// String is "rename-constraint <table>.<from> → <to>".
func (c renameConstraint) String() string {
	return kindOf(c, objectOn(c.Table, source(c.From, c.def))+" → "+c.To)
}

// String is "set-table-comment <table>".
func (c setTableComment) String() string { return kindOf(c, c.Table.String()) }

// String is "set-table-collation <table>".
func (c setTableCollation) String() string { return kindOf(c, c.Table.String()) }

// String is "create-enum <type>".
func (c createEnum) String() string { return kindOf(c, c.Enum.ref().String()) }

// String is "drop-enum <type>".
func (c dropEnum) String() string { return kindOf(c, c.Enum.ref().String()) }

// String is "rename-enum <from> → <to>".
func (c renameEnum) String() string { return kindOf(c, c.From.String()+" → "+c.To.String()) }

// String is "rename-sequence <table>.<column> (<from> → <to>)".
func (c renameSequence) String() string {
	return kindOf(c, objectOn(c.Table, c.Column)+" ("+c.From+" → "+c.To+")")
}

// String is "alter-enum <type> (<from values> → <to values>)".
func (c alterEnum) String() string {
	return kindOf(c, c.To.ref().String()+" ("+strings.Join(c.From.Values, ", ")+" → "+strings.Join(c.To.Values, ", ")+")")
}

func kindOf(c Change, object string) string { return string(c.Kind()) + " " + object }

// source is a rename's from name, or def, the label of an unnamed source.
func source(from, def string) string {
	if from == "" {
		return def
	}
	return from
}

// objectOn is "<table>.<name>".
func objectOn(t TableRef, name string) string { return t.String() + "." + name }
