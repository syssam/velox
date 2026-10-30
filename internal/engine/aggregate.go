package engine

import (
	"fmt"
)

// aggregateSQL names each function in the SQL.
var aggregateSQL = [...]string{AggregateSum: "SUM", AggregateAvg: "AVG", AggregateMin: "MIN", AggregateMax: "MAX"}

// ErrAggregateChanged is the refusal of a Rewrite hook that changed the
// statement's Aggregate: the scan depends on it.
func ErrAggregateChanged(entity string) error {
	return fmt.Errorf("velox: %s: a Rewrite hook changed the statement's Aggregate", entity)
}
