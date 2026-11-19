package migrate

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/ddl"
)

// A Class says how much care a change needs. A change with no finding is
// safe.
type Class uint8

// The classes, from least care to most. They are bare, so they read as
// migrate.Safe, migrate.Review and migrate.Destructive.
const (
	Safe Class = iota
	Review
	Destructive
)

// String is "safe", "review" or "destructive"; "class(<n>)" for another
// value.
func (c Class) String() string {
	switch c {
	case Safe:
		return "safe"
	case Review:
		return "review"
	case Destructive:
		return "destructive"
	}
	return "class(" + strconv.Itoa(int(c)) + ")"
}

// A Finding is what Lint says about one change.
type Finding struct {
	Code    string // "drop-column"
	Class   Class
	Change  int    // the index of the change in the slice Lint was given
	Target  string // "users.name": what an Allow names
	Pos     string // the schema's file:line, when known
	Message string
}

// An Allow lets a destructive finding through.
type Allow struct{ Code, Target string } // Target "" covers every target of Code

// destructive are the codes an Allow can name. Lint gives the first nine;
// delete-rows, update-rows, drop-schema, drop-database, drop-domain, drop-type,
// drop-collation and drop-partition are LintFile's only;
// lossCode is Up's, which no plan can give.
var destructive = []string{
	"drop-table", "drop-column", "alter-type", "set-not-null", "add-required-column",
	"add-unique", "drop-enum-value", "reorder-enum", "convert-collation",
	"delete-rows", "update-rows", "drop-schema", "drop-database",
	"drop-domain", "drop-type", "drop-collation", "drop-partition", lossCode,
}

// ParseAllow reads the items of the -allow flag, joined by commas, each a
// destructive code and optionally "=" and the target it covers:
// "drop-column=users.name,drop-table". A comma inside parentheses is the
// target's, as in "unmodeled-loss=t.(a, b)". An empty s, or one of white space,
// allows nothing: nil and no error. In a list, an empty item, an empty
// target, or a code that is not destructive is an *Error with Code "allow".
func ParseAllow(s string) ([]Allow, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var out []Allow
	for _, item := range allowItems(s) {
		item = strings.TrimSpace(item)
		code, target, hasTarget := strings.Cut(item, "=")
		code, target = strings.TrimSpace(code), strings.TrimSpace(target)
		switch {
		case item == "":
			return nil, &Error{Code: "allow", Reason: "an allowance is empty; write code or code=target, joined by commas"}
		case !slices.Contains(destructive, code):
			return nil, &Error{Code: "allow", Object: item, Reason: fmt.Sprintf("%q is not a destructive code; only %s can be allowed", code, strings.Join(destructive, ", "))}
		case hasTarget && target == "":
			return nil, &Error{Code: "allow", Object: item, Reason: "the target after = is empty; leave out the = to cover every target"}
		}
		out = append(out, Allow{Code: code, Target: target})
	}
	return out, nil
}

// allowItems is s cut at each comma outside parentheses.
func allowItems(s string) []string {
	var out []string
	from, depth := 0, 0
	for i, c := range s {
		switch c {
		case '(':
			depth++
		case ')':
			depth = max(depth-1, 0)
		case ',':
			if depth == 0 {
				out = append(out, s[from:i])
				from = i + 1
			}
		}
	}
	return append(out, s[from:])
}

// Blocked is the destructive findings that no allowance covers, in order. An
// Allow covers a finding of its Code, for its Target or, when that is "", for
// every target.
func Blocked(findings []Finding, allow []Allow) []Finding {
	var out []Finding
	for _, f := range findings {
		if f.Class != Destructive {
			continue
		}
		if !slices.ContainsFunc(allow, func(a Allow) bool { return a.Code == f.Code && (a.Target == "" || a.Target == f.Target) }) {
			out = append(out, f)
		}
	}
	return out
}

