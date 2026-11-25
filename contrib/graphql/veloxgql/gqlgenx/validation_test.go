package gqlgenx_test

import (
	"context"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
)

// TestQuietValidation: under QuietValidation and SetDisableSuggestion a
// server returns MaxErrors of validation's errors, says there are more, and
// suggests nothing; each error comes once.
func TestQuietValidation(t *testing.T) {
	db, _ := handlerDB(t, veloxgql.WithLimits(veloxgql.Limits{MaxErrors: 3}))
	srv := serve(handlerResolvers(db, nil))
	srv.SetDisableSuggestion(true)
	srv.SetValidationRulesFn(gqlgenx.QuietValidation(db))
	do := func(q string) string { return post(t, context.Background(), srv, q) }
	got := do(`{ a b c d e f }`)
	if n := strings.Count(got, `"message"`); n != 4 || !strings.Contains(got, "over 3 validation errors") || !strings.Contains(got, `"code":"GRAPHQL_VALIDATION_FAILED"`) {
		t.Errorf("6 unknown fields: %d errors: %s", n, got)
	}
	if got := do(`{ a b c }`); strings.Contains(got, "validation errors") || strings.Count(got, `"message"`) != 3 {
		t.Errorf("at the limit: %s", got)
	}
	// gqlgen adds its rules for no suggestions under SetDisableSuggestion,
	// unless they are there: each error comes once.
	for _, q := range []string{`{ a }`, `{ count { __typename } }`, `{ cont }`, `{ count(x: 1) }`} {
		got := do(q)
		if strings.Count(got, `"message"`) != 1 || strings.Contains(got, "Did you mean") {
			t.Errorf("%s: %s", q, got)
		}
	}
	// gqlgen's own rules suggest: the test's schema gives them a name to.
	plain := serve(handlerResolvers(db, nil))
	if got := post(t, context.Background(), plain, `{ cont }`); !strings.Contains(got, "Did you mean") {
		t.Errorf("gqlgen's rules: %s", got)
	}
	// A nil DB has the default MaxErrors.
	if gqlgenx.QuietValidation(nil)() == nil {
		t.Error("QuietValidation(nil) gave no rules")
	}
}
