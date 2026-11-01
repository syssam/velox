package migrate

import (
	"fmt"
	"slices"
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/ddl"
)

// pgParts is what a change writes in the main script: actions of an
// ALTER TABLE on table, in phase, which consecutive changes share, then
// whole statements; and the online units that follow them.
type pgParts struct {
	table   string
	phase   int
	actions []string
	stmts   []string
	units   [][]string
}

// postgres renders the changes for PostgreSQL: the main script in r.out,
// the enum script in r.enum and the online script in r.online.
func (r *renderer) postgres() error {
	fresh := r.newSchemas()
	var pending *alter
	flush := func() {
		if pending != nil {
			r.emit("ALTER TABLE "+pending.table+" "+strings.Join(pending.actions, ", "), pending.changes...)
			pending = nil
		}
	}
	for i, c := range r.cs {
		if s := createsIn(c); fresh[s] {
			// The schema is created before the first statement in it; the
			// statement carries no review line of the change.
			flush()
			delete(fresh, s)
			r.out = append(r.out, Statement{SQL: "CREATE SCHEMA IF NOT EXISTS " + r.ident(s), Change: i})
		}
		pc, err := r.pgChange(i, c)
		if err != nil {
			return err
		}
		if len(pc.actions) > 0 {
			if pending == nil || pending.table != pc.table || pending.phase != pc.phase {
				flush()
				pending = &alter{table: pc.table, phase: pc.phase}
			}
			pending.actions = append(pending.actions, pc.actions...)
			pending.changes = append(pending.changes, i)
		}
		if len(pc.stmts) > 0 || len(pc.units) > 0 {
			flush()
			for _, s := range pc.stmts {
				r.emit(s, i)
			}
		}
		for _, u := range pc.units {
			r.unit(i, u...)
		}
	}
	flush()
	return nil
}

// newSchemas is the database schemas the plan creates objects in that no
// table or enum type of from uses.
func (r *renderer) newSchemas() map[string]bool {
	used := map[string]bool{"": true}
	for _, t := range r.from.Tables {
		used[t.Schema] = true
	}
	for _, e := range r.from.Enums {
		used[e.Schema] = true
	}
	out := map[string]bool{}
	for _, c := range r.cs {
		if s := createsIn(c); !used[s] {
			out[s] = true
		}
	}
	return out
}

// createsIn is the database schema c puts a table or a type in, or "".
func createsIn(c Change) string {
	switch c := c.(type) {
	case createTable:
		return c.Table.Schema
	case createEnum:
		return c.Enum.Schema
	case renameTable:
		return c.To.Schema
	case renameEnum:
		return c.To.Schema
	}
	return ""
}

// unit adds a unit of the online script, the statements of change i.
func (r *renderer) unit(i int, stmts ...string) {
	u := 0
	if n := len(r.online); n > 0 {
		u = r.online[n-1].Unit + 1
	}
	for j, s := range stmts {
		st := Statement{SQL: s, Change: i, Unit: u}
		if j == 0 {
			st.Review = r.review([]int{i}, nil)
		}
		r.online = append(r.online, st)
	}
}