// Lint classifies changes, the output of Diff from from. Each change gets at
// most one finding of each code, in the order of the changes; a change with
// no finding is safe. The table of a change counts as existing when from has
// it, or it renames one of from's. A nil change gets no finding; Render
// refuses it. Lint does not change from or the changes.
func Lint(from *Schema, changes []Change) []Finding {
	return lintChanges(from, nil, changes).out
}

// lintChanges lints changes and returns the linter, whose findings are Lint's
// and whose rebuilt tables are the ones Render rebuilds on SQLite.
func lintChanges(from, to *Schema, changes []Change) *linter {
	if from == nil {
		from = &Schema{}
	}
	l := &linter{
		from: from, to: to, d: from.Dialect, cs: changes,
		renamed: map[TableRef]TableRef{}, enumFrom: map[TableRef]TableRef{},
		created: map[TableRef]bool{}, dropped: map[TableRef]bool{},
		rebuild: make([]bool, len(changes)), rebuilt: map[TableRef]bool{},
		quiet: make([]bool, len(changes)), rebuildWhat: make([]string, len(changes)),
	}
	l.scan()
	for i, c := range changes {
		if c != nil {
			l.change(i, c)
		}
	}
	return l
}

type linter struct {
	from *Schema
	to   *Schema // the plan's to, when known: it names what a rename of an unnamed index makes
	d    velox.Dialect
	cs   []Change
	out  []Finding

	renamed  map[TableRef]TableRef // a renamed table's name in from, by its name after the rename
	enumFrom map[TableRef]TableRef // the same for enum types
	created  map[TableRef]bool     // the tables the plan creates
	dropped  map[TableRef]bool     // the tables the plan drops, by their names in from
	rebuild  []bool                // SQLite: the change rebuilds its table
	rebuilt  map[TableRef]bool     // SQLite: the tables with a rebuild, by their names in from
	quiet    []bool                // the change is half of a pair that leaves things as they were

	rebuildWhat []string // SQLite: what a rebuilding change does, as its sqlite-rebuild finding says
}

func (l *linter) add(i int, code string, class Class, target, pos, format string, args ...any) {
	l.out = append(l.out, Finding{Code: code, Class: class, Change: i, Target: target, Pos: pos, Message: fmt.Sprintf(format, args...)})
}

// canon is the name of the table in from.
func (l *linter) canon(ref TableRef) TableRef {
	if r, ok := l.renamed[ref]; ok {
		return r
	}
	return ref
}

// existing reports whether the table ref is one of from's, as named in the
// plan.
func (l *linter) existing(ref TableRef) bool {
	return !l.created[ref] && (l.from.table(ref) != nil || l.from.table(l.canon(ref)) != nil)
}

// scan finds what a change needs to know of the others.
func (l *linter) scan() {
	for _, c := range l.cs {
		switch c := c.(type) {
		case renameTable:
			l.renamed[c.To] = c.From
		case renameEnum:
			l.enumFrom[c.To] = c.From
		case createTable:
			l.created[c.Table.Ref()] = true
		case dropTable:
			l.dropped[c.Table.Ref()] = true
		}
	}
	// A foreign key dropped and added again as it was, around a column's
	// type change or an index's replacement, is no change of its own.
	for i, c := range l.cs {
		d, ok := c.(dropForeignKey)
		if !ok {
			continue
		}
		for j, a := range l.cs {
			if a, ok := a.(addForeignKey); ok && l.canon(a.Table) == l.canon(d.Table) && l.sameKey(d.ForeignKey, a.ForeignKey) {
				l.quiet[i], l.quiet[j] = true, true
			}
		}
	}
	if l.d == velox.SQLite {
		for i, c := range l.cs {
			if c != nil && l.rebuilds(c) {
				l.rebuild[i] = true
				l.rebuilt[l.canon(c.Target())] = true
			}
		}
	}
}

// sameKey reports whether a and b are one foreign key, whatever their names:
// the same columns, target and action, the target's renames applied.
func (l *linter) sameKey(a, b ForeignKey) bool {
	return l.canon(a.RefTable) == l.canon(b.RefTable) && a.OnDelete == b.OnDelete &&
		slices.Equal(a.Columns, b.Columns) && slices.Equal(a.RefColumns, b.RefColumns)
}

