package migrate_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// refused checks that Up refuses fsys on db, as an *Error of Code unmodeled that says
// each of says.
func refused(t *testing.T, db *sql.DB, fsys fstest.MapFS, says ...string) {
	t.Helper()
	_, err := migrate.Up(context.Background(), db, fsys)
	var e *migrate.Error
	if !errors.As(err, &e) || e.Code != "unmodeled" {
		t.Fatalf("Up: %v; want an *Error of Code unmodeled\n%s", err, sqlFiles(fsys))
	}
	for _, s := range says {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("the error does not say %q: %v", s, err)
		}
	}
}

// oneValue is the first column of q's first row, as text.
func oneValue(t *testing.T, db *sql.DB, q string) string {
	t.Helper()
	var s sql.NullString
	if err := db.QueryRow(q).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return s.String
}

// planned is a database at the table t of a base column after the statements run on
// it by hand, and the directory that adds the plan to the table with to.
func planned(t *testing.T, d velox.Dialect, base, to migrate.Column, opts migrate.PlanOptions, hand ...string) (*sql.DB, fstest.MapFS) {
	t.Helper()
	db, _ := freshDB(t, d)
	fsys := dirOf(t, d, step{to: oneTable(d, base), name: "base"})
	mustUp(t, db, fsys)
	for _, q := range hand {
		mustExec(t, db, q)
	}
	return db, extend(t, fsys, d, step{to: oneTable(d, to), name: "change", opts: opts})
}

