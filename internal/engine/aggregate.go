package engine

import (
	"fmt"
)

// Aggregate is the function an aggregate read computes over its one column:
// what Sum, Avg, Min and Max set.
type Aggregate uint8

// The aggregate functions.
const (
	AggregateSum Aggregate = iota + 1 // SUM
	AggregateAvg                      // AVG
	AggregateMin                      // MIN
	AggregateMax                      // MAX
)

// aggregateSQL names each function in the SQL.
var aggregateSQL = [...]string{AggregateSum: "SUM", AggregateAvg: "AVG", AggregateMin: "MIN", AggregateMax: "MAX"}

// ErrAggregateChanged is the refusal of a Rewrite hook that changed the
// statement's Aggregate: the scan depends on it.
func ErrAggregateChanged(entity string) error {
	return fmt.Errorf("velox: %s: a Rewrite hook changed the statement's Aggregate", entity)
}
