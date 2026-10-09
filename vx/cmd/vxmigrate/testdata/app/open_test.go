package app

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/syssam/velox/contrib/graphql/gqlrelay"
	"github.com/syssam/velox/dialect"
	velsql "github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/entity"

	_ "modernc.org/sqlite"
)

// byNameDesc is the order input "by name, descending", of an entity that
// takes a list of orders.
func byNameDesc(t *testing.T) []*entity.UserOrder {
	t.Helper()
	f := &entity.UserOrderField{}
	if err := f.UnmarshalGQL("NAME"); err != nil {
		t.Fatal(err)
	}
	return []*entity.UserOrder{{Field: f, Direction: gqlrelay.OrderDirectionDesc}}
}

var seq atomic.Int64

// This file is the part of the tests the tool does not migrate: how a
// database is opened is the application's own, and an order input is what a
// GraphQL engine hands over.

// byTitle is the order input "by title, ascending".
func byTitle(t *testing.T) *entity.PostOrder {
	t.Helper()
	f := &entity.PostOrderField{}
	if err := f.UnmarshalGQL("TITLE"); err != nil {
		t.Fatal(err)
	}
	return &entity.PostOrder{Field: f, Direction: gqlrelay.OrderDirectionAsc}
}

func open(t *testing.T) *Store {
	t.Helper()
	drv, err := velsql.Open(dialect.SQLite, fmt.Sprintf("file:app%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", seq.Add(1)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = drv.Close() })
	client := integration.NewClient(integration.Driver(drv))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &Store{Client: client}
}
