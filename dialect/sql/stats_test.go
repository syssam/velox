package sql

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueryStats_Stats(t *testing.T) {
	t.Parallel()

	stats := &QueryStats{}
	stats.TotalQueries.Add(10)
	stats.TotalExecs.Add(5)
	stats.TotalDuration.Add(int64(150 * time.Millisecond))
	stats.SlowQueries.Add(2)
	stats.Errors.Add(1)

	snapshot := stats.Stats()
	assert.Equal(t, int64(10), snapshot.TotalQueries)
	assert.Equal(t, int64(5), snapshot.TotalExecs)
	assert.Equal(t, 150*time.Millisecond, snapshot.TotalDuration)
	assert.Equal(t, int64(2), snapshot.SlowQueries)
	assert.Equal(t, int64(1), snapshot.Errors)
}

func TestQueryStats_Reset(t *testing.T) {
	t.Parallel()

	stats := &QueryStats{}
	stats.TotalQueries.Add(10)
	stats.TotalExecs.Add(5)
	stats.TotalDuration.Add(100)
	stats.SlowQueries.Add(3)
	stats.Errors.Add(2)

	preReset := stats.Reset()

	// Verify pre-reset snapshot captures the values before clearing.
	assert.Equal(t, int64(10), preReset.TotalQueries)
	assert.Equal(t, int64(5), preReset.TotalExecs)
	assert.Equal(t, time.Duration(100), preReset.TotalDuration)
	assert.Equal(t, int64(3), preReset.SlowQueries)
	assert.Equal(t, int64(2), preReset.Errors)

	// Verify all counters are zeroed after reset.
	snapshot := stats.Stats()
	assert.Equal(t, int64(0), snapshot.TotalQueries)
	assert.Equal(t, int64(0), snapshot.TotalExecs)
	assert.Equal(t, time.Duration(0), snapshot.TotalDuration)
	assert.Equal(t, int64(0), snapshot.SlowQueries)
	assert.Equal(t, int64(0), snapshot.Errors)
}

func TestStatsSnapshot_AvgQueryDuration(t *testing.T) {
	t.Parallel()

	t.Run("with operations", func(t *testing.T) {
		t.Parallel()
		s := StatsSnapshot{
			TotalQueries:  8,
			TotalExecs:    2,
			TotalDuration: 100 * time.Millisecond,
		}
		// 100ms / 10 = 10ms
		assert.Equal(t, 10*time.Millisecond, s.AvgQueryDuration())
	})

	t.Run("zero operations", func(t *testing.T) {
		t.Parallel()
		s := StatsSnapshot{}
		assert.Equal(t, time.Duration(0), s.AvgQueryDuration())
	})

	t.Run("only queries", func(t *testing.T) {
		t.Parallel()
		s := StatsSnapshot{
			TotalQueries:  5,
			TotalDuration: 50 * time.Millisecond,
		}
		assert.Equal(t, 10*time.Millisecond, s.AvgQueryDuration())
	})

	t.Run("only execs", func(t *testing.T) {
		t.Parallel()
		s := StatsSnapshot{
			TotalExecs:    4,
			TotalDuration: 40 * time.Millisecond,
		}
		assert.Equal(t, 10*time.Millisecond, s.AvgQueryDuration())
	})
}

func TestStatsSnapshot_String(t *testing.T) {
	t.Parallel()

	s := StatsSnapshot{
		TotalQueries:  10,
		TotalExecs:    5,
		TotalDuration: 150 * time.Millisecond,
		SlowQueries:   2,
		Errors:        1,
	}
	str := s.String()
	assert.Contains(t, str, "queries=10")
	assert.Contains(t, str, "execs=5")
	assert.Contains(t, str, "slow=2")
	assert.Contains(t, str, "errors=1")
	assert.Contains(t, str, "avg=")
}

func TestStatsSnapshot_String_Zero(t *testing.T) {
	t.Parallel()
	s := StatsSnapshot{}
	str := s.String()
	assert.Contains(t, str, "queries=0")
	assert.Contains(t, str, "execs=0")
	assert.Contains(t, str, "slow=0")
	assert.Contains(t, str, "errors=0")
}

func TestNewStatsDriver(t *testing.T) {
	t.Parallel()

	// Create a nil-safe test for the constructor
	drv := &Driver{}
	sd := NewStatsDriver(drv)
	require.NotNil(t, sd)
	assert.Equal(t, 100*time.Millisecond, sd.SlowThreshold())
	assert.NotNil(t, sd.QueryStats())
}

func TestNewStatsDriver_WithOptions(t *testing.T) {
	t.Parallel()

	drv := &Driver{}
	sd := NewStatsDriver(drv,
		WithSlowThreshold(200*time.Millisecond),
		WithSlowQueryHook(func(_ context.Context, _ string, _ []any, _ time.Duration) {}),
	)
	require.NotNil(t, sd)
	assert.Equal(t, 200*time.Millisecond, sd.SlowThreshold())
}

func TestStatsDriver_SetSlowThreshold(t *testing.T) {
	t.Parallel()

	drv := &Driver{}
	sd := NewStatsDriver(drv)
	assert.Equal(t, 100*time.Millisecond, sd.SlowThreshold())

	sd.SetSlowThreshold(500 * time.Millisecond)
	assert.Equal(t, 500*time.Millisecond, sd.SlowThreshold())
}

func TestWithSlowQueryLog(t *testing.T) {
	t.Parallel()

	drv := &Driver{}
	// Just verify it doesn't panic
	sd := NewStatsDriver(drv, WithSlowQueryLog())
	require.NotNil(t, sd)
}

func TestNewLogDriver(t *testing.T) {
	t.Parallel()

	drv := &Driver{}
	dd := NewLogDriver(drv)
	require.NotNil(t, dd)
}

func TestNewLogDriver_WithFunc(t *testing.T) {
	t.Parallel()

	drv := &Driver{}
	dd := NewLogDriver(drv, LogWithFunc(func(_ context.Context, _ ...any) {}))
	require.NotNil(t, dd)
}