// pgChange renders change i: what goes in the main script is returned, and
// the enum script's and online script's statements are added.
func (r *renderer) pgChange(i int, c Change) (pgParts, error) {
	switch c := c.(type) {
	case createTable:
		return pgParts{stmts: r.pgCreate(&c.Table)}, nil
	case dropTable:
		return pgParts{stmts: []string{"DROP TABLE " + r.table(c.Table.Ref())}}, nil
	case renameTable:
		var stmts []string
		at := c.From
		if c.From.Name != c.To.Name {
			stmts = append(stmts, "ALTER TABLE "+r.table(c.From)+" RENAME TO "+r.ident(c.To.Name))
			at.Name = c.To.Name
		}
		if c.From.Schema != c.To.Schema {
			stmts = append(stmts, "ALTER TABLE "+r.table(at)+" SET SCHEMA "+r.ident(schemaName(c.To.Schema)))
		}
		return pgParts{stmts: stmts}, nil
	case addColumn:
		pc := pgParts{table: r.table(c.Table), phase: phaseColumn, actions: []string{"ADD COLUMN " + r.pgColumn(c.Column)}}
		if c.Column.Comment != "" {
			pc.stmts = append(pc.stmts, r.columnComment(c.Table, c.Column))
		}
		return pc, nil
	case dropColumn:
		return pgParts{table: r.table(c.Table), phase: phaseDropColumn, actions: []string{"DROP COLUMN " + r.ident(c.Column.Name)}}, nil
	case renameColumn:
		return pgParts{stmts: []string{"ALTER TABLE " + r.table(c.Table) + " RENAME COLUMN " + r.ident(c.From) + " TO " + r.ident(c.To)}}, nil
	case alterColumn:
		return r.pgAlterColumn(c)
	case renameSequence:
		return pgParts{stmts: []string{"ALTER SEQUENCE " + r.indexName(c.Table, c.From) + " RENAME TO " + r.ident(c.To)}}, nil
	case addIndex:
		if c.Index.Constraint {
			return pgParts{stmts: []string{"ALTER TABLE " + r.table(c.Table) + " ADD CONSTRAINT " + r.ident(c.Index.Name) + " UNIQUE (" + r.idents(c.Index.Columns) + ")"}}, nil
		}
		if r.l.existing(c.Table) {
			r.unit(i, "DROP INDEX CONCURRENTLY IF EXISTS "+r.indexName(c.Table, c.Index.Name), r.pgIndex(c.Table, c.Index, true))
			return pgParts{}, nil
		}
		return pgParts{stmts: []string{r.pgIndex(c.Table, c.Index, false)}}, nil
	case dropIndex:
		if r.dropOnline(c) {
			r.unit(i, "DROP INDEX CONCURRENTLY IF EXISTS "+r.indexName(r.afterRename(c.Table), c.Index.Name))
			return pgParts{}, nil
		}
		if c.Index.Constraint {
			return pgParts{stmts: []string{"ALTER TABLE " + r.table(c.Table) + " DROP CONSTRAINT " + r.ident(c.Index.Name)}}, nil
		}
		return pgParts{stmts: []string{"DROP INDEX " + r.indexName(c.Table, c.Index.Name)}}, nil
	case renameIndex:
		if r.renamesConstraint(c) {
			return pgParts{stmts: []string{"ALTER TABLE " + r.table(c.Table) + " RENAME CONSTRAINT " + r.ident(c.From) + " TO " + r.ident(c.To)}}, nil
		}
		return pgParts{stmts: []string{"ALTER INDEX " + r.indexName(c.Table, c.From) + " RENAME TO " + r.ident(c.To)}}, nil
	case addForeignKey:
		return r.pgConstraint(i, c.Table, c.ForeignKey.Name, r.foreignKey(c.ForeignKey)), nil
	case addCheck:
		return r.pgConstraint(i, c.Table, c.Check.Name, "CHECK ("+c.Check.Expr+")"), nil
	case dropForeignKey:
		return pgParts{table: r.table(c.Table), phase: phaseDrop, actions: []string{"DROP CONSTRAINT " + r.ident(c.ForeignKey.Name)}}, nil
	case dropCheck:
		return pgParts{table: r.table(c.Table), phase: phaseDrop, actions: []string{"DROP CONSTRAINT " + r.ident(c.Check.Name)}}, nil
	case renameConstraint:
		return pgParts{stmts: []string{"ALTER TABLE " + r.table(c.Table) + " RENAME CONSTRAINT " + r.ident(c.From) + " TO " + r.ident(c.To)}}, nil
	case setTableComment:
		return pgParts{stmts: []string{"COMMENT ON TABLE " + r.table(c.Table) + " IS " + r.commentText(c.To)}}, nil
	case createEnum:
		return pgParts{stmts: []string{r.createType(c.Enum.ref(), c.Enum.Values)}}, nil
	case renameEnum:
		var stmts []string
		at := c.From
		if c.From.Name != c.To.Name {
			stmts = append(stmts, "ALTER TYPE "+r.table(c.From)+" RENAME TO "+r.ident(c.To.Name))
			at.Name = c.To.Name
		}
		if c.From.Schema != c.To.Schema {
			stmts = append(stmts, "ALTER TYPE "+r.table(at)+" SET SCHEMA "+r.ident(schemaName(c.To.Schema)))
		}
		return pgParts{stmts: stmts}, nil
	case alterEnum:
		if added, ok := addedValues(c.From.Values, c.To.Values); ok {
			r.addValues(i, c, added)
			return pgParts{}, nil
		}
		stmts, err := r.swapEnum(c.To)
		return pgParts{stmts: stmts}, err
	case dropEnum:
		return pgParts{stmts: []string{"DROP TYPE " + r.table(c.Enum.ref())}}, nil
	}
	return pgParts{}, r.refuse(i, fmt.Sprintf("PostgreSQL has no %s", c.Kind()))
}

