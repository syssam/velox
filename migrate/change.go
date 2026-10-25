package migrate

import (
	"fmt"
	"strings"
)

// A Change is one step of a migration. Each kind is a struct of this package;
// a caller switches on the type.
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

// A ColumnChange is the set of what an AlterColumn changes.
type ColumnChange uint8

// The ColumnChange bits, one per part of a column an AlterColumn changes.
const (
	ColumnType ColumnChange = 1 << iota
	ColumnNullable
	ColumnDefault
	ColumnIdentity // Identity, and Sequence with it
	ColumnComment
)

// String is the names of the set bits, joined by "|" in the order
// "type|nullable|default|identity|comment"; "0" for none.
func (c ColumnChange) String() string {
	if c == 0 {
		return "0"
	}
	var parts []string
	for i, name := range [...]string{"type", "nullable", "default", "identity", "comment"} {
		if bit := ColumnChange(1) << i; c&bit != 0 {
			parts = append(parts, name)
			c &^= bit
		}
	}
	if c != 0 {
		parts = append(parts, fmt.Sprintf("%#x", uint8(c)))
	}
	return strings.Join(parts, "|")
}

// CreateTable creates a table, with its indexes, checks and foreign keys.
type CreateTable struct{ Table Table }

// DropTable drops a table; Table is as in from.
type DropTable struct{ Table Table }

// RenameTable renames a table.
type RenameTable struct{ From, To TableRef }

// AddColumn adds a column.
type AddColumn struct {
	Table  TableRef
	Column Column
}

// DropColumn drops a column.
type DropColumn struct {
	Table  TableRef
	Column Column
}

// RenameColumn renames a column.
type RenameColumn struct {
	Table    TableRef
	From, To string
}

// AlterColumn changes what What names of a column, From, to To's.
type AlterColumn struct {
	Table    TableRef
	From, To Column
	What     ColumnChange
}

// RenameSequence renames the PostgreSQL sequence a column owns.
type RenameSequence struct {
	Table            TableRef
	Column, From, To string
}

// AddIndex adds an index.
type AddIndex struct {
	Table TableRef
	Index Index
}

// DropIndex drops an index.
type DropIndex struct {
	Table TableRef
	Index Index
}

// RenameIndex renames an index.
type RenameIndex struct {
	Table    TableRef
	From, To string
	def      string // the label of an unnamed source, From "", by its definition
}

// AddForeignKey adds a foreign key.
type AddForeignKey struct {
	Table      TableRef
	ForeignKey ForeignKey
}

// DropForeignKey drops a foreign key.
type DropForeignKey struct {
	Table      TableRef
	ForeignKey ForeignKey
}

// AddCheck adds a check.
type AddCheck struct {
	Table TableRef
	Check Check
}

// DropCheck drops a check.
type DropCheck struct {
	Table TableRef
	Check Check
}

// RenameConstraint renames a primary key, a foreign key or a check.
type RenameConstraint struct {
	Table    TableRef
	From, To string
	def      string // the label of an unnamed source, From "", by its definition
}

// SetTableComment changes a table's comment.
type SetTableComment struct {
	Table    TableRef
	From, To string
}

// SetTableCollation changes a MySQL table's collation.
type SetTableCollation struct {
	Table    TableRef
	From, To string
}

// CreateEnum creates a PostgreSQL enum type.
type CreateEnum struct{ Enum Enum }

// DropEnum drops a PostgreSQL enum type.
type DropEnum struct{ Enum Enum }

// RenameEnum renames a PostgreSQL enum type; the refs are the type's schema
// and name.
type RenameEnum struct{ From, To TableRef }

// AlterEnum changes a PostgreSQL enum type's values: it adds values, or
// reorders or removes them.
type AlterEnum struct{ From, To Enum }

// DiffOptions are the renames Diff cannot see.
type DiffOptions struct {
	RenameTables  map[TableRef]TableRef // a table of from → its name in to
	RenameColumns map[ColumnRef]string  // a column of from (its table by from's name) → its name in to
}

func (e *Enum) ref() TableRef { return TableRef{Schema: e.Schema, Name: e.Name} }

// Kind is "create-table".
func (CreateTable) Kind() Kind { return "create-table" }

// Kind is "drop-table".
func (DropTable) Kind() Kind { return "drop-table" }

