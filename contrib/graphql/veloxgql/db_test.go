package veloxgql_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
)

func TestDBFrom(t *testing.T) {
	_, err := veloxgql.DBFromContext(context.Background())
	if !errors.Is(err, veloxgql.ErrNoDB) || !strings.Contains(err.Error(), "serve the schema through gqlgenx.NewExtension") {
		t.Errorf("DBFromContext with no DB: %v, want ErrNoDB naming gqlgenx.NewExtension", err)
	}
	db := &velox.DB{}
	if got, err := veloxgql.DBFromContext(gqlgenx.NewExtension(db).NewContext(context.Background())); got != db || err != nil {
		t.Errorf("DBFromContext = %p, %v, want %p", got, err, db)
	}
}

// TestDB: DB is DBFromContext's DB, and panics with its error where there
// is none.
func TestDB(t *testing.T) {
	db := &velox.DB{}
	if got := veloxgql.DB(gqlgenx.NewExtension(db).NewContext(context.Background())); got != db {
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
