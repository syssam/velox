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

// mysqlColumn is t.name's EXTRA and collation.
func mysqlColumn(t *testing.T, db *sql.DB, name string) (extra, collation string) {
	t.Helper()
	var coll sql.NullString
	err := db.QueryRow(`SELECT EXTRA, COLLATION_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 't' AND COLUMN_NAME = ?`, name).Scan(&extra, &coll)
	if err != nil {
		t.Fatal(err)
	}
	return extra, coll.String
}

// mysqlGuarded is a database whose t.at has ON UPDATE and t.c a collation of
// its own, which the model cannot hold, and the plan that changes their
// comments, a MODIFY COLUMN of each.
func mysqlGuarded(t *testing.T, opts migrate.PlanOptions) (*sql.DB, fstest.MapFS) {
	t.Helper()
	d := velox.MySQL
	db, _ := freshDB(t, d)
	at := migrate.Column{Name: "at", Type: "timestamp", Default: "CURRENT_TIMESTAMP"}
	c := migrate.Column{Name: "c", Type: "varchar(10)", Nullable: true}
	fsys := dirOf(t, d, step{to: oneTable(d, at, c), name: "base"})
	mustUp(t, db, fsys)
	mustExec(t, db, "ALTER TABLE t MODIFY COLUMN at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP, MODIFY COLUMN c varchar(10) COLLATE utf8mb4_general_ci NULL")
	at.Comment, c.Comment = "when", "what"
	return db, extend(t, fsys, d, step{to: oneTable(d, at, c), name: "comment", opts: opts})
}

// TestMySQLCommentChangeRefused: a change of a column's comment is written as
// MODIFY COLUMN, which would lose ON UPDATE and the collation: Up refuses
// before it changes anything, naming them, and records no version.
func TestMySQLCommentChangeRefused(t *testing.T) {
	db, fsys := mysqlGuarded(t, migrate.PlanOptions{})
	_, err := migrate.Up(context.Background(), db, fsys)
	var e *migrate.Error
	if !errors.As(err, &e) || e.Code != "unmodeled" {
		a, b := mysqlColumn(t, db, "at")
		t.Fatalf("Up: %v; want an *Error of Code unmodeled; EXTRA %q, collation %q\n%s", err, a, b, sqlFiles(fsys))
	}
	for _, want := range []string{"t.at", "ON UPDATE", "t.c", "utf8mb4_general_ci", "velox:allow unmodeled-loss t.at", "migrate hash", "0002_comment.sql"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not say %q: %v", want, err)
		}
	}
	if extra, _ := mysqlColumn(t, db, "at"); !strings.Contains(strings.ToLower(extra), "on update") {
		t.Errorf("EXTRA %q after the refusal", extra)
	}
	if _, coll := mysqlColumn(t, db, "c"); coll != "utf8mb4_general_ci" {
		t.Errorf("collation %q after the refusal", coll)
	}
	if st := mustStatus(t, db, fsys); len(st.Pending) != 1 || st.Dirty != nil {
		t.Errorf("pending %v, dirty %v", st.Pending, st.Dirty)
	}
}

// TestMySQLModifyKeptByTheFile: a statement that writes what it would lose
// runs, and keeps it, with no directive.
func TestMySQLModifyKeptByTheFile(t *testing.T) {
	db, fsys := mysqlGuarded(t, migrate.PlanOptions{})
	fsys = rewrite(t, fsys, "0002_comment.sql", func(s string) string {
		s = strings.Replace(s, "DEFAULT CURRENT_TIMESTAMP COMMENT", "DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT", 1)
		return strings.Replace(s, "varchar(10) NULL COMMENT", "varchar(10) COLLATE utf8mb4_general_ci NULL COMMENT", 1)
	})
	if _, err := migrate.Up(context.Background(), db, fsys); err != nil {
		t.Fatalf("Up: %v\n%s", err, sqlFiles(fsys))
	}
	if extra, _ := mysqlColumn(t, db, "at"); !strings.Contains(strings.ToLower(extra), "on update") {
		t.Errorf("EXTRA %q", extra)
	}
	if _, coll := mysqlColumn(t, db, "c"); coll != "utf8mb4_general_ci" {
		t.Errorf("collation %q", coll)
	}
}

