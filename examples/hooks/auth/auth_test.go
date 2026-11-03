package auth_test

import (
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/hooks/auth"
	"github.com/syssam/velox/examples/hooks/gen"
	_ "github.com/syssam/velox/examples/hooks/gen/runtime"
	"github.com/syssam/velox/migrate"
)

func TestUserFromContext(t *testing.T) {
	ctx := t.Context()
	if u := auth.UserFromContext(ctx); u != "" {
		t.Errorf("a context with no user has user %q", u)
	}
	if u := auth.UserFromContext(auth.WithUser(ctx, "ada")); u != "ada" {
		t.Errorf("UserFromContext = %q, want ada", u)
	}
}

// TestCreatedBy inserts through gen.Task, which lists the hook for inserts:
// the context's user fills an empty column, and a column the caller set stays.
func TestCreatedBy(t *testing.T) {
	ctx := t.Context()
	db, err := velox.Connect("sqlite", filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrate.Up(ctx, velox.Pool(db), os.DirFS("../migrations")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, user, set, want string
	}{
		{"filled from the context", "ada", "", "ada"},
		{"kept when the caller set it", "ada", "grace", "grace"},
		{"empty with no user", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := gen.Task{Title: tc.name, CreatedBy: tc.set}
			if err := db.Insert(&x).Exec(auth.WithUser(ctx, tc.user)); err != nil {
				t.Fatal(err)
			}
			if x.CreatedBy != tc.want {
				t.Errorf("CreatedBy = %q, want %q", x.CreatedBy, tc.want)
			}
			got, err := db.Query[gen.Task]().Where(gen.Tasks.ID.EQ(x.ID)).One(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if got.CreatedBy != tc.want {
				t.Errorf("stored CreatedBy = %q, want %q", got.CreatedBy, tc.want)
			}
		})
	}
}
