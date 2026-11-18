package migrate

import (
	"bytes"
	"cmp"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/ddl"
)

// A Warning is a difference between a database and its snapshot that is not
// a change: a spelling the database prints its own way, an expression
// default, a primary key Diff cannot change, or what the model has no place
// for.
type Warning struct {
	Object  string // "users.users_active_idx"
	Message string
}

// String is "<Object>: <Message>".
func (w Warning) String() string { return w.Object + ": " + w.Message }

// A DriftReport is what tells a database from its snapshot.
type DriftReport struct {
	Changes  []Change // what would bring db to the snapshot
	Warnings []Warning
}

// A note is what inspect saw that the model has no place for: a MySQL
// engine other than InnoDB, an invalid PostgreSQL index, a NOT VALID
// constraint, a GENERATED ALWAYS identity. The model holds no notes.
type note struct {
	table      TableRef
	object     string // as a Warning's Object: "users", "users.users_x"
	message    string // as a Warning's Message
	baseline   bool   // only Baseline reports it: what a build from the snapshot loses, not a difference; drift skips it
	keeps      []keep // what a statement that replaces the object must write to keep
	constraint bool   // the object is a constraint, not a column
}

// A keep is an attribute of a column the model cannot hold: what it is, and
// the words a MODIFY COLUMN writes to keep it, any of them.
type keep struct {
	what  string
	words []string
}

// drift compares an inspected schema and its notes with snapshot: the
// changes that would bring the database to snapshot, and the warnings (each
// note on a table of snapshot among them). Drift calls it after inspecting.
//
// Only the tables snapshot has are compared, and the enum types they use. On
// a copy of inspected, what differs only in spelling takes snapshot's
// spelling before Diff compares the two: a predicate or check that
// samePredicateIn calls the same with the enum types of both, an expression
// default (with a warning when it does not), a number default of one value
// on a number column (0.00 and 0), a date or time literal default of one
// value (PostgreSQL's cast, MySQL's padded), a predicate or check
// the normaliser cannot read (with a warning when the texts differ), MySQL's
// RESTRICT for no action, the names SQLite does not keep, matched by
// definition, and a primary key Diff would refuse (with a warning); on
// MySQL, a primary key's name, always PRIMARY there, and the index MySQL
// makes for a foreign key no index covers.
func drift(inspected *Schema, notes []note, snapshot *Schema) (*DriftReport, error) {
	c, err := compare(inspected, notes, snapshot, false)
	if err != nil {
		return nil, err
	}
	snap := snapshot
	if len(c.implicit) > 0 {
		snap = snapshot.clone()
		for _, im := range c.implicit {
			t := snap.table(im.table)
			t.Indexes = append(t.Indexes, im.index)
		}
		snap.sort()
	}
	changes, err := Diff(c.db, snap, DiffOptions{})
	if err != nil {
		return nil, err
	}
	sortWarnings(c.warnings)
	return &DriftReport{Changes: changes, Warnings: c.warnings}, nil
}

