package migrate

import (
	"cmp"
	"slices"

	"github.com/syssam/velox2"
)

// A Schema is the database as one dialect sees it. The struct tags on it and
// the types under it are the keys of the snapshot JSON.
type Schema struct {
	Dialect velox.Dialect
	Tables  []Table // sorted by (Schema, Name)
	Enums   []Enum  // PostgreSQL enum types, sorted by (Schema, Name); empty on other dialects
}

// A TableRef names a table.
type TableRef struct {
	Schema string `json:"schema,omitempty"` // the PostgreSQL schema or MySQL database; "" for the connection's default
	Name   string `json:"name"`
}

// A ColumnRef names a column.
type ColumnRef struct {
	Table  TableRef
	Column string
}

// A Table is a table and what hangs on it.
type Table struct {
	Schema         string       `json:"schema,omitempty"`
	Name           string       `json:"name"`
	Columns        []Column     `json:"columns,omitempty"`          // in creation order; compared by name, not by order
	PrimaryKey     []string     `json:"primary_key,omitempty"`      // column names, in key order
	PrimaryKeyName string       `json:"primary_key_name,omitempty"` // PostgreSQL "<table>_pkey" (capped); MySQL "PRIMARY"; SQLite ""
	Indexes        []Index      `json:"indexes,omitempty"`          // sorted by Name
	ForeignKeys    []ForeignKey `json:"foreign_keys,omitempty"`     // sorted by Name
	Checks         []Check      `json:"checks,omitempty"`           // sorted by Name
	Comment        string       `json:"comment,omitempty"`          // "" on SQLite
	Collation      string       `json:"collation,omitempty"`        // MySQL: the table's collation, "utf8mb4_bin" for planned tables; "" elsewhere
	Pos            string       `json:"-"`                          // the schema's file:line, when known; never serialized or compared
}

// A Column is a table's column.
type Column struct {
	Name     string `json:"name"`
	Type     string `json:"type"` // the canonical spelling for the dialect
	Nullable bool   `json:"nullable,omitempty"`
	Default  string `json:"default,omitempty"`  // the SQL of the DEFAULT clause, canonical; "" for none
	Identity bool   `json:"identity,omitempty"` // the database assigns the value: IDENTITY, AUTO_INCREMENT, or SQLite's rowid
	Sequence string `json:"sequence,omitempty"` // PostgreSQL: the sequence the column owns, an identity's or a serial's; "" otherwise
	Comment  string `json:"comment,omitempty"`  // "" on SQLite
	Pos      string `json:"-"`
}

// An Index is a secondary index.
type Index struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns,omitempty"`
	Unique  bool     `json:"unique,omitempty"`
	Where   string   `json:"where,omitempty"` // the predicate of a partial index; "" for none; never on MySQL
	Pos     string   `json:"-"`
}

// A ForeignKey is a foreign key constraint.
type ForeignKey struct {
	Name       string   `json:"name"`
	Columns    []string `json:"columns,omitempty"`
	RefTable   TableRef `json:"ref_table"`
	RefColumns []string `json:"ref_columns,omitempty"`
	OnDelete   string   `json:"on_delete,omitempty"` // "", "CASCADE", "SET NULL" or "RESTRICT"; "" is NO ACTION
	Pos        string   `json:"-"`
}

// A Check is a CHECK constraint.
type Check struct {
	Name string `json:"name"`
	Expr string `json:"expr"` // canonical
	Pos  string `json:"-"`
}

// An Enum is a PostgreSQL CREATE TYPE … AS ENUM.
type Enum struct {
	Schema string   `json:"schema,omitempty"`
	Name   string   `json:"name"`
	Values []string `json:"values,omitempty"` // in the type's order
	Pos    string   `json:"-"`
}

// Ref is the table's name.
func (t *Table) Ref() TableRef { return TableRef{Schema: t.Schema, Name: t.Name} }

// String is "billing.invoices", or "invoices" in the default schema.
func (r TableRef) String() string {
	if r.Schema == "" {
		return r.Name
	}
	return r.Schema + "." + r.Name
}

// Table finds a table by its name. The table it returns must not be modified.
func (s *Schema) Table(ref TableRef) (*Table, bool) {
	for i := range s.Tables {
		if t := &s.Tables[i]; t.Schema == ref.Schema && t.Name == ref.Name {
			return t, true
		}
	}
	return nil, false
}

