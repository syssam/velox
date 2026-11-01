package migrate

import (
	"maps"
	"slices"
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/ddl"
)

// A statement that replaces a column writes all it keeps, or on PostgreSQL its type
// and default, so what it does not write is gone. What it writes is read here and
// compared with the column the database holds, as a SQLite rebuild's new table is.

// raw is the text from word i up to word j.
func (l *fileLinter) raw(i, j int) string {
	if i >= len(l.ws) || i >= j {
		return ""
	}
	return l.textUpTo(l.ws[i].start, j)
}

// rawAfter is the text from the end of word i up to word j.
func (l *fileLinter) rawAfter(i, j int) string { return l.textUpTo(l.ws[i].end, j) }

func (l *fileLinter) textUpTo(from, j int) string {
	end := len(l.src)
	if j < len(l.ws) {
		end = l.ws[j].start
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(l.src[from:end]), ";"))
}

// mysqlAttrs are the words that end a column's type or a part of its definition in a
// MySQL statement.
var mysqlAttrs = []string{
	"NOT", "NULL", "AUTO_INCREMENT", "DEFAULT", "ON", "COMMENT", "COLLATE", "CHARACTER", "CHARSET",
	"VISIBLE", "INVISIBLE", "UNIQUE", "PRIMARY", "KEY", "CHECK", "CONSTRAINT", "REFERENCES",
	"GENERATED", "AS", "VIRTUAL", "STORED", "COLUMN_FORMAT", "STORAGE", "ENGINE_ATTRIBUTE",
	"SECONDARY_ENGINE_ATTRIBUTE", "SRID", "FIRST", "AFTER",
}

// mysqlAttrAt is the first of mysqlAttrs outside parentheses from word i on, or end.
func (l *fileLinter) mysqlAttrAt(i, end int) int {
	for depth := 0; i < end; i++ {
		switch {
		case l.punct(i, "("):
			depth++
		case l.punct(i, ")"):
			depth--
		case depth == 0 && slices.Contains(mysqlAttrs, l.upper(i)):
			return i
		}
	}
	return end
}

// mysqlStated is the column a MODIFY or CHANGE writes in words [i, end), by the name
// of live. A key's column stays NOT NULL though nothing says so.
func mysqlStated(l *fileLinter, live Column, key []string, i, end int) Column {
	at := l.mysqlAttrAt(i, end)
	out := Column{Name: live.Name, Type: mysqlStatedType(l.raw(i, at)), Nullable: !slices.ContainsFunc(key, func(k string) bool { return strings.EqualFold(k, live.Name) })}
	for j := at; j < end; {
		next := l.mysqlAttrAt(j+1, end)
		switch l.upper(j) {
		case "NOT":
			out.Nullable = false
			next = l.mysqlAttrAt(j+2, end)
		case "NULL":
			out.Nullable = true
		case "AUTO_INCREMENT":
			out.Identity = true
		case "PRIMARY":
			out.Nullable = false
		case "DEFAULT":
			if l.upper(j+1) == "NULL" {
				next = l.mysqlAttrAt(j+2, end)
				break
			}
			out.Default = normalizeDefault(velox.MySQL, l.rawAfter(j, next))
		case "COMMENT":
			if text, ok := ddl.Unquote(velox.MySQL, l.rawAfter(j, next)); ok {
				out.Comment = text
			}
		}
		j = next
	}
	return out
}

// mysqlStatedType is a type as a statement writes it, in Inspect's spelling.
func mysqlStatedType(raw string) string {
	t := ddl.NormalizeType(velox.MySQL, raw)
	if values, ok := ddl.ParseMySQLEnum(t); ok {
		return ddl.MySQLEnum(values)
	}
	return t
}

