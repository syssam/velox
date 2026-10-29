package migrate

import (
	"testing"

	"github.com/syssam/velox2"
)

// TestSamePredicateKnownLimits pins the documented exception to Drift's
// comparison of predicates: a cast whose meaning depends on the column's
// type, which samePredicate does not know. On PostgreSQL the first two
// pairs read apart, yet match; an interval literal keeps its cast, so the
// third pair does not.
func TestSamePredicateKnownLimits(t *testing.T) {
	for _, c := range []struct {
		a, b         string
		same, parsed bool
	}{
		{`d >= '2020-01-01 10:00'::timestamp`, `d >= '2020-01-01 10:00'`, true, true},
		{`qty::text = '05'`, `qty = '05'`, true, true},
		{`d < now() - '1 day'::interval`, `d < now() - '1 day'`, false, true},
	} {
		same, parsed := samePredicate(velox.Postgres, c.a, c.b)
		if same != c.same || parsed != c.parsed {
			t.Errorf("samePredicate(%q, %q) = %v, %v; want %v, %v", c.a, c.b, same, parsed, c.same, c.parsed)
		}
	}
}
