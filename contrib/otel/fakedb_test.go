package veloxotel_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync"
	"time"
)

// fakeDB is a database/sql connector whose every query returns rows (with
// the columns cols, testmodel.Invoice's when nil) and whose every exec
// reports affected. errs are returned by the next calls, BEGIN included, one
// each; a nil entry lets its call succeed. With block, calls wait for their
// context to end.
type fakeDB struct {
	mu        sync.Mutex
	rows      [][]driver.Value
	cols      []string
	errs      []error
	affected  int64
	commitErr error
	block     bool
	log       []string
}

func (f *fakeDB) Connect(context.Context) (driver.Conn, error) { return &fakeConn{f}, nil }
func (f *fakeDB) Driver() driver.Driver                        { return fakeDriver{} }

// open returns f as a *sql.DB.
func (f *fakeDB) open() *sql.DB { return sql.OpenDB(f) }

func (f *fakeDB) call(ctx context.Context, what string) error {
	if f.block {
		<-ctx.Done()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, what)
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		return err
	}
	return nil
}

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("fake: open it with sql.OpenDB")
}

type fakeConn struct{ f *fakeDB }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("fake: no prepared statements")
}
func (c *fakeConn) Close() error { return nil }
func (c *fakeConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *fakeConn) BeginTx(ctx context.Context, _ driver.TxOptions) (driver.Tx, error) {
	if err := c.f.call(ctx, "BEGIN"); err != nil {
		return nil, err
	}
	return fakeTx{c.f}, nil
}

func (c *fakeConn) QueryContext(ctx context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	if err := c.f.call(ctx, q); err != nil {
		return nil, err
	}
	return &fakeRows{data: c.f.rows, cols: c.f.cols}, nil
}

func (c *fakeConn) ExecContext(ctx context.Context, q string, _ []driver.NamedValue) (driver.Result, error) {
	if err := c.f.call(ctx, q); err != nil {
		return nil, err
	}
	return driver.RowsAffected(c.f.affected), nil
}

type fakeTx struct{ f *fakeDB }

func (t fakeTx) Commit() error {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	t.f.log = append(t.f.log, "COMMIT")
	return t.f.commitErr
}

func (t fakeTx) Rollback() error {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	t.f.log = append(t.f.log, "ROLLBACK")
	return nil
}

type fakeRows struct {
	data [][]driver.Value
	cols []string
	i    int
}

// Columns names cols, or testmodel.Invoice's columns when cols is nil.
func (r *fakeRows) Columns() []string {
	if r.cols != nil {
		return r.cols
	}
	return []string{"id", "created_at", "updated_at", "org_id", "number", "total", "paid", "paid_at", "note", "data"}
}
func (r *fakeRows) Close() error { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.i >= len(r.data) {
		return io.EOF
	}
	copy(dest, r.data[r.i])
	r.i++
	return nil
}

var t0 = time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)

// invoiceRow is one testmodel.Invoice row.
func invoiceRow(id int64) []driver.Value {
	return []driver.Value{id, t0, t0, int64(1), "INV", 12.5, true, nil, nil, []byte("d")}
}
