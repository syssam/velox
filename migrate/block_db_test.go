package migrate_test

import (
	"testing"

	"github.com/syssam/velox"
)

// TestUpStatementBlock: a trigger or routine between -- velox:statement
// begin and end lines runs as one statement on each dialect, and works
// after: the trigger fires on an insert.
func TestUpStatementBlock(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		d    velox.Dialect
		file string
	}{
		{velox.SQLite, "CREATE TABLE a (x integer);\nCREATE TABLE n (c integer);\nINSERT INTO n VALUES (0);\n" +
			"-- velox:statement begin\nCREATE TRIGGER a_count AFTER INSERT ON a BEGIN\n  UPDATE n SET c = c + 1;\n  UPDATE n SET c = c + 1;\nEND;\n-- velox:statement end\n"},
		{velox.MySQL, "CREATE TABLE a (x int);\nCREATE TABLE n (c int);\nINSERT INTO n VALUES (0);\n" +
			"-- velox:statement begin\nCREATE TRIGGER a_count AFTER INSERT ON a FOR EACH ROW BEGIN\n  UPDATE n SET c = c + 1;\n  UPDATE n SET c = c + 1;\nEND;\n-- velox:statement end\n"},
		{velox.Postgres, "CREATE TABLE a (x integer);\nCREATE TABLE n (c integer);\nINSERT INTO n VALUES (0);\n" +
			"-- velox:statement begin\nCREATE PROCEDURE bump() LANGUAGE sql BEGIN ATOMIC\n  UPDATE n SET c = c + 1;\n  UPDATE n SET c = c + 1;\nEND;\n-- velox:statement end\n" +
			"CREATE FUNCTION a_count() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN CALL bump(); RETURN NEW; END $$;\n" +
			"CREATE TRIGGER a_count AFTER INSERT ON a FOR EACH ROW EXECUTE FUNCTION a_count();\n"},
	} {
		t.Run(c.d.String(), func(t *testing.T) {
			t.Parallel()
			db, _ := freshDB(t, c.d)
			fsys := dirOf(t, c.d, files("0001_counted.sql", c.file))
			mustUp(t, db, fsys)
			if _, err := db.Exec("INSERT INTO a VALUES (1)"); err != nil {
				t.Fatal(err)
			}
			var n int
			if err := db.QueryRow("SELECT c FROM n").Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 2 {
				t.Errorf("the trigger counted %d, want 2: both statements of its body ran", n)
			}
		})
	}
}