// TestMySQLModifyAllowed: the directive accepts the loss, for its target, and
// one for the other column alone leaves this one refused.
func TestMySQLModifyAllowed(t *testing.T) {
	db, fsys := mysqlGuarded(t, allowAll("unmodeled-loss"))
	if _, err := migrate.Up(context.Background(), db, fsys); err != nil {
		t.Fatalf("Up: %v\n%s", err, sqlFiles(fsys))
	}
	if extra, _ := mysqlColumn(t, db, "at"); strings.Contains(strings.ToLower(extra), "on update") {
		t.Errorf("EXTRA %q: the loss was accepted", extra)
	}

	db, fsys = mysqlGuarded(t, migrate.PlanOptions{Allow: []migrate.Allow{{Code: "unmodeled-loss", Target: "t.c"}}})
	_, err := migrate.Up(context.Background(), db, fsys)
	var e *migrate.Error
	if !errors.As(err, &e) || e.Code != "unmodeled" || !strings.Contains(err.Error(), "t.at") || strings.Contains(err.Error(), "t.c:") {
		t.Errorf("Up: %v", err)
	}
}

// TestMySQLModifyFollowsRenames: the guard reads a column under the name the
// database holds, before a rename the same file makes; a column it creates has
// nothing to lose.
func TestMySQLModifyFollowsRenames(t *testing.T) {
	d := velox.MySQL
	db, _ := freshDB(t, d)
	at := migrate.Column{Name: "at", Type: "timestamp", Default: "CURRENT_TIMESTAMP"}
	fsys := dirOf(t, d, step{to: oneTable(d, at), name: "base"})
	mustUp(t, db, fsys)
	mustExec(t, db, "ALTER TABLE t MODIFY COLUMN at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP")
	moved := extend(t, fsys, d, files("0002_rebuild.sql", "RENAME TABLE t TO t2;\nALTER TABLE t2 RENAME COLUMN at TO changed;\nALTER TABLE t2 MODIFY COLUMN changed timestamp NOT NULL COMMENT 'x';\n"))
	_, err := migrate.Up(context.Background(), db, moved)
	var e *migrate.Error
	if !errors.As(err, &e) || e.Code != "unmodeled" || !strings.Contains(err.Error(), "t2.changed") {
		t.Errorf("Up: %v", err)
	}

	created := extend(t, fsys, d, files("0002_rebuild.sql", "ALTER TABLE t ADD COLUMN n integer;\nALTER TABLE t MODIFY COLUMN n bigint;\nCREATE TABLE u (a integer);\nALTER TABLE u MODIFY a bigint;\n"))
	if _, err := migrate.Up(context.Background(), db, created); err != nil {
		t.Errorf("Up: %v", err)
	}
}

// nonStrict is one connection whose session has no strict mode, as a server
// configured so gives it: Up takes the same connection.
func nonStrict(t *testing.T, db *sql.DB) {
	t.Helper()
	db.SetMaxOpenConns(1)
	mustExec(t, db, "SET SESSION sql_mode = ''")
}

// TestMySQLUpIsStrict: in a session with no strict mode, MySQL would cut or
// change rows and call it done. Up runs its files strict, so each fails, the
// rows as they were; no server setting changes.
func TestMySQLUpIsStrict(t *testing.T) {
	d := velox.MySQL
	for _, c := range []struct {
		name, from, to   string
		nullable, narrow bool
		value            string
		allow            string
	}{
		{name: "cut", from: "varchar(10)", to: "varchar(3)", nullable: true, value: "'abcdefgh'", allow: "alter-type"},
		{name: "null to empty", from: "varchar(10)", to: "varchar(10)", narrow: true, nullable: true, value: "NULL", allow: "set-not-null"},
		{name: "enum value", from: "enum('a','b')", to: "enum('a')", nullable: true, value: "'b'", allow: "drop-enum-value"},
		{name: "clamp", from: "bigint", to: "int", nullable: true, value: "99999999999", allow: "alter-type"},
	} {
		t.Run(c.name, func(t *testing.T) {
			db, _ := freshDB(t, d)
			var global string
			if err := db.QueryRow("SELECT @@GLOBAL.sql_mode").Scan(&global); err != nil {
				t.Fatal(err)
			}
			fsys := dirOf(t, d, step{to: oneTable(d, migrate.Column{Name: "c", Type: c.from, Nullable: c.nullable}), name: "base"})
			mustUp(t, db, fsys)
			mustExec(t, db, "INSERT INTO t (id, c) VALUES (1, "+c.value+")")
			var before sql.NullString
			if err := db.QueryRow("SELECT c FROM t").Scan(&before); err != nil {
				t.Fatal(err)
			}
			nonStrict(t, db)
			fsys = extend(t, fsys, d, step{to: oneTable(d, migrate.Column{Name: "c", Type: c.to, Nullable: c.nullable && !c.narrow}), name: "narrow", opts: allowAll(c.allow)})
			_, err := migrate.Up(context.Background(), db, fsys)
			if _, ok := errors.AsType[*migrate.DirtyError](err); !ok {
				t.Fatalf("Up: %v\n%s", err, sqlFiles(fsys))
			}
			var after sql.NullString
			if err := db.QueryRow("SELECT c FROM t").Scan(&after); err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Errorf("c was %v, is %v", before, after)
			}
			var now string
			if err := db.QueryRow("SELECT @@GLOBAL.sql_mode").Scan(&now); err != nil || now != global {
				t.Errorf("the server's sql_mode: %q, %v", now, err)
			}
		})
	}
}

