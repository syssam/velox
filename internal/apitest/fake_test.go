package apitest

import (
	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/fakeconn"
)

// openFake returns a DB of dialect d that runs statements on e.
func openFake(d velox.Dialect, e *fakeconn.Exec, opts ...velox.Option) *velox.DB {
	return velox.OpenConn(d, velox.DriverConn(d, e), opts...)
}

// fakeConn returns a Conn that runs statements on e, for a PostgreSQL DB.
func fakeConn(e *fakeconn.Exec) velox.Conn { return velox.DriverConn(velox.Postgres, e) }
