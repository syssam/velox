package veloxgql_test

import (
	"context"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// TestService: Service puts the DB and DefaultLimits() in the context;
// ServiceWithLimits takes each zero field from DefaultLimits(), and panics
// on a negative one and on a nil DB.
func TestService(t *testing.T) {
	db := &velox.DB{}
	ctx := veloxgql.Service(context.Background(), db)
	if got, err := veloxgql.DBFromContext(ctx); got != db || err != nil {
		t.Errorf("DBFromContext(Service) = %p, %v, want %p", got, err, db)
	}
	if got := veloxgql.LimitsFromContext(ctx); got != veloxgql.DefaultLimits() {
		t.Errorf("LimitsFromContext(Service) = %+v, want the defaults", got)
	}
	got := veloxgql.LimitsFromContext(veloxgql.ServiceWithLimits(context.Background(), db, veloxgql.Limits{MaxPage: 500, Timeout: time.Second}))
	want := veloxgql.DefaultLimits()
	want.MaxPage, want.Timeout = 500, time.Second
	if got != want {
		t.Errorf("ServiceWithLimits(MaxPage 500, Timeout 1s) = %+v, want %+v", got, want)
	}
	for name, c := range map[string]struct {
		call func()
		want string
	}{
		"nil DB": {func() { veloxgql.Service(context.Background(), nil) }, "veloxgql.Service: nil DB"},
		"negative MaxList": {func() { veloxgql.ServiceWithLimits(context.Background(), db, veloxgql.Limits{MaxList: -1}) },
			"veloxgql.ServiceWithLimits: Limits.MaxList is -1, and a limit must not be negative (zero is the default)"},
		"negative Timeout": {func() { veloxgql.ServiceWithLimits(context.Background(), db, veloxgql.Limits{Timeout: -time.Second}) },
			"veloxgql.ServiceWithLimits: Limits.Timeout is -1s, and a limit must not be negative (zero is the default)"},
	} {
		func() {
			defer func() {
				if got := recover(); got != c.want {
					t.Errorf("%s: panicked with %v, want %q", name, got, c.want)
				}
			}()
			c.call()
		}()
	}
}
