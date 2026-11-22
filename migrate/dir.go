package migrate

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/buildinfo"
)

// Config is a directory's settings, the content of velox.json.
type Config struct {
	Dialect     velox.Dialect
	Table       string        // default "velox_migrations"
	LockTimeout time.Duration // default 5s
}

// The defaults of velox.json's optional keys, and the longest history table
// name, the planner's cap on an identifier.
const (
	defaultTable       = "velox_migrations"
	defaultLockTimeout = 5 * time.Second
	maxTableName       = 63
)

// identifier is what a history table's name must match.
var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// A Dir is a migration directory as ReadDir read it, or an empty one NewDir
// made.
type Dir struct {
	Config   Config
	Files    []File  // by Version
	Snapshot *Schema // the model after the last file; the empty schema of the dialect when there are no files

	sums  map[string]string // path → h1 of each file ReadDir read: the SQL files, schema.json, velox.json
	fresh bool              // made by NewDir: Add writes velox.json too
}

// check fills Config's defaults when fill is set, and refuses what velox.json
// refuses: an unknown dialect, a table that is not an identifier of at most 63
// bytes, a negative lock timeout.
func (c Config) check(fill bool) (Config, error) {
	if fill {
		c.Table = cmp.Or(c.Table, defaultTable)
		if c.LockTimeout == 0 {
			c.LockTimeout = defaultLockTimeout
		}
	}
	refuse := func(format string, args ...any) error {
		return &Error{Code: "config", Reason: fmt.Sprintf(format, args...)}
	}
	switch {
	case !knownDialect(c.Dialect):
		return Config{}, refuse("the dialect %q is not postgres, mysql or sqlite", c.Dialect.String())
	case !identifier.MatchString(c.Table) || len(c.Table) > maxTableName:
		return Config{}, refuse("the table %q is not an identifier of at most %d bytes (A-Z, a-z, 0-9 and _, not starting with a digit)", c.Table, maxTableName)
	case c.LockTimeout < 0:
		return Config{}, refuse("the lock timeout %s is negative", c.LockTimeout)
	}
	return c, nil
}

// NewDir makes the empty directory c describes, with c's defaults filled: the
// table velox_migrations and a lock timeout of 5s. A Config with a lock
// timeout of 0 takes the default; velox.json's "0s" turns it off. Add writes
// velox.json with the first plan.
func NewDir(c Config) (*Dir, error) {
	c, err := c.check(true)
	if err != nil {
		return nil, err
	}
	return &Dir{Config: c, Snapshot: &Schema{Dialect: c.Dialect}, sums: map[string]string{}, fresh: true}, nil
}

// configJSON is velox.json, as written.
type configJSON struct {
	Version     int    `json:"version"`
	Dialect     string `json:"dialect"`
	Table       string `json:"table"`
	LockTimeout string `json:"lock_timeout"`
}

// marshal is velox.json: every key, indented by two spaces, with a final
// newline.
func (c Config) marshal() []byte {
	b, _ := json.MarshalIndent(configJSON{1, c.Dialect.String(), c.Table, c.LockTimeout.String()}, "", "  ")
	return append(b, '\n')
}

// configKeys is the members of velox.json's object. It refuses what is not
// one object, a key given twice and a null value.
func configKeys(data []byte) (map[string]json.RawMessage, error) {
	bad := func(format string, args ...any) error {
		return &Error{Code: "config", Pos: "velox.json", Reason: fmt.Sprintf(format, args...)}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil {
		return nil, bad("%v", err)
	} else if tok != json.Delim('{') {
		return nil, bad("the content is not a JSON object")
	}
	out := map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, bad("%v", err)
		}
		key := tok.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, bad("%s: %v", key, err)
		}
		switch _, dup := out[key]; {
		case dup:
			return nil, bad("the key %q is given twice", key)
		case string(v) == "null":
			return nil, bad("the key %q is null; leave it out for its default", key)
		}
		out[key] = v
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return nil, bad("%v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, bad("text after the JSON object")
	}
	return out, nil
}

