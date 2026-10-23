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
	{Kind: migrate.MainScript, Statements: []migrate.Statement{{SQL: "ALTER TABLE users DROP COLUMN legacy", Review: "drop-column: drops a; b"}}},
	{Kind: migrate.MainScript, Directives: migrate.Directives{Allow: []migrate.Allow{{Code: "drop-table", Target: "posts"}}}, Statements: []migrate.Statement{{SQL: "DROP TABLE posts", Review: "drop-table: it's posts'"}}},
	{Kind: migrate.OnlineScript, Directives: migrate.Directives{NoTransaction: true}, Statements: []migrate.Statement{
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
