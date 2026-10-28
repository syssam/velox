package migrate_test

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/migrate"
)

// stmt is a statement piece.
func stmt(sql string, line int) migrate.Piece { return migrate.Piece{SQL: sql, Line: line} }

// directive is a directive piece.
func directive(text string, line int) migrate.Piece {
	return migrate.Piece{Directive: text, Line: line}
}

// atomic is the refusal of a BEGIN ATOMIC body.
const atomic = "BEGIN ATOMIC bodies are not supported in migration files; write the body as a string, AS $$ … $$"

var splitCases = []struct {
	d    velox.Dialect
	src  string
	want []migrate.Piece
	err  string // the Reason of the error, at line
	line int
}{
	{d: velox.Postgres, src: "SELECT 'a;b'; SELECT 2", want: []migrate.Piece{stmt("SELECT 'a;b'", 1), stmt("SELECT 2", 1)}},
	{d: velox.MySQL, src: "SELECT 'a;b'; SELECT 2", want: []migrate.Piece{stmt("SELECT 'a;b'", 1), stmt("SELECT 2", 1)}},
	{d: velox.SQLite, src: "SELECT 'a;b'; SELECT 2", want: []migrate.Piece{stmt("SELECT 'a;b'", 1), stmt("SELECT 2", 1)}},
	{d: velox.Postgres, src: `SELECT "x;y"`, want: []migrate.Piece{stmt(`SELECT "x;y"`, 1)}},
	{d: velox.MySQL, src: `SELECT "x;y"`, want: []migrate.Piece{stmt(`SELECT "x;y"`, 1)}},
	{d: velox.SQLite, src: `SELECT "x;y"`, want: []migrate.Piece{stmt(`SELECT "x;y"`, 1)}},
	{d: velox.MySQL, src: "SELECT `a;b`", want: []migrate.Piece{stmt("SELECT `a;b`", 1)}},
	{d: velox.MySQL, src: `SELECT 'it\'s;'`, want: []migrate.Piece{stmt(`SELECT 'it\'s;'`, 1)}},
	{d: velox.MySQL, src: "SELECT 1 # c;\n;", want: []migrate.Piece{stmt("SELECT 1", 1)}},
	{d: velox.MySQL, src: "SELECT 1--2;", want: []migrate.Piece{stmt("SELECT 1--2", 1)}},
	{d: velox.MySQL, src: "SELECT 1 -- c;\n;", want: []migrate.Piece{stmt("SELECT 1", 1)}},
	{d: velox.Postgres, src: "SELECT $$a;b$$", want: []migrate.Piece{stmt("SELECT $$a;b$$", 1)}},
	{d: velox.Postgres, src: "SELECT $t$ $x$ ; $t$", want: []migrate.Piece{stmt("SELECT $t$ $x$ ; $t$", 1)}},
	{d: velox.Postgres, src: `SELECT E'\';'`, want: []migrate.Piece{stmt(`SELECT E'\';'`, 1)}},
	{d: velox.Postgres, src: "SELECT /* a /* b; */ ; */ 1", want: []migrate.Piece{stmt("SELECT /* a /* b; */ ; */ 1", 1)}},
	{d: velox.Postgres, src: "SELECT a$b; SELECT 2", want: []migrate.Piece{stmt("SELECT a$b", 1), stmt("SELECT 2", 1)}},
	{d: velox.SQLite, src: "SELECT [a;b]", want: []migrate.Piece{stmt("SELECT [a;b]", 1)}},
	{d: velox.Postgres, src: "-- review: drop-column: drops column legacy; it's \"gone\"\nSELECT 1;", want: []migrate.Piece{stmt("SELECT 1", 2)}},
	{d: velox.MySQL, src: "-- review: drop-column: drops column legacy; it's \"gone\"\nSELECT 1;", want: []migrate.Piece{stmt("SELECT 1", 2)}},
	{d: velox.Postgres, src: "SELECT 1 -- c;\n;", want: []migrate.Piece{stmt("SELECT 1", 1)}},
	{d: velox.SQLite, src: "SELECT 1 /* c; */ ;", want: []migrate.Piece{stmt("SELECT 1", 1)}},
	{d: velox.Postgres, src: ";;SELECT 1", want: []migrate.Piece{stmt("SELECT 1", 1)}},
	{d: velox.Postgres, src: "SELECT 1", want: []migrate.Piece{stmt("SELECT 1", 1)}},
	{d: velox.Postgres, src: "SELECT\n  1 -- one\n  + 2;", want: []migrate.Piece{stmt("SELECT\n  1 -- one\n  + 2", 1)}},
	{d: velox.Postgres, src: "-- velox:no-transaction\n\nSELECT 1;\n-- velox:unit\nSELECT 2;", want: []migrate.Piece{
		directive("no-transaction", 1), stmt("SELECT 1", 3), directive("unit", 4), stmt("SELECT 2", 5),
	}},
	{d: velox.Postgres, src: "", want: nil},
	{d: velox.Postgres, src: "SELECT 'abc", err: "unterminated string literal", line: 1},
	{d: velox.MySQL, src: `SELECT 'abc\'`, err: "unterminated string literal", line: 1},
	{d: velox.Postgres, src: "SELECT 1;\nSELECT $$x", err: "unterminated dollar-quoted string", line: 2},
	{d: velox.Postgres, src: "/* x", err: "unterminated comment", line: 1},
	{d: velox.Postgres, src: "/* /* x */", err: "unterminated comment", line: 1},
	{d: velox.Postgres, src: `SELECT "x`, err: "unterminated quoted name", line: 1},
	{d: velox.SQLite, src: "SELECT [x", err: "unterminated quoted name", line: 1},
	{d: velox.Postgres, src: "SELECT\n-- velox:unit\n1;", err: "a directive inside a statement", line: 2},
	{d: velox.Postgres, src: "SELECT 1; -- velox:unit\nSELECT 2;", err: "a directive must start its line", line: 1},
	{d: velox.MySQL, src: "/* x */ -- velox:no-transaction\nSELECT 2;", err: "a directive must start its line", line: 1},
	{d: velox.Postgres, src: "  \t-- velox:unit\nSELECT 2;", want: []migrate.Piece{directive("unit", 1), stmt("SELECT 2", 2)}},
	{d: velox.MySQL, src: "/*!40101 SET NAMES utf8mb4 */;\nSELECT 2;", want: []migrate.Piece{stmt("/*!40101 SET NAMES utf8mb4 */", 1), stmt("SELECT 2", 2)}},
	{d: velox.MySQL, src: "/*! SET a = 1; */ ; /* plain; */ SELECT 1 /*!80000 , 2 */", want: []migrate.Piece{stmt("/*! SET a = 1; */", 1), stmt("SELECT 1 /*!80000 , 2 */", 1)}},
	{d: velox.MySQL, src: "/* plain */;", want: nil},
	{d: velox.MySQL, src: "SELECT ab!=1; SELECT 2", want: []migrate.Piece{stmt("SELECT ab!=1", 1), stmt("SELECT 2", 1)}},
	{d: velox.MySQL, src: "SELECT 1 /*!80000 , 2", err: "unterminated comment", line: 1},
	{d: velox.Postgres, src: "/*!x*/ SELECT 1", want: []migrate.Piece{stmt("SELECT 1", 1)}},
	{d: velox.Postgres, src: "SELECT 'it''s;'", want: []migrate.Piece{stmt("SELECT 'it''s;'", 1)}},
	{d: velox.MySQL, src: `SELECT "a""b;"`, want: []migrate.Piece{stmt(`SELECT "a""b;"`, 1)}},
	{d: velox.MySQL, src: "SELECT 'a''b;'", want: []migrate.Piece{stmt("SELECT 'a''b;'", 1)}},
	{d: velox.SQLite, src: "SELECT `a;``b`", want: []migrate.Piece{stmt("SELECT `a;``b`", 1)}},
	{d: velox.Postgres, src: `SELECT e'\';'`, want: []migrate.Piece{stmt(`SELECT e'\';'`, 1)}},
	{d: velox.Postgres, src: "SELECT 1 # 2; SELECT 3", want: []migrate.Piece{stmt("SELECT 1 # 2", 1), stmt("SELECT 3", 1)}},
	{d: velox.SQLite, src: `SELECT 'a\'; SELECT 2`, want: []migrate.Piece{stmt(`SELECT 'a\'`, 1), stmt("SELECT 2", 1)}},
	{d: velox.MySQL, src: "SELECT /* /* */ 1; SELECT 2", want: []migrate.Piece{stmt("SELECT /* /* */ 1", 1), stmt("SELECT 2", 1)}},
	{d: velox.Postgres, src: "SELECT 'a\nb\nc';\n\nSELECT 2;", want: []migrate.Piece{stmt("SELECT 'a\nb\nc'", 1), stmt("SELECT 2", 5)}},
	// Triggers and procedures, whose bodies hold ";", are refused at their statement.
	{d: velox.SQLite, src: "SELECT 1;\nCREATE TRIGGER t AFTER INSERT ON a BEGIN UPDATE a SET x = 1; END;", err: "triggers and procedures are not supported in migration files", line: 2},
	{d: velox.SQLite, src: "create temp trigger t after insert on a begin select 1; end;", err: "triggers and procedures are not supported in migration files", line: 1},
	{d: velox.SQLite, src: "CREATE TEMPORARY TRIGGER IF NOT EXISTS t AFTER INSERT ON a BEGIN SELECT 1; END;", err: "triggers and procedures are not supported in migration files", line: 1},
	{d: velox.MySQL, src: "CREATE TRIGGER t BEFORE INSERT ON a FOR EACH ROW SET NEW.x = 1;", err: "triggers and procedures are not supported in migration files", line: 1},
	{d: velox.MySQL, src: "\nCREATE PROCEDURE p() BEGIN SELECT 1; END;", err: "triggers and procedures are not supported in migration files", line: 2},
	{d: velox.MySQL, src: "CREATE FUNCTION f() RETURNS int RETURN 1;", err: "triggers and procedures are not supported in migration files", line: 1},
	{d: velox.MySQL, src: "CREATE EVENT e ON SCHEDULE EVERY 1 DAY DO DELETE FROM a;", err: "triggers and procedures are not supported in migration files", line: 1},
	{d: velox.MySQL, src: "CREATE DEFINER = `u`@`%` TRIGGER t BEFORE INSERT ON a FOR EACH ROW SET NEW.x = 1;", err: "triggers and procedures are not supported in migration files", line: 1},
	{d: velox.MySQL, src: "CREATE DEFINER = `TRIGGER`@`%` VIEW v AS SELECT 1", want: []migrate.Piece{stmt("CREATE DEFINER = `TRIGGER`@`%` VIEW v AS SELECT 1", 1)}},
	{d: velox.MySQL, src: "BEGIN SELECT 1; END;", err: "triggers and procedures are not supported in migration files", line: 1},
	{d: velox.SQLite, src: "SELECT 1; BEGIN SELECT 1; END;", err: "triggers and procedures are not supported in migration files", line: 1},
	{d: velox.SQLite, src: "BEGIN; BEGIN TRANSACTION; begin work; BEGIN IMMEDIATE; BEGIN EXCLUSIVE TRANSACTION",
		want: []migrate.Piece{stmt("BEGIN", 1), stmt("BEGIN TRANSACTION", 1), stmt("begin work", 1), stmt("BEGIN IMMEDIATE", 1), stmt("BEGIN EXCLUSIVE TRANSACTION", 1)}},
	{d: velox.MySQL, src: "BEGIN; BEGIN WORK; SELECT 'CREATE TRIGGER'; CREATE TABLE `trigger` (id int)",
		want: []migrate.Piece{stmt("BEGIN", 1), stmt("BEGIN WORK", 1), stmt("SELECT 'CREATE TRIGGER'", 1), stmt("CREATE TABLE `trigger` (id int)", 1)}},
	{d: velox.Postgres, src: "CREATE FUNCTION f() RETURNS trigger AS $$ BEGIN RETURN NEW; END $$ LANGUAGE plpgsql; CREATE TRIGGER t BEFORE INSERT ON a FOR EACH ROW EXECUTE FUNCTION f()",
		want: []migrate.Piece{stmt("CREATE FUNCTION f() RETURNS trigger AS $$ BEGIN RETURN NEW; END $$ LANGUAGE plpgsql", 1), stmt("CREATE TRIGGER t BEFORE INSERT ON a FOR EACH ROW EXECUTE FUNCTION f()", 1)}},
	// BEGIN ATOMIC bodies and psql meta-command lines are refused by name.
	{d: velox.Postgres, src: "CREATE FUNCTION f() RETURNS int LANGUAGE sql BEGIN ATOMIC SELECT 1; END;", err: atomic, line: 1},
	{d: velox.Postgres, src: "SELECT 1;\nCREATE OR REPLACE PROCEDURE p() LANGUAGE sql\nBEGIN ATOMIC INSERT INTO a VALUES (1); INSERT INTO a VALUES (2); END;", err: atomic, line: 2},
	{d: velox.Postgres, src: "CREATE FUNCTION f() RETURNS int LANGUAGE sql begin atomic end", err: atomic, line: 1},
	{d: velox.Postgres, src: "SELECT 'BEGIN ATOMIC'; CREATE FUNCTION f() RETURNS int AS $$ BEGIN ATOMIC $$ LANGUAGE sql",
		want: []migrate.Piece{stmt("SELECT 'BEGIN ATOMIC'", 1), stmt("CREATE FUNCTION f() RETURNS int AS $$ BEGIN ATOMIC $$ LANGUAGE sql", 1)}},
	{d: velox.Postgres, src: "\\restrict abc123\n\nSET statement_timeout = 0;\nCREATE TABLE a (id integer);\n\\unrestrict abc123\n",
		err: `a psql meta-command (\restrict) is not SQL; remove it`, line: 1},
	{d: velox.Postgres, src: "SELECT 1;\n  \\connect other\nSELECT 2;", err: `a psql meta-command (\connect) is not SQL; remove it`, line: 2},
	{d: velox.Postgres, src: "SELECT 1\n\\gset\n;", err: `a psql meta-command (\gset) is not SQL; remove it`, line: 2},
	{d: velox.Postgres, src: "SELECT '\n\\x';", want: []migrate.Piece{stmt("SELECT '\n\\x'", 1)}},
	{d: velox.MySQL, src: "SELECT '\n\\x';", want: []migrate.Piece{stmt("SELECT '\n\\x'", 1)}},
	{d: velox.Postgres, src: "SELECT $$\n\\x$$; /* a\n\\y */ SELECT 2;", want: []migrate.Piece{stmt("SELECT $$\n\\x$$", 1), stmt("SELECT 2", 3)}},
	{d: velox.Postgres, src: "-- c\n\\x\n", err: `a psql meta-command (\x) is not SQL; remove it`, line: 2},
	{d: velox.Postgres, src: "SELECT 1; \\dt", err: `a psql meta-command (\dt) is not SQL; remove it`, line: 1},
	{d: velox.MySQL, src: "SELECT 1\n\\g\n", want: []migrate.Piece{stmt("SELECT 1\n\\g", 1)}},
	{d: velox.SQLite, src: "SELECT 1\n\\g\n", want: []migrate.Piece{stmt("SELECT 1\n\\g", 1)}},
}