// schemaName is the name of the database schema s: "" is public.
func schemaName(s string) string {
	if s == "" {
		return "public"
	}
	return s
}

// pgConstraint adds a foreign key or check, def, named name, to table: on
// an existing table as an online unit, added NOT VALID and validated after,
// and in the main script on a table the plan creates.
func (r *renderer) pgConstraint(i int, table TableRef, name, def string) pgParts {
	t := r.table(table)
	if r.l.existing(table) {
		r.unit(i,
			"ALTER TABLE "+t+" DROP CONSTRAINT IF EXISTS "+r.ident(name),
			"ALTER TABLE "+t+" ADD CONSTRAINT "+r.ident(name)+" "+def+" NOT VALID",
			"ALTER TABLE "+t+" VALIDATE CONSTRAINT "+r.ident(name))
		return pgParts{}
	}
	return pgParts{table: t, phase: phaseAdd, actions: []string{"ADD CONSTRAINT " + r.ident(name) + " " + def}}
}

// dropOnline reports whether the drop of an index goes to the online
// script: an index of an existing table the plan keeps, on no column the
// plan drops, whose name is not taken (indexNameTaken), and no constraint's,
// which DROP INDEX CONCURRENTLY cannot drop. The others are dropped in the
// main script: with a table or column that goes anyway, in time to free the
// name for another index, or as the constraint.
func (r *renderer) dropOnline(c dropIndex) bool {
	k := r.l.canon(c.Table)
	if c.Index.Constraint || !r.l.existing(c.Table) || r.l.dropped[k] {
		return false
	}
	for _, o := range r.cs {
		if dc, ok := o.(dropColumn); ok && r.l.canon(dc.Table) == k && slices.Contains(c.Index.Columns, dc.Column.Name) {
			return false
		}
	}
	return !indexNameTaken(r.to, c.Table.Schema, c.Index.Name, func(u TableRef) bool { return r.l.canon(u) == k })
}

// renamesConstraint reports whether c renames a unique constraint's index:
// from's index c.From of c.Table is a constraint's.
func (r *renderer) renamesConstraint(c renameIndex) bool {
	t := r.from.table(r.l.canon(c.Table))
	if t == nil {
		return false
	}
	ix := t.index(c.From)
	return ix != nil && ix.Constraint
}

// afterRename is the name the table of from named ref has after the main
// script's renames.
func (r *renderer) afterRename(ref TableRef) TableRef {
	for _, c := range r.cs {
		if rt, ok := c.(renameTable); ok && rt.From == ref {
			return rt.To
		}
	}
	return ref
}