// TestMySQLModifyComparesEveryAttribute: a MODIFY writes the whole column from the
// snapshot, so a comment-only plan would change what a hand edit made, with Up nil.
// Whatever differs and the file does not name is refused, and the row keeps its value.
func TestMySQLModifyComparesEveryAttribute(t *testing.T) {
	d := velox.MySQL
	const price = "decimal(12,2)"
	plain := migrate.Column{Name: "c", Type: price, Nullable: true}
	commented := plain
	commented.Comment = "what"

	t.Run("a scale widened by hand", func(t *testing.T) {
		db, fsys := planned(t, d, plain, commented, migrate.PlanOptions{}, "ALTER TABLE t MODIFY c decimal(12,4) NULL", "INSERT INTO t (id, c) VALUES (1, 12.3456)")
		refused(t, db, fsys, "t.c:type", "decimal(12,4)", "decimal(12,2)")
		if got := oneValue(t, db, "SELECT c FROM t"); got != "12.3456" {
			t.Errorf("the value after the refusal: %s", got)
		}
	})
	t.Run("a NOT NULL by hand", func(t *testing.T) {
		db, fsys := planned(t, d, plain, commented, migrate.PlanOptions{}, "ALTER TABLE t MODIFY c decimal(12,2) NOT NULL")
		refused(t, db, fsys, "t.c:not-null")
		if got := oneValue(t, db, "SELECT IS_NULLABLE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 't' AND COLUMN_NAME = 'c'"); got != "NO" {
			t.Errorf("IS_NULLABLE after the refusal: %s", got)
		}
	})
	t.Run("widening loses a default and a comment", func(t *testing.T) {
		small := migrate.Column{Name: "c", Type: "varchar(10)", Nullable: true}
		wide := migrate.Column{Name: "c", Type: "varchar(20)", Nullable: true}
		db, fsys := planned(t, d, small, wide, migrate.PlanOptions{}, "ALTER TABLE t MODIFY c varchar(10) NULL DEFAULT 'x' COMMENT 'said'")
		refused(t, db, fsys, "t.c:default", "t.c:comment", "'said'")
		if got := oneValue(t, db, "SELECT COLUMN_DEFAULT FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 't' AND COLUMN_NAME = 'c'"); got != "x" {
			t.Errorf("the default after the refusal: %q", got)
		}
		// The file keeps them, and a directive accepts each by name.
		kept := rewrite(t, fsys, "0002_change.sql", func(s string) string {
			return strings.Replace(s, "varchar(20) NULL", "varchar(20) NULL DEFAULT 'x' COMMENT 'said'", 1)
		})
		mustUp(t, db, kept)
		if got := oneValue(t, db, "SELECT COLUMN_COMMENT FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 't' AND COLUMN_NAME = 'c'"); got != "said" {
			t.Errorf("the comment the file keeps: %q", got)
		}
		db, fsys = planned(t, d, small, wide, migrate.PlanOptions{Allow: []migrate.Allow{{Code: "unmodeled-loss", Target: "t.c:default"}, {Code: "unmodeled-loss", Target: "t.c:comment"}}}, "ALTER TABLE t MODIFY c varchar(10) NULL DEFAULT 'x' COMMENT 'said'")
		mustUp(t, db, fsys)
	})
	t.Run("an id retype loses AUTO_INCREMENT", func(t *testing.T) {
		db, _ := freshDB(t, d)
		schema := func(typ string) *migrate.Schema {
			s := oneTable(d)
			s.Tables[0].Columns[0] = migrate.Column{Name: "id", Type: typ}
			return s
		}
		base := dirOf(t, d, step{to: schema("int"), name: "base"})
		mustUp(t, db, base)
		mustExec(t, db, "ALTER TABLE t MODIFY id int NOT NULL AUTO_INCREMENT")
		fsys := extend(t, base, d, step{to: schema("bigint"), name: "wider"})
		refused(t, db, fsys, "t.id:identity", "AUTO_INCREMENT")
		mustExec(t, db, "INSERT INTO t () VALUES ()")
	})
	t.Run("the plan's own changes pass", func(t *testing.T) {
		base := migrate.Column{Name: "c", Type: "varchar(10)", Nullable: true, Default: "'a'", Comment: "one"}
		to := migrate.Column{Name: "c", Type: "varchar(20)", Comment: "two", Default: "'b'"}
		db, fsys := planned(t, d, base, to, allowAll("set-not-null"))
		for _, want := range []string{"t.c:type=varchar(10)", "t.c:not-null=nullable", "t.c:default='a'", "t.c:comment=one"} {
			if !strings.Contains(string(fsys["0002_change.sql"].Data), "-- velox:drops "+want+"\n") {
				t.Errorf("the file does not drop %s:\n%s", want, fsys["0002_change.sql"].Data)
			}
		}
		mustUp(t, db, fsys)
		// A rendering that says one value otherwise is the same value.
		same := migrate.Column{Name: "c", Type: "decimal(12,2)", Nullable: true, Default: "1.50"}
		db, fsys = planned(t, d, same, migrate.Column{Name: "c", Type: "decimal(12,2)", Nullable: true, Default: "1.50", Comment: "x"}, migrate.PlanOptions{})
		mustUp(t, db, fsys)
		at := migrate.Column{Name: "c", Type: "timestamp", Default: "CURRENT_TIMESTAMP"}
		db, fsys = planned(t, d, at, migrate.Column{Name: "c", Type: "timestamp", Default: "CURRENT_TIMESTAMP", Comment: "x"}, migrate.PlanOptions{})
		mustUp(t, db, fsys)
		en := migrate.Column{Name: "c", Type: "enum('a','b')", Nullable: true, Default: "'a'"}
		db, fsys = planned(t, d, en, migrate.Column{Name: "c", Type: "enum('a','b')", Nullable: true, Default: "'a'", Comment: "x"}, migrate.PlanOptions{})
		mustUp(t, db, fsys)
		flag := migrate.Column{Name: "c", Type: "tinyint(1)", Default: "1"}
		db, fsys = planned(t, d, flag, migrate.Column{Name: "c", Type: "tinyint(1)", Default: "1", Comment: "x"}, migrate.PlanOptions{})
		mustUp(t, db, fsys)
	})
}

