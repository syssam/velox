package gen

import (
	"cmp"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/ddl"
	"github.com/syssam/velox2/migrate"
)

// Tables builds the database model of g for the dialect d: one table per
// entity and per many-to-many join table, sorted. Its errors are *Error
// values with positions, joined as Build's are. Tables does not change g.
func Tables(g *Graph, d velox.Dialect) (*migrate.Schema, error) {
	if d != velox.Postgres && d != velox.MySQL && d != velox.SQLite {
		return nil, &Error{Reason: "Tables: unknown dialect " + d.String()}
	}
	t := &tabler{d: d, ids: map[*Entity]string{}, fks: map[[3]string]foreignKey{}, enums: map[[2]string]bool{}}
	for _, e := range g.Entities {
		t.ids[e] = t.idType(e.Fields[0])
	}
	for _, fk := range foreignKeys(g) {
		if k := [3]string{fk.Schema, fk.Table, fk.Column}; t.fks[k].Edge == nil {
			t.fks[k] = fk // a self join table of one column is rule 35's
		}
	}
	s := &migrate.Schema{Dialect: d}
	for _, e := range g.Entities {
		s.Tables = append(s.Tables, t.entity(e))
	}
	for _, jt := range joinTables(g) {
		s.Tables = append(s.Tables, t.joinTable(jt))
	}
	s.Enums = t.enumList
	slices.SortStableFunc(s.Tables, func(a, b migrate.Table) int {
		return cmp.Or(strings.Compare(a.Schema, b.Schema), strings.Compare(a.Name, b.Name))
	})
	slices.SortStableFunc(s.Enums, func(a, b migrate.Enum) int {
		return cmp.Or(strings.Compare(a.Schema, b.Schema), strings.Compare(a.Name, b.Name))
	})
	for i := range s.Tables {
		tb := &s.Tables[i]
		slices.SortStableFunc(tb.Indexes, func(a, b migrate.Index) int { return strings.Compare(a.Name, b.Name) })
		slices.SortStableFunc(tb.ForeignKeys, func(a, b migrate.ForeignKey) int { return strings.Compare(a.Name, b.Name) })
		slices.SortStableFunc(tb.Checks, func(a, b migrate.Check) int { return strings.Compare(a.Name, b.Name) })
	}
	if len(t.errs) > 0 {
		slices.SortStableFunc(t.errs, func(a, b refused) int {
			return cmp.Or(strings.Compare(a.at.e.Name, b.at.e.Name), cmp.Compare(a.at.rank, b.at.rank), cmp.Compare(a.at.i, b.at.i))
		})
		errs := make([]error, len(t.errs))
		for i, r := range t.errs {
			errs[i] = r.err
		}
		return nil, errors.Join(errs...)
	}
	return s, nil
}

// tabler holds the state of one Tables.
type tabler struct {
	d        velox.Dialect
	ids      map[*Entity]string       // each entity's ID column type
	fks      map[[3]string]foreignKey // by (schema, table, column)
	enums    map[[2]string]bool       // the PostgreSQL enum types listed, by (schema, name)
	enumList []migrate.Enum
	errs     []refused
}

// comment is c, or "" on SQLite, which keeps no comments.
func (t *tabler) comment(c string) string {
	if t.d == velox.SQLite {
		return ""
	}
	return c
}

// pkName is the name of table's primary key on the dialect.
func (t *tabler) pkName(table string) string {
	switch t.d {
	case velox.Postgres:
		return pkName(table)
	case velox.MySQL:
		return "PRIMARY"
	}
	return ""
}

// collation is a planned table's collation on the dialect.
func (t *tabler) collation() string {
	if t.d == velox.MySQL {
		return "utf8mb4_bin"
	}
	return ""
}

// idType is the column type of the ID id: an integer ID is bigint on
// PostgreSQL when it is unsigned 64-bit, as an identity cannot be numeric,
// and integer on SQLite, its rowid.
func (t *tabler) idType(id *Field) string {
	if integer(id.Type) {
		switch {
		case t.d == velox.SQLite:
			return "integer"
		case id.SchemaType[t.d.String()] != "":
		case t.d == velox.Postgres && (id.Type == "uint" || id.Type == "uint64"):
			return "bigint"
		}
	}
	return t.plainType(id)
}