// parseConfig reads velox.json: the version and the dialect are required, an
// unknown key is an error, and the table and lock timeout default.
func parseConfig(data []byte) (Config, error) {
	bad := func(format string, args ...any) error {
		return &Error{Code: "config", Pos: "velox.json", Reason: fmt.Sprintf(format, args...)}
	}
	raw, err := configKeys(bytes.TrimPrefix(data, bom))
	if err != nil {
		return Config{}, err
	}
	for _, k := range slices.Sorted(maps.Keys(raw)) {
		switch k {
		case "version", "dialect", "table", "lock_timeout":
		default:
			return Config{}, bad("unknown key %q", k)
		}
	}
	get := func(key string, into any) (bool, error) {
		v, ok := raw[key]
		if !ok {
			return false, nil
		}
		if err := json.Unmarshal(v, into); err != nil {
			return true, bad("%s: %v", key, err)
		}
		return true, nil
	}
	var version int
	if ok, err := get("version", &version); err != nil {
		return Config{}, err
	} else if !ok {
		return Config{}, bad("version is required")
	} else if version != 1 {
		return Config{}, bad("version %d; this velox reads version 1", version)
	}
	var name string
	if ok, err := get("dialect", &name); err != nil {
		return Config{}, err
	} else if !ok {
		return Config{}, bad("dialect is required")
	}
	var c Config
	for _, d := range [...]velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
		if d.String() == name {
			c.Dialect = d
		}
	}
	if c.Dialect == 0 {
		return Config{}, bad("the dialect %q is not postgres, mysql or sqlite", name)
	}
	c.Table = defaultTable
	if _, err := get("table", &c.Table); err != nil {
		return Config{}, err
	}
	c.LockTimeout = defaultLockTimeout
	var timeout string
	if ok, err := get("lock_timeout", &timeout); err != nil {
		return Config{}, err
	} else if ok {
		t, err := time.ParseDuration(timeout)
		if err != nil {
			return Config{}, bad("lock_timeout: %v", err)
		}
		c.LockTimeout = t
	}
	c, err = c.check(false)
	if err != nil {
		e, _ := errors.AsType[*Error](err)
		e.Pos = "velox.json"
		return Config{}, e
	}
	return c, nil
}

// inSum reports whether velox.sum covers the top-level file name.
func inSum(name string) bool {
	return strings.HasSuffix(name, ".sql") || name == "schema.json" || name == "velox.json"
}

// topFiles is the content of the files velox.sum covers, by path.
func topFiles(fsys fs.FS) (map[string][]byte, error) {
	ents, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, &Error{Code: "directory", Reason: err.Error()}
	}
	out := map[string][]byte{}
	for _, e := range ents {
		if e.IsDir() || !inSum(e.Name()) {
			continue
		}
		data, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, &Error{Code: "directory", Reason: err.Error()}
		}
		out[e.Name()] = data
	}
	return out, nil
}

// sumText is velox.sum over sums, the h1 of each inSum file by path: the
// hash of the lines below, then a line for each path, sorted.
func sumText(sums map[string]string) []byte {
	var b strings.Builder
	for _, p := range slices.Sorted(maps.Keys(sums)) {
		b.WriteString(p + " " + sums[p] + "\n")
	}
	return []byte(sum([]byte(b.String())) + "\n" + b.String())
}

// Sum is the velox.sum fsys should have: the hash of the lines below it, then
// one line, "<path> h1:…", for each top-level *.sql file, schema.json and
// velox.json, sorted by path. Other files and directories are not inSum.
// A file's hash is over its content with a leading UTF-8 byte order mark
// dropped and CRLF read as LF, so a checkout that changes line endings gives
// the same sums.
func Sum(fsys fs.FS) ([]byte, error) {
	files, err := topFiles(fsys)
	if err != nil {
		return nil, err
	}
	sums := map[string]string{}
	for p, data := range files {
		sums[p] = sum(data)
	}
	return sumText(sums), nil
}