// pgAlterColumn renders the alteration of a column. A type change drops
// the default before it and sets it after, as PostgreSQL cannot cast a
// default; SET NOT NULL is an online unit through a validated check.
func (r *renderer) pgAlterColumn(c alterColumn) (pgParts, error) {
	pc := pgParts{table: r.table(c.Table), phase: phaseColumn}
	col := r.ident(c.To.Name)
	act := func(s string) { pc.actions = append(pc.actions, "ALTER COLUMN "+col+" "+s) }
	identity := c.What&columnIdentity != 0 && !c.From.Identity && c.To.Identity
	switch {
	case c.What&columnType != 0:
		if c.From.Default != "" {
			act("DROP DEFAULT")
		}
		act("TYPE " + c.To.Type + pgUsing(col, c.From.Type, c.To.Type))
		if c.To.Default != "" {
			act("SET DEFAULT " + c.To.Default)
		}
		if seq, ok := r.serialSequence(c); ok {
			pc.stmts = append(pc.stmts, "ALTER SEQUENCE "+seq+" AS "+c.To.Type)
		}
	case c.What&columnDefault != 0 && c.To.Default == "":
		act("DROP DEFAULT")
	case c.What&columnDefault != 0:
		act("SET DEFAULT " + c.To.Default)
	}
	if c.What&columnNullable != 0 {
		switch {
		case c.To.Nullable:
			act("DROP NOT NULL")
		case identity:
			// An identity needs NOT NULL first.
			act("SET NOT NULL")
		default:
			t := r.table(c.Table)
			nn := r.ident(ddl.NotNull(c.Table.Name, c.To.Name))
			pc.units = append(pc.units, []string{
				"ALTER TABLE " + t + " DROP CONSTRAINT IF EXISTS " + nn,
				"ALTER TABLE " + t + " ADD CONSTRAINT " + nn + " CHECK (" + col + " IS NOT NULL) NOT VALID",
				"ALTER TABLE " + t + " VALIDATE CONSTRAINT " + nn,
				"ALTER TABLE " + t + " ALTER COLUMN " + col + " SET NOT NULL",
				"ALTER TABLE " + t + " DROP CONSTRAINT " + nn,
			})
		}
	}
	if c.What&columnIdentity != 0 {
		switch {
		case identity:
			// A serial's default is dropped above; its sequence goes
			// before the identity takes the name. One from does not
			// name is read from the default.
			if strings.HasPrefix(c.From.Default, "nextval(") {
				seq := r.indexName(c.Table, c.From.Sequence)
				if c.From.Sequence == "" {
					ref, ok := nextvalSequence(c.From.Default)
					if !ok {
						return pgParts{}, unnamedSequence(c)
					}
					seq = r.table(ref)
				}
				pc.stmts = append(pc.stmts, "DROP SEQUENCE "+seq)
			}
			pc.stmts = append(pc.stmts,
				"ALTER TABLE "+pc.table+" ALTER COLUMN "+col+" ADD "+r.identity(c.To),
				fmt.Sprintf("SELECT setval(pg_get_serial_sequence(%s, %s), coalesce(max(%s), 0) + 1, false) FROM %s",
					ddl.Literal(r.d, pc.table), ddl.Literal(r.d, c.To.Name), col, pc.table))
		case c.From.Identity && !c.To.Identity:
			act("DROP IDENTITY")
		case c.From.Identity && c.To.Sequence != "":
			// The identity stays; only its sequence's name changes, which
			// takes from's name for it.
			if c.From.Sequence == "" {
				return pgParts{}, unnamedSequence(c)
			}
			pc.stmts = append(pc.stmts, "ALTER SEQUENCE "+r.indexName(c.Table, c.From.Sequence)+" RENAME TO "+r.ident(c.To.Sequence))
		}
	}
	if c.What&columnComment != 0 {
		pc.stmts = append(pc.stmts, r.columnComment(c.Table, c.To))
	}
	return pc, nil
}

// serialSequence is the sequence of c's column, retyped to an integer type,
// when the column is and stays a serial: a sequence that keeps its type stops
// at that type's top, and pgCreate writes it with the column's. An identity's
// follows its column on its own, and one not read from the default is left.
func (r *renderer) serialSequence(c alterColumn) (string, bool) {
	switch c.To.Type {
	case "smallint", "integer", "bigint":
	default:
		return "", false
	}
	if c.From.Identity || c.To.Identity || !strings.HasPrefix(c.To.Default, "nextval(") {
		return "", false
	}
	if c.To.Sequence != "" {
		return r.indexName(c.Table, c.To.Sequence), true
	}
	ref, ok := nextvalSequence(c.To.Default)
	if !ok {
		return "", false
	}
	return r.table(ref), true
}

