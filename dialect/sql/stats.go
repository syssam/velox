// Package sql provides query statistics and slow query detection utilities.
package sql

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/syssam/velox/dialect"
)

// QueryStats holds query execution statistics.
type QueryStats struct {
	// TotalQueries is the total number of queries executed.
	TotalQueries atomic.Int64
	// TotalExecs is the total number of exec statements executed.
	TotalExecs atomic.Int64
	// TotalDuration is the total time spent executing queries.
	TotalDuration atomic.Int64 // nanoseconds
	// SlowQueries is the count of queries exceeding the slow threshold.
	SlowQueries atomic.Int64
	// Errors is the count of query errors.
	Errors atomic.Int64
}

// Stats returns a snapshot of the current statistics.
func (s *QueryStats) Stats() StatsSnapshot {
	return StatsSnapshot{
		TotalQueries:  s.TotalQueries.Load(),
		TotalExecs:    s.TotalExecs.Load(),
		TotalDuration: time.Duration(s.TotalDuration.Load()),
		SlowQueries:   s.SlowQueries.Load(),
		Errors:        s.Errors.Load(),
	}
}

// Reset resets all statistics to zero.
func (s *QueryStats) Reset() {
	s.TotalQueries.Store(0)
	s.TotalExecs.Store(0)
	s.TotalDuration.Store(0)
	s.SlowQueries.Store(0)
	s.Errors.Store(0)
}

// StatsSnapshot is a point-in-time snapshot of query statistics.
type StatsSnapshot struct {
	TotalQueries  int64
	TotalExecs    int64
	TotalDuration time.Duration
	SlowQueries   int64
	Errors        int64
}

// AvgQueryDuration returns the average query duration.
func (s StatsSnapshot) AvgQueryDuration() time.Duration {
	total := s.TotalQueries + s.TotalExecs
	if total == 0 {
		return 0
	}
	return s.TotalDuration / time.Duration(total)
}

// String returns a human-readable summary of the statistics.
func (s StatsSnapshot) String() string {
	return fmt.Sprintf(
		"queries=%d execs=%d duration=%s avg=%s slow=%d errors=%d",
		s.TotalQueries, s.TotalExecs, s.TotalDuration, s.AvgQueryDuration(),
		s.SlowQueries, s.Errors,
	)
}

// SlowQueryHook is a function called when a slow query is detected.
type SlowQueryHook func(ctx context.Context, query string, args []any, duration time.Duration)

// StatsDriver wraps a Driver with query statistics collection.
type StatsDriver struct {
	*Driver
	stats         *QueryStats
	slowThreshold time.Duration
	slowHook      SlowQueryHook
	mu            sync.RWMutex
}

// StatsOption configures the StatsDriver.
type StatsOption func(*StatsDriver)

// WithSlowThreshold sets the threshold for slow query detection.
// Queries taking longer than this duration will be counted as slow queries.
// Default is 100ms.
func WithSlowThreshold(d time.Duration) StatsOption {
	return func(s *StatsDriver) {
		s.slowThreshold = d
	}
}

// WithSlowQueryHook sets a callback function for slow queries.
// The hook is called whenever a query exceeds the slow threshold.
func WithSlowQueryHook(hook SlowQueryHook) StatsOption {
	return func(s *StatsDriver) {
		s.slowHook = hook
	}
}

// WithSlowQueryLog logs slow queries to the default logger.
// This is a convenience wrapper around WithSlowQueryHook.
func WithSlowQueryLog() StatsOption {
	return WithSlowQueryHook(func(_ context.Context, query string, args []any, duration time.Duration) {
		slog.Warn("slow query detected", "duration", duration, "query", query, "args", args)
	})
}