// TestPlanLineExpectsAValue: a plan's attribute line records what the plan believes
// the attribute is. A hand numeric(12,4) under a plan of int to bigint would become an
// integer, printed as a widening, with Up nil and no drift after; varchar(500) under
// varchar(50) to varchar(100) would shrink. Up names both and changes nothing, and a
// line with no value, written by hand, accepts it.
func TestPlanLineExpectsAValue(t *testing.T) {
	for _, d := range []velox.Dialect{velox.MySQL, velox.Postgres} {
		t.Run(d.String(), func(t *testing.T) {
			num, str := "decimal(12,4)", "varchar(500)"
			if d == velox.Postgres {
				num, str = "numeric(12,4)", "character varying(500)"
			}
			nullable := func(typ string) migrate.Column { return migrate.Column{Name: "c", Type: typ, Nullable: true} }
			small, wide := "int", "bigint"
			if d == velox.Postgres {
				small = "integer"
			}
			db, fsys := planned(t, d, nullable(small), nullable(wide), migrate.PlanOptions{},
				"ALTER TABLE t "+retype(d, num), "INSERT INTO t (id, c) VALUES (1, 12.3456)")
			if text := string(fsys["0002_change.sql"].Data); !strings.Contains(text, "-- velox:drops t.c:type="+small+"\n") {
				t.Errorf("the file:\n%s", text)
			}
			refused(t, db, fsys, "t.c:type", small, num, "expects")
			if got := oneValue(t, db, "SELECT c FROM t"); got != "12.3456" {
				t.Errorf("the value after the refusal: %s", got)
			}
			accepted := rewrite(t, fsys, "0002_change.sql", func(s string) string {
				return strings.Replace(s, "-- velox:drops t.c:type="+small+"\n", "-- velox:drops t.c:type\n", 1)
			})
			mustUp(t, db, accepted)

			db, fsys = planned(t, d, nullable("varchar(50)"), nullable("varchar(100)"), migrate.PlanOptions{}, "ALTER TABLE t "+retype(d, "varchar(500)"))
			refused(t, db, fsys, "t.c:type", str, "expects")
			if got := oneValue(t, db, columnTypeQuery(d)); !strings.Contains(got, "500") {
				t.Errorf("the type after the refusal: %s", got)
			}

			// What the plan expects passes, and a column already there is no loss.
			db, fsys = planned(t, d, nullable("varchar(50)"), nullable("varchar(100)"), migrate.PlanOptions{})
			mustUp(t, db, fsys)
			db, fsys = planned(t, d, nullable("varchar(50)"), nullable("varchar(100)"), migrate.PlanOptions{}, "ALTER TABLE t "+retype(d, "varchar(100)"))
			mustUp(t, db, fsys)
		})
	}
	t.Run("sqlite", func(t *testing.T) {
		d := velox.SQLite
		base := migrate.Column{Name: "c", Type: "integer", Nullable: true}
		to := migrate.Column{Name: "c", Type: "text", Nullable: true}
		db, _ := freshDB(t, d)
		fsys := dirOf(t, d, step{to: oneTable(d, base), name: "base"})
		mustUp(t, db, fsys)
		mustExec(t, db, "DROP TABLE t")
		mustExec(t, db, "CREATE TABLE t (id integer NOT NULL PRIMARY KEY, c numeric)")
		fsys = extend(t, fsys, d, step{to: oneTable(d, to), name: "change", opts: allowAll("alter-type")})
		refused(t, db, fsys, "t.c:type", "numeric", "integer")
	})
	t.Run("a default and a comment", func(t *testing.T) {
		d := velox.MySQL
		base := migrate.Column{Name: "c", Type: "varchar(10)", Nullable: true, Default: "'a'", Comment: "one"}
		to := migrate.Column{Name: "c", Type: "varchar(10)", Nullable: true, Default: "'a'", Comment: "two"}
		db, fsys := planned(t, d, base, to, migrate.PlanOptions{}, "ALTER TABLE t MODIFY c varchar(10) NULL DEFAULT 'a' COMMENT 'by hand'")
		refused(t, db, fsys, "t.c:comment", "one", "by hand")
		if text := string(fsys["0002_change.sql"].Data); !strings.Contains(text, `-- velox:drops t.c:comment=one`) {
			t.Errorf("the file:\n%s", text)
		}
		// White space in a value round-trips.
		to.Comment, base.Comment = "two words", "one word"
		db, fsys = planned(t, d, base, to, migrate.PlanOptions{})
		mustUp(t, db, fsys)
	})
	t.Run("an index", func(t *testing.T) {
		d := velox.MySQL
		col := func(name string) migrate.Column { return migrate.Column{Name: name, Type: "int", Nullable: true} }
		schema := func(cols ...string) *migrate.Schema {
			s := oneTable(d, col("a"), col("b"), col("h"))
			s.Tables[0].Indexes = []migrate.Index{{Name: "t_x", Columns: cols}}
			return s
		}
		db, _ := freshDB(t, d)
		fsys := dirOf(t, d, step{to: schema("a"), name: "base"})
		mustUp(t, db, fsys)
		mustExec(t, db, "ALTER TABLE t DROP INDEX t_x, ADD INDEX t_x (a, h)")
		fsys = extend(t, fsys, d, step{to: schema("a", "b"), name: "change"})
		refused(t, db, fsys, "t.t_x:columns", "expects", "a, h")
	})
}

