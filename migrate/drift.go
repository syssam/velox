package migrate

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/ddl"
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
	table   TableRef
	object  string // as a Warning's Object: "users", "users.users_x"
	message string // as a Warning's Message
}

// drift compares an inspected schema and its notes with snapshot: the
// changes that would bring the database to snapshot, and the warnings (each
// note on a table of snapshot among them). Drift calls it after inspecting.
//
// Only the tables snapshot has are compared, and the enum types they use. On
// a copy of inspected, what differs only in spelling takes snapshot's
// spelling before Diff compares the two: a predicate or check that
// samePredicateIn calls the same with the enum types of both, an expression
// default (with a warning when it does not), a predicate or check the
// normaliser cannot read (with a warning when the texts differ), MySQL's
// RESTRICT for no action, the names SQLite does not keep, matched by
// definition, and a primary key Diff would refuse (with a warning).
func drift(inspected *Schema, notes []note, snapshot *Schema) (*DriftReport, error) {
	if inspected == nil || snapshot == nil {
		return nil, &Error{Code: "nil", Reason: "drift needs the inspected schema and the snapshot"}
	}
	if err := snapshot.validate("the snapshot"); err != nil {
		return nil, err
	}
	if !knownDialect(inspected.Dialect) {
		return nil, &Error{Code: "dialect", Reason: fmt.Sprintf("the database has the unknown dialect %q", inspected.Dialect.String())}
	}
	if inspected.Dialect != snapshot.Dialect {
		return nil, &Error{Code: "dialect", Reason: fmt.Sprintf("the database is %s and the snapshot %s", inspected.Dialect, snapshot.Dialect)}
	}
	c := &drifter{d: snapshot.Dialect, db: inspected.clone(), snap: snapshot,
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
	for _, n := range notes {
		if snapshot.table(n.table) != nil {
			c.warnings = append(c.warnings, Warning{Object: n.object, Message: n.message})
		}
	}
	changes, err := Diff(c.db, snapshot, DiffOptions{})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(c.warnings, func(a, b Warning) int {
		return cmp.Or(cmp.Compare(a.Object, b.Object), cmp.Compare(a.Message, b.Message))
	})
	return &DriftReport{Changes: changes, Warnings: c.warnings}, nil
}

// drifter holds one drift: db is the copy of the inspected schema it
// changes.
type drifter struct {
	d        velox.Dialect
	db, snap *Schema
	enums    map[string]bool // the enum types of both, as enumCasts
	warnings []Warning
}

// same is samePredicateIn with the enum types of both schemas.
func (c *drifter) same(a, b string) (same, comparable bool) {
	return samePredicateIn(c.d, a, b, c.enums)
}

func (c *drifter) warn(object, format string, args ...any) {
	c.warnings = append(c.warnings, Warning{Object: object, Message: fmt.Sprintf(format, args...)})
}

// table gives t, a table of the copy, u's spellings where they differ only
// in spelling.
func (c *drifter) table(t, u *Table) {
	ref := t.Ref().String()
	c.names(t, u)

	if !slices.Equal(t.PrimaryKey, u.PrimaryKey) {
		c.warn(ref, "the database has %s and the snapshot %s; change it by hand", keyText(t.PrimaryKey), keyText(u.PrimaryKey))
		t.PrimaryKey = slices.Clone(u.PrimaryKey)
	}
	if (t.PrimaryKeyName == "") != (u.PrimaryKeyName == "") {
		c.warn(ref, "the database names the primary key %q and the snapshot %q; change it by hand", t.PrimaryKeyName, u.PrimaryKeyName)
		t.PrimaryKeyName = u.PrimaryKeyName
	}

	for i := range t.Columns {
		tc := &t.Columns[i]
		uc := u.column(tc.Name)
		if uc != nil && tc.Default != uc.Default && castLiteral(tc.Default) && literalDefault(c.d, uc.Default) {
			// A time literal as PostgreSQL prints it: one value, or a change.
			if same, ok := c.same(tc.Default, uc.Default); same && ok {
				tc.Default = uc.Default
			}
			continue
		}
		if uc == nil || tc.Default == uc.Default || tc.Default == "" || uc.Default == "" ||
			literalDefault(c.d, tc.Default) || literalDefault(c.d, uc.Default) {
			continue
		}
		if same, _ := c.same(tc.Default, uc.Default); !same {
			c.warn(ref+"."+tc.Name, "the default is %s in the database and %s in the snapshot", tc.Default, uc.Default)
		}
		tc.Default = uc.Default
	}

	for i := range t.Indexes {
		ti := &t.Indexes[i]
		ui := u.index(ti.Name)
		if ui == nil || ti.Where == ui.Where || ti.Where == "" || ui.Where == "" ||
			ti.Unique != ui.Unique || !slices.Equal(ti.Columns, ui.Columns) {
			continue
		}
		same, comparable := c.same(ti.Where, ui.Where)
		switch {
		case !comparable && !same:
			c.warn(ref+"."+ti.Name, "the predicate is %s in the database and %s in the snapshot; one cannot be parsed, so they are not compared", ti.Where, ui.Where)
		case !same:
			continue // another predicate: Diff rebuilds the index
		}
		ti.Where = ui.Where
	}

	for i := range t.Checks {
		tc := &t.Checks[i]
		uc := u.check(tc.Name)
		if uc == nil || tc.Expr == uc.Expr {
			continue
		}
		col, vs, ok := ddl.ParseEnumCheck(c.d, tc.Expr)
		ucol, uvs, uok := ddl.ParseEnumCheck(c.d, uc.Expr)
		if ok && uok {
			if col == ucol && slices.Equal(vs, uvs) {
				tc.Expr = uc.Expr
			}
			continue
		}
		same, comparable := c.same(tc.Expr, uc.Expr)
		switch {
		case same:
		case !comparable:
			c.warn(ref+"."+tc.Name, "the check is %s in the database and %s in the snapshot; one cannot be parsed, so they are not compared", tc.Expr, uc.Expr)
		default:
			continue // another check: Diff drops and adds it
		}
		tc.Expr = uc.Expr
	}

	if c.d == velox.MySQL {
		for i := range t.ForeignKeys {
			tf := &t.ForeignKeys[i]
			if uf := u.foreignKey(tf.Name); uf != nil && restricts(tf.OnDelete) && restricts(uf.OnDelete) {
				tf.OnDelete = uf.OnDelete
			}
		}
	}
}

// names gives the indexes, foreign keys and checks of t named "", as SQLite
// keeps no name for some, the name of one of u of their definition that t
// lacks.
func (c *drifter) names(t, u *Table) {
	for i := range t.Indexes {
		ti := &t.Indexes[i]
		if ti.Name != "" {
			continue
		}
		for _, ui := range u.Indexes {
			if t.index(ui.Name) == nil && ti.Unique == ui.Unique && slices.Equal(ti.Columns, ui.Columns) && c.sameText(ti.Where, ui.Where) {
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
			if t.foreignKey(uf.Name) == nil && c.sameKey(*tf, uf) {
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
			if t.check(uc.Name) == nil && c.sameText(tc.Expr, uc.Expr) {
				tc.Name = uc.Name
				break
			}
		}
	}
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
	same, comparable := c.same(a, b)
	return same && comparable
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
