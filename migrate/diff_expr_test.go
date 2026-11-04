package migrate

import (
	"testing"

	"github.com/syssam/velox"
)

func TestRenameIdent(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		d          velox.Dialect
		expr, want string
	}{
		{velox.Postgres, `"bio" <> 'bio'`, `"about" <> 'bio'`},
		{velox.Postgres, `bio IS NOT NULL AND Bio <> ''`, `about IS NOT NULL AND about <> ''`},
		{velox.Postgres, `bio(x) > 0 AND biography > 0 AND "bio""s" > 0`, `bio(x) > 0 AND biography > 0 AND "bio""s" > 0`},
		{velox.Postgres, `'it''s bio' = bio`, `'it''s bio' = about`},
		{velox.SQLite, "`bio` IN ('a', 'b') AND \"bio\" > 1", "\"about\" IN ('a', 'b') AND \"about\" > 1"},
		{velox.MySQL, "`bio` IN ('a\\'bio', \"bio\") OR bio > 1", "`about` IN ('a\\'bio', \"bio\") OR about > 1"},
		{velox.Postgres, `1bio > 0 AND t.bio > 0`, `1bio > 0 AND t.about > 0`},
		{velox.Postgres, `"BIO" > 0 AND BIO > 0`, `"BIO" > 0 AND about > 0`},
		{velox.SQLite, `"BIO" > 0`, `"about" > 0`},
		{velox.MySQL, "`Bio` > 0", "`about` > 0"},
		{velox.Postgres, `E'it\'s bio' = bio AND e'bio' = bio`, `E'it\'s bio' = about AND e'bio' = about`},
		{velox.Postgres, `$$bio's$$ = bio AND $t$ bio $t$ = bio`, `$$bio's$$ = about AND $t$ bio $t$ = about`},
	} {
		if got := renameIdent(c.d, c.expr, "bio", "about"); got != c.want {
			t.Errorf("%v: %s: %s, want %s", c.d, c.expr, got, c.want)
		}
	}
	if got := renameIdent(velox.Postgres, `bio > 0`, "bio", "About"); got != `"About" > 0` {
		t.Errorf("a name that needs quotes: %s", got)
	}
	// On PostgreSQL an unquoted name folds to lower case: bio is not "Bio".
	if got := renameIdent(velox.Postgres, `bio > 0 AND "Bio" > 0`, "Bio", "x"); got != `bio > 0 AND "x" > 0` {
		t.Errorf("a column with capitals: %s", got)
	}
}
