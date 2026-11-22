package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// StatementCounter counts the statements that run with a context
// CountStatements returned, on a DB opened with WithStatementCounting, by
// shape: the statement's text without its comment and arguments. It is
// safe for concurrent use.
type StatementCounter struct {
	mu     sync.Mutex
	total  int
	shapes []StatementShape
	index  map[string]int // into shapes, by the shape's key
}

// StatementShape is one shape of statement a StatementCounter saw: its
// text the first time, the entity it is about ("" for raw SQL), its op, and
// how many times it ran.
type StatementShape struct {
	SQL    string
	Entity string
	Op     Op
	Count  int
}

// StatementBudget is what a context may run, checked by
// StatementCounter.Check. A zero field sets no limit.
type StatementBudget struct {
	Statements int // the most statements, of every kind
	Repeats    int // the most times one shape of read may run
}

// BudgetError is StatementCounter.Check's error: the statements counted,
// the budget, and the reads of a shape that ran more than Repeats times.
type BudgetError struct {
	Total   int
	Budget  StatementBudget
	Repeats []StatementShape
}

// Error names what went over: the count of statements, and each read
// repeated, which is how an N+1 shows.
func (e *BudgetError) Error() string {
	var b strings.Builder
	b.WriteString("velox: over the statement budget:")
	if e.Budget.Statements > 0 && e.Total > e.Budget.Statements {
		fmt.Fprintf(&b, " %d statements ran, of %d allowed;", e.Total, e.Budget.Statements)
	}
	for _, s := range e.Repeats {
		fmt.Fprintf(&b, "\n  ran %d times, of %d allowed: %s", s.Count, e.Budget.Repeats, s.SQL)
	}
	if len(e.Repeats) > 0 {
		b.WriteString("\none read run once per row of another is an N+1: load the relation with With, or read the keys with one IN")
	}
	return b.String()
}

// counterKey is the context key of a StatementCounter.
type counterKey struct{}

// CountStatements returns ctx with a new StatementCounter, which counts the
// statements that run with the returned context, or one derived from it.
// A counter already on ctx is replaced for the new context only.
func CountStatements(ctx context.Context) (context.Context, *StatementCounter) {
	c := &StatementCounter{}
	return context.WithValue(ctx, counterKey{}, c), c
}

// WithStatementCounting adds the Around hook that counts each statement in
// the StatementCounter of its context. A DB without it counts nothing and
// pays nothing; with it, a statement whose context has no counter pays one
// context lookup.
func WithStatementCounting() Option { return WithAround(countAround) }

// countAround counts st in its context's counter, then runs it.
func countAround(ctx context.Context, st *Statement, next func(context.Context) error) error {
	if c, ok := ctx.Value(counterKey{}).(*StatementCounter); ok {
		c.add(st)
	}
	return next(ctx)
}

// add counts st.
func (c *StatementCounter) add(st *Statement) {
	key := st.Fingerprint
	if key == "" {
		key = st.SQL
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.total++
	if i, ok := c.index[key]; ok {
		c.shapes[i].Count++
		return
	}
	if c.index == nil {
		c.index = map[string]int{}
	}
	entity := ""
	if st.Entity != nil {
		entity = st.Entity.Name
	}
	c.index[key] = len(c.shapes)
	c.shapes = append(c.shapes, StatementShape{SQL: st.SQL, Entity: entity, Op: st.Op, Count: 1})
}

// Total is the number of statements counted.
func (c *StatementCounter) Total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

// Shapes returns each shape counted, in the order each first ran.
func (c *StatementCounter) Shapes() []StatementShape {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]StatementShape(nil), c.shapes...)
}

// Check returns a *BudgetError when the statements counted so far are over
// b: more than b.Statements in all, or a read, a SELECT or a raw query,
// of one shape run more than b.Repeats times. Otherwise it returns nil.
func (c *StatementCounter) Check(b StatementBudget) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := &BudgetError{Total: c.total, Budget: b}
	if b.Repeats > 0 {
		for _, s := range c.shapes {
			if s.Op.Reads() && s.Count > b.Repeats {
				e.Repeats = append(e.Repeats, s)
			}
		}
	}
	if len(e.Repeats) == 0 && (b.Statements <= 0 || c.total <= b.Statements) {
		return nil
	}
	return e
}