// rebuilds reports whether SQLite can only make c by rebuilding its table: it
// alters a column's type, nullability, default or identity, adds or drops a
// key or a check, renames a constraint, adds a column it cannot add in place,
// drops one that something uses, or adds, drops or renames a UNIQUE
// constraint's index.
func (l *linter) rebuilds(c Change) bool {
	if l.dropped[l.canon(c.Target())] || !l.existing(c.Target()) {
		return false
	}
	switch c := c.(type) {
	case alterColumn:
		return c.What&(columnType|columnNullable|columnDefault|columnIdentity) != 0
	case addColumn:
		col := c.Column
		if col.Identity {
			return false
		}
		if col.Default == "" {
			return !col.Nullable
		}
		return !literalDefault(l.d, col.Default)
	case dropColumn:
		t := l.from.table(l.canon(c.Table))
		return t != nil && l.uses(t, c.Column.Name)
	case addForeignKey, dropForeignKey, addCheck, dropCheck, renameConstraint:
		return true
	case addIndex:
		return tableUnique(c.Index)
	case dropIndex:
		return tableUnique(c.Index)
	case renameIndex:
		return c.From == "" // a UNIQUE constraint's index, which no DROP INDEX drops
	}
	return false
}

// uses reports whether a key, an index or a check of from holds the column
// col of t, or a foreign key of another table points at it.
func (l *linter) uses(t *Table, col string) bool {
	if slices.Contains(t.PrimaryKey, col) {
		return true
	}
	for _, ix := range t.Indexes {
		if slices.Contains(ix.Columns, col) || ix.Where != "" && namesColumn(l.d, ix.Where, col) {
			return true
		}
	}
	for _, fk := range t.ForeignKeys {
		if slices.Contains(fk.Columns, col) {
			return true
		}
	}
	for _, ck := range t.Checks {
		if namesColumn(l.d, ck.Expr, col) {
			return true
		}
	}
	for i := range l.from.Tables {
		for _, fk := range l.from.Tables[i].ForeignKeys {
			if fk.RefTable == t.Ref() && slices.Contains(fk.RefColumns, col) {
				return true
			}
		}
	}
	return false
}

func (l *linter) change(i int, c Change) {
	switch c := c.(type) {
	case dropTable:
		l.add(i, "drop-table", Destructive, c.Table.Ref().String(), "", "drops table %s and its rows", c.Table.Ref())
	case renameTable:
		l.add(i, "rename-table", Review, c.To.String(), "", "renames table %s to %s; code that uses the old name breaks while old and new versions run together", c.From, c.To)
	case setTableCollation:
		l.add(i, "convert-collation", Destructive, c.Table.String(), "", "converts table %s from collation %s to %s, a rewrite that can make values equal that were not", c.Table, c.From, c.To)
	case addColumn:
		l.addColumn(i, c)
	case dropColumn:
		l.add(i, "drop-column", Destructive, colTarget(c.Table, c.Column.Name), "", "drops column %s and its data", colTarget(c.Table, c.Column.Name))
		l.rebuildFinding(i, "drops column %s, which a key, index or constraint uses", colTarget(c.Table, c.Column.Name))
	case renameColumn:
		l.add(i, "rename-column", Review, colTarget(c.Table, c.To), "", "renames column %s to %s; code that uses the old name breaks while old and new versions run together", colTarget(c.Table, c.From), c.To)
	case alterColumn:
		l.alterColumn(i, c)
	case addIndex:
		l.addIndex(i, c)
	case dropIndex:
		l.dropIndex(i, c)
	case renameIndex:
		l.rebuildFinding(i, "renames the index of a UNIQUE constraint%s to %s", l.renamedUnique(c), c.To)
	case addForeignKey:
		if l.existing(c.Table) && !l.quiet[i] {
			l.add(i, "add-foreign-key", Review, keyObject(c.Table, c.ForeignKey), c.ForeignKey.Pos, "adds foreign key %s; it fails if rows have no match in %s", keyLabel(c.ForeignKey), c.ForeignKey.RefTable)
		}
		l.rebuildFinding(i, "adds foreign key %s", keyLabel(c.ForeignKey))
	case dropForeignKey:
		if !l.quiet[i] && !l.dropped[l.canon(c.Table)] && !l.rebuild[i] && !l.replacesKey(c) {
			l.add(i, "drop-foreign-key", Review, keyObject(c.Table, c.ForeignKey), "", "drops foreign key %s; rows are no longer checked against %s", keyLabel(c.ForeignKey), c.ForeignKey.RefTable)
		}
		l.rebuildFinding(i, "drops foreign key %s", keyLabel(c.ForeignKey))
	case addCheck:
		l.addCheck(i, c)
	case dropCheck:
		if !l.dropped[l.canon(c.Table)] && !l.rebuild[i] && !l.replacesCheck(c) {
			l.add(i, "drop-check", Review, checkObject(c.Table, c.Check), "", "drops %s; rows are no longer limited by it", checkNoun(c.Check))
		}
		l.rebuildFinding(i, "drops %s", checkNoun(c.Check))
	case renameConstraint:
		l.rebuildFinding(i, "renames constraint %s to %s", source(c.From, c.def), c.To)
	case alterEnum:
		l.alterEnum(i, c)
	}
}