// ReadDir reads the migration directory fsys: velox.json, schema.json and the
// top-level *.sql files, by version. It checks velox.sum first, whatever else
// is wrong with the directory, and returns a *SumError when it is missing or
// does not match the files; velox.sum is read with CRLF as LF, as the files
// are. Then it parses velox.json (Code "config"), schema.json, which must be
// there when there are files, must not when there are none, and must be of
// velox.json's dialect, and every file with ParseFile. Two files of one
// version are an error (Code "directory"). A directory with no files has the
// empty schema of its dialect as its Snapshot.
func ReadDir(fsys fs.FS) (*Dir, error) {
	files, err := topFiles(fsys)
	if err != nil {
		return nil, err
	}
	sums := map[string]string{}
	for p, data := range files {
		sums[p] = sum(data)
	}
	have, err := fs.ReadFile(fsys, "velox.sum")
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, &SumError{Path: "velox.sum"}
	case err != nil:
		return nil, &Error{Code: "directory", Reason: err.Error()}
	}
	have = asText(have)
	if !bytes.Equal(have, sumText(sums)) {
		return nil, &SumError{Path: "velox.sum"}
	}
	raw, ok := files["velox.json"]
	if !ok {
		return nil, &Error{Code: "config", Pos: "velox.json", Reason: "the file is missing"}
	}
	cfg, err := parseConfig(raw)
	if err != nil {
		return nil, err
	}
	var names []string
	for p := range files {
		if strings.HasSuffix(p, ".sql") {
			names = append(names, p)
		}
	}
	slices.Sort(names)
	d := &Dir{Config: cfg, Snapshot: &Schema{Dialect: cfg.Dialect}, sums: sums}
	snap, haveSnap := files["schema.json"]
	switch {
	case len(names) > 0 && !haveSnap:
		return nil, &Error{Code: "directory", Pos: "schema.json", Reason: "the file is missing; the directory has migration files"}
	case len(names) == 0 && haveSnap:
		return nil, &Error{Code: "directory", Pos: "schema.json", Reason: "the directory has no migration files, so it has no snapshot"}
	case haveSnap:
		var s Schema
		if err := s.UnmarshalJSON(bytes.TrimPrefix(snap, bom)); err != nil {
			return nil, err
		}
		if s.Dialect != cfg.Dialect {
			return nil, &Error{Code: "directory", Pos: "schema.json", Reason: fmt.Sprintf("the snapshot is %s's; velox.json says %s", s.Dialect, cfg.Dialect)}
		}
		d.Snapshot = &s
	}
	for _, p := range names {
		f, err := ParseFile(cfg.Dialect, p, files[p])
		if err != nil {
			return nil, err
		}
		d.Files = append(d.Files, f)
	}
	slices.SortStableFunc(d.Files, func(a, b File) int { return cmp.Compare(a.Version, b.Version) })
	for i := 1; i < len(d.Files); i++ {
		if a, b := d.Files[i-1], d.Files[i]; a.Version == b.Version {
			return nil, &Error{Code: "directory", Reason: fmt.Sprintf("%s and %s have one version", a.Path, b.Path)}
		}
	}
	return d, nil
}

// generator is "velox-gen" and the version of the velox module the program
// is built from, "(devel)" when the build does not say, as velox-gen version
// prints it.
func generator() string { return buildinfo.Generator() }

// maxName is the longest name of a plan's files.
const maxName = 40

// fileLabel is a plan file's name beyond its version.
var fileLabel = regexp.MustCompile(`^[a-z0-9_]+$`)

// defaultName is a plan's file name when Add is given none: the kind of the
// first change that is not about an enum type (the first change when all are),
// up to its first "-", its table ("<schema>_<name>" when qualified), and
// for a column change the column: create_users, add_users_email,
// drop_posts_legacy. It is lower-cased with every other byte an underscore,
// cut to 40 bytes with no trailing underscore.
func defaultName(changes []Change) string {
	if len(changes) == 0 {
		return ""
	}
	c := changes[0]
	for _, ch := range changes {
		if !strings.HasSuffix(string(ch.Kind()), "-enum") {
			c = ch
			break
		}
	}
	kind, _, _ := strings.Cut(string(c.Kind()), "-")
	parts := []string{kind}
	if t := c.Target(); t.Schema != "" {
		parts = append(parts, t.Schema, t.Name)
	} else {
		parts = append(parts, t.Name)
	}
	switch c := c.(type) {
	case addColumn:
		parts = append(parts, c.Column.Name)
	case dropColumn:
		parts = append(parts, c.Column.Name)
	case renameColumn:
		parts = append(parts, c.To)
	case alterColumn:
		parts = append(parts, c.To.Name)
	case renameSequence:
		parts = append(parts, c.Column)
	}
	b := []byte(strings.ToLower(strings.Join(parts, "_")))
	for i, c := range b {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			b[i] = '_'
		}
	}
	if len(b) > maxName {
		b = b[:maxName]
	}
	return strings.TrimRight(string(b), "_")
}

