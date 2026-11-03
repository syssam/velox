package migrate

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/ddl"
)

// pgTable is a table inspectPostgres reads: its oid, its schema as the
// database names it, and the model's table.
type pgTable struct {
	oid     int64
	schema  string
	t       Table
	enums   map[string]string // an enum column's type, as a cast to it prints
	exts    map[string]bool   // the extensions its columns' types come from
	domains map[string]bool   // the domains its columns' types pass
}

// pgEnum is an enum type a column uses.
type pgEnum struct {
	oid int64
	ref TableRef
}

// inspectPostgres reads tables through pg_catalog, the default schema being
// current_schema(), and the enum types their columns use and enums names.
func inspectPostgres(ctx context.Context, q querier, tables, enums []TableRef) (*Schema, []note, error) {
	r := &pgReader{ctx: ctx, q: q, enums: map[int64]TableRef{}}
	def, err := r.strings(`SELECT coalesce(current_schema(), '')`)
	if err != nil {
		return nil, nil, err
	}
	if len(def) != 1 || def[0] == "" {
		return nil, nil, errors.New("the connection has no current schema; set search_path")
	}
	r.def = def[0]
	ts, err := r.tables(tables)
	if err != nil {
		return nil, nil, err
	}
	s := &Schema{Dialect: velox.Postgres}
	for _, pt := range ts {
		if err := r.table(&pt); err != nil {
			return nil, nil, err
		}
		s.Tables = append(s.Tables, pt.t)
	}
	for _, ref := range enums {
		err := r.each(`SELECT t.oid::bigint FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
			WHERE t.typtype = 'e' AND n.nspname = $1 AND t.typname = $2`, []any{cmp.Or(ref.Schema, r.def), ref.Name},
			func(rows *sql.Rows) error {
				var oid int64
				if err := rows.Scan(&oid); err != nil {
					return err
				}
				r.enums[oid] = TableRef{Schema: r.schema(cmp.Or(ref.Schema, r.def)), Name: ref.Name}
				return nil
			})
		if err != nil {
			return nil, nil, err
		}
	}
	used := make([]pgEnum, 0, len(r.enums))
	for oid, ref := range r.enums {
		used = append(used, pgEnum{oid, ref})
	}
	slices.SortFunc(used, func(a, b pgEnum) int { return cmp.Compare(a.oid, b.oid) })
	for _, e := range used {
		values, err := r.strings(`SELECT enumlabel FROM pg_enum WHERE enumtypid = $1::bigint::oid ORDER BY enumsortorder`, e.oid)
		if err != nil {
			return nil, nil, err
		}
		s.Enums = append(s.Enums, Enum{Schema: e.ref.Schema, Name: e.ref.Name, Values: values})
	}
	return s, r.notes, nil
}

// pgReader holds one inspection.
type pgReader struct {
	ctx   context.Context
	q     querier
	def   string             // current_schema()
	enums map[int64]TableRef // the enum types the columns use, by oid
	notes []note
}

// each runs query and calls scan for each row.
func (r *pgReader) each(query string, args []any, scan func(*sql.Rows) error) error {
	return eachRow(r.ctx, r.q, query, args, scan)
}

// strings is the first column of query's rows.
func (r *pgReader) strings(query string, args ...any) ([]string, error) {
	var out []string
	err := r.each(query, args, func(rows *sql.Rows) error {
		var s string
		if err := rows.Scan(&s); err != nil {
			return err
		}
		out = append(out, s)
		return nil
	})
	return out, err
}

// schema is the model's name of the database schema nsp: "" for the default.
func (r *pgReader) schema(nsp string) string {
	if nsp == r.def {
		return ""
	}
	return nsp
}

const pgTablesQuery = `SELECT c.oid::bigint, n.nspname, c.relname, coalesce(obj_description(c.oid, 'pg_class'), ''), c.relpersistence::text
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'p') AND n.nspname = $1`