// TestMySQLUpStaysStrict: a file that turns the mode off does so for itself;
// the next file is strict again.
func TestMySQLUpStaysStrict(t *testing.T) {
	d := velox.MySQL
	db, _ := freshDB(t, d)
	col := func(typ string) migrate.Column { return migrate.Column{Name: "c", Type: typ, Nullable: true} }
	fsys := dirOf(t, d, step{to: oneTable(d, col("varchar(10)")), name: "base"})
	mustUp(t, db, fsys)
	mustExec(t, db, "INSERT INTO t (id, c) VALUES (1, 'abcdefgh')")
	fsys = extend(t, fsys, d, files("0002_off.sql", "SET SESSION sql_mode = '';\n"))
	fsys = extend(t, fsys, d, files("0003_cut.sql", "ALTER TABLE t MODIFY COLUMN c varchar(3) NULL;\n"))
	_, err := migrate.Up(context.Background(), db, fsys)
	var de *migrate.DirtyError
	if !errors.As(err, &de) || de.Version != 3 {
		t.Errorf("Up: %v", err)
	}
}

// TestMySQLGuardReadsEveryShape: each shape that wrote a column's definition past
// the guard loses ON UPDATE no longer: another case, a qualified name, a column
// followed through CHANGE, an executable comment, and what it cannot read at all.
func TestMySQLGuardReadsEveryShape(t *testing.T) {
	d := velox.MySQL
	setup := func(t *testing.T, q string) (*sql.DB, fstest.MapFS) {
		t.Helper()
		db, _ := mysqlGuarded(t, migrate.PlanOptions{})
		var name string
		if err := db.QueryRow("SELECT DATABASE()").Scan(&name); err != nil {
			t.Fatal(err)
		}
		base := dirOf(t, d, step{to: oneTable(d, migrate.Column{Name: "at", Type: "timestamp", Default: "CURRENT_TIMESTAMP"}, migrate.Column{Name: "c", Type: "varchar(10)", Nullable: true}), name: "base"})
		return db, extend(t, base, d, files("0002_x.sql", strings.ReplaceAll(q, "{db}", name)))
	}
	const keep = "timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP"
	for _, c := range []struct{ name, q, says string }{
		{"case", "ALTER TABLE t MODIFY AT timestamp NOT NULL;", "t.at"},
		{"case of COLUMN", "alter table t modify column At timestamp NOT NULL;", "t.at"},
		{"quoted database", "ALTER TABLE `{db}`.t MODIFY at timestamp NOT NULL;", "ON UPDATE"},
		{"database", "ALTER TABLE {db}.`t` MODIFY at timestamp NOT NULL;", "ON UPDATE"},
		{"qualified column", "ALTER TABLE t MODIFY t.at timestamp NOT NULL;", "ON UPDATE"},
		{"CHANGE loses it", "ALTER TABLE t CHANGE at at2 timestamp NOT NULL;", "t.at2"},
		{"CHANGE, then MODIFY", "ALTER TABLE t CHANGE at at2 " + keep + ";\nALTER TABLE t MODIFY at2 timestamp NOT NULL;\n", "t.at2"},
		{"a swap in one ALTER", "ALTER TABLE t CHANGE at b timestamp NOT NULL, CHANGE c at varchar(10) COLLATE utf8mb4_general_ci NULL;", "t.b"},
		{"RENAME COLUMN, CHANGE", "ALTER TABLE t RENAME COLUMN at TO at2;\nALTER TABLE t CHANGE at2 at3 timestamp NOT NULL;", "t.at3"},
		{"RENAME TABLE, qualified", "RENAME TABLE t TO u;\nALTER TABLE `{db}`.u MODIFY at timestamp NOT NULL;", "u.at"},
		{"executable comment", "ALTER TABLE t /*!80000 MODIFY at timestamp NOT NULL */;", "t.at"},
		{"CONVERT", "ALTER TABLE t CONVERT TO CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;", "t.c"},
		{"EXECUTE", "PREPARE s FROM 'ALTER TABLE t MODIFY at timestamp NOT NULL';\nEXECUTE s;", "t.at"},
		{"CALL", "CALL p();", "t.at"},
		{"an action it cannot read", "ALTER TABLE t SOMETHING NEW;", "t.at"},
	} {
		t.Run(c.name, func(t *testing.T) {
			db, fsys := setup(t, c.q)
			_, err := migrate.Up(context.Background(), db, fsys)
			var e *migrate.Error
			if !errors.As(err, &e) || e.Code != "unmodeled" || !strings.Contains(err.Error(), c.says) {
				t.Fatalf("Up: %v", err)
			}
			if extra, _ := mysqlColumn(t, db, "at"); !strings.Contains(strings.ToLower(extra), "on update") {
				t.Errorf("EXTRA %q", extra)
			}
		})
	}
	// What keeps it, or does not touch it, runs: no false refusal.
	for _, c := range []struct{ name, q string }{
		{"keeps it", "ALTER TABLE t MODIFY AT " + keep + ";"},
		{"keeps it through CHANGE", "ALTER TABLE t CHANGE at at2 " + keep + ";\nALTER TABLE t MODIFY at2 " + keep + ";"},
		{"other actions", "ALTER TABLE t ADD COLUMN n integer, ADD INDEX t_n (n), ALGORITHM=INPLACE, LOCK=NONE;\nALTER TABLE t ALTER COLUMN n SET DEFAULT 1;\nALTER TABLE t COMMENT = 'x';\nALTER TABLE t ENGINE=InnoDB;"},
		{"a column it makes", "ALTER TABLE t ADD COLUMN n integer;\nALTER TABLE t MODIFY n bigint;\nALTER TABLE t DROP COLUMN at;\nALTER TABLE t ADD COLUMN at integer;\nALTER TABLE t MODIFY at bigint;"},
		{"a table it makes", "DROP TABLE t;\nCREATE TABLE t (at timestamp);\nALTER TABLE t MODIFY at datetime;"},
	} {
		t.Run(c.name, func(t *testing.T) {
			db, fsys := setup(t, c.q)
			if _, err := migrate.Up(context.Background(), db, fsys); err != nil {
				t.Errorf("Up: %v", err)
			}
		})
	}
}

