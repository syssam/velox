package migrate

import (
	"fmt"
	"slices"
	"strings"

	"github.com/syssam/velox"
)

// applyChanges is s with changes applied as a model: what a database that
// held s would hold after the migration, for the invariants. It fails on a
// change that names what is not there.
func applyChanges(s *Schema, changes []Change) (*Schema, error) {
	s = s.clone()
	for i, c := range changes {
		err := apply(s, c)
		if err == nil {
			err = valid(s)
		}
		if err != nil {
			return nil, fmt.Errorf("change %d (%s %v): %w", i, c.Kind(), c.Target(), err)
		}
	}
	s.sort()
	return s, nil
}

func apply(s *Schema, c Change) error {
	switch c := c.(type) {
	case CreateTable:
		if _, ok := s.Table(c.Table.Ref()); ok {
			return fmt.Errorf("the table exists")
		}
		s.Tables = append(s.Tables, c.Table)
		s.Tables = s.clone().Tables // own the new table's lists
		return nil
	case DropTable:
		n := len(s.Tables)
		s.Tables = slices.DeleteFunc(s.Tables, func(t Table) bool { return t.Ref() == c.Table.Ref() })
		if len(s.Tables) == n {
			return fmt.Errorf("no such table")
		}
		for _, t := range s.Tables {
			for _, fk := range t.ForeignKeys {
				// SQLite keeps a cycle's keys inline, and drops a referenced table.
				if fk.RefTable == c.Table.Ref() && s.Dialect != velox.SQLite {
					return fmt.Errorf("%s.%s still references it", t.Name, fk.Name)
				}
			}
		}
		return nil
	case RenameTable:
		t, err := tableOf(s, c.From)
		if err != nil {
			return err
		}
		if _, ok := s.Table(c.To); ok {
			return fmt.Errorf("%v exists", c.To)
		}
		t.Schema, t.Name = c.To.Schema, c.To.Name
		for i := range s.Tables {
			for j := range s.Tables[i].ForeignKeys {
				if fk := &s.Tables[i].ForeignKeys[j]; fk.RefTable == c.From {
					fk.RefTable = c.To
				}
			}
		}
		return nil
	case CreateEnum:
		if slices.ContainsFunc(s.Enums, func(e Enum) bool { return e.ref() == c.Enum.ref() }) {
			return fmt.Errorf("the enum exists")
		}
		e := c.Enum
		e.Values = slices.Clone(e.Values)
		s.Enums = append(s.Enums, e)
		return nil
	case DropEnum:
		n := len(s.Enums)
		s.Enums = slices.DeleteFunc(s.Enums, func(e Enum) bool { return e.ref() == c.Enum.ref() })
		if len(s.Enums) == n {
			return fmt.Errorf("no such enum")
		}
		typ := enumTypeName(c.Enum.ref())
		for _, t := range s.Tables {
			for _, col := range t.Columns {
				if col.Type == typ {
					return fmt.Errorf("%s.%s still uses it", t.Name, col.Name)
				}
			}
		}
		return nil
	case RenameEnum:
		e, err := enumOf(s, c.From)
		if err != nil {
			return err
		}
		e.Schema, e.Name = c.To.Schema, c.To.Name
		from, to := enumTypeName(c.From), enumTypeName(c.To)
		for i := range s.Tables {
			for j := range s.Tables[i].Columns {
				if col := &s.Tables[i].Columns[j]; col.Type == from {
					col.Type = to
				}
			}
		}
		return nil
	case AlterEnum:
		e, err := enumOf(s, c.From.ref())
		if err != nil {
			return err
		}
		if !slices.Equal(e.Values, c.From.Values) {
			return fmt.Errorf("the values are %q, not %q", e.Values, c.From.Values)
		}
		e.Values = slices.Clone(c.To.Values)
		return nil
	}

	t, err := tableOf(s, c.Target())
	if err != nil {
		return err
	}
	switch c := c.(type) {
	case AddColumn:
		if colOf(t, c.Column.Name) != nil {
			return fmt.Errorf("column %s exists", c.Column.Name)
		}
		t.Columns = append(t.Columns, c.Column)
	case DropColumn:
		if err := mysqlColumnUse(s.Dialect, t, c.Column.Name, true); err != nil {
			return err
		}
		n := len(t.Columns)
		t.Columns = slices.DeleteFunc(t.Columns, func(col Column) bool { return col.Name == c.Column.Name })
		if len(t.Columns) == n {
			return fmt.Errorf("no column %s", c.Column.Name)
		}
		if s.Dialect == velox.MySQL { // MySQL shrinks the indexes on the column
			for i := range t.Indexes {
				t.Indexes[i].Columns = slices.DeleteFunc(t.Indexes[i].Columns, func(col string) bool { return col == c.Column.Name })
			}
		}
	case RenameColumn:
		if err := mysqlColumnUse(s.Dialect, t, c.From, false); err != nil {
			return err
		}
		col := colOf(t, c.From)
		if col == nil || colOf(t, c.To) != nil {
			return fmt.Errorf("cannot rename column %s to %s", c.From, c.To)
		}
		col.Name = c.To
		rename := func(names []string) {
			for i := range names {
				if names[i] == c.From {
					names[i] = c.To
				}
			}
		}
		rename(t.PrimaryKey)
		for i := range t.Indexes {
			rename(t.Indexes[i].Columns)
			t.Indexes[i].Where = renameIdent(s.Dialect, t.Indexes[i].Where, c.From, c.To)
		}
		for i := range t.Checks {
			t.Checks[i].Expr = renameIdent(s.Dialect, t.Checks[i].Expr, c.From, c.To)
		}
		for i := range t.ForeignKeys {
			rename(t.ForeignKeys[i].Columns)
		}
		for i := range s.Tables {
			for j := range s.Tables[i].ForeignKeys {
				if fk := &s.Tables[i].ForeignKeys[j]; fk.RefTable == t.Ref() {
					rename(fk.RefColumns)
				}
			}
		}
	case AlterColumn:
		col := colOf(t, c.From.Name)
		if col == nil {
			return fmt.Errorf("no column %s", c.From.Name)
		}
		if c.What == 0 || c.From.Name != c.To.Name {
			return fmt.Errorf("an empty or renaming alter")
		}
		if c.What&ColumnType != 0 {
			col.Type = c.To.Type
		}
		if c.What&ColumnNullable != 0 {
			col.Nullable = c.To.Nullable
		}
		if c.What&ColumnDefault != 0 {
			col.Default = c.To.Default
		}
		if c.What&ColumnIdentity != 0 {
			col.Identity, col.Sequence = c.To.Identity, c.To.Sequence
		}
		if c.What&ColumnComment != 0 {
			col.Comment = c.To.Comment
		}
	case RenameSequence:
		col := colOf(t, c.Column)
		if col == nil || col.Sequence != c.From {
			return fmt.Errorf("no sequence %s on %s", c.From, c.Column)
		}
		col.Sequence = c.To
	case AddIndex:
		if slices.ContainsFunc(t.Indexes, func(ix Index) bool { return ix.Name == c.Index.Name }) {
			return fmt.Errorf("index %s exists", c.Index.Name)
		}
		ix := c.Index
		ix.Columns = slices.Clone(ix.Columns)
		t.Indexes = append(t.Indexes, ix)
	case DropIndex:
		n := len(t.Indexes)
		t.Indexes = slices.DeleteFunc(t.Indexes, func(ix Index) bool { return ix.Name == c.Index.Name })
		if len(t.Indexes) == n {
			return fmt.Errorf("no index %s", c.Index.Name)
		}
	case RenameIndex:
		i := slices.IndexFunc(t.Indexes, func(ix Index) bool { return ix.Name == c.From })
		if i < 0 {
			return fmt.Errorf("no index %s", c.From)
		}
		t.Indexes[i].Name = c.To
	case AddForeignKey:
		if slices.ContainsFunc(t.ForeignKeys, func(fk ForeignKey) bool { return fk.Name == c.ForeignKey.Name }) {
			return fmt.Errorf("foreign key %s exists", c.ForeignKey.Name)
		}
		fk := c.ForeignKey
		fk.Columns, fk.RefColumns = slices.Clone(fk.Columns), slices.Clone(fk.RefColumns)
		t.ForeignKeys = append(t.ForeignKeys, fk)
	case DropForeignKey:
		n := len(t.ForeignKeys)
		t.ForeignKeys = slices.DeleteFunc(t.ForeignKeys, func(fk ForeignKey) bool { return fk.Name == c.ForeignKey.Name })
		if len(t.ForeignKeys) == n {
			return fmt.Errorf("no foreign key %s", c.ForeignKey.Name)
		}
	case AddCheck:
		if slices.ContainsFunc(t.Checks, func(ck Check) bool { return ck.Name == c.Check.Name }) {
			return fmt.Errorf("check %s exists", c.Check.Name)
		}
		t.Checks = append(t.Checks, c.Check)
	case DropCheck:
		n := len(t.Checks)
		t.Checks = slices.DeleteFunc(t.Checks, func(ck Check) bool { return ck.Name == c.Check.Name })
		if len(t.Checks) == n {
			return fmt.Errorf("no check %s", c.Check.Name)
		}
	case RenameConstraint:
		switch {
		case t.PrimaryKeyName == c.From:
			t.PrimaryKeyName = c.To
		case slices.ContainsFunc(t.ForeignKeys, func(fk ForeignKey) bool { return fk.Name == c.From }):
			t.ForeignKeys[slices.IndexFunc(t.ForeignKeys, func(fk ForeignKey) bool { return fk.Name == c.From })].Name = c.To
		case slices.ContainsFunc(t.Checks, func(ck Check) bool { return ck.Name == c.From }):
			t.Checks[slices.IndexFunc(t.Checks, func(ck Check) bool { return ck.Name == c.From })].Name = c.To
		default:
			return fmt.Errorf("no constraint %s", c.From)
		}
	case SetTableComment:
		if t.Comment != c.From {
			return fmt.Errorf("the comment is %q, not %q", t.Comment, c.From)
		}
		t.Comment = c.To
	case SetTableCollation:
		if t.Collation != c.From {
			return fmt.Errorf("the collation is %q, not %q", t.Collation, c.From)
		}
		t.Collation = c.To
	default:
		return fmt.Errorf("unknown change %T", c)
	}
	return nil
}

