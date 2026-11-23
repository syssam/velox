package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
)

// fkDriver is a driver whose connections log what they run, without
// ExecerContext, so that execPragma prepares the statement.
type fkDriver struct {
	log     *[]string
	openErr error
	execErr error
}

func (d fkDriver) Open(string) (driver.Conn, error) {
	if d.openErr != nil {
		return nil, d.openErr
	}
	return fkConn(d), nil
}

type fkConn fkDriver

func (c fkConn) Prepare(q string) (driver.Stmt, error) {
	*c.log = append(*c.log, "prepare "+q)
	return fkStmt(c), nil
}
func (c fkConn) Close() error              { *c.log = append(*c.log, "close"); return nil }
func (c fkConn) Begin() (driver.Tx, error) { return nil, errors.New("no") }

type fkStmt fkConn

func (s fkStmt) Close() error  { return nil }
func (s fkStmt) NumInput() int { return 0 }
func (s fkStmt) Exec([]driver.Value) (driver.Result, error) {
	*s.log = append(*s.log, "exec")
	return driver.RowsAffected(0), s.execErr
}
func (s fkStmt) Query([]driver.Value) (driver.Rows, error) { return nil, errors.New("no") }

// TestFKConnector: each connection runs the pragma, through Prepare when the
// driver has no ExecerContext; a failed pragma closes the connection and
// fails Connect; a failed Open fails it as is.
func TestFKConnector(t *testing.T) {
	var log []string
	c := fkConnector{d: fkDriver{log: &log}, dsn: "x"}
	if c.Driver() != c.d {
		t.Error("Driver")
	}
	if _, err := c.Connect(context.Background()); err != nil || len(log) != 2 || log[0] != "prepare PRAGMA foreign_keys = ON" {
		t.Errorf("%v, log %q", err, log)
	}
	log = nil
	boom := errors.New("boom")
	c.d = fkDriver{log: &log, execErr: boom}
	if _, err := c.Connect(context.Background()); !errors.Is(err, boom) || log[len(log)-1] != "close" {
		t.Errorf("a failed pragma: %v, log %q", err, log)
	}
	c.d = fkDriver{log: &log, openErr: boom}
	if _, err := c.Connect(context.Background()); !errors.Is(err, boom) {
		t.Errorf("a failed Open: %v", err)
	}
	for dsn, want := range map[string]bool{
		"a.db": false, "a.db?_pragma=busy_timeout(1)": false, "a.db?_pragma=foreign_keys(0)": true,
		"a.db?_foreign_keys=1": true, "a.db?_FK=0": true, "foreign_keys.db": false,
	} {
		if namesForeignKeys("sqlite3", dsn) != want {
			t.Errorf("namesForeignKeys(sqlite3, %q) = %t", dsn, !want)
		}
	}
	// modernc.org/sqlite reads _pragma alone: enttest's _fk=1 turns nothing on.
	for dsn, want := range map[string]bool{
		"a.db?_pragma=foreign_keys(0)": true, "a.db?cache=shared&_pragma=foreign_keys(1)": true,
		"file:ent?mode=memory&cache=shared&_fk=1": false, "a.db?_foreign_keys=1": false,
		"a.db?_pragma=busy_timeout(1)": false,
	} {
		if namesForeignKeys("sqlite", dsn) != want {
			t.Errorf("namesForeignKeys(sqlite, %q) = %t", dsn, !want)
		}
	}
}