// retypedKey is a database whose c.p_id references p.id, by hand with the
// attributes, and the plan that widens both, which adds the key again from the
// model.
func retypedKey(t *testing.T, d velox.Dialect, attrs string, opts migrate.PlanOptions) (*sql.DB, fstest.MapFS) {
	t.Helper()
	schema := func(typ string) *migrate.Schema {
		pc := func(t migrate.Table) migrate.Table {
			if d == velox.Postgres {
				t.PrimaryKeyName = t.Name + "_pkey"
			} else {
				t.PrimaryKeyName = "PRIMARY"
				t.Collation = "utf8mb4_bin"
			}
			return t
		}
		return &migrate.Schema{Dialect: d, Tables: []migrate.Table{
			pc(migrate.Table{Name: "c", Columns: []migrate.Column{{Name: "id", Type: "integer"}, {Name: "p_id", Type: typ}}, PrimaryKey: []string{"id"},
				ForeignKeys: []migrate.ForeignKey{{Name: "c_p", Columns: []string{"p_id"}, RefTable: migrate.TableRef{Name: "p"}, RefColumns: []string{"id"}}}}),
			pc(migrate.Table{Name: "p", Columns: []migrate.Column{{Name: "id", Type: typ}}, PrimaryKey: []string{"id"}}),
		}}
	}
	db, _ := freshDB(t, d)
	fsys := dirOf(t, d, step{to: schema("integer"), name: "base"})
	mustUp(t, db, fsys)
	if d == velox.Postgres {
		mustExec(t, db, `ALTER TABLE c DROP CONSTRAINT c_p, ADD CONSTRAINT c_p FOREIGN KEY (p_id) REFERENCES p (id) `+attrs)
	} else {
		mustExec(t, db, `ALTER TABLE c DROP FOREIGN KEY c_p`)
		mustExec(t, db, `ALTER TABLE c ADD CONSTRAINT c_p FOREIGN KEY (p_id) REFERENCES p (id) `+attrs)
	}
	return db, extend(t, fsys, d, step{to: schema("bigint"), name: "widen", opts: opts})
}