// mysqlColumnUse is MySQL's refusal to drop or rename a column of t a check
// uses (error 3959), and to drop one from a unique index of more columns,
// which it would keep unique over the others.
func mysqlColumnUse(d velox.Dialect, t *Table, col string, drop bool) error {
	if d != velox.MySQL {
		return nil
	}
	for _, ck := range t.Checks {
		if namesColumn(d, ck.Expr, col) {
			return fmt.Errorf("check %s uses column %s", ck.Name, col)
		}
	}
	for _, ix := range t.Indexes {
		if drop && ix.Unique && len(ix.Columns) > 1 && slices.Contains(ix.Columns, col) {
			return fmt.Errorf("unique index %s would keep its other columns unique", ix.Name)
		}
	}
	return nil
}

func tableOf(s *Schema, ref TableRef) (*Table, error) {
	for i := range s.Tables {
		if s.Tables[i].Ref() == ref {
			return &s.Tables[i], nil
		}
	}
	return nil, fmt.Errorf("no table %v", ref)
}

func enumOf(s *Schema, ref TableRef) (*Enum, error) {
	for i := range s.Enums {
		if s.Enums[i].ref() == ref {
			return &s.Enums[i], nil
		}
	}
	return nil, fmt.Errorf("no enum %v", ref)
}

