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

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/ddl"
)

// pgTable is a table inspectPostgres reads: its oid, its schema as the
// database names it, and the model's table.
type pgTable struct {
	oid    int64
	schema string
	t      Table
}

// pgEnum is an enum type a column uses.
type pgEnum struct {
	oid int64
	ref TableRef
}

// inspectPostgres reads tables through pg_catalog, the default schema being
// current_schema().
func inspectPostgres(ctx context.Context, q querier, tables []TableRef) (*Schema, []note, error) {
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
	enums := make([]pgEnum, 0, len(r.enums))
	for oid, ref := range r.enums {
		enums = append(enums, pgEnum{oid, ref})
	}
	slices.SortFunc(enums, func(a, b pgEnum) int { return cmp.Compare(a.oid, b.oid) })
	for _, e := range enums {
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
	rows, err := r.q.QueryContext(r.ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
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

const pgTablesQuery = `SELECT c.oid::bigint, n.nspname, c.relname, coalesce(obj_description(c.oid, 'pg_class'), '')
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'p') AND n.nspname = $1`

// tables finds the tables to read: those of refs that are there, once each,
// or with refs nil every table of the default schema but velox_migrations
// and partitions, which belong to their table.
func (r *pgReader) tables(refs []TableRef) ([]pgTable, error) {
	var out []pgTable
	scan := func(rows *sql.Rows) error {
		var pt pgTable
		if err := rows.Scan(&pt.oid, &pt.schema, &pt.t.Name, &pt.t.Comment); err != nil {
			return err
		}
		if !slices.ContainsFunc(out, func(x pgTable) bool { return x.oid == pt.oid }) {
			pt.t.Schema = r.schema(pt.schema)
			out = append(out, pt)
		}
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

// object is a Warning's Object for something of pt named name.
func (pt *pgTable) object(name string) string { return pt.t.Ref().String() + "." + name }

func (r *pgReader) note(pt *pgTable, name, message string) {
	r.notes = append(r.notes, note{table: pt.t.Ref(), object: pt.object(name), message: message})
}

// The columns, with what makes a serial: the sequence the column owns,
// by an auto (serial) or internal (identity) dependency, and its regclass
// spelling, as a nextval default prints it.
const pgColumnsQuery = `SELECT a.attnum, a.attname, format_type(a.atttypid, a.atttypmod), t.typtype::text, t.oid::bigint,
	tn.nspname, t.typname, a.attnotnull, coalesce(pg_get_expr(d.adbin, d.adrelid), ''),
	a.attidentity::text, a.attgenerated::text, coalesce(col_description(a.attrelid, a.attnum), ''),
	coalesce(seq.nspname, ''), coalesce(seq.relname, ''), coalesce(seq.reg, '')
FROM pg_attribute a
JOIN pg_type t ON t.oid = a.atttypid
JOIN pg_namespace tn ON tn.oid = t.typnamespace
LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
LEFT JOIN LATERAL (
	SELECT sn.nspname, s.relname, s.oid::regclass::text AS reg
	FROM pg_depend dep
	JOIN pg_class s ON s.oid = dep.objid AND s.relkind = 'S'
	JOIN pg_namespace sn ON sn.oid = s.relnamespace
	WHERE dep.classid = 'pg_class'::regclass AND dep.refclassid = 'pg_class'::regclass
		AND dep.refobjid = a.attrelid AND dep.refobjsubid = a.attnum AND dep.deptype IN ('a', 'i')
	ORDER BY s.relname LIMIT 1
) seq ON true
WHERE a.attrelid = $1::bigint::oid AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY a.attnum`

// columns reads pt's columns, and returns their names by number.
func (r *pgReader) columns(pt *pgTable) (map[int]string, error) {
	names := map[int]string{}
	err := r.each(pgColumnsQuery, []any{pt.oid}, func(rows *sql.Rows) error {
		var (
			num                                       int
			c                                         Column
			typ, typtype, tnsp, tname, def            string
			identity, generated, seqNsp, seqName, reg string
			typOID                                    int64
			notNull                                   bool
		)
		if err := rows.Scan(&num, &c.Name, &typ, &typtype, &typOID, &tnsp, &tname, &notNull, &def,
			&identity, &generated, &c.Comment, &seqNsp, &seqName, &reg); err != nil {
			return err
		}
		names[num] = c.Name
		c.Nullable = !notNull
		c.Type = ddl.NormalizeType(velox.Postgres, typ)
		if typtype == "e" {
			ref := TableRef{Schema: r.schema(tnsp), Name: tname}
			c.Type = ddl.EnumType(ref.Schema, ref.Name)
			r.enums[typOID] = ref
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
		pt.t.Columns = append(pt.t.Columns, c)
		return nil
	})
	return names, err
}

// The indexes but the primary key and those of exclusion constraints.
const pgIndexesQuery = `SELECT ic.relname, i.indisunique, i.indisvalid, i.indexprs IS NOT NULL, i.indkey::text, i.indnkeyatts,
	coalesce(pg_get_expr(i.indpred, i.indrelid), '')
FROM pg_index i JOIN pg_class ic ON ic.oid = i.indexrelid
WHERE i.indrelid = $1::bigint::oid AND NOT i.indisprimary
	AND NOT EXISTS (SELECT 1 FROM pg_constraint k WHERE k.conindid = i.indexrelid AND k.conrelid = i.indrelid AND k.contype = 'x')
ORDER BY ic.relname`

// indexes reads pt's indexes, columns by names. An invalid index is a note
// and left out; so is an index over an expression, which the model cannot
// hold. A predicate is kept as PostgreSQL prints it.
func (r *pgReader) indexes(pt *pgTable, names map[int]string) error {
	return r.each(pgIndexesQuery, []any{pt.oid}, func(rows *sql.Rows) error {
		var (
			ix          Index
			valid, expr bool
			key         string
			keys        int
		)
		if err := rows.Scan(&ix.Name, &ix.Unique, &valid, &expr, &key, &keys, &ix.Where); err != nil {
			return err
		}
		if !valid {
			r.note(pt, ix.Name, "the index is invalid, as a failed CREATE INDEX CONCURRENTLY leaves it, and serves no query; drop it and create it again")
			return nil
		}
		if expr {
			return nil
		}
		for i, f := range strings.Fields(key) {
			n, err := strconv.Atoi(f)
			if err != nil {
				return err
			}
			if i >= keys {
				break // INCLUDE columns
			}
			if names[n] == "" {
				return nil // an expression
			}
			ix.Columns = append(ix.Columns, names[n])
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
	coalesce(pg_get_expr(k.conbin, k.conrelid), '')
FROM pg_constraint k
LEFT JOIN pg_class rc ON rc.oid = k.confrelid
LEFT JOIN pg_namespace rn ON rn.oid = rc.relnamespace
WHERE k.conrelid = $1::bigint::oid AND k.contype IN ('p', 'f', 'c')
ORDER BY k.conname`

// pgOnDelete is the model's ON DELETE of confdeltype.
var pgOnDelete = map[string]string{"a": "", "r": "RESTRICT", "c": "CASCADE", "n": "SET NULL", "d": "SET DEFAULT"}

// constraints reads pt's primary key, foreign keys and checks. A unique
// constraint is left out: its index is read. An enum check is in the
// model's form, any other check as PostgreSQL prints it.
func (r *pgReader) constraints(pt *pgTable) error {
	return r.each(pgConstraintsQuery, []any{pt.oid}, func(rows *sql.Rows) error {
		var (
			name, typ, cols, refCols, refNsp, refName, onDelete, expr string
			validated                                                 bool
		)
		if err := rows.Scan(&name, &typ, &cols, &refCols, &refNsp, &refName, &onDelete, &validated, &expr); err != nil {
			return err
		}
		var columns, refColumns []string
		if err := json.Unmarshal([]byte(cols), &columns); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(refCols), &refColumns); err != nil {
			return err
		}
		if !validated {
			r.note(pt, name, "the constraint is NOT VALID: the rows from before it are not checked; VALIDATE CONSTRAINT checks them")
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
			if col, values, ok := ddl.ParseEnumCheck(velox.Postgres, normalizePredicate(velox.Postgres, expr)); ok &&
				slices.ContainsFunc(pt.t.Columns, func(c Column) bool { return c.Name == col }) {
				expr = ddl.EnumCheck(velox.Postgres, col, values)
			}
			pt.t.Checks = append(pt.t.Checks, Check{Name: name, Expr: expr})
		}
		return nil
	})
}