// tables finds the tables to read: those of refs that are there, once each,
// or with refs nil every table of the default schema but velox_migrations
// and partitions, which belong to their table. An UNLOGGED or TEMPORARY
// table is a note.
func (r *pgReader) tables(refs []TableRef) ([]pgTable, error) {
	var out []pgTable
	scan := func(rows *sql.Rows) error {
		var pt pgTable
		var persistence string
		if err := rows.Scan(&pt.oid, &pt.schema, &pt.t.Name, &pt.t.Comment, &persistence); err != nil {
			return err
		}
		if slices.ContainsFunc(out, func(x pgTable) bool { return x.oid == pt.oid }) {
			return nil
		}
		pt.t.Schema = r.schema(pt.schema)
		switch persistence {
		case "u":
			r.note(&pt, "", "the table is UNLOGGED, which the model cannot hold: a crash empties it")
		case "t":
			r.note(&pt, "", "the table is TEMPORARY, which the model cannot hold: it ends with its session")
		}
		out = append(out, pt)
		return nil
	}
	if refs == nil {
		err := r.each(pgTablesQuery+` AND c.relname <> 'velox_migrations' AND NOT c.relispartition`, []any{r.def}, scan)
		return out, err
	}
	for _, ref := range refs {
		nsp := cmp.Or(ref.Schema, r.def)
		if err := r.each(pgTablesQuery+` AND c.relname = $2`, []any{nsp, ref.Name}, scan); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// table reads pt's columns, indexes and constraints into pt.t.
func (r *pgReader) table(pt *pgTable) error {
	names, err := r.columns(pt)
	if err != nil {
		return err
	}
	if err := r.indexes(pt, names); err != nil {
		return err
	}
	return r.constraints(pt)
}

// object is a Warning's Object for something of pt named name, or for pt
// itself when name is "".
func (pt *pgTable) object(name string) string {
	if name == "" {
		return pt.t.Ref().String()
	}
	return pt.t.Ref().String() + "." + name
}

func (r *pgReader) note(pt *pgTable, name, message string) {
	r.notes = append(r.notes, note{table: pt.t.Ref(), object: pt.object(name), message: message})
}

// baselineNote is a note only Baseline reports.
func (r *pgReader) baselineNote(pt *pgTable, name, message string) {
	r.notes = append(r.notes, note{table: pt.t.Ref(), object: pt.object(name), message: message, baseline: true})
}

// The columns, with what makes a serial: the sequence the column owns,
// by an auto (serial) or internal (identity) dependency, its regclass
// spelling, as a nextval default prints it, and its start and increment;
// the column's collation when it is not its type's; the extension its type
// comes from, and that extension's type when a domain lies before it; and
// the nearest domain its type passes that is neither an extension's member
// nor information_schema's or pg_catalog's; and the name of a NOT VALID NOT
// NULL constraint of the column, PostgreSQL 18's. They walk from the column's
// type through typbasetype while it is a domain and through typelem while
// it is an array, at most 16 steps, the nearest type with an extension
// member row (deptype e) winning.
const pgColumnsQuery = `SELECT a.attnum, a.attname, format_type(a.atttypid, a.atttypmod), t.typtype::text, t.oid::bigint,
	tn.nspname, t.typname, a.attnotnull, coalesce(pg_get_expr(d.adbin, d.adrelid), ''),
	a.attidentity::text, a.attgenerated::text, coalesce(col_description(a.attrelid, a.attnum), ''),
	coalesce(seq.nspname, ''), coalesce(seq.relname, ''), coalesce(seq.reg, ''),
	coalesce(seq.seqstart, 1), coalesce(seq.seqincrement, 1),
	coalesce((SELECT quote_ident(co.collname) FROM pg_collation co
		WHERE co.oid = a.attcollation AND a.attcollation <> t.typcollation AND a.attcollation <> 0), ''),
	coalesce(w.ext, ''), coalesce(w.over, ''), coalesce(w.dom, ''),
	CASE WHEN a.attstorage <> t.typstorage THEN a.attstorage::text ELSE '' END, a.attcompression::text,
	coalesce((SELECT k.conname FROM pg_constraint k WHERE k.conrelid = a.attrelid AND k.contype = 'n'
		AND NOT k.convalidated AND k.conkey[1] = a.attnum ORDER BY k.conname LIMIT 1), '')
FROM pg_attribute a
JOIN pg_type t ON t.oid = a.atttypid
JOIN pg_namespace tn ON tn.oid = t.typnamespace
LEFT JOIN LATERAL (
	WITH RECURSIVE walk(oid, depth) AS (
		SELECT t.oid, 0
		UNION ALL
		SELECT CASE WHEN wt.typtype = 'd' THEN wt.typbasetype ELSE wt.typelem END, walk.depth + 1
		FROM walk JOIN pg_type wt ON wt.oid = walk.oid
		WHERE walk.depth < 16 AND (wt.typtype = 'd' OR (wt.typelem <> 0 AND wt.typlen = -1))
	)
	SELECT m.ext, m.over,
		(SELECT format_type(walk.oid, NULL) FROM walk JOIN pg_type wt ON wt.oid = walk.oid
			WHERE wt.typtype = 'd' AND wt.typnamespace NOT IN ('pg_catalog'::regnamespace, 'information_schema'::regnamespace)
				AND NOT EXISTS (SELECT 1 FROM pg_depend de WHERE de.classid = 'pg_type'::regclass
					AND de.objid = walk.oid AND de.deptype = 'e')
			ORDER BY walk.depth LIMIT 1) AS dom
	FROM (SELECT 1) one LEFT JOIN LATERAL (
		SELECT e.extname AS ext,
			CASE WHEN EXISTS (SELECT 1 FROM walk w2 JOIN pg_type t2 ON t2.oid = w2.oid
				WHERE t2.typtype = 'd' AND w2.depth < walk.depth) THEN format_type(walk.oid, NULL) ELSE '' END AS over
		FROM walk JOIN pg_depend de ON de.objid = walk.oid
		JOIN pg_extension e ON e.oid = de.refobjid
		WHERE de.classid = 'pg_type'::regclass AND de.refclassid = 'pg_extension'::regclass AND de.deptype = 'e'
		ORDER BY walk.depth, e.extname LIMIT 1
	) m ON true
) w ON true
LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
LEFT JOIN LATERAL (
	SELECT sn.nspname, s.relname, s.oid::regclass::text AS reg, ps.seqstart, ps.seqincrement
	FROM pg_depend dep
	JOIN pg_class s ON s.oid = dep.objid AND s.relkind = 'S'
	JOIN pg_namespace sn ON sn.oid = s.relnamespace
	LEFT JOIN pg_sequence ps ON ps.seqrelid = s.oid
	WHERE dep.classid = 'pg_class'::regclass AND dep.refclassid = 'pg_class'::regclass
		AND dep.refobjid = a.attrelid AND dep.refobjsubid = a.attnum AND dep.deptype IN ('a', 'i')
	ORDER BY dep.deptype = 'i' DESC, s.relname LIMIT 1
) seq ON true
WHERE a.attrelid = $1::bigint::oid AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY a.attnum`

// pgStorages and pgCompressions name pg_attribute's codes.
var (
	pgStorages     = map[string]string{"p": "PLAIN", "e": "EXTERNAL", "m": "MAIN", "x": "EXTENDED"}
	pgCompressions = map[string]string{"p": "pglz", "l": "lz4"}
)

// columns reads pt's columns, and returns their names by number. A
// collation other than the type's is a note. An owned sequence that does
// not start and increment by 1, an extension's type, once per extension,
// and a domain, once per domain, are baseline notes.
func (r *pgReader) columns(pt *pgTable) (map[int]string, error) {
	names := map[int]string{}
	err := r.each(pgColumnsQuery, []any{pt.oid}, func(rows *sql.Rows) error {
		var (
			num                                       int
			c                                         Column
			typ, typtype, tnsp, tname, def            string
			identity, generated, seqNsp, seqName, reg string
			collation, ext, over, domain              string
			storage, compression, notValid            string
			typOID, start, increment                  int64
			notNull                                   bool
		)
		if err := rows.Scan(&num, &c.Name, &typ, &typtype, &typOID, &tnsp, &tname, &notNull, &def,
			&identity, &generated, &c.Comment, &seqNsp, &seqName, &reg, &start, &increment, &collation, &ext, &over, &domain, &storage, &compression, &notValid); err != nil {
			return err
		}
		if collation != "" {
			r.note(pt, c.Name, "the column has COLLATE "+collation+", which the model cannot hold; it is compared without")
			r.notes[len(r.notes)-1].keeps = []keep{{"COLLATE " + collation, []string{"COLLATE"}}}
		}
		if name := pgStorages[storage]; name != "" {
			r.note(pt, c.Name, "the column has STORAGE "+name+", which the model cannot hold; it is compared without")
			r.notes[len(r.notes)-1].keeps = []keep{{"STORAGE " + name, []string{"STORAGE"}}}
		}
		if name := pgCompressions[compression]; name != "" {
			r.note(pt, c.Name, "the column has COMPRESSION "+name+", which the model cannot hold; it is compared without")
			r.notes[len(r.notes)-1].keeps = []keep{{"COMPRESSION " + name, []string{"COMPRESSION"}}}
		}
		names[num] = c.Name
		c.Nullable = !notNull
		if notValid != "" {
			// attnotnull is set, but the rows from before the constraint
			// may hold NULL.
			c.Nullable = true
			r.note(pt, c.Name, "the column's NOT NULL is the constraint "+notValid+", which is NOT VALID: the rows from before it are not checked, so the column is read as nullable; VALIDATE CONSTRAINT checks them")
		}
		c.Type = ddl.NormalizeType(velox.Postgres, typ)
		if typtype == "e" {
			ref := TableRef{Schema: r.schema(tnsp), Name: tname}
			c.Type = ddl.EnumType(ref.Schema, ref.Name)
			r.enums[typOID] = ref
			if pt.enums == nil {
				pt.enums = map[string]string{}
			}
			pt.enums[c.Name] = typ
		}
		seq := seqName
		if seqNsp != pt.schema {
			seq = seqNsp + "." + seqName
		}
		switch {
		case identity != "":
			c.Identity = true
			c.Sequence = seq
			if identity == "a" {
				r.note(pt, c.Name, "the column is GENERATED ALWAYS AS IDENTITY, which the model holds as BY DEFAULT: a value given on insert is refused")
			}
		case generated != "":
			r.note(pt, c.Name, "the column is generated from an expression, which the model cannot hold")
		default:
			c.Default = normalizeDefault(velox.Postgres, def)
			// A serial: the default is nextval of the sequence it owns.
			if seqName != "" && c.Default == "nextval("+ddl.Literal(velox.Postgres, reg)+")" {
				c.Sequence = seq
			}
		}
		if c.Sequence != "" && (start != 1 || increment != 1) {
			r.baselineNote(pt, c.Name, "the sequence "+c.Sequence+" starts at "+strconv.FormatInt(start, 10)+
				" and increments by "+strconv.FormatInt(increment, 10)+"; 0001_baseline.sql creates it with the defaults")
		}
		if ext != "" && !pt.exts[ext] {
			if pt.exts == nil {
				pt.exts = map[string]bool{}
			}
			pt.exts[ext] = true
			from := " comes from the extension " + ext
			if over != "" {
				from = " is over " + over + ", from the extension " + ext
			}
			r.baselineNote(pt, "", "the type "+c.Type+from+"; add CREATE EXTENSION "+ext+
				" to 0001_baseline.sql before the tables, then run migrate hash")
		}
		if domain != "" && !pt.domains[domain] {
			if pt.domains == nil {
				pt.domains = map[string]bool{}
			}
			pt.domains[domain] = true
			r.baselineNote(pt, "", "the type "+domain+" is a domain, which 0001_baseline.sql does not create; add its CREATE DOMAIN"+
				" before the tables, then run migrate hash")
		}
		pt.t.Columns = append(pt.t.Columns, c)
		return nil
	})
	return names, err
}

// The indexes but those of exclusion constraints. The primary key's is read
// for what the model cannot hold, and is no Index; a unique constraint's is
// an Index with Constraint set.
const pgIndexesQuery = `SELECT ic.relname, i.indisunique, i.indisvalid, i.indexprs IS NOT NULL, i.indkey::text, i.indnkeyatts,
	i.indnatts, coalesce(pg_get_expr(i.indpred, i.indrelid), ''), am.amname, i.indoption::text,
	coalesce((to_jsonb(i) ->> 'indnullsnotdistinct')::boolean, false),
	EXISTS (SELECT 1 FROM generate_series(0, i.indnkeyatts - 1) k JOIN pg_opclass oc ON oc.oid = i.indclass[k]
		WHERE NOT oc.opcdefault),
	EXISTS (SELECT 1 FROM generate_series(0, i.indnkeyatts - 1) k
		JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = i.indkey[k]
		WHERE i.indcollation[k] <> a.attcollation),
	i.indisprimary,
	EXISTS (SELECT 1 FROM pg_constraint k WHERE k.conindid = i.indexrelid AND k.conrelid = i.indrelid AND k.contype = 'u')
FROM pg_index i JOIN pg_class ic ON ic.oid = i.indexrelid JOIN pg_am am ON am.oid = ic.relam
WHERE i.indrelid = $1::bigint::oid
	AND NOT EXISTS (SELECT 1 FROM pg_constraint k WHERE k.conindid = i.indexrelid AND k.conrelid = i.indrelid AND k.contype = 'x')
ORDER BY ic.relname`

// pgUnheld is what an index has that the model cannot hold, in the words
// of the note: the method but btree, DESC, NULLS FIRST or LAST, an
// operator class or collation of its own, INCLUDE columns, NULLS NOT
// DISTINCT. option is pg_index.indoption as text.
func pgUnheld(method, option string, opclass, collat, notDistinct bool, keys, atts int) ([]string, error) {
	var has []string
	if method != "btree" {
		has = append(has, "the "+method+" method")
	}
	var desc, nullsFirst, nullsLast bool
	for f := range strings.FieldsSeq(option) {
		o, err := strconv.Atoi(f)
		if err != nil {
			return nil, err
		}
		d := o&1 != 0
		desc = desc || d
		nullsFirst = nullsFirst || (!d && o&2 != 0)
		nullsLast = nullsLast || (d && o&2 == 0)
	}
	for _, x := range []struct {
		on   bool
		what string
	}{
		{desc, "DESC"}, {nullsFirst, "NULLS FIRST"}, {nullsLast, "NULLS LAST"},
		{opclass, "an operator class of its own"}, {collat, "a collation of its own"},
		{atts > keys, "INCLUDE columns"}, {notDistinct, "NULLS NOT DISTINCT"},
	} {
		if x.on {
			has = append(has, x.what)
		}
	}
	return has, nil
}

// indexes reads pt's indexes, columns by names. An invalid index is a note
// and left out; so is an index over an expression, which the model cannot
// hold. What else an index has that the model cannot hold (a method but
// btree, DESC, NULLS FIRST or LAST, an operator class or collation of its
// own, INCLUDE, NULLS NOT DISTINCT) is a note, and the index is read without
// it. A predicate is kept as PostgreSQL prints it. A unique constraint's
// index has Constraint set. The primary key's index is no Index, the
// primary key being read from its constraint; it is read only for what it
// has that the model cannot hold, which is one note.
// PostgreSQL itself refuses a primary key of DESC, NULLS FIRST or LAST, an
// operator class or collation of its own (SQLSTATE 42809), so INCLUDE is
// what can be there.
func (r *pgReader) indexes(pt *pgTable, names map[int]string) error {
	return r.each(pgIndexesQuery, []any{pt.oid}, func(rows *sql.Rows) error {
		var (
			ix                                                 Index
			valid, expr, notDistinct, opclass, collat, primary bool
			key, method, option                                string
			keys, atts                                         int
		)
		if err := rows.Scan(&ix.Name, &ix.Unique, &valid, &expr, &key, &keys, &atts, &ix.Where,
			&method, &option, &notDistinct, &opclass, &collat, &primary, &ix.Constraint); err != nil {
			return err
		}
		if primary {
			has, err := pgUnheld(method, option, opclass, collat, notDistinct, keys, atts)
			if err != nil {
				return err
			}
			if len(has) > 0 {
				r.note(pt, ix.Name, "the primary key's index has "+strings.Join(has, ", ")+", which the model cannot hold; the key is compared without")
			}
			return nil
		}
		if !valid {
			r.note(pt, ix.Name, "the index is invalid, as a failed CREATE INDEX CONCURRENTLY leaves it, and serves no query; drop it and create it again")
			return nil
		}
		nums := strings.Fields(key)
		for _, f := range nums[:min(keys, len(nums))] {
			n, err := strconv.Atoi(f)
			if err != nil {
				return err
			}
			if names[n] == "" {
				expr = true
				break
			}
			ix.Columns = append(ix.Columns, names[n])
		}
		if expr {
			r.note(pt, ix.Name, "the index is over an expression, which the model cannot hold; it is not compared")
			return nil
		}
		has, err := pgUnheld(method, option, opclass, collat, notDistinct, keys, atts)
		if err != nil {
			return err
		}
		if len(has) > 0 {
			r.note(pt, ix.Name, "the index has "+strings.Join(has, ", ")+", which the model cannot hold; it is compared without")
		}
		pt.t.Indexes = append(pt.t.Indexes, ix)
		return nil
	})
}

// The primary key, foreign keys and checks; a column list as JSON, so any
// name reads back.
const pgConstraintsQuery = `SELECT k.conname, k.contype::text,
	coalesce((SELECT json_agg(a.attname ORDER BY u.o) FROM unnest(k.conkey) WITH ORDINALITY u(n, o)
		JOIN pg_attribute a ON a.attrelid = k.conrelid AND a.attnum = u.n), '[]')::text,
	coalesce((SELECT json_agg(a.attname ORDER BY u.o) FROM unnest(k.confkey) WITH ORDINALITY u(n, o)
		JOIN pg_attribute a ON a.attrelid = k.confrelid AND a.attnum = u.n), '[]')::text,
	coalesce(rn.nspname, ''), coalesce(rc.relname, ''), k.confdeltype::text, k.convalidated,
	coalesce(pg_get_expr(k.conbin, k.conrelid), ''), k.confupdtype::text, k.condeferrable, k.confmatchtype::text
FROM pg_constraint k
LEFT JOIN pg_class rc ON rc.oid = k.confrelid
LEFT JOIN pg_namespace rn ON rn.oid = rc.relnamespace
WHERE k.conrelid = $1::bigint::oid AND k.contype IN ('p', 'f', 'c', 'u', 'x')
ORDER BY k.conname`

// pgOnDelete is the model's ON DELETE of confdeltype, and of confupdtype.
var pgOnDelete = map[string]string{"a": "", "r": "RESTRICT", "c": "CASCADE", "n": "SET NULL", "d": "SET DEFAULT"}

// constraints reads pt's primary key, foreign keys and checks. A unique
// constraint is left out, its index read. An enum
// check is in the model's form, any other check as PostgreSQL prints it.
// What a constraint has that the model cannot hold is one note: NOT VALID,
// an exclusion constraint, DEFERRABLE, and a foreign key's ON UPDATE or
// MATCH FULL.
func (r *pgReader) constraints(pt *pgTable) error {
	return r.each(pgConstraintsQuery, []any{pt.oid}, func(rows *sql.Rows) error {
		var (
			name, typ, cols, refCols, refNsp, refName, onDelete, expr, onUpdate, match string
			validated, deferrable                                                      bool
		)
		if err := rows.Scan(&name, &typ, &cols, &refCols, &refNsp, &refName, &onDelete, &validated, &expr,
			&onUpdate, &deferrable, &match); err != nil {
			return err
		}
		var columns, refColumns []string
		if err := json.Unmarshal([]byte(cols), &columns); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(refCols), &refColumns); err != nil {
			return err
		}
		var says []string
		if !validated {
			says = append(says, "the constraint is NOT VALID: the rows from before it are not checked; VALIDATE CONSTRAINT checks them")
		}
		var has []string
		var keeps []keep
		held := func(what, word string) {
			has = append(has, what)
			keeps = append(keeps, keep{what, []string{word}})
		}
		if typ == "x" {
			held("an exclusion constraint", "EXCLUDE")
		}
		if deferrable {
			held("DEFERRABLE", "DEFERRABLE")
		}
		if typ == "f" && onUpdate != "a" {
			held("ON UPDATE "+pgOnDelete[onUpdate], "UPDATE")
		}
		if typ == "f" && match == "f" {
			held("MATCH FULL", "MATCH")
		}
		if len(has) > 0 {
			says = append(says, "the constraint is "+strings.Join(has, ", ")+", which the model cannot hold; it is compared without")
		}
		if len(says) > 0 {
			r.note(pt, name, strings.Join(says, "; "))
			r.notes[len(r.notes)-1].keeps = keeps
			r.notes[len(r.notes)-1].constraint = true
		}
		switch typ {
		case "p":
			pt.t.PrimaryKey, pt.t.PrimaryKeyName = columns, name
		case "f":
			pt.t.ForeignKeys = append(pt.t.ForeignKeys, ForeignKey{
				Name: name, Columns: columns, RefColumns: refColumns, OnDelete: pgOnDelete[onDelete],
				RefTable: TableRef{Schema: r.schema(refNsp), Name: refName},
			})
		case "c":
			pt.t.Checks = append(pt.t.Checks, Check{Name: name, Expr: pt.enumCheck(expr)})
		}
		return nil
	})
}

// enumCheck is expr in the model's form when it holds a column to values,
// "col" IN ('a', 'b'), and as printed when not. On an enum column the
// casts PostgreSQL adds to its type are dropped first.
func (pt *pgTable) enumCheck(expr string) string {
	column := func(s string) (col string, values []string, ok bool) {
		return parseEnumCheck(velox.Postgres, s, pt.t.Columns)
	}
	if col, values, ok := column(expr); ok {
		return ddl.EnumCheck(velox.Postgres, col, values)
	}
	for _, c := range pt.t.Columns {
		typ, ok := pt.enums[c.Name]
		if !ok {
			continue
		}
		if col, values, ok := column(dropCasts(expr, typ)); ok && col == c.Name {
			return ddl.EnumCheck(velox.Postgres, col, values)
		}
	}
	return expr
}

// dropCasts is expr without its casts to typ (and to typ[]), outside
// literals and quoted names.
func dropCasts(expr, typ string) string {
	var b strings.Builder
	cast := "::" + typ
	for i := 0; i < len(expr); {
		switch c := expr[i]; {
		case c == '\'' || c == '"':
			j := i + 1
			for j < len(expr) {
				if expr[j] == c {
					if j+1 < len(expr) && expr[j+1] == c {
						j += 2
						continue
					}
					break
				}
				j++
			}
			j = min(j+1, len(expr))
			b.WriteString(expr[i:j])
			i = j
		case strings.HasPrefix(expr[i:], cast) && !nameByte(expr, i+len(cast)):
			i += len(cast)
			if strings.HasPrefix(expr[i:], "[]") {
				i += 2
			}
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// nameByte reports whether s[i] goes on a name: a letter, digit, _, $ or .
func nameByte(s string, i int) bool {
	if i >= len(s) {
		return false
	}
	c := s[i]
	return c == '_' || c == '$' || c == '.' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}
