package gqlgenx_test

import (
	"context"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
)

// TestHarden: a server Harden set up refuses a document over the DB's
// limits before parsing, a request over MaxCost before it runs, and
// suggests no field name; a request within the limits runs.
func TestHarden(t *testing.T) {
	db, l := limitDB(t, veloxgql.Limits{MaxCost: 202, MaxDepth: 4})
	srv := handler.New(testSchema(handlerResolvers(db, nil)))
	srv.AddTransport(transport.POST{})
	gqlgenx.Harden(srv, db)
	ctx := context.Background()

	if got := post(t, ctx, srv, `{ people(first: 30) { totalCount __typename } }`); !strings.Contains(got, "COST_LIMIT_EXCEEDED") {
		t.Errorf("over MaxCost: %s", got)
	}
	if got := post(t, ctx, srv, `{ a { b { c { d { e } } } } }`); !strings.Contains(got, "too large") {
		t.Errorf("over MaxDepth: %s", got)
	}
	if got := post(t, ctx, srv, `{ peopl }`); strings.Contains(got, "Did you mean") {
		t.Errorf("a suggestion: %s", got)
	}
	if stmts, _ := l.take(); len(stmts) != 0 {
		t.Errorf("refused requests ran %v", stmts)
	}
	if got, want := post(t, ctx, srv, `{ people(first: 30) { totalCount } }`), `{"data":{"people":{"totalCount":0}}}`; got != want {
		t.Errorf("within the limits: got %s, want %s", got, want)
	}
}
