package engine

import "github.com/syssam/velox2/dbtype"

// The plain data types, which package dbtype documents.
type (
	Aggregate       = dbtype.Aggregate
	AssignOp        = dbtype.AssignOp
	Assignment      = dbtype.Assignment
	Caller          = dbtype.Caller
	ColumnDesc      = dbtype.ColumnDesc
	ConstraintError = dbtype.ConstraintError
	ConstraintKind  = dbtype.ConstraintKind
	Deadlines       = dbtype.Deadlines
	Dialect         = dbtype.Dialect
	IndexInfo       = dbtype.IndexInfo
	Lock            = dbtype.Lock
	LockWait        = dbtype.LockWait
	Op              = dbtype.Op
	OrderTerm       = dbtype.OrderTerm
	PolicyError     = dbtype.PolicyError
	Result          = dbtype.Result
	Route           = dbtype.Route
	RowScope        = dbtype.RowScope
	SchemaError     = dbtype.SchemaError
	Tag             = dbtype.Tag
	TxAround        = dbtype.TxAround
	TxInfo          = dbtype.TxInfo
	TxOutcome       = dbtype.TxOutcome
	TxRetry         = dbtype.TxRetry
	ValidationError = dbtype.ValidationError
)

// Their constants.
const (
	AggregateSum = dbtype.AggregateSum
	AggregateAvg = dbtype.AggregateAvg
	AggregateMin = dbtype.AggregateMin
	AggregateMax = dbtype.AggregateMax

	AssignSet = dbtype.AssignSet
	AssignAdd = dbtype.AssignAdd
	AssignSub = dbtype.AssignSub

	ConstraintUnique     = dbtype.ConstraintUnique
	ConstraintForeignKey = dbtype.ConstraintForeignKey
	ConstraintNotNull    = dbtype.ConstraintNotNull
	ConstraintCheck      = dbtype.ConstraintCheck

	Postgres = dbtype.Postgres
	MySQL    = dbtype.MySQL
	SQLite   = dbtype.SQLite

	LockForUpdate = dbtype.LockForUpdate
	LockForShare  = dbtype.LockForShare

	LockSkipLocked = dbtype.LockSkipLocked
	LockNoWait     = dbtype.LockNoWait

	OpSelect   = dbtype.OpSelect
	OpInsert   = dbtype.OpInsert
	OpUpdate   = dbtype.OpUpdate
	OpDelete   = dbtype.OpDelete
	OpRawQuery = dbtype.OpRawQuery
	OpRawExec  = dbtype.OpRawExec

	TxCommit   = dbtype.TxCommit
	TxRollback = dbtype.TxRollback
	TxError    = dbtype.TxError
)

// ErrDenied is dbtype.ErrDenied, which PolicyError.Is compares against.
var ErrDenied = dbtype.ErrDenied
