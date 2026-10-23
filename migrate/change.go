package migrate

// A Change is one step of a migration. Each kind is a struct of this package;
// a caller switches on the type.
type Change interface {
	Kind() Kind
	// Target is the object it acts on, by its name when the change runs: a
	// table, or an enum type's ref for the enum changes.
	Target() TableRef
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

const (
	ColumnType ColumnChange = 1 << iota
	ColumnNullable
	ColumnDefault
	ColumnIdentity // Identity, and Sequence with it
	ColumnComment
)

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

func (CreateTable) Kind() Kind       { return "create-table" }
func (DropTable) Kind() Kind         { return "drop-table" }
func (RenameTable) Kind() Kind       { return "rename-table" }
func (AddColumn) Kind() Kind         { return "add-column" }
func (DropColumn) Kind() Kind        { return "drop-column" }
func (RenameColumn) Kind() Kind      { return "rename-column" }
func (AlterColumn) Kind() Kind       { return "alter-column" }
func (RenameSequence) Kind() Kind    { return "rename-sequence" }
func (AddIndex) Kind() Kind          { return "add-index" }
func (DropIndex) Kind() Kind         { return "drop-index" }
func (RenameIndex) Kind() Kind       { return "rename-index" }
func (AddForeignKey) Kind() Kind     { return "add-foreign-key" }
func (DropForeignKey) Kind() Kind    { return "drop-foreign-key" }
func (AddCheck) Kind() Kind          { return "add-check" }
func (DropCheck) Kind() Kind         { return "drop-check" }
func (RenameConstraint) Kind() Kind  { return "rename-constraint" }
func (SetTableComment) Kind() Kind   { return "set-table-comment" }
func (SetTableCollation) Kind() Kind { return "set-table-collation" }
func (CreateEnum) Kind() Kind        { return "create-enum" }
func (DropEnum) Kind() Kind          { return "drop-enum" }
func (RenameEnum) Kind() Kind        { return "rename-enum" }
func (AlterEnum) Kind() Kind         { return "alter-enum" }

func (c CreateTable) Target() TableRef       { return c.Table.Ref() }
func (c DropTable) Target() TableRef         { return c.Table.Ref() }
func (c RenameTable) Target() TableRef       { return c.To }
func (c AddColumn) Target() TableRef         { return c.Table }
func (c DropColumn) Target() TableRef        { return c.Table }
func (c RenameColumn) Target() TableRef      { return c.Table }
func (c AlterColumn) Target() TableRef       { return c.Table }
func (c RenameSequence) Target() TableRef    { return c.Table }
func (c AddIndex) Target() TableRef          { return c.Table }
func (c DropIndex) Target() TableRef         { return c.Table }
func (c RenameIndex) Target() TableRef       { return c.Table }
func (c AddForeignKey) Target() TableRef     { return c.Table }
func (c DropForeignKey) Target() TableRef    { return c.Table }
func (c AddCheck) Target() TableRef          { return c.Table }
func (c DropCheck) Target() TableRef         { return c.Table }
func (c RenameConstraint) Target() TableRef  { return c.Table }
func (c SetTableComment) Target() TableRef   { return c.Table }
func (c SetTableCollation) Target() TableRef { return c.Table }
func (c CreateEnum) Target() TableRef        { return c.Enum.ref() }
func (c DropEnum) Target() TableRef          { return c.Enum.ref() }
func (c RenameEnum) Target() TableRef        { return c.To }
func (c AlterEnum) Target() TableRef         { return c.To.ref() }

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