// TestUpForeignKeyAddedAgain: a retype adds a foreign key again from the model,
// which would lose ON UPDATE and DEFERRABLE: Up refuses before it changes
// anything, naming them; the directive accepts it, and a file that writes them
// keeps them.
func TestUpForeignKeyAddedAgain(t *testing.T) {
	for _, c := range []struct {
		d     velox.Dialect
		attrs string
		says  []string
	}{
		{velox.Postgres, "ON UPDATE CASCADE DEFERRABLE", []string{"c.c_p", "ON UPDATE CASCADE", "DEFERRABLE", "velox:allow unmodeled-loss c.c_p"}},
		{velox.MySQL, "ON UPDATE CASCADE", []string{"c.c_p", "ON UPDATE CASCADE", "velox:allow unmodeled-loss c.c_p"}},
	} {
		t.Run(c.d.String(), func(t *testing.T) {
			rule := func(db *sql.DB) string {
				t.Helper()
				var r string
				q := `SELECT UPDATE_RULE FROM information_schema.REFERENTIAL_CONSTRAINTS WHERE CONSTRAINT_NAME = 'c_p'`
				if c.d == velox.MySQL {
					q += ` AND CONSTRAINT_SCHEMA = DATABASE()`
				}
				if err := db.QueryRow(q).Scan(&r); err != nil {
					t.Fatal(err)
				}
				return strings.ToUpper(r)
			}
			db, fsys := retypedKey(t, c.d, c.attrs, migrate.PlanOptions{})
			_, err := migrate.Up(context.Background(), db, fsys)
			var e *migrate.Error
			if !errors.As(err, &e) || e.Code != "unmodeled" {
				t.Fatalf("Up: %v\n%s", err, sqlFiles(fsys))
			}
			for _, want := range c.says {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the error does not say %q: %v", want, err)
				}
			}
			if r := rule(db); r != "CASCADE" {
				t.Errorf("the rule after the refusal: %s", r)
			}

			db, fsys = retypedKey(t, c.d, c.attrs, migrate.PlanOptions{Allow: []migrate.Allow{{Code: "unmodeled-loss", Target: "c.c_p"}}})
			if _, err := migrate.Up(context.Background(), db, fsys); err != nil {
				t.Fatalf("Up with the directive: %v\n%s", err, sqlFiles(fsys))
			}
			if r := rule(db); r == "CASCADE" {
				t.Errorf("the accepted loss did not happen")
			}

			db, fsys = retypedKey(t, c.d, c.attrs, migrate.PlanOptions{})
			for p := range fsys {
				if strings.HasSuffix(p, ".sql") {
					quote := "`"
					if c.d == velox.Postgres {
						quote = `"`
					}
					from := "REFERENCES " + quote + "p" + quote + " (" + quote + "id" + quote + ")"
					fsys = rewrite(t, fsys, p, func(s string) string {
						if strings.Contains(p, "base") {
							return s
						}
						return strings.ReplaceAll(s, from, from+" "+c.attrs)
					})
				}
			}
			if _, err := migrate.Up(context.Background(), db, fsys); err != nil {
				t.Fatalf("Up keeping them: %v\n%s", err, sqlFiles(fsys))
			}
			if r := rule(db); r != "CASCADE" {
				t.Errorf("the rule: %s", r)
			}
		})
	}
}

// pgCollation is t.c's collation.
func pgCollation(t *testing.T, db *sql.DB) string {
	t.Helper()
	var coll string
	err := db.QueryRow(`SELECT coalesce(collation_name, '') FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 't' AND column_name = 'c'`).Scan(&coll)
	if err != nil {
		t.Fatal(err)
	}
	return coll
}