// Kind is "rename-table".
func (RenameTable) Kind() Kind { return "rename-table" }

// Kind is "add-column".
func (AddColumn) Kind() Kind { return "add-column" }

// Kind is "drop-column".
func (DropColumn) Kind() Kind { return "drop-column" }

// Kind is "rename-column".
func (RenameColumn) Kind() Kind { return "rename-column" }

// Kind is "alter-column".
func (AlterColumn) Kind() Kind { return "alter-column" }

// Kind is "rename-sequence".
func (RenameSequence) Kind() Kind { return "rename-sequence" }

// Kind is "add-index".
func (AddIndex) Kind() Kind { return "add-index" }

// Kind is "drop-index".
func (DropIndex) Kind() Kind { return "drop-index" }

// Kind is "rename-index".
func (RenameIndex) Kind() Kind { return "rename-index" }

// Kind is "add-foreign-key".
func (AddForeignKey) Kind() Kind { return "add-foreign-key" }

// Kind is "drop-foreign-key".
func (DropForeignKey) Kind() Kind { return "drop-foreign-key" }

// Kind is "add-check".
func (AddCheck) Kind() Kind { return "add-check" }

// Kind is "drop-check".
func (DropCheck) Kind() Kind { return "drop-check" }

// Kind is "rename-constraint".
func (RenameConstraint) Kind() Kind { return "rename-constraint" }

// Kind is "set-table-comment".
func (SetTableComment) Kind() Kind { return "set-table-comment" }

// Kind is "set-table-collation".
func (SetTableCollation) Kind() Kind { return "set-table-collation" }

// Kind is "create-enum".
func (CreateEnum) Kind() Kind { return "create-enum" }

// Kind is "drop-enum".
func (DropEnum) Kind() Kind { return "drop-enum" }

// Kind is "rename-enum".
func (RenameEnum) Kind() Kind { return "rename-enum" }

// Kind is "alter-enum".
func (AlterEnum) Kind() Kind { return "alter-enum" }

// Target is the table it creates.
func (c CreateTable) Target() TableRef { return c.Table.Ref() }

// Target is the table it drops.
func (c DropTable) Target() TableRef { return c.Table.Ref() }

// Target is the table's new name.
func (c RenameTable) Target() TableRef { return c.To }

// Target is the table it adds the column to.
func (c AddColumn) Target() TableRef { return c.Table }

// Target is the table it drops the column from.
func (c DropColumn) Target() TableRef { return c.Table }

// Target is the column's table.
func (c RenameColumn) Target() TableRef { return c.Table }

// Target is the column's table.
func (c AlterColumn) Target() TableRef { return c.Table }

// Target is the column's table.
func (c RenameSequence) Target() TableRef { return c.Table }

// Target is the index's table.
func (c AddIndex) Target() TableRef { return c.Table }

// Target is the index's table.
func (c DropIndex) Target() TableRef { return c.Table }

// Target is the index's table.
func (c RenameIndex) Target() TableRef { return c.Table }

// Target is the foreign key's table.
func (c AddForeignKey) Target() TableRef { return c.Table }

// Target is the foreign key's table.
func (c DropForeignKey) Target() TableRef { return c.Table }

// Target is the check's table.
func (c AddCheck) Target() TableRef { return c.Table }

// Target is the check's table.
func (c DropCheck) Target() TableRef { return c.Table }

// Target is the constraint's table.
func (c RenameConstraint) Target() TableRef { return c.Table }

// Target is the table whose comment it sets.
func (c SetTableComment) Target() TableRef { return c.Table }

// Target is the table whose collation it sets.
func (c SetTableCollation) Target() TableRef { return c.Table }

// Target is the enum type's ref.
func (c CreateEnum) Target() TableRef { return c.Enum.ref() }

// Target is the enum type's ref.
func (c DropEnum) Target() TableRef { return c.Enum.ref() }

// Target is the enum type's new ref.
func (c RenameEnum) Target() TableRef { return c.To }

// Target is the enum type's ref.
func (c AlterEnum) Target() TableRef { return c.To.ref() }