// header is the first line of a file Add writes, naming the subcommand
// that wrote it.
func header(subcommand string) string {
	return "-- Generated by " + generator() + " migrate " + subcommand + ". Review before applying; velox.sum checks it.\n"
}

// PathOf is the path in d of the file of version, which the history names
// name; or, when d has none, the path Dir.Add would have given it.
func (d *Dir) PathOf(version uint64, name string) string {
	if i := slices.IndexFunc(d.Files, func(f File) bool { return f.Version == version }); i >= 0 {
		return d.Files[i].Path
	}
	return versionFile(version, name)
}

// Add writes plan p, which makes to, as the directory's next files, and
// returns them by path with their content: one file for each of p's scripts,
// at the last version plus one, two, three, zero-padded to four digits, named
// <version>_<name>_enum.sql, <version>_<name>.sql and
// <version>_<name>_online.sql by the script's kind; schema.json, which is
// to, with the constraints of the snapshot's indexes that it keeps;
// velox.json, when the directory is new; and velox.sum over all the
// directory's files. An empty name is the one defaultName makes. Each SQL
// file starts with a header line saying velox-gen migrate diff wrote it, or
// migrate baseline when the name is "baseline" and the directory has no file
// yet; then the script as Render wrote it, but an enum script without a lock
// timeout of its own takes the Config's: it runs outside a transaction, where
// nothing else would bound its wait. A Config lock timeout of 0s still writes
// "-- velox:lock-timeout 0s" into the enum file, which turns the timeout off
// explicitly.
//
// Add refuses, with an *Error of Code "directory", a nil plan or one with no
// script, a schema of another dialect than the directory's, and a name that
// is not of a-z, 0-9 and _ or is over 40 bytes. It does not change d, p or
// to; a second plan reads the directory again.
func (d *Dir) Add(name string, p *Plan, to *Schema) (map[string][]byte, error) {
	refuse := func(format string, args ...any) error {
		return &Error{Code: "directory", Reason: fmt.Sprintf(format, args...)}
	}
	switch {
	case p == nil || len(p.Scripts) == 0:
		return nil, refuse("the plan has no script to write")
	case to == nil:
		return nil, refuse("the schema is nil")
	case !knownDialect(d.Config.Dialect) || to.Dialect != d.Config.Dialect:
		return nil, refuse("the schema is %s's; the directory is %s's", to.Dialect, d.Config.Dialect)
	}
	if name == "" {
		name = defaultName(p.Changes)
	}
	if !fileLabel.MatchString(name) || len(name) > maxName {
		return nil, refuse("the name %q is not of a-z, 0-9 and _, or is over %d bytes", name, maxName)
	}
	snapshot, err := json.MarshalIndent(carryConstraints(d.Snapshot, to, p.Changes), "", "  ")
	if err != nil {
		return nil, err
	}
	last := uint64(0)
	for _, f := range d.Files {
		last = max(last, f.Version)
	}
	out := map[string][]byte{"schema.json": append(snapshot, '\n')}
	subcommand := "diff"
	if name == "baseline" && len(d.Files) == 0 {
		subcommand = "baseline"
	}
	for i, s := range p.Scripts {
		var suffix string
		switch s.Kind {
		case ScriptMain:
		case ScriptEnum:
			suffix = "_enum"
			if !s.Directives.LockTimeoutSet {
				s.Directives.LockTimeout, s.Directives.LockTimeoutSet = d.Config.LockTimeout, true
			}
		case ScriptOnline:
			suffix = "_online"
		default:
			return nil, refuse("script %d has the unknown kind %s", i, s.Kind)
		}
		path := versionFile(last+uint64(i)+1, name+suffix)
		var b strings.Builder
		b.WriteString(header(subcommand))
		writeScript(&b, s)
		out[path] = []byte(b.String())
	}
	if d.fresh {
		out["velox.json"] = d.Config.marshal()
	}
	sums := maps.Clone(d.sums)
	if sums == nil {
		sums = map[string]string{}
	}
	for p, data := range out {
		sums[p] = sum(data)
	}
	out["velox.sum"] = sumText(sums)
	return out, nil
}

