package dialect

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// =============================================================================
// Capability Tests
// =============================================================================

func TestCapabilities_Has(t *testing.T) {
	t.Parallel()
	pg := GetCapabilities(Postgres)

	if !pg.Has(CapReturning) {
		t.Error("Postgres should support RETURNING")
	}
	if !pg.Has(CapReturning, CapUpsert) {
		t.Error("Postgres should support both RETURNING and Upsert")
	}
	if pg.Has(CapLastInsertID) {
		t.Error("Postgres should NOT support LastInsertID")
	}
}

func TestCapabilities_HasAny(t *testing.T) {
	t.Parallel()
	my := GetCapabilities(MySQL)

	if !my.HasAny(CapReturning, CapUpsert) {
		t.Error("MySQL should support at least Upsert")
	}
	if my.HasAny(CapReturning, CapArrayType) {
		t.Error("MySQL should NOT support RETURNING or ArrayType")
	}
}

func TestGetCapabilities_Unknown(t *testing.T) {
	t.Parallel()
	caps := GetCapabilities("cockroach")
	if caps.Has(CapReturning) {
		t.Error("unknown dialect should have no capabilities")
	}
	if caps.HasAny(CapUpsert, CapForUpdate) {
		t.Error("unknown dialect should have no capabilities")
	}
}

func TestGetCapabilities_Postgres(t *testing.T) {
	t.Parallel()
	pg := GetCapabilities(Postgres)
	mustHave := []Capability{
		CapReturning, CapUpsert, CapJSONOperators,
		CapForUpdate, CapForShare, CapForNoKeyUpdate, CapForKeyShare,
		CapSchemas, CapEnumType, CapArrayType,
		CapCTE, CapWindowFunctions,
	}
	for _, c := range mustHave {
		if !pg.Has(c) {
			t.Errorf("Postgres missing capability %d", c)
		}
	}
	mustNotHave := []Capability{CapLastInsertID}
	for _, c := range mustNotHave {
		if pg.Has(c) {
			t.Errorf("Postgres should not have capability %d", c)
		}
	}
}

func TestGetCapabilities_MySQL(t *testing.T) {
	t.Parallel()
	my := GetCapabilities(MySQL)
	mustHave := []Capability{
		CapUpsert, CapJSONOperators, CapForUpdate, CapForShare,
		CapSchemas, CapEnumType, CapCTE, CapWindowFunctions, CapLastInsertID,
	}
	for _, c := range mustHave {
		if !my.Has(c) {
			t.Errorf("MySQL missing capability %d", c)
		}
	}
	mustNotHave := []Capability{CapReturning, CapArrayType, CapForNoKeyUpdate, CapForKeyShare}
	for _, c := range mustNotHave {
		if my.Has(c) {
			t.Errorf("MySQL should not have capability %d", c)
		}
	}
}

func TestGetCapabilities_SQLite(t *testing.T) {
	t.Parallel()
	sl := GetCapabilities(SQLite)
	mustHave := []Capability{
		CapReturning, CapUpsert, CapJSONOperators,
		CapCTE, CapWindowFunctions, CapLastInsertID,
	}
	for _, c := range mustHave {
		if !sl.Has(c) {
			t.Errorf("SQLite missing capability %d", c)
		}
	}
	mustNotHave := []Capability{
		CapForUpdate, CapForShare, CapForNoKeyUpdate, CapForKeyShare,
		CapSchemas, CapEnumType, CapArrayType,
	}
	for _, c := range mustNotHave {
		if sl.Has(c) {
			t.Errorf("SQLite should not have capability %d", c)
		}
	}
}

func TestCapabilities_HasEmpty(t *testing.T) {
	t.Parallel()
	caps := Capabilities{}
	// Has with no arguments should return true (vacuous truth).
	if !caps.Has() {
		t.Error("Has() with no args should be true")
	}
}

func TestCapabilities_HasAnyEmpty(t *testing.T) {
	t.Parallel()
	caps := GetCapabilities(Postgres)
	// HasAny with no arguments should return false (no match possible).
	if caps.HasAny() {
		t.Error("HasAny() with no args should be false")
	}
}

// =============================================================================
// NopTx Tests
// =============================================================================

