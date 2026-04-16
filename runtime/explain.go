package runtime

// QueryPlan describes a query's execution plan without executing it.
// Returned by generated Explain() methods for debugging and logging.
type QueryPlan struct {
	SQL          string
	Args         []any
	Edges        []EdgePlan
	Interceptors []string
}

// EdgePlan describes a planned eager-load sub-query.
type EdgePlan struct {
	Name string
	SQL  string
	Args []any
}