// rebuildFinding adds the sqlite-rebuild finding of change i, if it
// rebuilds. what says what the change does.
func (l *linter) rebuildFinding(i int, what string, args ...any) {
	if !l.rebuild[i] {
		return
	}
	c := l.cs[i]
	l.rebuildWhat[i] = fmt.Sprintf(what, args...)
	l.add(i, "sqlite-rebuild", Review, c.Target().String(), changePos(c), "%s", rebuildMessage(l.rebuildWhat[i], c.Target()))
}

// rebuildMessage is the message of a sqlite-rebuild finding: what the
// change does, and the rebuild of table.
func rebuildMessage(what string, table TableRef) string {
	return fmt.Sprintf("%s; SQLite rebuilds table %s: it copies every row into a new table", what, table)
}

// changePos is the Pos of the element of to that c adds or alters; a dropped
// element has none.
func changePos(c Change) string {
	switch c := c.(type) {
	case addColumn:
		return c.Column.Pos
	case alterColumn:
		return c.To.Pos
	case addForeignKey:
		return c.ForeignKey.Pos
	case addCheck:
		return c.Check.Pos
	}
	return ""
}

func colTarget(t TableRef, name string) string { return t.String() + "." + name }

// indexLabel is ix's name, or for an index SQLite keeps no name for its
// columns, "(a, b)". keyLabel is the same for a foreign key, and checkLabel
// for a check, unnamed "check (<expr>)".
func indexLabel(ix Index) string {
	if ix.Name == "" {
		return "(" + strings.Join(ix.Columns, ", ") + ")"
	}
	return ix.Name
}

func keyLabel(fk ForeignKey) string {
	if fk.Name == "" {
		return "(" + strings.Join(fk.Columns, ", ") + ")"
	}
	return fk.Name
}

func checkLabel(ck Check) string {
	if ck.Name == "" {
		return "check (" + ck.Expr + ")"
	}
	return ck.Name
}

// checkNoun is "check <name>", or for an unnamed check its label.
func checkNoun(ck Check) string {
	if ck.Name == "" {
		return checkLabel(ck)
	}
	return "check " + ck.Name
}

// indexObject is "<table>.<index>", the index by indexLabel; keyObject and
// checkObject are the same for a foreign key and a check.
func indexObject(t TableRef, ix Index) string    { return colTarget(t, indexLabel(ix)) }
func keyObject(t TableRef, fk ForeignKey) string { return colTarget(t, keyLabel(fk)) }
func checkObject(t TableRef, ck Check) string    { return colTarget(t, checkLabel(ck)) }