// compare is drift up to Diff: the checks of both schemas, the copy of
// inspected cut to snapshot's tables and the enum types they use or snapshot
// names, given snapshot's spellings by table, and the notes on snapshot's
// tables as warnings, baseline notes only when adopt, the notes on one object
// as one warning, joined with "; also, " in their order. In adopt mode,
// Baseline's, the copy keeps the database's form wherever drift would force
// snapshot's over a difference.
func compare(inspected *Schema, notes []note, snapshot *Schema, adopt bool) (*drifter, error) {
	if inspected == nil || snapshot == nil {
		return nil, &Error{Code: "nil", Reason: "drift needs the inspected schema and the snapshot"}
	}
	name := "the snapshot"
	if adopt {
		name = "the schema"
	}
	if err := snapshot.validate(name); err != nil {
		return nil, err
	}
	if !knownDialect(inspected.Dialect) {
		return nil, &Error{Code: "dialect", Reason: fmt.Sprintf("the database has the unknown dialect %q", inspected.Dialect.String())}
	}
	if inspected.Dialect != snapshot.Dialect {
		return nil, &Error{Code: "dialect", Reason: fmt.Sprintf("the database is %s and %s %s", inspected.Dialect, name, snapshot.Dialect)}
	}
	snapshot = snapshot.clone()
	snapshot.oneForm()
	c := &drifter{d: snapshot.Dialect, db: inspected.clone(), snap: snapshot, adopt: adopt, other: name,
		enums: enumCasts(&Schema{Dialect: snapshot.Dialect, Enums: append(slices.Clone(snapshot.Enums), inspected.Enums...)})}
	c.db.Tables = slices.DeleteFunc(c.db.Tables, func(t Table) bool { return snapshot.table(t.Ref()) == nil })
	c.db.Enums = slices.DeleteFunc(c.db.Enums, func(e Enum) bool {
		return snapshot.enum(e.ref()) == nil && len(usage(c.db, e.ref())) == 0
	})
	for i := range c.db.Tables {
		t := &c.db.Tables[i]
		c.table(t, snapshot.table(t.Ref()))
	}
	if err := c.db.validate("the database"); err != nil {
		return nil, err
	}
	noted := map[string]int{} // object -> the index in c.warnings of its note warning
	for _, n := range notes {
		if snapshot.table(n.table) == nil || (!adopt && n.baseline) {
			continue
		}
		if i, ok := noted[n.object]; ok {
			c.warnings[i].Message += "; also, " + n.message
			continue
		}
		noted[n.object] = len(c.warnings)
		c.warnings = append(c.warnings, Warning{Object: n.object, Message: n.message})
	}
	return c, nil
}

// sortWarnings sorts ws by object, then message.
func sortWarnings(ws []Warning) {
	slices.SortFunc(ws, func(a, b Warning) int {
		return cmp.Or(cmp.Compare(a.Object, b.Object), cmp.Compare(a.Message, b.Message))
	})
}

// drifter holds one drift: db is the copy of the inspected schema it
// changes.
type drifter struct {
	d        velox.Dialect
	db, snap *Schema
	adopt    bool            // Baseline's: db keeps its form where it differs
	other    string          // what the warnings call snap: "the snapshot", or Baseline's "the schema"
	enums    map[string]bool // the enum types of both, as enumCasts
	warnings []Warning
	implicit []implicitIndex // MySQL: the indexes it made for foreign keys
}

// implicitIndex is an index MySQL made for a foreign key of table.
type implicitIndex struct {
	table TableRef
	index Index
}

// same is samePredicateIn with the enum types of both schemas.
func (c *drifter) same(a, b string) (same, parsed bool) {
	return samePredicateIn(c.d, a, b, c.enums)
}

func (c *drifter) warn(object, format string, args ...any) {
	c.warnings = append(c.warnings, Warning{Object: object, Message: fmt.Sprintf(format, args...)})
}

// table gives t, a table of the copy, u's spellings where they differ only
// in spelling.
func (c *drifter) table(t, u *Table) {
	c.names(t, u)
	c.primaryKey(t, u)
	for i := range t.Columns {
		c.columnDefault(t, &t.Columns[i], u.column(t.Columns[i].Name))
	}
	ti, _ := pair(t.Indexes, u.Indexes, indexName, func(a, b Index) bool {
		return a.Unique == b.Unique && a.Method == b.Method && slices.Equal(a.Columns, b.Columns) && c.sameText(a.Where, b.Where)
	})
	for i := range t.Indexes {
		c.index(t, u, &t.Indexes[i], at(u.Indexes, ti[i]))
	}
	tc, _ := pair(t.Checks, u.Checks, checkName, func(a, b Check) bool { return c.sameCheck(a.Expr, b.Expr) })
	for i := range t.Checks {
		c.check(t, &t.Checks[i], at(u.Checks, tc[i]))
	}
	if c.d == velox.MySQL {
		c.mysqlForeignKeys(t, u)
	}
}