// statedChanges is what stated changes of live, spellings of one value apart, as
// drift reads them. A type and default are compared alone: an enum type or an
// identity is no matter here, and an expression drift would warn of and take is a
// change.
func statedChanges(d velox.Dialect, live, stated Column) columnMask {
	a, b := Column{Name: "c", Type: live.Type, Nullable: true, Default: live.Default}, Column{Name: "c", Type: stated.Type, Nullable: true, Default: stated.Default}
	if d == velox.Postgres && !numberType(d, a.Type) {
		a.Type, b.Type = "text", "text"
	}
	one := func(c Column) *Schema {
		return &Schema{Dialect: d, Tables: []Table{{Name: "t", Columns: []Column{c}}}}
	}
	mask := columnChange(live, stated) &^ (columnType | columnDefault | columnIdentity)
	if live.Identity != stated.Identity {
		mask |= columnIdentity
	}
	if live.Type != stated.Type {
		mask |= columnType
	}
	if d == velox.MySQL {
		a.Default, b.Default = mysqlOneDefault(a.Type, a.Default), mysqlOneDefault(a.Type, b.Default)
	}
	if a.Default == b.Default || d == velox.MySQL && mysqlSameBinary(a.Type, a.Default, b.Default) {
		return mask
	}
	if a.Default != "" && b.Default != "" && !literalDefault(d, a.Default) && !literalDefault(d, b.Default) {
		if same, _ := samePredicateIn(d, a.Default, b.Default, nil); !same {
			mask |= columnDefault
		}
		return mask
	}
	if cmpr, err := compare(one(a), nil, one(b), false); err != nil || cmpr.db.Tables[0].Columns[0].Default != b.Default {
		mask |= columnDefault
	}
	return mask
}

// mysqlReplaced compares the column a MODIFY or CHANGE writes in [i, end) with the
// one the database holds, attribute by attribute, and what it writes is the column
// from here on. A difference the file's drops do not name is refused.
func (w *recreates) mysqlReplaced(l *fileLinter, st *tableState, ref TableRef, origin, name string, i, end int) {
	k := slices.IndexFunc(st.def.Columns, func(c Column) bool { return strings.EqualFold(c.Name, origin) })
	if k < 0 {
		return
	}
	live := st.def.Columns[k]
	stated := mysqlStated(l, live, st.def.PrimaryKey, i, end)
	if strings.EqualFold(name, live.Name) {
		name = live.Name
	}
	target := colTarget(ref, name)
	lines := statedLines(velox.MySQL, live, stated, statedChanges(velox.MySQL, live, stated))
	for _, attr := range slices.Sorted(maps.Keys(lines)) {
		w.attrLost(ref, target, attr, lines[attr], columnValue(live, attr), func(want string) bool { return sameColumnValue(velox.MySQL, live, attr, want) })
	}
	st.def.Columns[k].Type, st.def.Columns[k].Nullable, st.def.Columns[k].Default = stated.Type, stated.Nullable, stated.Default
	st.def.Columns[k].Comment, st.def.Columns[k].Identity = stated.Comment, stated.Identity
}

// pgRetyped compares what the statement that retypes the column writes in [i, end)
// with the column the database holds. It says the type itself, so a type is compared
// only with what a line of the file expects of it; and it leaves a default it does
// not name alone, so one it drops or sets is compared.
func (w *recreates) pgRetyped(l *fileLinter, st *tableState, ref TableRef, origin, name string, i, end int) {
	k := slices.IndexFunc(st.def.Columns, func(c Column) bool { return c.Name == origin })
	if k < 0 {
		return
	}
	live := st.def.Columns[k]
	target := colTarget(ref, name)
	for j := i; j < end; j++ {
		if l.upper(j) == "COLLATE" {
			end = j
		}
	}
	if typ := ddl.NormalizeType(velox.Postgres, l.raw(i, end)); typ != live.Type && w.expects(ref, target+":type") {
		w.attrLost(ref, target, "type", "the column is "+live.Type+", and the statement writes "+typ, live.Type,
			func(want string) bool { return sameColumnValue(velox.Postgres, live, "type", want) })
	}
	def, set := l.defaultOf(w, name)
	if !set {
		return
	}
	stated := live
	stated.Default = def
	lines := statedLines(velox.Postgres, live, stated, statedChanges(velox.Postgres, live, stated)&columnDefault)
	for _, attr := range slices.Sorted(maps.Keys(lines)) {
		w.attrLost(ref, target, attr, lines[attr], columnValue(live, attr), func(want string) bool { return sameColumnValue(velox.Postgres, live, attr, want) })
	}
}