func colOf(t *Table, name string) *Column {
	for i := range t.Columns {
		if t.Columns[i].Name == name {
			return &t.Columns[i]
		}
	}
	return nil
}

// valid checks that what s's tables name exists: the columns of each
// primary key, index and foreign key, the table and columns a key references
// (a table may come later on SQLite, which resolves a reference when it is
// used), the enum type of each PostgreSQL enum column; and that no two
// tables, enums, or objects of one table share a name.
func valid(s *Schema) error {
	if err := namespaces(s); err != nil {
		return err
	}
	enums := map[string]bool{}
	for _, e := range s.Enums {
		typ := enumTypeName(e.ref())
		if enums[typ] {
			return fmt.Errorf("two enums %s", typ)
		}
		enums[typ] = true
	}
	tables := map[TableRef]bool{}
	for i := range s.Tables {
		t := &s.Tables[i]
		if tables[t.Ref()] {
			return fmt.Errorf("two tables %v", t.Ref())
		}
		tables[t.Ref()] = true
		has := func(what string, cols []string) error {
			for _, c := range cols {
				if t.column(c) == nil {
					return fmt.Errorf("%s: %s has no column %s", what, t.Name, c)
				}
			}
			return nil
		}
		names := map[string]bool{}
		for _, c := range t.Columns {
			if names[c.Name] {
				return fmt.Errorf("%s has two columns %s", t.Name, c.Name)
			}
			names[c.Name] = true
			if s.Dialect == velox.Postgres && strings.HasPrefix(c.Type, `"`) && !enums[c.Type] {
				return fmt.Errorf("%s.%s is of the missing type %s", t.Name, c.Name, c.Type)
			}
		}
		if err := has("the primary key", t.PrimaryKey); err != nil {
			return err
		}
		names = map[string]bool{}
		for _, ix := range t.Indexes {
			if names[ix.Name] {
				return fmt.Errorf("%s has two indexes %s", t.Name, ix.Name)
			}
			names[ix.Name] = true
			if err := has("index "+ix.Name, ix.Columns); err != nil {
				return err
			}
		}
		names = map[string]bool{}
		for _, fk := range t.ForeignKeys {
			if names[fk.Name] {
				return fmt.Errorf("%s has two constraints %s", t.Name, fk.Name)
			}
			names[fk.Name] = true
			if err := has("foreign key "+fk.Name, fk.Columns); err != nil {
				return err
			}
			if s.Dialect == velox.MySQL && !covered(t, fk.Columns) {
				return fmt.Errorf("foreign key %s of %s has no index", fk.Name, t.Name)
			}
			r := s.table(fk.RefTable)
			if r == nil {
				if s.Dialect != velox.SQLite {
					return fmt.Errorf("foreign key %s references the missing table %v", fk.Name, fk.RefTable)
				}
				continue
			}
			for _, c := range fk.RefColumns {
				if r.column(c) == nil {
					return fmt.Errorf("foreign key %s references the missing column %v.%s", fk.Name, fk.RefTable, c)
				}
			}
		}
		for _, ck := range t.Checks {
			if names[ck.Name] {
				return fmt.Errorf("%s has two constraints %s", t.Name, ck.Name)
			}
			names[ck.Name] = true
		}
	}
	return nil
}