// retype is the action that makes the column c the type typ, by hand.
func retype(d velox.Dialect, typ string) string {
	if d == velox.Postgres {
		return "ALTER COLUMN c TYPE " + typ
	}
	return "MODIFY c " + typ + " NULL"
}

// columnTypeQuery reads the type of t.c.
func columnTypeQuery(d velox.Dialect) string {
	if d == velox.Postgres {
		return "SELECT character_maximum_length FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 't' AND column_name = 'c'"
	}
	return "SELECT COLUMN_TYPE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 't' AND COLUMN_NAME = 'c'"
}

// TestMySQLModifyReadsDefaultSpellings: a statement's default spelled otherwise than
// the server prints it is one value, so a comment change is no refusal.
func TestMySQLModifyReadsDefaultSpellings(t *testing.T) {
	d := velox.MySQL
	for _, c := range []struct{ typ, def string }{
		{"datetime(6)", "now(6)"},
		{"datetime(6)", "CURRENT_TIMESTAMP(6)"},
		{"datetime", "now()"},
		{"timestamp", "current_timestamp()"},
		{"timestamp", "LOCALTIMESTAMP"},
		{"tinyint(1)", "true"},
		{"tinyint(1)", "FALSE"},
		{"binary(1)", "0x00"},
		{"varbinary(4)", "x'6162'"},
		{"int", "'5'"},
		{"decimal(4,2)", "'1.50'"},
	} {
		t.Run(c.typ+" "+c.def, func(t *testing.T) {
			base := migrate.Column{Name: "c", Type: c.typ, Nullable: true, Default: c.def}
			to := base
			to.Comment = "x"
			db, fsys := planned(t, d, base, to, migrate.PlanOptions{})
			mustUp(t, db, fsys)
			r, err := migrate.Drift(context.Background(), db, oneTable(d, to))
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Changes) > 0 {
				t.Errorf("drift:\n%s", reportText(r))
			}
		})
	}
	// Another value is still one.
	base := migrate.Column{Name: "c", Type: "int", Nullable: true, Default: "'5'"}
	to := migrate.Column{Name: "c", Type: "int", Nullable: true, Default: "'5'", Comment: "x"}
	db, fsys := planned(t, d, base, to, migrate.PlanOptions{}, "ALTER TABLE t ALTER COLUMN c SET DEFAULT 6")
	refused(t, db, fsys, "t.c:default")
}

// TestPostgresRetypeReadsTheDefault: a retype drops the snapshot's default and sets
// it after, which replaced a hand default, 42 with 1. Up refuses; a default the plan
// changes passes, and one the statement leaves alone stays.
func TestPostgresRetypeReadsTheDefault(t *testing.T) {
	d := velox.Postgres
	col := func(typ, def string) migrate.Column { return migrate.Column{Name: "c", Type: typ, Default: def} }
	db, fsys := planned(t, d, col("integer", "1"), col("bigint", "1"), migrate.PlanOptions{}, "ALTER TABLE t ALTER COLUMN c SET DEFAULT 42")
	refused(t, db, fsys, "t.c:default", "42")
	if got := oneValue(t, db, "SELECT column_default FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 't' AND column_name = 'c'"); got != "42" {
		t.Errorf("the default after the refusal: %s", got)
	}

	db, fsys = planned(t, d, col("integer", "1"), col("bigint", "1"), migrate.PlanOptions{})
	mustUp(t, db, fsys)

	db, fsys = planned(t, d, col("integer", "1"), col("bigint", "2"), migrate.PlanOptions{})
	mustUp(t, db, fsys)

	db, fsys = planned(t, d, col("integer", ""), col("bigint", ""), migrate.PlanOptions{}, "ALTER TABLE t ALTER COLUMN c SET DEFAULT 42")
	mustUp(t, db, fsys)
	if got := oneValue(t, db, "SELECT column_default FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 't' AND column_name = 'c'"); !strings.Contains(got, "42") {
		t.Errorf("the default a retype leaves: %s", got)
	}

	// The plan's line expects the snapshot's default; a line with no value is the
	// file's to accept whatever is there.
	db, fsys = planned(t, d, col("integer", "1"), col("bigint", "2"), migrate.PlanOptions{}, "ALTER TABLE t ALTER COLUMN c SET DEFAULT 42")
	if !strings.Contains(string(fsys["0002_change.sql"].Data), "-- velox:drops t.c:default=1\n") {
		t.Errorf("the file:\n%s", fsys["0002_change.sql"].Data)
	}
	refused(t, db, fsys, "t.c:default", "expects 1", "holds 42")
	fsys = rewrite(t, fsys, "0002_change.sql", func(s string) string {
		return strings.Replace(s, "-- velox:drops t.c:default=1\n", "-- velox:drops t.c:default\n", 1)
	})
	mustUp(t, db, fsys)
}