// columnValue is the attribute attr of c as a velox:drops line records it. The plan
// writes the line with what it believes, and Up compares it with what is there.
func columnValue(c Column, attr string) string {
	switch attr {
	case "type":
		return c.Type
	case "not-null":
		if c.Nullable {
			return "nullable"
		}
		return "not-null"
	case "default":
		return c.Default
	case "identity":
		if c.Identity {
			return "identity"
		}
		return "none"
	case "comment":
		return c.Comment
	}
	return ""
}

// sameColumnValue reports whether want, a line's value, is what c holds of attr,
// spellings of one value apart.
func sameColumnValue(d velox.Dialect, c Column, attr, want string) bool {
	switch attr {
	case "type":
		return mysqlEnumOrType(d, want) == c.Type
	case "default":
		stated := c
		stated.Default = normalizeDefault(d, want)
		return statedChanges(d, c, stated)&columnDefault == 0
	}
	return columnValue(c, attr) == want
}

// mysqlEnumOrType is a type as a line records it, in Inspect's spelling.
func mysqlEnumOrType(d velox.Dialect, typ string) string {
	if d == velox.MySQL {
		return mysqlStatedType(typ)
	}
	return ddl.NormalizeType(d, typ)
}

// expected is what a line of f records the attribute object to be: "-- velox:drops
// t.c:type=int" says int. A line with no value expects nothing and accepts any.
func expected(f File, object string) (string, bool) {
	for _, line := range f.Directives.Drops {
		if v, ok := strings.CutPrefix(line, object+"="); ok {
			return v, true
		}
	}
	return "", false
}

// spellings is a target as the walk names it and as a plan may: with the schema or
// not, as its table is.
func (w *recreates) spellings(ref TableRef, o string) []string {
	out := []string{o}
	if ref.Schema == "" && w.s.home() != "" {
		out = append(out, w.s.home()+"."+o)
	} else if bare, ok := strings.CutPrefix(o, ref.Schema+"."); ok {
		out = append(out, bare)
	}
	return out
}

// expectedOf is what a line of the file being read or the one checked records of
// object, under any of its spellings.
func (w *recreates) expectedOf(ref TableRef, object string) (string, bool) {
	for _, n := range w.spellings(ref, object) {
		for _, f := range []File{w.f, w.file} {
			if v, ok := expected(f, n); ok {
				return v, true
			}
		}
	}
	return "", false
}

// expects reports whether a line records a value of object.
func (w *recreates) expects(ref TableRef, object string) bool {
	_, ok := w.expectedOf(ref, object)
	return ok
}

// attrLost records what the statement changes of the attribute attr of target, the
// database holding holds, unless a line of the file expects what is there or a line
// with no value or a directive accepts it. line says what changes. A line that expects
// something else is a plan made over another database: a hand edit it would undo.
func (w *recreates) attrLost(ref TableRef, target, attr, line, holds string, same func(want string) bool) {
	object := target + ":" + attr
	want, ok := w.expectedOf(ref, object)
	switch {
	case !ok:
		w.lost(ref, []string{target}, object, object+": "+line)
	case !same(want):
		w.lost(ref, []string{target}, object, object+": the plan expects "+orNone(want)+", and the database holds "+orNone(holds)+": "+line)
	}
}

// orNone is a value in a line, or "nothing" for an empty one.
func orNone(s string) string {
	if s == "" {
		return "nothing"
	}
	return s
}

// defaultOf is the default the statement leaves the column name with, when one of
// its actions sets or drops it.
func (l *fileLinter) defaultOf(w *recreates, name string) (def string, set bool) {
	l.eachDefault(w, func(col, d string) {
		if col == w.key(name) {
			def, set = d, true
		}
	})
	return def, set
}