func (CreateTable) isChange()       {}
func (DropTable) isChange()         {}
func (RenameTable) isChange()       {}
func (AddColumn) isChange()         {}
func (DropColumn) isChange()        {}
func (RenameColumn) isChange()      {}
func (AlterColumn) isChange()       {}
func (RenameSequence) isChange()    {}
func (AddIndex) isChange()          {}
func (DropIndex) isChange()         {}
func (RenameIndex) isChange()       {}
func (AddForeignKey) isChange()     {}
func (DropForeignKey) isChange()    {}
func (AddCheck) isChange()          {}
func (DropCheck) isChange()         {}
func (RenameConstraint) isChange()  {}
func (SetTableComment) isChange()   {}
func (SetTableCollation) isChange() {}
func (CreateEnum) isChange()        {}
func (DropEnum) isChange()          {}
func (RenameEnum) isChange()        {}
func (AlterEnum) isChange()         {}

// String is "<kind> <object>": the object is the table, "<table>.<column>",
// "<table>.<index or constraint>" or the enum type, as named when the change
// runs. A rename adds " → <new name>", an alter-column " (<What>)", a
// sequence rename " (<from> → <to>)" after its column, and an alter-enum
// " (<from values> → <to values>)".
func (c CreateTable) String() string { return kindOf(c, c.Table.Ref().String()) }

// String is "drop-table <table>".
func (c DropTable) String() string { return kindOf(c, c.Table.Ref().String()) }

// String is "rename-table <from> → <to>".
func (c RenameTable) String() string { return kindOf(c, c.From.String()+" → "+c.To.String()) }

// String is "add-column <table>.<column>".
func (c AddColumn) String() string { return kindOf(c, objectOn(c.Table, c.Column.Name)) }

// String is "drop-column <table>.<column>".
func (c DropColumn) String() string { return kindOf(c, objectOn(c.Table, c.Column.Name)) }

// String is "rename-column <table>.<from> → <to>".
func (c RenameColumn) String() string { return kindOf(c, objectOn(c.Table, c.From)+" → "+c.To) }

// String is "alter-column <table>.<column> (<What>)".
func (c AlterColumn) String() string {
	return kindOf(c, objectOn(c.Table, c.To.Name)+" ("+c.What.String()+")")
}

// String is "add-index <table>.<index>".
func (c AddIndex) String() string { return kindOf(c, indexObject(c.Table, c.Index)) }

// String is "drop-index <table>.<index>".
func (c DropIndex) String() string { return kindOf(c, indexObject(c.Table, c.Index)) }

// String is "rename-index <table>.<from> → <to>".
func (c RenameIndex) String() string {
	return kindOf(c, objectOn(c.Table, source(c.From, c.def))+" → "+c.To)
}

// String is "add-foreign-key <table>.<constraint>".
func (c AddForeignKey) String() string { return kindOf(c, keyObject(c.Table, c.ForeignKey)) }

// String is "drop-foreign-key <table>.<constraint>".
func (c DropForeignKey) String() string { return kindOf(c, keyObject(c.Table, c.ForeignKey)) }

// String is "add-check <table>.<constraint>".
func (c AddCheck) String() string { return kindOf(c, checkObject(c.Table, c.Check)) }

// String is "drop-check <table>.<constraint>".
func (c DropCheck) String() string { return kindOf(c, checkObject(c.Table, c.Check)) }

// String is "rename-constraint <table>.<from> → <to>".
func (c RenameConstraint) String() string {
	return kindOf(c, objectOn(c.Table, source(c.From, c.def))+" → "+c.To)
}

// String is "set-table-comment <table>".
func (c SetTableComment) String() string { return kindOf(c, c.Table.String()) }

// String is "set-table-collation <table>".
func (c SetTableCollation) String() string { return kindOf(c, c.Table.String()) }

// String is "create-enum <type>".
func (c CreateEnum) String() string { return kindOf(c, c.Enum.ref().String()) }

// String is "drop-enum <type>".
func (c DropEnum) String() string { return kindOf(c, c.Enum.ref().String()) }

// String is "rename-enum <from> → <to>".
func (c RenameEnum) String() string { return kindOf(c, c.From.String()+" → "+c.To.String()) }

// String is "rename-sequence <table>.<column> (<from> → <to>)".
func (c RenameSequence) String() string {
	return kindOf(c, objectOn(c.Table, c.Column)+" ("+c.From+" → "+c.To+")")
}

// String is "alter-enum <type> (<from values> → <to values>)".
func (c AlterEnum) String() string {
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
