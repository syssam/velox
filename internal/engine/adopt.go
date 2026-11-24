package engine

import (
	"context"
	"database/sql"
	"errors"

	"github.com/syssam/velox/internal/conn"
)

// UseTx returns a DB whose statements run on tx, a transaction the caller began
// on a *sql.DB of db's dialect, as Tx's DB runs on its own. The caller owns tx:
// the DB never begins, commits or rolls back, and it works until the caller
// ends tx, after which database/sql fails its statements. A Tx on it is a
// savepoint of tx. It retries nothing, takes no Tx option or deadline, and runs
// no TxAround hook, which wraps a Tx call that UseTx is not. As it never
// sees tx end, a recording's Changes go to its Sink, a Recorder's too, and
// none is dropped when tx rolls back (txFrame.adopted).
//
// It is a function, not a method, as Pool is: a method of DB mentioning *sql.Tx
// makes every package that uses a Query larger to compile.
//
// It refuses a nil tx, a db that is in a transaction, one that is sharded, and
// one whose connection does not run on a *sql.Tx, veloxpgx's.
func UseTx(db *DB, tx *sql.Tx) (*DB, error) {
	if tx == nil {
		return nil, errors.New("velox: UseTx: tx is nil")
	}
	if db.InTx {
		return nil, errors.New("velox: UseTx: db is in a transaction already; UseTx takes one the caller began, and a nested Tx is a savepoint")
	}
	primary := db.Ex // a transaction runs on the primary, as Tx's does
	if db.Rt != nil {
		if db.Rt.One == nil {
			return nil, errors.New("velox: UseTx: a sharded DB runs a transaction on one shard's primary, which it chooses itself; UseTx takes a *sql.Tx of a DB that is not sharded")
		}
		primary = db.Rt.One.Primary
	}
	a, ok := primary.(conn.Adopter)
	if !ok {
		return nil, errNoSQLTx
	}
	ex, ok := a.Adopt(tx)
	if !ok {
		return nil, errNoSQLTx
	}
	f := newTxFrame(context.Background(), DB{D: db.D, Ex: ex, Rt: db.Rt, Cfg: db.Cfg, InTx: true, bypass: db.bypass, rec: db.rec}, nil)
	f.adopted = true
	adoptedSource(&f.db)
	return &f.db, nil
}

var errNoSQLTx = errors.New("velox: UseTx: this DB's connection does not run on a *sql.Tx; veloxpgx's runs on pgx's own transactions")