func (l *linter) addColumn(i int, c addColumn) {
	col, target := c.Column, colTarget(c.Table, c.Column.Name)
	if l.existing(c.Table) {
		if !col.Nullable && col.Default == "" && !col.Identity {
			l.add(i, "add-required-column", Destructive, target, col.Pos, "adds column %s as NOT NULL with no default; it fails if the table has rows: give the field a Default or make it Nillable, or allow it only on an empty table", target)
		}
		if l.d == velox.Postgres && col.Default != "" && !literalDefault(l.d, col.Default) {
			l.add(i, "volatile-default", Review, target, col.Pos, "adds column %s with default %s; if the expression is volatile, PostgreSQL rewrites the table under an exclusive lock", target, col.Default)
		}
	}
	l.rebuildFinding(i, "adds column %s, which SQLite cannot add in place", target)
}

func (l *linter) alterColumn(i int, c alterColumn) {
	target := colTarget(c.Table, c.To.Name)
	if c.What&columnType != 0 {
		l.alterType(i, c, target)
	}
	if c.What&columnNullable != 0 && c.From.Nullable && !c.To.Nullable {
		l.add(i, "set-not-null", Destructive, target, c.To.Pos, "makes column %s NOT NULL; it fails if any row holds NULL", target)
	}
	if c.What&columnIdentity != 0 && l.d == velox.Postgres && !c.From.Identity && c.To.Identity {
		l.add(i, "add-identity", Review, target, c.To.Pos, "adds an identity to column %s and sets its counter past the column's largest value", target)
		if strings.HasPrefix(c.From.Default, "nextval(") {
			l.add(i, "serial-to-identity", Review, target, c.To.Pos, "turns serial column %s into an identity that goes on from its sequence: its default and sequence are dropped", target)
		}
	}
	var what []string
	for _, w := range [...]struct {
		bit  columnMask
		name string
	}{{columnType, "type"}, {columnNullable, "nullability"}, {columnDefault, "default"}, {columnIdentity, "identity"}} {
		if c.What&w.bit != 0 {
			what = append(what, w.name)
		}
	}
	l.rebuildFinding(i, "changes the %s of column %s", strings.Join(what, ", "), target)
}

// alterType classifies a type change: an enum losing or moving values, a
// widening, or any other change. On SQLite one that keeps every value is no
// finding of its own, only its rebuild's.
func (l *linter) alterType(i int, c alterColumn, target string) {
	from, to := c.From.Type, c.To.Type
	if l.d == velox.SQLite && sqliteKeepsValues(from, to) {
		return
	}
	if l.d == velox.MySQL {
		if a, ok := ddl.ParseMySQLEnum(from); ok {
			if b, ok := ddl.ParseMySQLEnum(to); ok {
				l.enumDelta(i, target, c.To.Pos, a, b, enumInline, "column "+target)
				return
			}
		}
	}
	if class, ok := widening(l.d, from, to); ok {
		more := ""
		if class != Safe {
			more = "; the table is rewritten under an exclusive lock"
		}
		l.add(i, "widen-type", class, target, c.To.Pos, "widens column %s from %s to %s%s", target, from, to, more)
		return
	}
	l.add(i, "alter-type", Destructive, target, c.To.Pos, "changes column %s from %s to %s, which can fail or change values%s", target, from, to, l.castWarning(from, to))
}

// castWarning is what a finding adds about the cast of a PostgreSQL type change
// the renderer cannot keep from cutting a value: a number to bit keeps its low
// bits, and a target velox cannot classify, a domain above all, is cast
// explicitly, which cuts where an assignment would fail.
func (l *linter) castWarning(from, to string) string {
	if l.d != velox.Postgres {
		return ""
	}
	t := pgTypeOf(to)
	switch {
	case t.bits && t.limited && pgInteger(from):
		return "; a number cast to " + to + " keeps only its low bits, as 12::bit(3) is 100"
	case !t.known && !l.enumNamed(t.base):
		return "; velox cannot classify the type " + to + ", and a cast to it may truncate what does not fit"
	}
	return ""
}