// primaryKey warns of a primary key, or its name, that differs from u's, and
// gives t u's unless the drifter adopts the database's.
func (c *drifter) primaryKey(t, u *Table) {
	ref := t.Ref().String()
	if !slices.Equal(t.PrimaryKey, u.PrimaryKey) {
		c.warn(ref, "the database has %s and %s %s; change it by hand", keyText(t.PrimaryKey), c.other, keyText(u.PrimaryKey))
		if !c.adopt {
			t.PrimaryKey = slices.Clone(u.PrimaryKey)
		}
	}
	if c.d == velox.MySQL && !c.adopt {
		// MySQL names every primary key PRIMARY and holds no other name.
		// Baseline keeps the database's, which has a key or none.
		t.PrimaryKeyName = u.PrimaryKeyName
	}
	if (t.PrimaryKeyName == "") != (u.PrimaryKeyName == "") && c.d != velox.MySQL {
		c.warn(ref, "the database names the primary key %q and %s %q; change it by hand", t.PrimaryKeyName, c.other, u.PrimaryKeyName)
		if !c.adopt {
			t.PrimaryKeyName = u.PrimaryKeyName
		}
	}
}

// columnDefault gives tc, a column of t, uc's default where the two differ
// only in spelling, and warns of one that differs in value. uc is nil when
// u has no such column.
func (c *drifter) columnDefault(t *Table, tc, uc *Column) {
	ref := t.Ref().String()
	if uc != nil && c.d == velox.MySQL && mysqlBinaryType(tc.Type) && mysqlBinaryType(uc.Type) &&
		mysqlBinaryLiteral(tc.Default) && mysqlBinaryLiteral(uc.Default) {
		// A binary literal default is its bytes, both sides decoded
		// alike: the same bytes, or a change; one not decoded is compared
		// as written. An expression is compared below.
		a, aok := mysqlBinaryBytes(tc.Type, tc.Default)
		b, bok := mysqlBinaryBytes(uc.Type, uc.Default)
		if aok && bok && bytes.Equal(a, b) {
			tc.Default = uc.Default
		}
		return
	}
	if uc != nil && tc.Default != uc.Default && numberType(c.d, uc.Type) && sameNumber(tc.Default, uc.Default) {
		tc.Default = uc.Default // 0.00 is 0
	}
	if uc != nil && tc.Default != uc.Default && c.d == velox.MySQL {
		// A time literal as MySQL prints it, padded: one value, or a change.
		if same, ok := mysqlSameTime(uc.Type, tc.Default, uc.Default); same && ok {
			tc.Default = uc.Default
		}
	}
	if uc != nil && tc.Default != uc.Default && castLiteral(tc.Default) && literalDefault(c.d, uc.Default) {
		// A time literal as PostgreSQL prints it: one value, or a change.
		if same, ok := c.same(tc.Default, uc.Default); same && ok {
			tc.Default = uc.Default
		}
		return
	}
	if uc == nil || tc.Default == uc.Default || tc.Default == "" || uc.Default == "" ||
		literalDefault(c.d, tc.Default) || literalDefault(c.d, uc.Default) {
		return
	}
	if same, _ := c.same(tc.Default, uc.Default); !same {
		c.warn(ref+"."+tc.Name, "the default is %s in the database and %s in %s", tc.Default, uc.Default, c.other)
		if c.adopt {
			return
		}
	}
	tc.Default = uc.Default
}

// index gives ti, an index of t, ui's predicate where the two differ only in
// spelling. ui is u's index paired with it, or nil.
func (c *drifter) index(t, u *Table, ti, ui *Index) {
	if ui == nil && c.adopt {
		c.adoptWhere(ti, u)
		return
	}
	// Diff ignores Constraint, but a drop renders from the snapshot's:
	// DROP INDEX of a constraint's index, or DROP CONSTRAINT of a plain
	// index, fails on this database.
	if ui != nil && !c.adopt && c.d == velox.Postgres && ti.Constraint != ui.Constraint {
		inDB, inSnap, drop := "a UNIQUE constraint", "an index", "DROP INDEX"
		if ui.Constraint {
			inDB, inSnap, drop = inSnap, inDB, "DROP CONSTRAINT"
		}
		c.warn(indexObject(t.Ref(), *ti), "the database has it as %s and %s as %s; a drop renders %s, which fails", inDB, c.other, inSnap, drop)
	}
	if ui == nil || ti.Where == ui.Where || ti.Where == "" || ui.Where == "" ||
		ti.Unique != ui.Unique || ti.Method != ui.Method || !slices.Equal(ti.Columns, ui.Columns) {
		return
	}
	same, parsed := c.same(ti.Where, ui.Where)
	switch {
	case !parsed && !same:
		c.warn(indexObject(t.Ref(), *ti), "the predicate is %s in the database and %s in %s; one cannot be parsed, so they are not compared", ti.Where, ui.Where, c.other)
		if c.adopt {
			return
		}
	case !same:
		return // another predicate: Diff rebuilds the index
	}
	ti.Where = ui.Where
}

