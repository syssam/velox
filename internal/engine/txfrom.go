package engine

import "context"

// TxFrom returns the DB of the transaction ctx is marked with, for db: the
// transaction's handle with db's policy bypass (or its absence), so that
// joining a transaction lends no caller another's WithoutPolicy. ok is false
// when ctx carries no transaction that has not ended, when the transaction
// is not of db (another Open, or a DB with other options), or when db is a
// recording handle and the transaction's is not, or the other way round.
// db already in that transaction is returned as it is.
func TxFrom(ctx context.Context, db *DB) (*DB, bool) {
	m, _ := ctx.Value(txKey{}).(*txFrame)
	switch {
	case m == nil || m.ended.Load():
		return nil, false
	case db.txRoot == m:
		return db, true
	case db.InTx || m.db.Cfg != db.Cfg || m.db.rec != db.rec:
		return nil, false
	}
	c := m.db
	c.owned = false
	c.bypass, c.misuse = db.bypass, db.misuse
	return &c, true
}