// enumNamed reports whether typ is the type of an enum type the plan or from
// names.
func (l *linter) enumNamed(typ string) bool {
	named := func(ref TableRef) bool { return enumTypeName(ref) == typ }
	has := func(s *Schema) bool {
		return s != nil && slices.ContainsFunc(s.Enums, func(e Enum) bool { return named(e.ref()) })
	}
	if has(l.from) || has(l.to) {
		return true
	}
	return slices.ContainsFunc(l.cs, func(c Change) bool {
		switch c := c.(type) {
		case createEnum:
			return named(c.Enum.ref())
		case alterEnum:
			return named(c.To.ref())
		case renameEnum:
			return named(c.To)
		}
		return false
	})
}

// How enum values are kept: a PostgreSQL enum type keeps their order, a MySQL
// column also stores each as its place in the list, and a check keeps no order.
const (
	enumType = iota
	enumInline
	enumCheck
)

// enumDelta adds the drop-enum-value and reorder-enum findings of enum
// values from becoming to. A MySQL enum also moves its values when one is
// added before the last it keeps.
func (l *linter) enumDelta(i int, target, pos string, from, to []string, kind int, what string) {
	var removed []string
	for _, v := range from {
		if !slices.Contains(to, v) {
			removed = append(removed, ddl.Literal(l.d, v))
		}
	}
	if len(removed) > 0 {
		l.add(i, "drop-enum-value", Destructive, target, pos, "removes %s from %s; rows that hold it fail or change", strings.Join(removed, ", "), what)
	}
	var kept []string // the values both have, in from's order
	for _, v := range from {
		if slices.Contains(to, v) {
			kept = append(kept, v)
		}
	}
	var seen []string // the same, in to's order
	last := 0         // the place in to after the last value from has
	for j, v := range to {
		if slices.Contains(from, v) {
			seen = append(seen, v)
			last = j + 1
		}
	}
	moved := kind != enumCheck && !slices.Equal(kept, seen)
	if kind == enumInline {
		moved = moved || slices.ContainsFunc(to[:last], func(v string) bool { return !slices.Contains(from, v) })
	}
	if moved {
		l.add(i, "reorder-enum", Destructive, target, pos, "changes the order of the values of %s; rows that hold them are rewritten", what)
	}
}

func (l *linter) alterEnum(i int, c alterEnum) {
	ref := c.To.ref()
	orig := c.From.Values
	from := ref
	if r, ok := l.enumFrom[ref]; ok {
		from = r
	}
	if e := l.from.enum(from); e != nil {
		orig = e.Values
	}
	l.enumDelta(i, ref.String(), c.To.Pos, orig, c.To.Values, enumType, "enum type "+ref.String())
}

func (l *linter) addIndex(i int, c addIndex) {
	if !l.existing(c.Table) {
		return
	}
	defer l.rebuildFinding(i, "adds a UNIQUE constraint on %s", indexObject(c.Table, c.Index))
	target := indexObject(c.Table, c.Index)
	if c.Index.Unique {
		l.add(i, "add-unique", Destructive, target, c.Index.Pos, "adds unique index %s; it fails if rows hold duplicates", target)
		return
	}
	l.add(i, "add-index", Review, target, c.Index.Pos, "adds index %s; building it scans the table", target)
}

func (l *linter) dropIndex(i int, c dropIndex) {
	target := indexObject(c.Table, c.Index)
	defer l.rebuildFinding(i, "drops the index of a UNIQUE constraint on %s", target)
	if l.dropped[l.canon(c.Table)] || (l.rebuilt[l.canon(c.Table)] && !l.rebuild[i]) || l.replacesIndex(c) {
		return
	}
	l.add(i, "drop-index", Review, target, "", "drops index %s; queries that used it get slower", target)
}

