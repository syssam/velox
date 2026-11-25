package gqlgenx

import (
	"github.com/99designs/gqlgen/graphql/handler"

	"github.com/syssam/velox"
)

// Harden applies to srv every piece of velox's hardening for a server open
// to clients, under db's limits (veloxgql.LimitsOf): RequestLimit,
// CostLimit and ResponseLimit, ErrorPresenter with slog,
// SetDisableSuggestion and QuietValidation. It is opt-in, a shorthand for
// those six calls, which a server may make itself instead:
//
//	srv := handler.New(es)
//	srv.AddTransport(transport.POST{})
//	gqlgenx.Harden(srv, db)
//	http.Handle("/query", gqlgenx.MaxBodyBytes(1<<20)(srv))
//
// Call it before extension.AutomaticPersistedQuery's Use, so that the cache
// never stores a refused query; Transactioner may come before or after it. A
// later SetErrorPresenter replaces its presenter. It leaves out
// MaxBodyBytes, which wraps the HTTP handler, and Transactioner, which
// changes what a mutation does.
func Harden(srv *handler.Server, db *velox.DB) {
	srv.Use(RequestLimit(db))
	srv.Use(CostLimit(db))
	srv.Use(ResponseLimit(db))
	srv.SetErrorPresenter(ErrorPresenter(nil))
	srv.SetDisableSuggestion(true)
	srv.SetValidationRulesFn(QuietValidation(db))
}