func TestNopTx(t *testing.T) {
	t.Parallel()
	d := &mockDriver{dialect: "sqlite"}
	tx := NopTx(d)

	if err := tx.Commit(); err != nil {
		t.Errorf("NopTx.Commit() = %v, want nil", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Errorf("NopTx.Rollback() = %v, want nil", err)
	}

	// Exec/Query delegate to the underlying driver.
	ctx := context.Background()
	if err := tx.Exec(ctx, "INSERT", nil, nil); err != nil {
		t.Errorf("NopTx.Exec() = %v, want nil", err)
	}
	if err := tx.Query(ctx, "SELECT", nil, nil); err != nil {
		t.Errorf("NopTx.Query() = %v, want nil", err)
	}
}

// =============================================================================
// DebugDriver Tests
// =============================================================================

func TestDebug_DefaultLogger(t *testing.T) {
	t.Parallel()
	d := &mockDriver{dialect: "sqlite"}
	dd := Debug(d)
	// Should not panic with default logger.
	ctx := context.Background()
	_ = dd.Exec(ctx, "INSERT INTO t VALUES (?)", []any{1}, nil)
	_ = dd.Query(ctx, "SELECT 1", nil, nil)
}

func TestDebug_CustomLogger(t *testing.T) {
	t.Parallel()
	var logs []string
	d := &mockDriver{dialect: "postgres"}
	dd := Debug(d, func(v ...any) {
		logs = append(logs, fmt.Sprint(v...))
	})

	ctx := context.Background()
	_ = dd.Exec(ctx, "INSERT INTO users", []any{"alice"}, nil)
	_ = dd.Query(ctx, "SELECT * FROM users", nil, nil)

	if len(logs) != 2 {
		t.Fatalf("expected 2 log entries, got %d", len(logs))
	}
	if !strings.Contains(logs[0], "driver.Exec") {
		t.Errorf("log[0] = %q, want contains driver.Exec", logs[0])
	}
	if !strings.Contains(logs[1], "driver.Query") {
		t.Errorf("log[1] = %q, want contains driver.Query", logs[1])
	}
}

func TestDebugWithContext(t *testing.T) {
	t.Parallel()
	var ctxSeen context.Context
	var logs []string
	d := &mockDriver{dialect: "mysql"}
	dd := DebugWithContext(d, func(ctx context.Context, v ...any) {
		ctxSeen = ctx
		logs = append(logs, fmt.Sprint(v...))
	})

	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "test-value")
	_ = dd.Exec(ctx, "UPDATE t SET x=1", nil, nil)

	if len(logs) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(logs))
	}
	if ctxSeen.Value(ctxKey{}) != "test-value" {
		t.Error("context not propagated to logger")
	}
}