// Equal reports whether s and t hold the same database. It ignores Pos and
// column order, treats a nil and an empty slice alike, and compares
// everything else exactly, Dialect included.
func (s *Schema) Equal(t *Schema) bool {
	if s == nil || t == nil {
		return s == t
	}
	if s.Dialect != t.Dialect {
		return false
	}
	// The lists are keyed, so compare them in key order whatever order the
	// caller built them in.
	a, b := s.clone(), t.clone()
	a.sort()
	b.sort()
	for _, x := range [...]*Schema{a, b} {
		for i := range x.Tables {
			slices.SortStableFunc(x.Tables[i].Columns, func(p, q Column) int { return cmp.Compare(p.Name, q.Name) })
		}
	}
	return slices.EqualFunc(a.Tables, b.Tables, tableEqual) && slices.EqualFunc(a.Enums, b.Enums, enumEqual)
}

func tableEqual(a, b Table) bool {
	return a.Schema == b.Schema && a.Name == b.Name &&
		a.PrimaryKeyName == b.PrimaryKeyName && a.Comment == b.Comment && a.Collation == b.Collation &&
		slices.Equal(a.PrimaryKey, b.PrimaryKey) &&
		slices.EqualFunc(a.Columns, b.Columns, func(x, y Column) bool {
			return x.Name == y.Name && x.Type == y.Type && x.Nullable == y.Nullable && x.Default == y.Default &&
				x.Identity == y.Identity && x.Sequence == y.Sequence && x.Comment == y.Comment
		}) &&
		slices.EqualFunc(a.Indexes, b.Indexes, func(x, y Index) bool {
			return x.Name == y.Name && x.Unique == y.Unique && x.Where == y.Where && slices.Equal(x.Columns, y.Columns)
		}) &&
		slices.EqualFunc(a.ForeignKeys, b.ForeignKeys, func(x, y ForeignKey) bool {
			return x.Name == y.Name && x.RefTable == y.RefTable && x.OnDelete == y.OnDelete &&
				slices.Equal(x.Columns, y.Columns) && slices.Equal(x.RefColumns, y.RefColumns)
		}) &&
		slices.EqualFunc(a.Checks, b.Checks, func(x, y Check) bool { return x.Name == y.Name && x.Expr == y.Expr })
}

func enumEqual(a, b Enum) bool {
	return a.Schema == b.Schema && a.Name == b.Name && slices.Equal(a.Values, b.Values)
}

// clone is a deep copy: Diff, Lint and Render plan on clones, so a Schema
// they are given is never written through.
func (s *Schema) clone() *Schema {
	c := &Schema{Dialect: s.Dialect, Tables: slices.Clone(s.Tables), Enums: slices.Clone(s.Enums)}
	for i := range c.Tables {
		t := &c.Tables[i]
		t.Columns = slices.Clone(t.Columns)
		t.PrimaryKey = slices.Clone(t.PrimaryKey)
		t.Indexes = slices.Clone(t.Indexes)
		for j := range t.Indexes {
			t.Indexes[j].Columns = slices.Clone(t.Indexes[j].Columns)
		}
		t.ForeignKeys = slices.Clone(t.ForeignKeys)
		for j := range t.ForeignKeys {
			fk := &t.ForeignKeys[j]
			fk.Columns = slices.Clone(fk.Columns)
			fk.RefColumns = slices.Clone(fk.RefColumns)
		}
		t.Checks = slices.Clone(t.Checks)
	}
	for i := range c.Enums {
		c.Enums[i].Values = slices.Clone(c.Enums[i].Values)
	}
	return c
}

// sort puts every list in its order: tables and enums by (Schema, Name),
// indexes, foreign keys and checks by Name. The keys are assumed unique; a
// duplicate keeps its place. Columns keep their creation order,
// and so do the lists of column names and enum values.
func (s *Schema) sort() {
	slices.SortStableFunc(s.Tables, func(a, b Table) int { return cmp.Or(cmp.Compare(a.Schema, b.Schema), cmp.Compare(a.Name, b.Name)) })
	slices.SortStableFunc(s.Enums, func(a, b Enum) int { return cmp.Or(cmp.Compare(a.Schema, b.Schema), cmp.Compare(a.Name, b.Name)) })
	for i := range s.Tables {
		t := &s.Tables[i]
		slices.SortStableFunc(t.Indexes, func(a, b Index) int { return cmp.Compare(a.Name, b.Name) })
		slices.SortStableFunc(t.ForeignKeys, func(a, b ForeignKey) int { return cmp.Compare(a.Name, b.Name) })
		slices.SortStableFunc(t.Checks, func(a, b Check) int { return cmp.Compare(a.Name, b.Name) })
	}
}