// eachDefault calls f, in order, with the column and default of each action of the
// statement that sets or drops a column's default, "" when it drops it.
func (l *fileLinter) eachDefault(w *recreates, f func(col, def string)) {
	for i := 0; i+2 < len(l.ws); i++ {
		if l.upper(i) != "ALTER" || l.upper(i+1) == "TABLE" {
			continue
		}
		col, j, ok := w.columnAt(l, l.skip(i+1, "COLUMN"))
		if !ok {
			continue
		}
		switch {
		case l.upper(j) == "DROP" && l.upper(j+1) == "DEFAULT":
			f(w.key(col), "")
		case l.upper(j) == "SET" && l.upper(j+1) == "DEFAULT":
			end, depth := j+2, 0
			for ; end < len(l.ws); end++ {
				if l.punct(end, "(") {
					depth++
				} else if l.punct(end, ")") {
					depth--
				} else if depth == 0 && l.punct(end, ",") {
					break
				}
			}
			f(w.key(col), normalizeDefault(l.d, l.rawAfter(j+1, end)))
		}
	}
}

// defaultsSet keeps the walk's columns as the statement's defaults leave them, for
// the statements after it.
func (w *recreates) defaultsSet(l *fileLinter, st *tableState) error {
	if w.quiet || st.made {
		return nil
	}
	var err error
	l.eachDefault(w, func(col, def string) {
		origin := w.origin(st.cols, col)
		if origin == "" || err != nil {
			return
		}
		if err = w.load(st); err != nil {
			return
		}
		k := slices.IndexFunc(st.def.Columns, func(c Column) bool { return w.key(c.Name) == origin })
		if k >= 0 {
			st.def.Columns[k].Default = def
		}
	})
	return err
}

// setsAgain reports whether a statement of the file sets what words names of the
// column, when it is a storage or compression, which ALTER COLUMN TYPE cannot write.
func (w *recreates) setsAgain(name string, words []string) bool {
	if len(words) != 1 || words[0] != "STORAGE" && words[0] != "COMPRESSION" {
		return false
	}
	return slices.ContainsFunc(w.f.Statements, func(q string) bool {
		l := &fileLinter{d: w.s.d, ws: lex(w.s.d, q)}
		for i := 0; i+1 < len(l.ws); i++ {
			if l.upper(i) != "ALTER" || l.upper(i+1) == "TABLE" {
				continue
			}
			col, j, ok := w.columnAt(l, l.skip(i+1, "COLUMN"))
			if ok && w.key(col) == w.key(name) && l.upper(j) == "SET" && l.upper(j+1) == words[0] {
				return true
			}
		}
		return false
	})
}

// statedLines is a line for each thing mask says stated changes of live, by the name
// a velox:drops line gives it.
func statedLines(d velox.Dialect, live, stated Column, mask columnMask) map[string]string {
	out := map[string]string{}
	or := func(s, none string) string {
		if s == "" {
			return none
		}
		return s
	}
	if mask&columnType != 0 {
		out["type"] = "the column is " + live.Type + ", and the statement writes " + stated.Type
	}
	if mask&columnNullable != 0 {
		out["not-null"] = "the column is NOT NULL, and the statement makes it nullable"
		if live.Nullable {
			out["not-null"] = "the column is nullable, and the statement makes it NOT NULL"
		}
	}
	if mask&columnDefault != 0 {
		out["default"] = "the column has " + or(live.Default, "no default") + " as its default, and the statement writes " + or(stated.Default, "none")
	}
	if mask&columnIdentity != 0 && d == velox.MySQL {
		out["identity"] = "the column is AUTO_INCREMENT, and the statement does not write it"
		if stated.Identity {
			out["identity"] = "the column is not AUTO_INCREMENT, and the statement makes it so"
		}
	}
	if mask&columnComment != 0 {
		out["comment"] = "the column has the comment " + or(ddl.Literal(velox.MySQL, live.Comment), "''") + ", and the statement writes " + ddl.Literal(velox.MySQL, stated.Comment)
	}
	return out
}

// columnAttrs is what c changes that a velox:drops line names, of the statement that
// rewrites the column: the plan's change list.
func columnAttrs(what columnMask) []string {
	var out []string
	add := func(on columnMask, attr string) {
		if what&on != 0 {
			out = append(out, attr)
		}
	}
	add(columnType, "type")
	add(columnNullable, "not-null")
	add(columnDefault, "default")
	add(columnIdentity, "identity")
	add(columnComment, "comment")
	return out
}