// carryConstraints is to with Constraint set on each index that has it in
// to already (a baseline's, read from the database), or whose index in
// from, the snapshot, is a constraint: the same table, after changes'
// renameTable, and the same name, after changes' renameIndex, with the
// same Unique and Columns, from's columns mapped through changes'
// renameColumn of that table, and to's Where empty. A redefined index is
// a new one, and plain, and so is every index of a table changes create,
// even under the name of one renamed away. A MySQL table that states no
// collation gets what its table has: from's, or a new one's. It returns a
// clone; from and to are not changed.
func carryConstraints(from, to *Schema, changes []Change) *Schema {
	out := to.clone()
	if from == nil {
		for i := range out.Tables {
			statedCollation(to.Dialect, &out.Tables[i], nil)
		}
		return out
	}
	r := renamesOf(changes)
	for i := range out.Tables {
		t := &out.Tables[i]
		ref := t.Ref()
		if r.created[ref] {
			statedCollation(to.Dialect, t, nil)
			continue
		}
		oref := ref
		if o, ok := r.tables[ref]; ok {
			oref = o
		}
		ft := from.table(oref)
		statedCollation(to.Dialect, t, ft)
		if ft != nil {
			r.carryIndexes(t, ft)
		}
	}
	return out
}

// statedCollation gives t, a table of d, the collation of was, or a new
// one's, when d is MySQL and t states none.
func statedCollation(d velox.Dialect, t, was *Table) {
	if d != velox.MySQL || t.Collation != "" {
		return
	}
	t.Collation = defaultMySQLCollation
	if was != nil && was.Collation != "" {
		t.Collation = was.Collation
	}
}

// schemaRenames is what carryConstraints reads of changes.
type schemaRenames struct {
	tables  map[TableRef]TableRef          // from's name of a table, by to's
	created map[TableRef]bool              // the tables changes create
	indexes map[TableRef]map[string]string // from's name of an index, by its table and name in to
	columns map[TableRef]map[string]string // to's name of a column, by its table in to and name in from
}

// renamesOf is the tables, indexes and columns changes rename, and the
// tables they create.
func renamesOf(changes []Change) schemaRenames {
	r := schemaRenames{
		tables:  map[TableRef]TableRef{},
		created: map[TableRef]bool{},
		indexes: map[TableRef]map[string]string{},
		columns: map[TableRef]map[string]string{},
	}
	put := func(m map[TableRef]map[string]string, ref TableRef, k, v string) {
		if m[ref] == nil {
			m[ref] = map[string]string{}
		}
		m[ref][k] = v
	}
	for _, c := range changes {
		switch c := c.(type) {
		case renameTable:
			r.tables[c.To] = c.From
		case createTable:
			r.created[c.Table.Ref()] = true
		case renameIndex:
			put(r.indexes, c.Table, c.To, c.From)
		case renameColumn:
			put(r.columns, c.Table, c.From, c.To)
		}
	}
	return r
}

// carryIndexes sets Constraint on each plain index of t whose index in ft,
// t's table in the snapshot, is the same constraint.
func (r schemaRenames) carryIndexes(t, ft *Table) {
	ref := t.Ref()
	for j := range t.Indexes {
		ix := &t.Indexes[j]
		if ix.Constraint || ix.Name == "" || ix.Where != "" {
			continue
		}
		name := ix.Name
		if n, ok := r.indexes[ref][ix.Name]; ok {
			name = n
		}
		fi := ft.index(name)
		if fi == nil || !fi.Constraint || fi.Unique != ix.Unique || len(fi.Columns) != len(ix.Columns) {
			continue
		}
		same := true
		for k, col := range fi.Columns {
			if n, ok := r.columns[ref][col]; ok {
				col = n
			}
			same = same && col == ix.Columns[k]
		}
		ix.Constraint = same
	}
}