// TestPostgresRetypeReadsStorage: ALTER COLUMN TYPE resets STORAGE and COMPRESSION to
// the new type's. Up refuses until the file sets them again or a directive accepts it.
func TestPostgresRetypeReadsStorage(t *testing.T) {
	d := velox.Postgres
	small, wide := migrate.Column{Name: "c", Type: "character varying(10)", Nullable: true}, migrate.Column{Name: "c", Type: "character varying(20)", Nullable: true}
	probe, _ := freshDB(t, d)
	lz4 := oneValue(t, probe, "SELECT count(*) FROM pg_settings WHERE name = 'default_toast_compression' AND 'lz4' = ANY(enumvals)") == "1"
	for _, c := range []struct {
		name, hand, says, again string
		needs                   bool
	}{
		{"storage", "ALTER TABLE t ALTER COLUMN c SET STORAGE EXTERNAL", "STORAGE", "ALTER TABLE t ALTER COLUMN c SET STORAGE EXTERNAL;\n", false},
		{"compression", "ALTER TABLE t ALTER COLUMN c SET COMPRESSION lz4", "COMPRESSION", "ALTER TABLE t ALTER COLUMN c SET COMPRESSION lz4;\n", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.needs && !lz4 {
				t.Skip("the server has no lz4")
			}
			db, fsys := planned(t, d, small, wide, migrate.PlanOptions{}, c.hand)
			refused(t, db, fsys, "t.c", c.says)
			again := rewrite(t, fsys, "0002_change.sql", func(s string) string { return s + c.again })
			mustUp(t, db, again)
			db, fsys = planned(t, d, small, wide, migrate.PlanOptions{Allow: []migrate.Allow{{Code: "unmodeled-loss", Target: "t.c"}}}, c.hand)
			mustUp(t, db, fsys)
		})
	}
}

// indexed is the table t of the columns a, b and d with the indexes.
func indexed(dialect velox.Dialect, ix ...migrate.Index) *migrate.Schema {
	col := func(name string) migrate.Column {
		return migrate.Column{Name: name, Type: "varchar(50)", Nullable: true}
	}
	s := oneTable(dialect, col("a"), col("b"), col("d"))
	s.Tables[0].Indexes = ix
	return s
}

// indexPlan is a database at indexed(from...) after the statements run by hand, and
// the directory that adds the plan to indexed(to...).
func indexPlan(t *testing.T, d velox.Dialect, from, to []migrate.Index, hand ...string) (*sql.DB, fstest.MapFS) {
	t.Helper()
	db, _ := freshDB(t, d)
	base := dirOf(t, d, step{to: indexed(d, from...), name: "base"})
	mustUp(t, db, base)
	for _, q := range hand {
		mustExec(t, db, q)
	}
	return db, extend(t, base, d, step{to: indexed(d, to...), name: "change"})
}

// TestPostgresAddIndexSparesAValidOne: an add-index plan's DROP INDEX CONCURRENTLY IF
// EXISTS is there to clear what a failed CONCURRENTLY left. It removed a hand UNIQUE
// over an expression. A valid index that is not what the plan makes is refused; an
// invalid one goes, and one of the same shape is made again.
func TestPostgresAddIndexSparesAValidOne(t *testing.T) {
	d := velox.Postgres
	x := []migrate.Index{{Name: "x", Columns: []string{"a"}}}
	db, fsys := indexPlan(t, d, nil, x, `CREATE UNIQUE INDEX x ON t (lower(a)) WHERE a IS NOT NULL`)
	refused(t, db, fsys, "t.x:unique", "t.x:columns", "t.x:where")
	if got := oneValue(t, db, "SELECT indexdef FROM pg_indexes WHERE indexname = 'x'"); !strings.Contains(got, "lower") {
		t.Errorf("the index after the refusal: %s", got)
	}

	db, fsys = indexPlan(t, d, nil, x, `CREATE INDEX x ON t (a)`)
	mustUp(t, db, fsys)

	db, fsys = indexPlan(t, d, nil, x, `INSERT INTO t (id, a) VALUES (1, 'p'), (2, 'p')`)
	if _, err := db.Exec(`CREATE UNIQUE INDEX CONCURRENTLY x ON t (a)`); err == nil {
		t.Fatal("no duplicate, so no invalid index")
	}
	if got := oneValue(t, db, "SELECT indisvalid::text FROM pg_index WHERE indexrelid = 'x'::regclass"); got != "false" {
		t.Fatalf("indisvalid: %s", got)
	}
	mustUp(t, db, fsys)
	if got := oneValue(t, db, "SELECT indisvalid::text FROM pg_index WHERE indexrelid = 'x'::regclass"); got != "true" {
		t.Errorf("the index the plan made: %s", got)
	}
}