// check gives tc, a check of t, uc's expression where the two differ only in
// spelling. uc is u's check paired with it, or nil.
func (c *drifter) check(t *Table, tc, uc *Check) {
	if uc == nil || tc.Expr == uc.Expr {
		return
	}
	col, vs, ok := ddl.ParseEnumCheck(c.d, tc.Expr)
	ucol, uvs, uok := ddl.ParseEnumCheck(c.d, uc.Expr)
	if ok && uok {
		if col == ucol && slices.Equal(vs, uvs) {
			tc.Expr = uc.Expr
		}
		return
	}
	same, parsed := c.same(tc.Expr, uc.Expr)
	switch {
	case same:
	case !parsed:
		c.warn(colTarget(t.Ref(), tc.Name), "the check is %s in the database and %s in %s; one cannot be parsed, so they are not compared", tc.Expr, uc.Expr, c.other)
		if c.adopt {
			return
		}
	default:
		return // another check: Diff drops and adds it
	}
	tc.Expr = uc.Expr
}

// mysqlForeignKeys compares t as if it had the index MySQL makes for a
// foreign key no index covers, and gives t's foreign keys u's spelling of a
// restricting ON DELETE.
func (c *drifter) mysqlForeignKeys(t, u *Table) {
	// The index MySQL makes for a foreign key no index covers, its name
	// and columns the key's: the snapshot is compared as if it had it.
	for _, ix := range t.Indexes {
		if fk := t.foreignKey(ix.Name); fk != nil && !ix.Unique && ix.Where == "" &&
			slices.Equal(ix.Columns, fk.Columns) && u.index(ix.Name) == nil && u.foreignKey(ix.Name) != nil {
			c.implicit = append(c.implicit, implicitIndex{t.Ref(), ix})
		}
	}
	for i := range t.ForeignKeys {
		tf := &t.ForeignKeys[i]
		if uf := u.foreignKey(tf.Name); uf != nil && restricts(tf.OnDelete) && restricts(uf.OnDelete) {
			tf.OnDelete = uf.OnDelete
		}
	}
}

// adoptWhere gives ti, an index u has no index of the name of, the
// predicate text of u's index of its columns, uniqueness and normalised
// predicate; ti keeps its name, which the next Diff renames by definition.
func (c *drifter) adoptWhere(ti *Index, u *Table) {
	if ti.Where == "" {
		return
	}
	for _, ui := range u.Indexes {
		if ui.Unique == ti.Unique && ui.Method == ti.Method && slices.Equal(ui.Columns, ti.Columns) && ui.Where != "" && c.sameText(ti.Where, ui.Where) {
			ti.Where = ui.Where
			return
		}
	}
}

// names gives the indexes, foreign keys and checks of t named "", as SQLite
// keeps no name for some, the name of one of u of their definition that t
// lacks. Baseline leaves an index unnamed: it is a UNIQUE constraint's,
// which the snapshot must hold as one, since no DROP INDEX drops it.
func (c *drifter) names(t, u *Table) {
	for i := range t.Indexes {
		if c.adopt {
			break
		}
		ti := &t.Indexes[i]
		if ti.Name != "" {
			continue
		}
		for _, ui := range u.Indexes {
			if ui.Name != "" && t.index(ui.Name) == nil && ti.Unique == ui.Unique && ti.Method == ui.Method && slices.Equal(ti.Columns, ui.Columns) && c.sameText(ti.Where, ui.Where) {
				ti.Name = ui.Name
				break
			}
		}
	}
	for i := range t.ForeignKeys {
		tf := &t.ForeignKeys[i]
		if tf.Name != "" {
			continue
		}
		for _, uf := range u.ForeignKeys {
			if uf.Name != "" && t.foreignKey(uf.Name) == nil && c.sameKey(*tf, uf) {
				tf.Name = uf.Name
				break
			}
		}
	}
	for i := range t.Checks {
		tc := &t.Checks[i]
		if tc.Name != "" {
			continue
		}
		for _, uc := range u.Checks {
			if uc.Name != "" && t.check(uc.Name) == nil && c.sameText(tc.Expr, uc.Expr) {
				tc.Name = uc.Name
				break
			}
		}
	}
}