func TestSplit(t *testing.T) {
	for _, c := range splitCases {
		got, err := migrate.Split(c.d, c.src)
		if c.err != "" {
			var e *migrate.Error
			if !errors.As(err, &e) || e.Code != "file" || e.Reason != c.err || e.Pos != strconv.Itoa(c.line) {
				t.Errorf("%v %q: error %v, want %q at line %d", c.d, c.src, err, c.err, c.line)
			}
			continue
		}
		if err != nil {
			t.Errorf("%v %q: %v", c.d, c.src, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v %q:\n got %+v\nwant %+v", c.d, c.src, got, c.want)
		}
	}
}

// splitSeedScripts are writeScript outputs whose review lines hold ;, ' and ".
var splitSeedScripts = []migrate.Script{
	{Kind: migrate.ScriptMain, Statements: []migrate.Statement{{SQL: "ALTER TABLE users DROP COLUMN legacy", Review: "drop-column: drops a; b"}}},
	{Kind: migrate.ScriptMain, Directives: migrate.Directives{Allow: []migrate.Allow{{Code: "drop-table", Target: "posts"}}}, Statements: []migrate.Statement{{SQL: "DROP TABLE posts", Review: "drop-table: it's posts'"}}},
	{Kind: migrate.ScriptOnline, Directives: migrate.Directives{NoTransaction: true}, Statements: []migrate.Statement{
		{SQL: `CREATE INDEX CONCURRENTLY "a" ON "t" ("x")`, Review: `add-index: "quoted; name`},
		{SQL: "SELECT 2", Unit: 1},
	}},
}

func FuzzSplit(f *testing.F) {
	for _, c := range splitCases {
		f.Add(uint8(c.d-1), c.src)
	}
	for _, s := range splitSeedScripts {
		for d := range uint8(3) {
			f.Add(d, migrate.WriteScript(s))
		}
	}
	f.Fuzz(func(t *testing.T, db uint8, src string) {
		d := velox.Dialect(db%3) + velox.Postgres
		pieces, err := migrate.Split(d, src)
		if err != nil {
			return
		}
		var stmts []string
		for _, p := range pieces {
			if p.SQL == "" {
				continue
			}
			if strings.Trim(p.SQL, " \t\n\r\f\v") != p.SQL {
				t.Fatalf("%v %q: statement %q has white space at an end", d, src, p.SQL)
			}
			again, err := migrate.Split(d, p.SQL+";")
			if err != nil || len(again) != 1 || again[0].SQL != p.SQL {
				t.Fatalf("%v %q: statement %q split again gives %+v, %v", d, src, p.SQL, again, err)
			}
			stmts = append(stmts, p.SQL)
		}
		// The last ends with ";" too: on MySQL a "--" at the end is a comment.
		again, err := migrate.Split(d, strings.Join(stmts, ";\n")+";")
		if err != nil {
			t.Fatalf("%v %q: the statements joined: %v", d, src, err)
		}
		var got []string
		for _, p := range again {
			got = append(got, p.SQL)
		}
		if !reflect.DeepEqual(got, stmts) {
			t.Fatalf("%v %q: the statements joined split as\n%q\nwant\n%q", d, src, got, stmts)
		}
	})
}