// TestIndexChangeReadsWhatTheStatementWrites: a plan that changes an index drops it
// and makes it from the model. What the model has no place for went with it, on
// PostgreSQL and on MySQL, past the guard.
func TestIndexChangeReadsWhatTheStatementWrites(t *testing.T) {
	one := []migrate.Index{{Name: "x", Columns: []string{"a"}}}
	two := []migrate.Index{{Name: "x", Columns: []string{"a", "b"}}}
	for _, c := range []struct {
		name string
		d    velox.Dialect
		hand string
		says string
	}{
		{"operator class", velox.Postgres, `CREATE INDEX x ON t (a varchar_pattern_ops)`, "t.x:key-options"},
		{"INCLUDE", velox.Postgres, `CREATE INDEX x ON t (a) INCLUDE (d)`, "t.x:include"},
		{"fillfactor", velox.Postgres, `CREATE INDEX x ON t (a) WITH (fillfactor = 70)`, "t.x:with"},
		{"DESC", velox.Postgres, `CREATE INDEX x ON t (a DESC)`, "t.x:key-options"},
		{"UNIQUE", velox.MySQL, `CREATE UNIQUE INDEX x ON t (a)`, "t.x:unique"},
		{"a prefix", velox.MySQL, `CREATE INDEX x ON t (a(5))`, "t.x:key-options"},
		{"DESC", velox.MySQL, `CREATE INDEX x ON t (a DESC)`, "t.x:key-options"},
	} {
		t.Run(c.d.String()+" "+c.name, func(t *testing.T) {
			drop := "DROP INDEX x"
			if c.d == velox.MySQL {
				drop = "DROP INDEX x ON t"
			}
			db, fsys := indexPlan(t, c.d, one, two, drop, c.hand)
			refused(t, db, fsys, c.says, "velox:allow unmodeled-loss "+c.says)
			var n int
			if err := db.QueryRow("SELECT count(*) FROM " + indexCatalog(c.d)).Scan(&n); err != nil || n != 1 {
				t.Errorf("the index after the refusal: %d, %v", n, err)
			}
		})
	}
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL} {
		t.Run(d.String()+" a plain change", func(t *testing.T) {
			db, fsys := indexPlan(t, d, one, two)
			mustUp(t, db, fsys)
		})
		t.Run(d.String()+" the file writes it", func(t *testing.T) {
			db, fsys := indexPlan(t, d, one, two)
			mustUp(t, db, fsys)
			back := extend(t, fsys, d, files("0009_back.sql", "-- velox:drops t.x:columns\n"+dropAndMake(d)))
			mustUp(t, db, back)
		})
	}
}

// indexCatalog is the rows that name t.x.
func indexCatalog(d velox.Dialect) string {
	if d == velox.Postgres {
		return "pg_indexes WHERE indexname = 'x'"
	}
	return "(SELECT DISTINCT INDEX_NAME FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 't' AND INDEX_NAME = 'x') i"
}

// dropAndMake makes t.x over a alone, by the statements of d's hand.
func dropAndMake(d velox.Dialect) string {
	if d == velox.Postgres {
		return "DROP INDEX x;\nCREATE INDEX x ON t (a);\n"
	}
	return "ALTER TABLE t DROP INDEX x, ADD INDEX x (a);\n"
}