// pgUsing is the USING clause of a column's change from type from to type to,
// or "" where the cast would hide a loss. An explicit cast to a string or bit
// type of limited length cuts what does not fit, silently, where the
// assignment cast it leaves to fails on it: a row over the length stops the
// file. A string reaches a bit type by no assignment cast, so it is cast to
// varbit, which has no limit, and assigned from there. The other types read the
// same either way, or have no assignment cast, as text to integer has not, and
// keep USING; Lint's finding says so of those that may still cut.
func pgUsing(col, from, to string) string {
	t, f := pgTypeOf(to), pgTypeOf(from)
	if t.limited && (!t.array || f.array) {
		switch {
		case !t.bits, f.bits:
			return ""
		case f.text && f.array:
			return " USING " + col + "::varbit[]"
		case f.text:
			return " USING " + col + "::varbit"
		}
	}
	if strings.HasPrefix(to, `"`) {
		return " USING " + col + "::text::" + to
	}
	return " USING " + col + "::" + to
}

// A pgType is what pgTypeOf reads of a type as written.
type pgType struct {
	base    string // the type without its array suffix, as written
	array   bool
	known   bool // a type of PostgreSQL's own that velox reads
	text    bool // a string type
	bits    bool // a bit string type
	limited bool // a string or bit string type of limited length
}

// pgStrings and pgBits are the spellings of the string and bit string types,
// each with whether the bare spelling, with no length, is limited: bare
// character and bit are of length 1; bare character varying, varchar, bpchar
// and bit varying have no limit.
var (
	pgStrings = map[string]bool{
		"text": false, "character varying": false, "varchar": false, "char varying": false, "national character varying": false,
		"national char varying": false, "nchar varying": false, "bpchar": false,
		"character": true, "char": true, "nchar": true, "national character": true, "national char": true,
	}
	pgBits = map[string]bool{"bit": true, "bit varying": false, "varbit": false}
)

// pgOthers are the first words of the other types of PostgreSQL's own, which
// hold no value a cast to them could cut without "can fail or change values"
// having said so.
var pgOthers = []string{
	"smallint", "integer", "int", "bigint", "int2", "int4", "int8", "smallserial", "serial", "bigserial", "serial2", "serial4", "serial8",
	"numeric", "decimal", "real", "double", "float", "float4", "float8", "money", "boolean", "bool", "bytea",
	"date", "time", "timetz", "timestamp", "timestamptz", "interval", "uuid", "json", "jsonb", "xml",
	"inet", "cidr", "macaddr", "macaddr8", "point", "line", "lseg", "box", "path", "polygon", "circle",
	"tsvector", "tsquery", "pg_lsn", "oid",
}

// pgTypeOf reads the type t, in any spelling: pg_catalog's, in double quotes,
// in any case, with a length and with array brackets. "char" in double quotes
// or of pg_catalog is PostgreSQL's one-byte type, which no spelling here is.
func pgTypeOf(t string) pgType {
	var out pgType
	base := strings.TrimSpace(t)
	for {
		b, ok := strings.CutSuffix(base, "[]")
		if !ok {
			break
		}
		base, out.array = strings.TrimSpace(b), true
	}
	out.base = base
	word, quoted := strings.ToLower(base), false
	for _, p := range []string{"pg_catalog.", `"pg_catalog".`} {
		if w, ok := strings.CutPrefix(word, p); ok {
			word, quoted = w, true
		}
	}
	if strings.HasPrefix(word, `"`) {
		end := strings.IndexByte(word[1:], '"')
		if end < 0 {
			return out
		}
		word, quoted = word[1:end+1]+word[end+2:], true
	}
	word, _, sized := strings.Cut(word, "(")
	word = strings.TrimSpace(word)
	if quoted && word == "char" {
		return out
	}
	if bare, ok := pgStrings[word]; ok {
		out.known, out.text, out.limited = true, true, word != "text" && (sized || bare)
		return out
	}
	if bare, ok := pgBits[word]; ok {
		out.known, out.bits, out.limited = true, true, sized || bare
		return out
	}
	first, _, _ := strings.Cut(word, " ")
	out.known = slices.Contains(pgOthers, first)
	return out
}