// entity is the table of e.
func (t *tabler) entity(e *Entity) migrate.Table {
	tb := migrate.Table{
		Schema: e.TableSchema, Name: e.Table, PrimaryKey: []string{e.Fields[0].Column}, PrimaryKeyName: t.pkName(e.Table),
		Collation: t.collation(), Pos: tableLoc(e).pos,
	}
	if raw, ok := e.Annotations["Comment"]; ok {
		var c struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			t.fail(place{e, rankTable, 0, loc{pos: tb.Pos}}, "its Comment annotation is not a comment: %v", err)
		}
		tb.Comment = t.comment(c.Text)
	}
	for i, f := range e.Fields {
		tb.Columns = append(tb.Columns, t.column(e, i, f, &tb))
	}

	var keys [][]string // the leading columns of the non-partial indexes, for fkIndexed
	var at []place      // the element that makes each index, for the checks
	for i, ix := range e.Indexes {
		tb.Indexes = append(tb.Indexes, migrate.Index{Name: t.symbol(ix.Name, ix.Derived), Columns: slices.Clone(ix.Columns), Unique: ix.Unique, Where: ix.Where, Pos: ix.Pos})
		at = append(at, place{e, rankIndex, i, indexLoc(i, ix)})
		if ix.Where == "" {
			keys = append(keys, ix.Columns)
		}
	}
	ixs, fs := fieldIndexes(e, nil, t.d)
	for i, ix := range ixs {
		tb.Indexes = append(tb.Indexes, migrate.Index{Name: ix.Name, Columns: ix.Columns, Unique: true, Pos: fs[i].Pos})
		at = append(at, place{e, rankField, slices.Index(e.Fields, fs[i]), fieldLoc(fs[i])})
		keys = append(keys, ix.Columns)
	}
	for _, f := range e.Fields {
		fk, ok := t.fks[[3]string{e.TableSchema, e.Table, f.Column}]
		if !ok {
			continue
		}
		if t.foreignKey(&tb, fk, keys) {
			at = append(at, edgePlace(fk.Owner, fk.Edge))
		}
	}
	t.check(e, &tb, at)
	return tb
}

// joinTable is the table of a many-to-many relation: its two key columns,
// typed as the IDs they reference, are its primary key.
func (t *tabler) joinTable(jt joinTable) migrate.Table {
	pos := jt.Edge.Pos
	tb := migrate.Table{
		Schema: jt.Schema, Name: jt.Table, PrimaryKey: slices.Clone(jt.Columns[:]), PrimaryKeyName: t.pkName(jt.Table),
		Collation: t.collation(), Pos: pos,
	}
	for i, ref := range []*Entity{jt.Owner, jt.Target} {
		c := jt.Columns[i]
		if i == 1 && c == jt.Columns[0] {
			break // rule 35's
		}
		tb.Columns = append(tb.Columns, migrate.Column{Name: c, Type: t.ids[ref], Pos: pos})
		if fk, ok := t.fks[[3]string{jt.Schema, jt.Table, c}]; ok {
			t.foreignKey(&tb, fk, nil)
		}
	}
	t.checkJoin(&tb, edgePlace(jt.Owner, jt.Edge))
	return tb
}

// symbol is name on the dialect: capped as ent caps it when derived, else
// as given.
func (t *tabler) symbol(name string, derived bool) string {
	if derived {
		return ddl.Symbol(t.d, name)
	}
	return name
}