// TestPostgresRetypeKeepsCollation: ALTER COLUMN TYPE takes the new type's
// collation unless it writes one, and the plan cannot read what the column has:
// Up refuses, naming it; a file that writes it keeps it, and the directive
// accepts the loss.
func TestPostgresRetypeKeepsCollation(t *testing.T) {
	d := velox.Postgres
	setup := func(t *testing.T, opts migrate.PlanOptions) (*sql.DB, fstest.MapFS) {
		t.Helper()
		db, _ := freshDB(t, d)
		col := func(typ string) migrate.Column { return migrate.Column{Name: "c", Type: typ, Nullable: true} }
		fsys := dirOf(t, d, step{to: oneTable(d, col("character varying(10)")), name: "base"})
		mustUp(t, db, fsys)
		mustExec(t, db, `ALTER TABLE t ALTER COLUMN c TYPE varchar(10) COLLATE "C"`)
		return db, extend(t, fsys, d, step{to: oneTable(d, col("character varying(20)")), name: "widen", opts: opts})
	}
	db, fsys := setup(t, migrate.PlanOptions{})
	_, err := migrate.Up(context.Background(), db, fsys)
	var e *migrate.Error
	if !errors.As(err, &e) || e.Code != "unmodeled" || !strings.Contains(err.Error(), "t.c") || !strings.Contains(err.Error(), "COLLATE") || !strings.Contains(err.Error(), "velox:allow unmodeled-loss t.c") {
		t.Fatalf("Up: %v\n%s", err, sqlFiles(fsys))
	}
	if coll := pgCollation(t, db); coll != "C" {
		t.Errorf("the collation after the refusal: %q", coll)
	}

	db, fsys = setup(t, migrate.PlanOptions{})
	fsys = rewrite(t, fsys, "0002_widen.sql", func(s string) string {
		return strings.Replace(s, " USING", ` COLLATE "C" USING`, 1)
	})
	if data := string(fsys["0002_widen.sql"].Data); !strings.Contains(data, `COLLATE "C"`) {
		fsys = rewrite(t, fsys, "0002_widen.sql", func(s string) string {
			return strings.Replace(s, "TYPE character varying(20)", `TYPE character varying(20) COLLATE "C"`, 1)
		})
	}
	mustUp(t, db, fsys)
	if coll := pgCollation(t, db); coll != "C" {
		t.Errorf("the collation the file keeps: %q", coll)
	}

	db, fsys = setup(t, allowAll("unmodeled-loss"))
	mustUp(t, db, fsys)
	if coll := pgCollation(t, db); coll == "C" {
		t.Error("the accepted loss did not happen")
	}

	// A rename is followed; a COLLATE inside USING is not the column's; a column the file makes has none to lose.
	for _, c := range []struct {
		q      string
		refuse bool
	}{
		{`ALTER TABLE t RENAME c TO d;` + "\n" + `ALTER TABLE t ALTER COLUMN d TYPE varchar(30);`, true},
		{`ALTER TABLE T ALTER c SET DATA TYPE varchar(30);`, true},
		{`ALTER TABLE public.t ALTER COLUMN "c" TYPE varchar(30);`, true},
		{`ALTER TABLE t ALTER COLUMN c TYPE varchar(30) USING c COLLATE "C";`, true},
		{`ALTER TABLE t ALTER COLUMN c TYPE varchar(30) COLLATE "C";`, false},
		{`ALTER TABLE t DROP COLUMN c;` + "\n" + `ALTER TABLE t ADD COLUMN c text;` + "\n" + `ALTER TABLE t ALTER COLUMN c TYPE varchar(30);`, false},
	} {
		db, _ := freshDB(t, d)
		base := dirOf(t, d, step{to: oneTable(d, migrate.Column{Name: "c", Type: "character varying(10)", Nullable: true}), name: "base"})
		mustUp(t, db, base)
		mustExec(t, db, `ALTER TABLE t ALTER COLUMN c TYPE varchar(10) COLLATE "C"`)
		fsys := extend(t, base, d, files("0002_x.sql", c.q+"\n"))
		_, err := migrate.Up(context.Background(), db, fsys)
		var e *migrate.Error
		if c.refuse && (!errors.As(err, &e) || e.Code != "unmodeled") {
			t.Errorf("%s: Up: %v", c.q, err)
		}
		if !c.refuse && err != nil {
			t.Errorf("%s: Up: %v", c.q, err)
		}
	}
}
