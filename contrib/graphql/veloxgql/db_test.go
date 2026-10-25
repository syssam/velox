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
	_, err := veloxgql.DBFromContext(context.Background())
	if !errors.Is(err, veloxgql.ErrNoDB) || !strings.Contains(err.Error(), "serve the schema through veloxgql.NewExtension") {
		t.Errorf("DBFromContext with no DB: %v, want ErrNoDB naming veloxgql.NewExtension", err)
	}
	db := &velox.DB{}
	if got, err := veloxgql.DBFromContext(veloxgql.NewExtension(db).NewContext(context.Background())); got != db || err != nil {
		t.Errorf("DBFromContext = %p, %v, want %p", got, err, db)
	}
}