// NewStatsDriver wraps a Driver with statistics collection.
//
// Example:
//
//	drv, _ := sql.Open("postgres", dsn)
//	statsDriver := sql.NewStatsDriver(drv,
//	    sql.WithSlowThreshold(200*time.Millisecond),
//	    sql.WithSlowQueryLog(),
//	)
//	client := ent.NewClient(ent.Driver(statsDriver))
//
//	// Later, check statistics:
//	stats := statsDriver.QueryStats().Stats()
//	fmt.Println(stats)
func NewStatsDriver(drv *Driver, opts ...StatsOption) *StatsDriver {
	s := &StatsDriver{
		Driver:        drv,
		stats:         &QueryStats{},
		slowThreshold: 100 * time.Millisecond,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// QueryStats returns the underlying QueryStats for reading statistics.
func (d *StatsDriver) QueryStats() *QueryStats {
	return d.stats
}

// SlowThreshold returns the current slow query threshold.
func (d *StatsDriver) SlowThreshold() time.Duration {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.slowThreshold
}

// SetSlowThreshold updates the slow query threshold.
func (d *StatsDriver) SetSlowThreshold(threshold time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.slowThreshold = threshold
}

// Query executes a query and records statistics.
func (d *StatsDriver) Query(ctx context.Context, query string, args, v any) error {
	start := time.Now()
	err := d.Driver.Query(ctx, query, args, v)
	d.record(ctx, query, args, start, err, true)
	return err
}

// Exec executes a statement and records statistics.
func (d *StatsDriver) Exec(ctx context.Context, query string, args, v any) error {
	start := time.Now()
	err := d.Driver.Exec(ctx, query, args, v)
	d.record(ctx, query, args, start, err, false)
	return err
}

func (d *StatsDriver) record(ctx context.Context, query string, args any, start time.Time, err error, isQuery bool) {
	duration := time.Since(start)
	if isQuery {
		d.stats.TotalQueries.Add(1)
	} else {
		d.stats.TotalExecs.Add(1)
	}
	d.stats.TotalDuration.Add(int64(duration))

	if err != nil {
		d.stats.Errors.Add(1)
	}

	d.mu.RLock()
	threshold := d.slowThreshold
	hook := d.slowHook
	d.mu.RUnlock()

	if duration > threshold {
		d.stats.SlowQueries.Add(1)
		if hook != nil {
			argsSlice, _ := args.([]any)
			hook(ctx, query, argsSlice, duration)
		}
	}
}

// Tx starts a transaction that also records statistics.
func (d *StatsDriver) Tx(ctx context.Context) (dialect.Tx, error) {
	tx, err := d.Driver.Tx(ctx)
	if err != nil {
		return nil, err
	}
	return &StatsTx{Tx: tx, driver: d}, nil
}

// StatsTx wraps a transaction with statistics collection.
type StatsTx struct {
	dialect.Tx
	driver *StatsDriver
}

// Query executes a query within the transaction and records statistics.
func (tx *StatsTx) Query(ctx context.Context, query string, args, v any) error {
	start := time.Now()
	err := tx.Tx.Query(ctx, query, args, v)
	tx.driver.record(ctx, query, args, start, err, true)
	return err
}

// Exec executes a statement within the transaction and records statistics.
func (tx *StatsTx) Exec(ctx context.Context, query string, args, v any) error {
	start := time.Now()
	err := tx.Tx.Exec(ctx, query, args, v)
	tx.driver.record(ctx, query, args, start, err, false)
	return err
}

// DebugDriver wraps a Driver with debug logging.
type DebugDriver struct {
	*Driver
	log func(context.Context, ...any)
}

// DebugOption configures the DebugDriver.
type DebugOption func(*DebugDriver)

// DebugWithLog sets a custom log function.
func DebugWithLog(logFunc func(context.Context, ...any)) DebugOption {
	return func(d *DebugDriver) {
		d.log = logFunc
	}
}

// NewDebugDriver wraps a Driver with debug logging.
//
// Example:
//
//	drv, _ := sql.Open("postgres", dsn)
//	debugDriver := sql.NewDebugDriver(drv, sql.DebugWithLog(func(ctx context.Context, v ...any) {
//	    log.Println(v...)
//	}))
//	client := ent.NewClient(ent.Driver(debugDriver))
func NewDebugDriver(drv *Driver, opts ...DebugOption) *DebugDriver {
	d := &DebugDriver{
		Driver: drv,
		log: func(_ context.Context, v ...any) {
			slog.Info(fmt.Sprint(v...))
		},
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Query executes a query and logs it.
func (d *DebugDriver) Query(ctx context.Context, query string, args, v any) error {
	d.log(ctx, fmt.Sprintf("query: %s args: %v", query, args))
	return d.Driver.Query(ctx, query, args, v)
}

// Exec executes a statement and logs it.
func (d *DebugDriver) Exec(ctx context.Context, query string, args, v any) error {
	d.log(ctx, fmt.Sprintf("exec: %s args: %v", query, args))
	return d.Driver.Exec(ctx, query, args, v)
}

// Tx starts a transaction with debug logging.
func (d *DebugDriver) Tx(ctx context.Context) (dialect.Tx, error) {
	d.log(ctx, "begin transaction")
	tx, err := d.Driver.Tx(ctx)
	if err != nil {
		return nil, err
	}
	return &DebugTx{Tx: tx, log: d.log}, nil
}

// DebugTx wraps a transaction with debug logging.
type DebugTx struct {
	dialect.Tx
	log func(context.Context, ...any)
}

// Query executes a query within the transaction and logs it.
func (tx *DebugTx) Query(ctx context.Context, query string, args, v any) error {
	tx.log(ctx, fmt.Sprintf("tx query: %s args: %v", query, args))
	return tx.Tx.Query(ctx, query, args, v)
}

// Exec executes a statement within the transaction and logs it.
func (tx *DebugTx) Exec(ctx context.Context, query string, args, v any) error {
	tx.log(ctx, fmt.Sprintf("tx exec: %s args: %v", query, args))
	return tx.Tx.Exec(ctx, query, args, v)
}

// Commit commits the transaction and logs it.
func (tx *DebugTx) Commit() error {
	tx.log(context.Background(), "commit transaction")
	return tx.Tx.Commit()
}

// Rollback rolls back the transaction and logs it.
func (tx *DebugTx) Rollback() error {
	tx.log(context.Background(), "rollback transaction")
	return tx.Tx.Rollback()
}

// Ensure interfaces are implemented.
var (
	_ dialect.Driver = (*StatsDriver)(nil)
	_ dialect.Tx     = (*StatsTx)(nil)
	_ dialect.Driver = (*DebugDriver)(nil)
	_ dialect.Tx     = (*DebugTx)(nil)
)

// OpenWithStats opens a database connection with statistics collection enabled.
//
// Example:
//
//	drv, stats, err := sql.OpenWithStats("postgres", dsn,
//	    sql.WithSlowThreshold(100*time.Millisecond),
//	    sql.WithSlowQueryLog(),
//	)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	client := ent.NewClient(ent.Driver(drv))
//
//	// Monitor statistics periodically
//	go func() {
//	    for range time.Tick(time.Minute) {
//	        s := stats.Stats()
//	        log.Printf("Query stats: %s", s)
//	    }
//	}()
func OpenWithStats(driverName, source string, opts ...StatsOption) (*StatsDriver, *QueryStats, error) {
	db, err := sql.Open(driverName, source)
	if err != nil {
		return nil, nil, err
	}
	drv := NewDriver(driverName, Conn{db, driverName})
	statsDriver := NewStatsDriver(drv, opts...)
	stats := statsDriver.QueryStats()
	return statsDriver, stats, nil
}