// covered reports whether the primary key or an index of t leads with cols,
// as MySQL needs for a foreign key.
func covered(t *Table, cols []string) bool {
	leads := func(ix []string) bool { return len(ix) >= len(cols) && slices.Equal(ix[:len(cols)], cols) }
	return leads(t.PrimaryKey) || slices.ContainsFunc(t.Indexes, func(ix Index) bool { return leads(ix.Columns) })
}

// namespaces checks the names the dialect keeps across tables: index names
// per schema on PostgreSQL and SQLite, foreign-key and check names per
// schema on PostgreSQL and MySQL.
func namespaces(s *Schema) error {
	type key struct{ schema, name string }
	indexes, constraints := map[key]string{}, map[key]string{}
	for _, t := range s.Tables {
		if s.Dialect != velox.MySQL {
			for _, ix := range t.Indexes {
				k := key{t.Schema, ix.Name}
				if o, ok := indexes[k]; ok {
					return fmt.Errorf("index %s is on %s and %s", ix.Name, o, t.Name)
				}
				indexes[k] = t.Name
			}
		}
		if s.Dialect == velox.SQLite {
			continue
		}
		var names []string
		for _, fk := range t.ForeignKeys {
			names = append(names, fk.Name)
		}
		for _, ck := range t.Checks {
			names = append(names, ck.Name)
		}
		for _, n := range names {
			k := key{t.Schema, n}
			if o, ok := constraints[k]; ok && o != t.Name {
				return fmt.Errorf("constraint %s is on %s and %s", n, o, t.Name)
			}
			constraints[k] = t.Name
		}
	}
	return nil
}
