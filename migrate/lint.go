package migrate

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/ddl"
)

// A Class says how much care a change needs. A change with no finding is
// safe.
type Class uint8

const (
	Safe Class = iota
	Review
	Destructive
)

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

// destructive are the codes an Allow can name.
var destructive = []string{
	"drop-table", "drop-column", "alter-type", "set-not-null", "add-required-column",
	"add-unique", "drop-enum-value", "reorder-enum", "convert-collation",
}

// ParseAllow reads the items of the -allow flag, joined by commas, each a
// destructive code and optionally "=" and the target it covers:
// "drop-column=users.name,drop-table". An empty item, an empty target, or a
// code that is not destructive is an *Error with Code "allow".
func ParseAllow(s string) ([]Allow, error) {
	var out []Allow
	for _, item := range strings.Split(s, ",") {
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
// it, or it renames one of from's. Lint does not change from or the changes.
func Lint(from *Schema, changes []Change) []Finding {
	if from == nil {
		from = &Schema{}
	}
	l := &linter{
		from: from, d: from.Dialect, cs: changes,
		renamed: map[TableRef]TableRef{}, enumFrom: map[TableRef]TableRef{},
		created: map[TableRef]bool{}, dropped: map[TableRef]bool{},
		rebuild: make([]bool, len(changes)), rebuilt: map[TableRef]bool{},
		quiet: make([]bool, len(changes)),
	}
	l.scan()
	for i, c := range changes {
		l.change(i, c)
	}
	return l.out
}

type linter struct {
	from *Schema
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
		case RenameTable:
			l.renamed[c.To] = c.From
		case RenameEnum:
			l.enumFrom[c.To] = c.From
		case CreateTable:
			l.created[c.Table.Ref()] = true
		case DropTable:
			l.dropped[c.Table.Ref()] = true
		}
	}
	// A foreign key dropped and added again as it was, around a column's
	// type change or an index's replacement, is no change of its own.
	for i, c := range l.cs {
		d, ok := c.(DropForeignKey)
		if !ok {
			continue
		}
		for j, a := range l.cs {
			if a, ok := a.(AddForeignKey); ok && l.canon(a.Table) == l.canon(d.Table) && l.sameKey(d.ForeignKey, a.ForeignKey) {
				l.quiet[i], l.quiet[j] = true, true
			}
		}
	}
	if l.d == velox.SQLite {
		for i, c := range l.cs {
			if l.rebuilds(c) {
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
// or drops one that something uses.
func (l *linter) rebuilds(c Change) bool {
	if l.dropped[l.canon(c.Target())] || !l.existing(c.Target()) {
		return false
	}
	switch c := c.(type) {
	case AlterColumn:
		return c.What&(ColumnType|ColumnNullable|ColumnDefault|ColumnIdentity) != 0
	case AddColumn:
		col := c.Column
		if col.Identity {
			return false
		}
		if col.Default == "" {
			return !col.Nullable
		}
		return !literalDefault(l.d, col.Default)
	case DropColumn:
		t := l.from.table(l.canon(c.Table))
		return t != nil && l.uses(t, c.Column.Name)
	case AddForeignKey, DropForeignKey, AddCheck, DropCheck, RenameConstraint:
		return true
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
	case DropTable:
		l.add(i, "drop-table", Destructive, c.Table.Ref().String(), "", "drops table %s and its rows", c.Table.Ref())
	case RenameTable:
		l.add(i, "rename-table", Review, c.To.String(), "", "renames table %s to %s; code that uses the old name breaks while old and new versions run together", c.From, c.To)
	case SetTableCollation:
		l.add(i, "convert-collation", Destructive, c.Table.String(), "", "converts table %s from collation %s to %s, a rewrite that can make values equal that were not", c.Table, c.From, c.To)
	case AddColumn:
		l.addColumn(i, c)
	case DropColumn:
		l.add(i, "drop-column", Destructive, colTarget(c.Table, c.Column.Name), "", "drops column %s and its data", colTarget(c.Table, c.Column.Name))
		l.rebuildFinding(i, "drops column %s, which a key, index or constraint uses", colTarget(c.Table, c.Column.Name))
	case RenameColumn:
		l.add(i, "rename-column", Review, colTarget(c.Table, c.To), "", "renames column %s to %s; code that uses the old name breaks while old and new versions run together", colTarget(c.Table, c.From), c.To)
	case AlterColumn:
		l.alterColumn(i, c)
	case AddIndex:
		l.addIndex(i, c)
	case DropIndex:
		l.dropIndex(i, c)
	case AddForeignKey:
		if l.existing(c.Table) && !l.quiet[i] {
			l.add(i, "add-foreign-key", Review, colTarget(c.Table, c.ForeignKey.Name), c.ForeignKey.Pos, "adds foreign key %s; it fails if rows have no match in %s", c.ForeignKey.Name, c.ForeignKey.RefTable)
		}
		l.rebuildFinding(i, "adds foreign key %s", c.ForeignKey.Name)
	case DropForeignKey:
		if !l.quiet[i] && !l.dropped[l.canon(c.Table)] && !l.rebuild[i] && !l.replacesKey(c) {
			l.add(i, "drop-foreign-key", Review, colTarget(c.Table, c.ForeignKey.Name), "", "drops foreign key %s; rows are no longer checked against %s", c.ForeignKey.Name, c.ForeignKey.RefTable)
		}
		l.rebuildFinding(i, "drops foreign key %s", c.ForeignKey.Name)
	case AddCheck:
		l.addCheck(i, c)
	case DropCheck:
		if !l.dropped[l.canon(c.Table)] && !l.rebuild[i] && !l.replacesCheck(c) {
			l.add(i, "drop-check", Review, colTarget(c.Table, c.Check.Name), "", "drops check %s; rows are no longer limited by it", c.Check.Name)
		}
		l.rebuildFinding(i, "drops check %s", c.Check.Name)
	case RenameConstraint:
		l.rebuildFinding(i, "renames constraint %s to %s", c.From, c.To)
	case AlterEnum:
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
	l.add(i, "sqlite-rebuild", Review, c.Target().String(), changePos(c), "%s; SQLite rebuilds table %s: it copies every row into a new table", fmt.Sprintf(what, args...), c.Target())
}

// changePos is the Pos of the element of to that c adds or alters; a dropped
// element has none.
func changePos(c Change) string {
	switch c := c.(type) {
	case AddColumn:
		return c.Column.Pos
	case AlterColumn:
		return c.To.Pos
	case AddForeignKey:
		return c.ForeignKey.Pos
	case AddCheck:
		return c.Check.Pos
	}
	return ""
}

func colTarget(t TableRef, name string) string { return t.String() + "." + name }

func (l *linter) addColumn(i int, c AddColumn) {
	col, target := c.Column, colTarget(c.Table, c.Column.Name)
	if l.existing(c.Table) {
		if !col.Nullable && col.Default == "" && !col.Identity {
			l.add(i, "add-required-column", Destructive, target, col.Pos, "adds column %s as NOT NULL with no default; it fails if the table has rows", target)
		}
		if l.d == velox.Postgres && col.Default != "" && !literalDefault(l.d, col.Default) {
			l.add(i, "volatile-default", Review, target, col.Pos, "adds column %s with default %s; if the expression is volatile, PostgreSQL rewrites the table under an exclusive lock", target, col.Default)
		}
	}
	l.rebuildFinding(i, "adds column %s, which SQLite cannot add in place", target)
}

func (l *linter) alterColumn(i int, c AlterColumn) {
	target := colTarget(c.Table, c.To.Name)
	if c.What&ColumnType != 0 && l.d != velox.SQLite {
		l.alterType(i, c, target)
	}
	if c.What&ColumnNullable != 0 && c.From.Nullable && !c.To.Nullable {
		l.add(i, "set-not-null", Destructive, target, c.To.Pos, "makes column %s NOT NULL; it fails if any row holds NULL", target)
	}
	if c.What&ColumnIdentity != 0 && l.d == velox.Postgres && !c.From.Identity && c.To.Identity {
		l.add(i, "add-identity", Review, target, c.To.Pos, "adds an identity to column %s and sets its counter past the column's largest value", target)
		if strings.HasPrefix(c.From.Default, "nextval(") {
			l.add(i, "serial-to-identity", Review, target, c.To.Pos, "turns serial column %s into an identity: its default and sequence are dropped", target)
		}
	}
	var what []string
	for _, w := range [...]struct {
		bit  ColumnChange
		name string
	}{{ColumnType, "type"}, {ColumnNullable, "nullability"}, {ColumnDefault, "default"}, {ColumnIdentity, "identity"}} {
		if c.What&w.bit != 0 {
			what = append(what, w.name)
		}
	}
	l.rebuildFinding(i, "changes the %s of column %s", strings.Join(what, ", "), target)
}

// alterType classifies a type change: an enum losing or moving values, a
// widening, or any other change.
func (l *linter) alterType(i int, c AlterColumn, target string) {
	from, to := c.From.Type, c.To.Type
	if l.d == velox.MySQL {
		if a, ok := mysqlEnum(from); ok {
			if b, ok := mysqlEnum(to); ok {
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
	l.add(i, "alter-type", Destructive, target, c.To.Pos, "changes column %s from %s to %s, which can fail or change values", target, from, to)
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

func (l *linter) alterEnum(i int, c AlterEnum) {
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

func (l *linter) addIndex(i int, c AddIndex) {
	if !l.existing(c.Table) {
		return
	}
	target := colTarget(c.Table, c.Index.Name)
	if c.Index.Unique {
		l.add(i, "add-unique", Destructive, target, c.Index.Pos, "adds unique index %s; it fails if rows hold duplicates", target)
		return
	}
	l.add(i, "add-index", Review, target, c.Index.Pos, "adds index %s; building it scans the table", target)
}

func (l *linter) dropIndex(i int, c DropIndex) {
	if l.dropped[l.canon(c.Table)] || l.rebuilt[l.canon(c.Table)] || l.replacesIndex(c) {
		return
	}
	target := colTarget(c.Table, c.Index.Name)
	l.add(i, "drop-index", Review, target, "", "drops index %s; queries that used it get slower", target)
}

// replacesIndex reports whether the plan adds an index that does what the
// dropped one did: its columns lead the new index's, under the same
// predicate, and a unique one is replaced by a unique one over the same
// columns.
func (l *linter) replacesIndex(d DropIndex) bool {
	for _, c := range l.cs {
		a, ok := c.(AddIndex)
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
func (l *linter) replacesKey(d DropForeignKey) bool {
	return slices.ContainsFunc(l.cs, func(c Change) bool {
		a, ok := c.(AddForeignKey)
		return ok && l.canon(a.Table) == l.canon(d.Table) && a.ForeignKey.Name == d.ForeignKey.Name
	})
}

// replacesCheck reports whether the plan adds a check of the dropped one's
// name, or one with its expression once the plan's column renames are applied
// to it (MySQL drops a check around the rename of its column).
func (l *linter) replacesCheck(d DropCheck) bool {
	expr := d.Check.Expr
	for _, c := range l.cs {
		if r, ok := c.(RenameColumn); ok && l.canon(r.Table) == l.canon(d.Table) {
			expr = renameIdent(l.d, expr, r.From, r.To)
		}
	}
	return slices.ContainsFunc(l.cs, func(c Change) bool {
		a, ok := c.(AddCheck)
		return ok && l.canon(a.Table) == l.canon(d.Table) && (a.Check.Name == d.Check.Name || a.Check.Expr == expr)
	})
}

func (l *linter) addCheck(i int, c AddCheck) {
	if l.existing(c.Table) {
		target := colTarget(c.Table, c.Check.Name)
		// A check that replaces one over the same column, listing fewer
		// values, removes values from an enum.
		for _, o := range l.cs {
			if d, ok := o.(DropCheck); ok && l.canon(d.Table) == l.canon(c.Table) && d.Check.Name == c.Check.Name {
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
		l.add(i, "add-check", Review, target, c.Check.Pos, "adds check %s; it fails if any row breaks it%s", target, more)
	}
	l.rebuildFinding(i, "adds check %s", c.Check.Name)
}

// checkEnum reads the values of a check of the form `"col" IN ('a', 'b')`.
func (l *linter) checkEnum(expr string) ([]string, bool) {
	_, list, ok := strings.Cut(expr, " IN (")
	if !ok || !strings.HasSuffix(list, ")") {
		return nil, false
	}
	return literalList(l.d, strings.TrimSuffix(list, ")"))
}

// mysqlEnum reads the values of the type enum('a','b').
func mysqlEnum(t string) ([]string, bool) {
	list, ok := strings.CutPrefix(t, "enum(")
	if !ok || !strings.HasSuffix(list, ")") {
		return nil, false
	}
	return literalList(velox.MySQL, strings.TrimSuffix(list, ")"))
}

// literalList reads string literals separated by commas.
func literalList(d velox.Dialect, s string) ([]string, bool) {
	var out []string
	for i := 0; ; {
		for i < len(s) && s[i] == ' ' {
			i++
		}
		if i >= len(s) || s[i] != '\'' {
			return nil, false
		}
		j := stringEnd(s, i, d == velox.MySQL)
		v, ok := ddl.Unquote(d, s[i:j])
		if !ok {
			return nil, false
		}
		out = append(out, v)
		for j < len(s) && s[j] == ' ' {
			j++
		}
		if j == len(s) {
			return out, true
		}
		if s[j] != ',' {
			return nil, false
		}
		i = j + 1
	}
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