// foreignKey adds fk to tb, with the index of its column unless the
// primary key or one of keys leads with it; it reports whether it added the
// index. On MySQL the index has the key's name, as MySQL names the index it
// makes for a key; elsewhere it is fkIndexName. MySQL cannot name the
// connection's default database, so a key from a table in another database
// to one in it is refused.
func (t *tabler) foreignKey(tb *migrate.Table, fk foreignKey, keys [][]string) bool {
	pos := fk.Edge.Pos
	if t.d == velox.MySQL && tb.Schema != "" && fk.Ref.TableSchema == "" {
		t.fail(edgePlace(fk.Owner, fk.Edge), "table %s.%s references %s in the connection's default database, which MySQL cannot name from another database; give %s a schema.TableIn",
			tb.Schema, tb.Name, fk.Ref.Table, fk.Ref.Table)
	}
	name := t.symbol(fk.Name, fk.Derived)
	tb.ForeignKeys = append(tb.ForeignKeys, migrate.ForeignKey{
		Name: name, Columns: []string{fk.Column}, RefTable: migrate.TableRef{Schema: fk.Ref.TableSchema, Name: fk.Ref.Table},
		RefColumns: []string{fk.Ref.Fields[0].Column}, OnDelete: fk.OnDelete, Pos: pos,
	})
	if fkIndexed(tb.PrimaryKey, keys, fk.Column) {
		return false
	}
	if t.d != velox.MySQL {
		name = fkIndexName(fk.Table, fk.Column)
	}
	tb.Indexes = append(tb.Indexes, migrate.Index{Name: name, Columns: []string{fk.Column}, Pos: pos})
	return true
}

// column is the column of e's field f, the i-th; an enum may add a check to
// tb.
func (t *tabler) column(e *Entity, i int, f *Field, tb *migrate.Table) migrate.Column {
	c := migrate.Column{Name: f.Column, Nullable: nullable(f), Comment: t.comment(f.Comment), Pos: f.Pos}
	fk, isFK := t.fks[[3]string{e.TableSchema, e.Table, f.Column}]
	switch {
	case i == 0:
		c.Type = t.ids[e]
		if integer(f.Type) {
			c.Identity = true
			if t.d == velox.Postgres {
				c.Sequence = seqName(e.Table, f.Column)
			}
		}
	case isFK:
		c.Type = t.ids[fk.Ref] // the referenced column's, without its identity
	default:
		c.Type = t.fieldType(e, f, tb)
	}
	if f.Implicit != "" && isFK {
		c.Comment, c.Pos = t.comment(fk.Edge.Comment), fk.Edge.Pos
	}
	if !c.Identity {
		c.Default = t.columnDefault(f, c.Type)
	}
	return c
}

// plainType is the type of a field that is not an enum: its SchemaType for
// the dialect, normalised, else the type of its field type and Size.
func (t *tabler) plainType(f *Field) string {
	if st := f.SchemaType[t.d.String()]; st != "" {
		return ddl.NormalizeType(t.d, st)
	}
	return baseType(t.d, f.Type, f.Size)
}

// fieldType is the type of e's field f. An enum is PostgreSQL's enum type,
// which it lists, or MySQL's enum(…); else, on SQLite and when its
// SchemaType opts the dialect out, its string type and a check, which it
// adds to tb.
func (t *tabler) fieldType(e *Entity, f *Field, tb *migrate.Table) string {
	if f.Type != "enum" {
		return t.plainType(f)
	}
	st := f.SchemaType[t.d.String()]
	switch t.d {
	case velox.Postgres:
		if name, native := enumType(e, f); native {
			t.enum(e, f, name)
			return ddl.EnumType(e.TableSchema, name)
		}
	case velox.MySQL:
		if st == "" || !ddl.StringType(t.d, st) {
			return ddl.MySQLEnum(enumValues(f))
		}
	}
	tb.Checks = append(tb.Checks, migrate.Check{
		Name: checkName(e.Table, f.Column), Expr: ddl.EnumCheck(t.d, f.Column, enumValues(f)), Pos: f.Pos,
	})
	if st == "" {
		return "text"
	}
	return ddl.NormalizeType(t.d, st)
}

// enum lists the PostgreSQL enum type name of e's field f, once per
// database schema.
func (t *tabler) enum(e *Entity, f *Field, name string) {
	k := [2]string{e.TableSchema, name}
	if t.enums[k] {
		return
	}
	t.enums[k] = true
	t.enumList = append(t.enumList, migrate.Enum{Schema: e.TableSchema, Name: name, Values: enumValues(f), Pos: f.Pos})
}