// pgInteger reports whether t is an integer type, of which a cast to bit keeps
// the low bits.
func pgInteger(t string) bool {
	switch word, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(t)), " "); word {
	case "smallint", "integer", "int", "bigint", "int2", "int4", "int8":
		return true
	}
	return false
}

// unnamedSequence is the refusal of an identity change that needs the name
// of a sequence from does not give.
func unnamedSequence(c alterColumn) error {
	return &Error{Object: colTarget(c.Table, c.To.Name), Code: "render",
		Reason: "the identity's sequence is not named in the snapshot; run velox-gen migrate baseline, or rename it by hand"}
}

// nextvalSequence is the sequence a serial's default nextval('…'::regclass)
// names, read by PostgreSQL's identifier rules: a name in double quotes as
// it is, any other folded to lower case, and at most a schema before it.
func nextvalSequence(def string) (TableRef, bool) {
	arg, ok := strings.CutPrefix(def, "nextval(")
	if ok {
		arg, ok = strings.CutSuffix(arg, ")")
	}
	if !ok {
		return TableRef{}, false
	}
	text, ok := ddl.Unquote(velox.Postgres, strings.TrimSuffix(arg, "::regclass"))
	if !ok {
		return TableRef{}, false
	}
	var parts []string
	for {
		var part string
		if rest, quoted := strings.CutPrefix(text, `"`); quoted {
			var b strings.Builder
			for {
				i := strings.IndexByte(rest, '"')
				if i < 0 {
					return TableRef{}, false
				}
				b.WriteString(rest[:i])
				rest = rest[i+1:]
				if !strings.HasPrefix(rest, `"`) {
					break
				}
				b.WriteByte('"')
				rest = rest[1:]
			}
			part, text = b.String(), rest
		} else {
			i := strings.IndexByte(text, '.')
			if i < 0 {
				i = len(text)
			}
			part, text = strings.ToLower(text[:i]), text[i:]
			if strings.ContainsAny(part, `" `) {
				return TableRef{}, false
			}
		}
		if part == "" {
			return TableRef{}, false
		}
		parts = append(parts, part)
		if text == "" {
			break
		}
		if text, ok = strings.CutPrefix(text, "."); !ok {
			return TableRef{}, false
		}
	}
	switch len(parts) {
	case 1:
		return TableRef{Name: parts[0]}, true
	case 2:
		return TableRef{Schema: parts[0], Name: parts[1]}, true
	}
	return TableRef{}, false
}

// identity is the identity clause of c.
func (r *renderer) identity(c Column) string {
	s := "GENERATED BY DEFAULT AS IDENTITY"
	if c.Sequence != "" {
		s += " (SEQUENCE NAME " + r.ident(c.Sequence) + ")"
	}
	return s
}

// pgColumn is c's definition: "c" <type> [GENERATED BY DEFAULT AS IDENTITY
// (SEQUENCE NAME "s")] [NOT NULL] [DEFAULT x].
func (r *renderer) pgColumn(c Column) string {
	s := r.ident(c.Name) + " " + c.Type
	if c.Identity {
		s += " " + r.identity(c)
	}
	if !c.Nullable {
		s += " NOT NULL"
	}
	if c.Default != "" {
		s += " DEFAULT " + c.Default
	}
	return s
}

// commentText is the literal of a comment; NULL removes one.
func (r *renderer) commentText(s string) string {
	if s == "" {
		return "NULL"
	}
	return ddl.Literal(r.d, s)
}

func (r *renderer) columnComment(table TableRef, c Column) string {
	return "COMMENT ON COLUMN " + r.table(table) + "." + r.ident(c.Name) + " IS " + r.commentText(c.Comment)
}