// renamedUnique is " on <table>.(<columns>)", the UNIQUE constraint's index
// c renames by the columns of to's index c.To, which a rename by definition
// shares; "" when the linter has no to.
func (l *linter) renamedUnique(c renameIndex) string {
	if l.to == nil {
		return ""
	}
	if t := l.to.table(c.Table); t != nil {
		if ix := t.index(c.To); ix != nil {
			return " on " + indexObject(c.Table, Index{Columns: ix.Columns})
		}
	}
	return ""
}

// replacesIndex reports whether the plan adds an index that does what the
// dropped one did: its columns lead the new index's, under the same
// predicate, and a unique one is replaced by a unique one over the same
// columns.
func (l *linter) replacesIndex(d dropIndex) bool {
	for _, c := range l.cs {
		a, ok := c.(addIndex)
		if !ok || l.canon(a.Table) != l.canon(d.Table) || a.Index.Where != d.Index.Where {
			continue
		}
		old, nu := d.Index, a.Index
		if old.Unique && (!nu.Unique || !slices.Equal(old.Columns, nu.Columns)) {
			continue
		}
		if len(old.Columns) <= len(nu.Columns) && slices.Equal(old.Columns, nu.Columns[:len(old.Columns)]) {
			return true
		}
	}
	return false
}

// replacesKey reports whether the plan adds a foreign key of the dropped
// one's name.
func (l *linter) replacesKey(d dropForeignKey) bool {
	return slices.ContainsFunc(l.cs, func(c Change) bool {
		a, ok := c.(addForeignKey)
		return ok && l.canon(a.Table) == l.canon(d.Table) && a.ForeignKey.Name != "" && a.ForeignKey.Name == d.ForeignKey.Name
	})
}

// replacesCheck reports whether the plan adds a check of the dropped one's
// name, or one with its expression once the plan's column renames are applied
// to it (MySQL drops a check around the rename of its column).
func (l *linter) replacesCheck(d dropCheck) bool {
	expr := d.Check.Expr
	for _, c := range l.cs {
		if r, ok := c.(renameColumn); ok && l.canon(r.Table) == l.canon(d.Table) {
			expr = renameIdent(l.d, expr, r.From, r.To)
		}
	}
	return slices.ContainsFunc(l.cs, func(c Change) bool {
		a, ok := c.(addCheck)
		return ok && l.canon(a.Table) == l.canon(d.Table) && (a.Check.Name != "" && a.Check.Name == d.Check.Name || a.Check.Expr == expr)
	})
}

func (l *linter) addCheck(i int, c addCheck) {
	if l.existing(c.Table) {
		target := checkObject(c.Table, c.Check)
		// A check that replaces one over the same column, listing fewer
		// values, removes values from an enum.
		for _, o := range l.cs {
			if d, ok := o.(dropCheck); ok && l.canon(d.Table) == l.canon(c.Table) && c.Check.Name != "" && d.Check.Name == c.Check.Name {
				if from, ok := l.checkEnum(d.Check.Expr); ok {
					if to, ok := l.checkEnum(c.Check.Expr); ok {
						l.enumDelta(i, target, c.Check.Pos, from, to, enumCheck, "check "+target)
					}
				}
			}
		}
		more := ""
		if l.d == velox.MySQL {
			more = "; MySQL copies the table"
		}
		what := "check " + target
		if c.Check.Name == "" {
			what = checkLabel(c.Check) + " on " + c.Table.String()
		}
		l.add(i, "add-check", Review, target, c.Check.Pos, "adds %s; it fails if any row breaks it%s", what, more)
	}
	l.rebuildFinding(i, "adds %s", checkNoun(c.Check))
}

// checkEnum reads the values of a check of the form `"col" IN ('a', 'b')`.
func (l *linter) checkEnum(expr string) ([]string, bool) {
	_, vs, ok := ddl.ParseEnumCheck(l.d, expr)
	return vs, ok
}