// columnDefault is the DEFAULT of f's column, of type typ: its SchemaDefault
// for the dialect, verbatim; else its literal Default of a bool, number,
// string or enum; else, for an Optional field that is not Nillable of type
// string, bool or number, its zero value; else "".
func (t *tabler) columnDefault(f *Field, typ string) string {
	if s := f.SchemaDefault[t.d.String()]; s != "" {
		return s
	}
	raw := f.Default
	if len(raw) == 0 {
		if !f.Optional || f.Nillable {
			return ""
		}
		switch {
		case f.Type == "string":
			raw = json.RawMessage(`""`)
		case f.Type == "bool":
			raw = json.RawMessage(`false`)
		case numeric[f.Type]:
			raw = json.RawMessage(`0`)
		default:
			return ""
		}
	}
	switch {
	case f.Type == "bool":
		var v bool
		if json.Unmarshal(raw, &v) != nil {
			return ""
		}
		if t.d == velox.Postgres {
			return strconv.FormatBool(v)
		}
		if v {
			return "1"
		}
		return "0"
	case numeric[f.Type]:
		// encoding/json prints numbers with strconv, in the shortest form
		// that reads back exactly.
		n := strings.TrimSpace(string(raw))
		if _, err := strconv.ParseFloat(n, 64); err != nil {
			return ""
		}
		return n
	case f.Type == "string" || f.Type == "enum":
		var v string
		if json.Unmarshal(raw, &v) != nil {
			return ""
		}
		lit := ddl.Literal(t.d, v)
		if t.d == velox.MySQL && mysqlExprDefault[typ] {
			return "(" + lit + ")" // MySQL takes no literal default on these types
		}
		return lit
	}
	return ""
}

// mysqlExprDefault are MySQL's text, blob and json types, whose default must
// be an expression.
var mysqlExprDefault = map[string]bool{
	"tinytext": true, "text": true, "mediumtext": true, "longtext": true,
	"tinyblob": true, "blob": true, "mediumblob": true, "longblob": true, "json": true,
}

// baseTypes are the column types of the field types whose type does not
// depend on their Size: PostgreSQL's, MySQL's, then SQLite's.
var baseTypes = map[string][3]string{
	"bool":    {"boolean", "tinyint(1)", "boolean"},
	"int8":    {"smallint", "tinyint", "integer"},
	"int16":   {"smallint", "smallint", "integer"},
	"int32":   {"integer", "int", "integer"},
	"int":     {"bigint", "bigint", "integer"},
	"int64":   {"bigint", "bigint", "integer"},
	"uint8":   {"smallint", "tinyint unsigned", "integer"},
	"uint16":  {"integer", "smallint unsigned", "integer"},
	"uint32":  {"bigint", "int unsigned", "integer"},
	"uint":    {"numeric(20,0)", "bigint unsigned", "integer"},
	"uint64":  {"numeric(20,0)", "bigint unsigned", "integer"},
	"float32": {"real", "float", "real"},
	"float64": {"double precision", "double", "real"},
	"time":    {"timestamp with time zone", "datetime(6)", "datetime"},
	"uuid":    {"uuid", "char(36)", "text"},
	"json":    {"jsonb", "json", "text"},
}

// baseType is the column type of the field type typ of Size size on d; ""
// for other, whose type is its SchemaType's.
func baseType(d velox.Dialect, typ string, size int) string {
	at := int(d) - int(velox.Postgres)
	if ts, ok := baseTypes[typ]; ok && at >= 0 && at < len(ts) {
		return ts[at]
	}
	switch typ {
	case "string":
		switch {
		case d == velox.Postgres && size > 0 && size <= 10485760:
			return "character varying(" + strconv.Itoa(size) + ")"
		case d == velox.MySQL:
			switch {
			case size == 0:
				return "varchar(255)"
			case size <= 16381:
				return "varchar(" + strconv.Itoa(size) + ")"
			case size <= 65535:
				return "text"
			case size <= 16777215:
				return "mediumtext"
			}
			return "longtext"
		}
		return "text"
	case "bytes":
		switch d {
		case velox.Postgres:
			return "bytea"
		case velox.MySQL:
			switch {
			case size == 0:
				return "blob"
			case size <= 255:
				return "varbinary(" + strconv.Itoa(size) + ")"
			case size <= 65535:
				return "blob"
			case size <= 16777215:
				return "mediumblob"
			}
			return "longblob"
		}
		return "blob"
	}
	return ""
}
