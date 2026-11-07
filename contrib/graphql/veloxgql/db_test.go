package veloxgql_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

func TestDBFrom(t *testing.T) {
	_, err := veloxgql.DBFromContext(context.Background())
	if !errors.Is(err, veloxgql.ErrNoDB) || !strings.Contains(err.Error(), "serve the schema through veloxgql.NewExtension") {
		t.Errorf("DBFromContext with no DB: %v, want ErrNoDB naming veloxgql.NewExtension", err)
	}
	db := &velox.DB{}
	if got, err := veloxgql.DBFromContext(veloxgql.NewExtension(db).NewContext(context.Background())); got != db || err != nil {
		t.Errorf("DBFromContext = %p, %v, want %p", got, err, db)
	}
}

// TestDB: DB is DBFromContext's DB, and panics with its error where there
// is none.
func TestDB(t *testing.T) {
	db := &velox.DB{}
	if got := veloxgql.DB(veloxgql.NewExtension(db).NewContext(context.Background())); got != db {
		t.Errorf("DB = %p, want %p", got, db)
	}
	defer func() {
		err, _ := recover().(error)
		if !errors.Is(err, veloxgql.ErrNoDB) {
			t.Errorf("DB with no DB panicked with %v, want ErrNoDB", err)
		}
	}()
	veloxgql.DB(context.Background())
	t.Error("DB with no DB did not panic")
}