// pgCreate is the CREATE TABLE of t, with its key, foreign keys, checks and
// unique constraints, then its other indexes and comments. A serial's
// sequence is created before the table and owned by its column after it.
func (r *renderer) pgCreate(t *Table) []string {
	ref := t.Ref()
	var out, defs, owned []string
	for _, c := range t.Columns {
		defs = append(defs, r.pgColumn(c))
		if c.Identity || c.Sequence == "" {
			continue
		}
		seq := r.indexName(ref, c.Sequence)
		create := "CREATE SEQUENCE " + seq
		switch c.Type {
		case "smallint", "integer", "bigint":
			create += " AS " + c.Type
		}
		out = append(out, create)
		owned = append(owned, "ALTER SEQUENCE "+seq+" OWNED BY "+r.table(ref)+"."+r.ident(c.Name))
	}
	if len(t.PrimaryKey) > 0 {
		pk := "PRIMARY KEY (" + r.idents(t.PrimaryKey) + ")"
		if t.PrimaryKeyName != "" {
			pk = "CONSTRAINT " + r.ident(t.PrimaryKeyName) + " " + pk
		}
		defs = append(defs, pk)
	}
	for _, fk := range t.ForeignKeys {
		defs = append(defs, "CONSTRAINT "+r.ident(fk.Name)+" "+r.foreignKey(fk))
	}
	for _, ck := range t.Checks {
		defs = append(defs, "CONSTRAINT "+r.ident(ck.Name)+" CHECK ("+ck.Expr+")")
	}
	for _, ix := range t.Indexes {
		if ix.Constraint {
			defs = append(defs, "CONSTRAINT "+r.ident(ix.Name)+" UNIQUE ("+r.idents(ix.Columns)+")")
		}
	}
	out = append(out, "CREATE TABLE "+r.table(ref)+" (\n  "+strings.Join(defs, ",\n  ")+"\n)")
	out = append(out, owned...)
	for _, ix := range t.Indexes {
		if !ix.Constraint {
			out = append(out, r.pgIndex(ref, ix, false))
		}
	}
	if t.Comment != "" {
		out = append(out, "COMMENT ON TABLE "+r.table(ref)+" IS "+r.commentText(t.Comment))
	}
	for _, c := range t.Columns {
		if c.Comment != "" {
			out = append(out, r.columnComment(ref, c))
		}
	}
	return out
}

// pgIndex is the CREATE INDEX of ix on table, CONCURRENTLY when online.
func (r *renderer) pgIndex(table TableRef, ix Index, online bool) string {
	s := "CREATE INDEX "
	if ix.Unique {
		s = "CREATE UNIQUE INDEX "
	}
	if online {
		s += "CONCURRENTLY "
	}
	s += r.ident(ix.Name) + " ON " + r.table(table) + " (" + r.idents(ix.Columns) + ")"
	if ix.Where != "" {
		s += " WHERE " + ix.Where
	}
	return s
}

// postgresScripts is the scripts in run order: the enum script, then main,
// then the online script, each left out when it has no statement. An
// allowance goes in each script that holds a statement of its change, and
// in the main script when none does.
func (r *renderer) postgresScripts(main Script, opts RenderOptions) []Script {
	in := func(sts []Statement) map[int]bool {
		m := map[int]bool{}
		for _, st := range sts {
			m[st.Change] = true
		}
		return m
	}
	enum, online, inMain := in(r.enum), in(r.online), in(r.out)
	allowIn := func(has func(int) bool) []Allow {
		var fs []Finding
		for _, f := range r.l.out {
			if has(f.Change) {
				fs = append(fs, f)
			}
		}
		allow := allowed(fs, opts.Allow)
		for _, a := range opts.Allow {
			if a.Code == lossCode { // no plan finds it; Up reads it
				allow = append(allow, a)
			}
		}
		return allow
	}
	var out []Script
	if len(r.enum) > 0 {
		d := main.Directives
		d.NoTransaction = true
		d.Allow = allowIn(func(i int) bool { return enum[i] })
		out = append(out, Script{Kind: ScriptEnum, Directives: d, Statements: r.enum})
	}
	if len(r.out) > 0 {
		main.Directives.Allow = allowIn(func(i int) bool { return inMain[i] || !enum[i] && !online[i] })
		main.Statements = r.out
		out = append(out, main)
	}
	if len(r.online) > 0 {
		d := Directives{NoTransaction: true, Allow: allowIn(func(i int) bool { return online[i] })}
		out = append(out, Script{Kind: ScriptOnline, Directives: d, Statements: r.online})
	}
	return out
}
