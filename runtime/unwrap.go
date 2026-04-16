package runtime

import "github.com/syssam/velox/dialect"

// TxDriverUnwrapper is implemented by transactional drivers (the generated
// *txDriver). Entity Unwrap() methods use it to swap the entity's driver from
// the transactional one back to the underlying base driver after Tx.Commit or
// Tx.Rollback.
//
// This matches Ent's Unwrap() contract: after a transaction ends, reads that
// reach the database via an entity returned from the transaction must go
// through the base driver — the tx-scoped *sql.Tx is done and any Exec/Query
// on it returns sql.ErrTxDone.
//
// Defined in runtime/ so entity sub-packages (which must not import the root
// generated package) can perform the type assertion.
type TxDriverUnwrapper interface {
	BaseDriver() dialect.Driver
}