// mysqlFile is MySQL's t with the columns a and b, after hand, and the file.
func mysqlFile(t *testing.T, file string, hand ...string) (*sql.DB, fstest.MapFS) {
	t.Helper()
	d := velox.MySQL
	db, _ := freshDB(t, d)
	col := func(name string) migrate.Column { return migrate.Column{Name: name, Type: "int", Nullable: true} }
	base := dirOf(t, d, step{to: oneTable(d, col("a"), col("b")), name: "base"})
	mustUp(t, db, base)
	for _, q := range hand {
		mustExec(t, db, q)
	}
	return db, extend(t, base, d, files("0002_x.sql", file))
}

// TestMySQLRefusesWhatARenameBreaks: MySQL renames past a view, which then fails 1356,
// and a trigger, whose INSERT then fails 1054, with Up nil. Up names each; a directive
// accepts it; a file that remakes the view or drops the trigger needs none.
func TestMySQLRefusesWhatARenameBreaks(t *testing.T) {
	view := "CREATE VIEW v AS SELECT a FROM t"
	viewB := "CREATE VIEW vb AS SELECT b FROM t"
	other := "CREATE TABLE u (id int)"
	trigger := "CREATE TRIGGER t_bi BEFORE INSERT ON t FOR EACH ROW SET NEW.a = NEW.a + 1"
	otherTrigger := "CREATE TRIGGER u_ai AFTER INSERT ON u FOR EACH ROW INSERT INTO t (id) VALUES (NEW.id)"
	for _, c := range []struct {
		name, file string
		hand       []string
		says       string
	}{
		{"RENAME COLUMN, a view", "ALTER TABLE t RENAME COLUMN a TO a2;\n", []string{view}, "v"},
		{"CHANGE, a view", "-- velox:drops t.a2:type\nALTER TABLE t CHANGE a a2 int NULL;\n", []string{view}, "v"},
		{"RENAME TABLE, a view", "RENAME TABLE t TO t2;\n", []string{view}, "v"},
		{"ALTER RENAME, a view", "ALTER TABLE t RENAME TO t2;\n", []string{view}, "v"},
		{"DROP COLUMN, a view", "ALTER TABLE t DROP COLUMN a;\n", []string{view}, "v"},
		{"DROP TABLE, a view", "DROP TABLE t;\n", []string{view}, "v"},
		{"RENAME COLUMN, a trigger", "ALTER TABLE t RENAME COLUMN a TO a2;\n", []string{trigger}, "t_bi"},
		{"RENAME TABLE, another table's trigger", "RENAME TABLE t TO t2;\n", []string{other, otherTrigger}, "u_ai"},
	} {
		t.Run(c.name, func(t *testing.T) {
			db, fsys := mysqlFile(t, c.file, c.hand...)
			refused(t, db, fsys, c.says, "velox:allow unmodeled-loss "+c.says)
			db, fsys = mysqlFile(t, "-- velox:allow unmodeled-loss "+c.says+"\n"+c.file, c.hand...)
			mustUp(t, db, fsys)
		})
	}
	for _, c := range []struct {
		name, file string
		hand       []string
	}{
		{"another column", "ALTER TABLE t RENAME COLUMN a TO a2;\n", []string{viewB}},
		{"the file remakes the view", "ALTER TABLE t RENAME COLUMN a TO a2;\nCREATE OR REPLACE VIEW v AS SELECT a2 FROM t;\n", []string{view}},
		{"the file drops the view first", "DROP VIEW v;\nRENAME TABLE t TO t2;\n", []string{view}},
		{"the file drops the trigger", "DROP TRIGGER t_bi;\nALTER TABLE t RENAME COLUMN a TO a2;\n", []string{trigger}},
		{"a table's own trigger moves with it", "RENAME TABLE t TO t2;\n", []string{trigger}},
		{"a table's own trigger goes with it", "DROP TABLE t;\n", []string{trigger}},
		{"a table the file makes", "CREATE TABLE n (a int);\nALTER TABLE n RENAME COLUMN a TO b;\nRENAME TABLE n TO n2;\n", []string{view}},
	} {
		t.Run(c.name, func(t *testing.T) {
			db, fsys := mysqlFile(t, c.file, c.hand...)
			mustUp(t, db, fsys)
		})
	}
}