// literalDefault reports whether a default is a string literal, a number,
// true, false or NULL, whatever else is an expression.
func literalDefault(d velox.Dialect, s string) bool {
	switch strings.ToLower(s) {
	case "true", "false", "null":
		return true
	}
	if _, ok := ddl.Unquote(d, s); ok {
		return true
	}
	if s == "" || !strings.ContainsAny(s[:1], "+-.0123456789") {
		return false
	}
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

// sqliteAffinity is the affinity SQLite gives a column of type typ, by its
// rules in order.
func sqliteAffinity(typ string) string {
	t := strings.ToUpper(typ)
	has := func(words ...string) bool {
		return slices.ContainsFunc(words, func(w string) bool { return strings.Contains(t, w) })
	}
	switch {
	case has("INT"):
		return "INTEGER"
	case has("CHAR", "CLOB", "TEXT"):
		return "TEXT"
	case has("BLOB") || t == "":
		return "BLOB"
	case has("REAL", "FLOA", "DOUB"): //nolint:misspell // SQLite's rule is these substrings
		return "REAL"
	}
	return "NUMERIC"
}

// sqliteKeepsValues reports whether a row copied from a column of type from
// into one of type to keeps its value: the same affinity; BLOB, which
// converts nothing; or a number going to INTEGER or NUMERIC, which hold a
// number as it is. TEXT to a number turns '007' into 7, a number to TEXT 7
// into '7', and INTEGER to REAL rounds above 2^53; BLOB to anything converts
// what the column stored as text or a number.
func sqliteKeepsValues(from, to string) bool {
	a, b := sqliteAffinity(from), sqliteAffinity(to)
	return a == b || b == "BLOB" || (a == "INTEGER" || a == "NUMERIC" || a == "REAL") && (b == "INTEGER" || b == "NUMERIC")
}

// widening says whether changing a column from type from to type to only
// makes room: Safe when the dialect does it without rewriting the table,
// Review when it rewrites. Types are in the canonical spelling.
func widening(d velox.Dialect, from, to string) (Class, bool) {
	switch d {
	case velox.Postgres:
		ints := []string{"smallint", "integer", "bigint"}
		if a, b := slices.Index(ints, from), slices.Index(ints, to); a >= 0 && b > a {
			return Review, true
		}
		if from == "real" && to == "double precision" {
			return Review, true
		}
		// Unbounded character varying is text.
		unbounded := func(t string) bool { return t == "text" || t == "character varying" }
		if unbounded(from) && unbounded(to) {
			return Safe, true
		}
		if n, ok := sizeOf(from, "character varying"); ok {
			m, sized := sizeOf(to, "character varying")
			if unbounded(to) || sized && m > n {
				return Safe, true
			}
		}
	case velox.MySQL:
		base := func(t string) (string, bool) {
			t, unsigned := strings.CutSuffix(t, " unsigned")
			if t == "integer" {
				t = "int"
			}
			return t, unsigned
		}
		a, sa := base(from)
		b, sb := base(to)
		for _, family := range [][]string{
			{"tinyint", "smallint", "mediumint", "int", "bigint"},
			{"float", "double"},
			{"tinytext", "text", "mediumtext", "longtext"},
			{"tinyblob", "blob", "mediumblob", "longblob"},
		} {
			if x, y := slices.Index(family, a), slices.Index(family, b); x >= 0 && y > x && sa == sb {
				return Review, true
			}
		}
		if n, ok := sizeOf(from, "varchar"); ok {
			if m, ok := sizeOf(to, "varchar"); ok && m > n {
				return Review, true
			}
		}
	}
	return 0, false
}

// sizeOf is n of a type of the form base(n).
func sizeOf(t, base string) (int, bool) {
	n, ok := strings.CutPrefix(t, base+"(")
	if !ok {
		return 0, false
	}
	n, ok = strings.CutSuffix(n, ")")
	if !ok {
		return 0, false
	}
	v, err := strconv.Atoi(n)
	return v, err == nil
}