func TestDebugDriver_Exec_Error(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("exec fail")
	d := &mockDriver{execErr: wantErr}
	dd := Debug(d, func(...any) {})

	err := dd.Exec(context.Background(), "INSERT", nil, nil)
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestDebugDriver_Query_Error(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("query fail")
	d := &mockDriver{queryErr: wantErr}
	dd := Debug(d, func(...any) {})

	err := dd.Query(context.Background(), "SELECT", nil, nil)
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestDebugDriver_Tx(t *testing.T) {
	t.Parallel()
	var logs []string
	d := &mockDriver{dialect: "postgres", tx: &mockTx{}}
	dd := Debug(d, func(v ...any) {
		logs = append(logs, fmt.Sprint(v...))
	})

	tx, err := dd.Tx(context.Background())
	if err != nil {
		t.Fatalf("Tx() error: %v", err)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "started") {
		t.Errorf("expected Tx start log, got %v", logs)
	}

	// DebugTx should log Exec, Query, Commit.
	_ = tx.Exec(context.Background(), "INSERT", nil, nil)
	_ = tx.Query(context.Background(), "SELECT", nil, nil)
	_ = tx.Commit()

	if len(logs) != 4 {
		t.Fatalf("expected 4 log entries, got %d: %v", len(logs), logs)
	}
	if !strings.Contains(logs[1], "Tx(") || !strings.Contains(logs[1], "Exec") {
		t.Errorf("log[1] = %q, want Tx Exec log", logs[1])
	}
	if !strings.Contains(logs[2], "Tx(") || !strings.Contains(logs[2], "Query") {
		t.Errorf("log[2] = %q, want Tx Query log", logs[2])
	}
	if !strings.Contains(logs[3], "committed") {
		t.Errorf("log[3] = %q, want committed", logs[3])
	}
}

func TestDebugDriver_Tx_Error(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("tx fail")
	d := &mockDriver{txErr: wantErr}
	dd := Debug(d, func(...any) {})

	_, err := dd.Tx(context.Background())
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestDebugTx_Rollback(t *testing.T) {
	t.Parallel()
	var logs []string
	d := &mockDriver{dialect: "sqlite", tx: &mockTx{}}
	dd := Debug(d, func(v ...any) {
		logs = append(logs, fmt.Sprint(v...))
	})

	tx, _ := dd.Tx(context.Background())
	_ = tx.Rollback()

	if len(logs) != 2 {
		t.Fatalf("expected 2 log entries, got %d", len(logs))
	}
	if !strings.Contains(logs[1], "rolled back") {
		t.Errorf("log[1] = %q, want rolled back", logs[1])
	}
}

func TestDebugDriver_ExecContext_Unsupported(t *testing.T) {
	t.Parallel()
	d := &mockDriver{dialect: "sqlite"}
	dd := Debug(d, func(...any) {}).(*DebugDriver)

	_, err := dd.ExecContext(context.Background(), "INSERT", 1)
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("expected not supported error, got %v", err)
	}
}

func TestDebugDriver_QueryContext_Unsupported(t *testing.T) {
	t.Parallel()
	d := &mockDriver{dialect: "sqlite"}
	dd := Debug(d, func(...any) {}).(*DebugDriver)

	_, err := dd.QueryContext(context.Background(), "SELECT", 1)
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("expected not supported error, got %v", err)
	}
}

func TestDebugDriver_BeginTx_Unsupported(t *testing.T) {
	t.Parallel()
	d := &mockDriver{dialect: "sqlite"}
	dd := Debug(d, func(...any) {}).(*DebugDriver)

	_, err := dd.BeginTx(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("expected not supported error, got %v", err)
	}
}

func TestDebugTx_ExecContext_Unsupported(t *testing.T) {
	t.Parallel()
	d := &mockDriver{dialect: "sqlite", tx: &mockTx{}}
	dd := Debug(d, func(...any) {}).(*DebugDriver)

	tx, _ := dd.Tx(context.Background())
	dtx := tx.(*DebugTx)
	_, err := dtx.ExecContext(context.Background(), "INSERT")
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("expected not supported error, got %v", err)
	}
}

func TestDebugTx_QueryContext_Unsupported(t *testing.T) {
	t.Parallel()
	d := &mockDriver{dialect: "sqlite", tx: &mockTx{}}
	dd := Debug(d, func(...any) {}).(*DebugDriver)

	tx, _ := dd.Tx(context.Background())
	dtx := tx.(*DebugTx)
	_, err := dtx.QueryContext(context.Background(), "SELECT")
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("expected not supported error, got %v", err)
	}
}

func TestDebugDriver_Dialect(t *testing.T) {
	t.Parallel()
	d := &mockDriver{dialect: "postgres"}
	dd := Debug(d, func(...any) {})
	if got := dd.Dialect(); got != "postgres" {
		t.Errorf("Dialect() = %q, want postgres", got)
	}
}

func TestDebugDriver_Close(t *testing.T) {
	t.Parallel()
	d := &mockDriver{dialect: "sqlite"}
	dd := Debug(d, func(...any) {})
	if err := dd.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

// =============================================================================
// Mock implementations
// =============================================================================

type mockDriver struct {
	dialect  string
	execErr  error
	queryErr error
	txErr    error
	tx       Tx
}

func (d *mockDriver) Exec(_ context.Context, _ string, _, _ any) error  { return d.execErr }
func (d *mockDriver) Query(_ context.Context, _ string, _, _ any) error { return d.queryErr }
func (d *mockDriver) Dialect() string                                   { return d.dialect }
func (d *mockDriver) Close() error                                      { return nil }
func (d *mockDriver) Tx(_ context.Context) (Tx, error) {
	if d.txErr != nil {
		return nil, d.txErr
	}
	return d.tx, nil
}

type mockTx struct{}

func (t *mockTx) Exec(_ context.Context, _ string, _, _ any) error  { return nil }
func (t *mockTx) Query(_ context.Context, _ string, _, _ any) error { return nil }
func (t *mockTx) Commit() error                                     { return nil }
func (t *mockTx) Rollback() error                                   { return nil }

// Verify mockDriver satisfies the Driver interface.
var _ Driver = (*mockDriver)(nil)

// Verify mockTx satisfies the Tx interface.
var _ Tx = (*mockTx)(nil)

// Verify mockTx satisfies driver.Tx (embedded in Tx).
var _ driver.Tx = (*mockTx)(nil)