// TestMySQLDropColumnShrinksIndex: dropping a column of a hand UNIQUE (a, b) leaves a
// UNIQUE (a), in silence. Up refuses it; -allow drop-column says nothing of the index,
// and a file that drops the index does.
func TestMySQLDropColumnShrinksIndex(t *testing.T) {
	hand := "CREATE UNIQUE INDEX pair ON t (a, b)"
	db, fsys := mysqlFile(t, "ALTER TABLE t DROP COLUMN b;\n", hand)
	refused(t, db, fsys, "t.pair", "velox:allow unmodeled-loss t.pair")
	db, fsys = mysqlFile(t, "-- velox:allow drop-column t.b\nALTER TABLE t DROP COLUMN b;\n", hand)
	refused(t, db, fsys, "t.pair")
	for _, file := range []string{
		"-- velox:allow unmodeled-loss t.pair\nALTER TABLE t DROP COLUMN b;\n",
		"ALTER TABLE t DROP INDEX pair, DROP COLUMN b;\n",
		"ALTER TABLE t DROP COLUMN a, DROP COLUMN b;\n",
	} {
		db, fsys = mysqlFile(t, file, hand)
		if _, err := migrate.Up(context.Background(), db, fsys); err != nil {
			t.Errorf("%s: %v", file, err)
		}
	}
	db, fsys = mysqlFile(t, "ALTER TABLE t DROP COLUMN b;\n", "CREATE INDEX alone ON t (b)")
	mustUp(t, db, fsys)
}

// TestPostgresGuardReadsTheSearchPath: with search_path=app,public and t in public,
// the server's t is public.t. The guard read app.t, found nothing, and let the
// collation go.
func TestPostgresGuardReadsTheSearchPath(t *testing.T) {
	d := velox.Postgres
	admin, dsn := freshDB(t, d)
	mustExec(t, admin, `CREATE SCHEMA app`)
	mustExec(t, admin, `CREATE TABLE public.t (id integer PRIMARY KEY, c varchar(10) COLLATE "C")`)
	path := "search_path=app,public"
	if strings.Contains(dsn, "?") {
		path = "&" + path
	} else {
		path = "?" + path
	}
	db := openDB(t, "pgx", dsn+path)
	base := dirOf(t, d)
	refuse := extend(t, base, d, files("0001_x.sql", "ALTER TABLE t ALTER COLUMN c TYPE varchar(20);\n"))
	refused(t, db, refuse, "public.t.c", "COLLATE")
	if got := oneValue(t, db, "SELECT collation_name FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 't' AND column_name = 'c'"); got != "C" {
		t.Errorf("the collation after the refusal: %q", got)
	}
	// A file that writes it keeps it.
	said := extend(t, base, d, files("0001_x.sql", `ALTER TABLE t ALTER COLUMN c TYPE varchar(20) COLLATE "C";
`))
	mustUp(t, db, said)
	if got := oneValue(t, db, "SELECT character_maximum_length::text FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 't' AND column_name = 'c'"); got != "20" {
		t.Errorf("the length: %s", got)
	}
}

// TestUpTargetKeepsTheGuardsView: a foreign key dropped in a file under -to and added
// again in a pending file past it lost ON UPDATE: the later file was out of sight.
func TestUpTargetKeepsTheGuardsView(t *testing.T) {
	d := velox.Postgres
	const key = "ALTER TABLE c DROP CONSTRAINT c_p;\n"
	db, _ := freshDB(t, d)
	base := dirOf(t, d, files("0001_base.sql", "CREATE TABLE p (id integer PRIMARY KEY);\nCREATE TABLE c (id integer PRIMARY KEY, p_id integer, CONSTRAINT c_p FOREIGN KEY (p_id) REFERENCES p (id) ON UPDATE CASCADE);\n"))
	mustUp(t, db, base)
	fsys := extend(t, extend(t, base, d, files("0002_drop.sql", key)), d, files("0003_add.sql", "ALTER TABLE c ADD CONSTRAINT c_p FOREIGN KEY (p_id) REFERENCES p (id);\n"))
	_, err := migrate.Up(context.Background(), db, fsys, migrate.WithTarget(2))
	var e *migrate.Error
	if !errors.As(err, &e) || e.Code != "unmodeled" || !strings.Contains(err.Error(), "ON UPDATE") {
		t.Fatalf("Up -to 2: %v", err)
	}
	if got := oneValue(t, db, "SELECT count(*) FROM information_schema.referential_constraints WHERE constraint_name = 'c_p'"); got != "1" {
		t.Errorf("the key after the refusal: %s", got)
	}
	// Alone, with no file to add it again, the drop is the file's own.
	alone := extend(t, base, d, files("0002_drop.sql", key))
	mustUp(t, db, alone, migrate.WithTarget(2))
}
