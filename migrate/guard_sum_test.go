package migrate_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// TestAcceptedLinesLeaveTheHistorySum: production refuses a file staging applied.
// The directive that lets production through leaves staging's history as it is,
// though velox.sum sees the line; a changed statement is a *ChecksumError still.
func TestAcceptedLinesLeaveTheHistorySum(t *testing.T) {
	t.Parallel()
	d := velox.SQLite
	base := dirOf(t, d, step{to: guardSchema(false), name: "base"})
	fsys := extend(t, base, d, step{to: guardSchema(true), name: "nullable"})
	staging, _ := freshDB(t, d)
	mustUp(t, staging, fsys)
	prod, _ := freshDB(t, d)
	mustUp(t, prod, base)
	mustExec(t, prod, "CREATE TRIGGER t_ai AFTER INSERT ON t BEGIN SELECT 1; END")
	_, err := migrate.Up(context.Background(), prod, fsys)
	var e *migrate.Error
	if !errors.As(err, &e) || e.Code != "unmodeled" {
		t.Fatalf("Up on production: %v", err)
	}

	const path = "0002_nullable.sql"
	accepted := rewrite(t, fsys, path, func(s string) string {
		head, rest, _ := strings.Cut(s, "\n")
		return head + "\n-- velox:allow unmodeled-loss t.t_ai\n-- velox:drops t.anything\n" + rest
	})
	if string(accepted["velox.sum"].Data) == string(fsys["velox.sum"].Data) {
		t.Error("velox.sum does not see the directive")
	}
	if _, err := migrate.Up(context.Background(), staging, accepted); err != nil {
		t.Errorf("Up on staging, after the directive: %v", err)
	}
	if _, err := migrate.Up(context.Background(), prod, accepted); err != nil {
		t.Errorf("Up on production, with the directive: %v", err)
	}
	dir, err := migrate.ReadDir(accepted)
	if err != nil {
		t.Fatal(err)
	}
	sums := historyOf(t, staging)
	if got, want := sums[1].Sum, dir.Files[1].Sum; got != want {
		t.Errorf("the history holds %s, the file says %s", got, want)
	}

	edited := rewrite(t, accepted, path, func(s string) string {
		return s + "CREATE TABLE more (x integer);\n"
	})
	_, err = migrate.Up(context.Background(), staging, edited)
	if ce := (*migrate.ChecksumError)(nil); !errors.As(err, &ce) {
		t.Errorf("Up on an edited statement: %v", err)
	}
}

// TestSumReadsOnlyDirectives: the lines left out are the allow and drops
// directives, not a line of a literal that looks like one, nor another directive.
func TestSumReadsOnlyDirectives(t *testing.T) {
	t.Parallel()
	sumOf := func(d velox.Dialect, src string) string {
		t.Helper()
		f, err := migrate.ParseFile(d, "0001_a.sql", []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		return f.Sum
	}
	const plain = "SELECT 1;\n"
	for _, d := range []velox.Dialect{velox.SQLite, velox.Postgres, velox.MySQL} {
		if a, b := sumOf(d, plain), sumOf(d, "-- velox:allow drop-table\n-- velox:allow unmodeled-loss t.c\n-- velox:drops t.c:type\n"+plain); a != b {
			t.Errorf("%s: the directives change the sum: %s, %s", d, a, b)
		}
		if d != velox.SQLite && sumOf(d, plain) == sumOf(d, "-- velox:lock-timeout 1s\n"+plain) {
			t.Errorf("%s: a lock timeout does not change the sum", d)
		}
		a := sumOf(d, "SELECT '\n-- velox:allow drop-table\n';\n")
		b := sumOf(d, "SELECT '\n';\n")
		if a == b {
			t.Errorf("%s: a literal's line is left out of the sum", d)
		}
	}
}