// sameCheck reports whether two checks are one: the same enum check, or
// one by sameText.
func (c *drifter) sameCheck(a, b string) bool {
	col, vs, ok := ddl.ParseEnumCheck(c.d, a)
	ucol, uvs, uok := ddl.ParseEnumCheck(c.d, b)
	if ok && uok {
		return col == ucol && slices.Equal(vs, uvs)
	}
	return c.sameText(a, b)
}

// sameText reports whether two predicates or checks are one by their normal
// forms, both "" included.
func (c *drifter) sameText(a, b string) bool {
	if a == b {
		return true
	}
	if a == "" || b == "" {
		return false
	}
	same, parsed := c.same(a, b)
	return same && parsed
}

// sameKey is sameForeignKey, with MySQL's RESTRICT and no action one.
func (c *drifter) sameKey(a, b ForeignKey) bool {
	if c.d == velox.MySQL && restricts(a.OnDelete) && restricts(b.OnDelete) {
		a.OnDelete = b.OnDelete
	}
	return sameForeignKey(a, b)
}

// castLiteral reports whether s is a '…' literal and a cast.
func castLiteral(s string) bool {
	end := literalEnd(s)
	return end > 0 && strings.HasPrefix(s[end:], "::")
}

// restricts reports whether a MySQL ON DELETE is RESTRICT or no action, one
// action there.
func restricts(onDelete string) bool { return onDelete == "" || onDelete == "RESTRICT" }

// keyText is "the primary key (a, b)", or "no primary key".
func keyText(cols []string) string {
	if len(cols) == 0 {
		return "no primary key"
	}
	return "the primary key (" + strings.Join(cols, ", ") + ")"
}

// mysqlTimes are the layouts of sameTime for MySQL's date and time types.
var mysqlTimes = map[string]string{"date": "date", "datetime": "timestamp", "timestamp": "timestamp", "time": "time"}

// mysqlSameTime reports, when a and b are literals of the MySQL date or
// time type typ, whether they are one value: MySQL pads a datetime(6)'s
// '2020-01-01 00:00:00' to '2020-01-01 00:00:00.000000'. ok is false when
// typ is no such type or either is not one of its values.
func mysqlSameTime(typ, a, b string) (same, ok bool) {
	if i := strings.IndexByte(typ, '('); i >= 0 {
		typ = typ[:i]
	}
	layout, known := mysqlTimes[typ]
	va, oka := ddl.Unquote(velox.MySQL, a)
	vb, okb := ddl.Unquote(velox.MySQL, b)
	if !known || !oka || !okb {
		return false, false
	}
	return sameTime(layout, &predNode{kind: nodeLit, str: true, val: va}, &predNode{kind: nodeLit, str: true, val: vb})
}

// numberLiteral is a number literal as a default holds it.
var numberLiteral = regexp.MustCompile(`^[-+]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][-+]?[0-9]+)?$`)

// sameNumber reports whether a and b are number literals of one value:
// 0, 0.00 and 0e0.
func sameNumber(a, b string) bool {
	if !numberLiteral.MatchString(a) || !numberLiteral.MatchString(b) {
		return false
	}
	x, okx := new(big.Rat).SetString(strings.TrimPrefix(a, "+"))
	y, oky := new(big.Rat).SetString(strings.TrimPrefix(b, "+"))
	return okx && oky && x.Cmp(y) == 0
}

// numberTypes are the number types of each dialect, by their first word.
var numberTypes = map[string]bool{
	"smallint": true, "integer": true, "int": true, "bigint": true, "tinyint": true, "mediumint": true,
	"numeric": true, "decimal": true, "real": true, "double": true, "float": true,
	"int2": true, "int4": true, "int8": true, "float4": true, "float8": true,
}

// numberType reports whether typ, canonical for d, is a number type, whose
// literal default is its value.
func numberType(d velox.Dialect, typ string) bool {
	word := typ
	if i := strings.IndexAny(word, "( "); i >= 0 {
		word = word[:i]
	}
	return numberTypes[word] && (d != velox.Postgres || !strings.HasSuffix(typ, "[]"))
}
