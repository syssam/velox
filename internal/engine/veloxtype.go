package engine

import "github.com/syssam/velox/veloxtype"

// AssignOp and the rest are the plain data types, which package veloxtype documents.
type (
	Aggregate       = veloxtype.Aggregate
	AssignOp        = veloxtype.AssignOp
	Assignment      = veloxtype.Assignment
	Caller          = veloxtype.Caller
	ColumnDesc      = veloxtype.ColumnDesc
	ConstraintError = veloxtype.ConstraintError
	ConstraintKind  = veloxtype.ConstraintKind
	Deadlines       = veloxtype.Deadlines
	Dialect         = veloxtype.Dialect
	IndexInfo       = veloxtype.IndexInfo
	Lock            = veloxtype.Lock
	LockWait        = veloxtype.LockWait
	Op              = veloxtype.Op
	OrderTerm       = veloxtype.OrderTerm
	PolicyError     = veloxtype.PolicyError
	Result          = veloxtype.Result
	Route           = veloxtype.Route
	RowScope        = veloxtype.RowScope
	SchemaError     = veloxtype.SchemaError
	Tag             = veloxtype.Tag
	TxAround        = veloxtype.TxAround
	TxInfo          = veloxtype.TxInfo
	TxOutcome       = veloxtype.TxOutcome
	TxRetry         = veloxtype.TxRetry
	ValidationError = veloxtype.ValidationError
)

// Their constants.
const (
	AggregateSum = veloxtype.AggregateSum
	AggregateAvg = veloxtype.AggregateAvg
	AggregateMin = veloxtype.AggregateMin
	AggregateMax = veloxtype.AggregateMax

	AssignSet = veloxtype.AssignSet
	AssignAdd = veloxtype.AssignAdd
	AssignSub = veloxtype.AssignSub

	ConstraintUnique     = veloxtype.ConstraintUnique
	ConstraintForeignKey = veloxtype.ConstraintForeignKey
	ConstraintNotNull    = veloxtype.ConstraintNotNull
	ConstraintCheck      = veloxtype.ConstraintCheck

	Postgres = veloxtype.Postgres
	MySQL    = veloxtype.MySQL
	SQLite   = veloxtype.SQLite

	LockForUpdate = veloxtype.LockForUpdate
	LockForShare  = veloxtype.LockForShare

	LockSkipLocked = veloxtype.LockSkipLocked
	LockNoWait     = veloxtype.LockNoWait

	OpSelect   = veloxtype.OpSelect
	OpInsert   = veloxtype.OpInsert
	OpUpdate   = veloxtype.OpUpdate
	OpDelete   = veloxtype.OpDelete
	OpRawQuery = veloxtype.OpRawQuery
	OpRawExec  = veloxtype.OpRawExec

	TxCommit   = veloxtype.TxCommit
	TxRollback = veloxtype.TxRollback
	TxError    = veloxtype.TxError
)

// ErrDenied is veloxtype.ErrDenied, which PolicyError.Is compares against.
var ErrDenied = veloxtype.ErrDenied
