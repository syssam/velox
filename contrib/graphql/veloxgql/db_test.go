package veloxgql_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
)

func TestDBFrom(t *testing.T) {
	_, err := veloxgql.DBFrom(context.Background())
	if !errors.Is(err, veloxgql.ErrNoDB) || !strings.Contains(err.Error(), "veloxgql.Handler") {
		t.Errorf("DBFrom with no DB: %v, want ErrNoDB naming veloxgql.Handler", err)
	}
	if _, err := veloxgql.DBFrom(veloxgql.WithDB(context.Background(), nil)); !errors.Is(err, veloxgql.ErrNoDB) {
		t.Errorf("DBFrom with a nil DB: %v, want ErrNoDB", err)
	}
	db := &velox.DB{}
	if got, err := veloxgql.DBFrom(veloxgql.WithDB(context.Background(), db)); got != db || err != nil {
		t.Errorf("DBFrom = %p, %v, want %p", got, err, db)
	}
}
