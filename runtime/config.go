package runtime

import (
	"context"

	"github.com/syssam/velox/dialect"
)

// Config holds the database driver configuration.
// Defined in runtime/ so that entity sub-packages can import it directly
// without creating circular dependencies with the root generated package.
//
// HookStore and InterStore carry pointers to the generated entity.HookStore
// and entity.InterceptorStore structs as `any`. Each entity client constructor
// type-asserts these once; after that, all hook/interceptor access is a direct
// struct field read — zero callbacks, zero switch statements, zero indirection.
type Config struct {
	// Driver is the database driver used for executing queries.
	Driver dialect.Driver
	// Debug enables verbose logging of all queries.
	Debug bool
	// Log is the logger function for debug output.
	Log func(...any)
	// HookStore is a pointer to the generated entity.HookStore struct.
	// Entity client constructors type-assert this once to *entity.HookStore.
	HookStore any
	// InterStore is a pointer to the generated entity.InterceptorStore struct.
	// Entity client constructors type-assert this once to *entity.InterceptorStore.
	InterStore any
	// SchemaConfig carries the generated internal.SchemaConfig value set by
	// the client's AlternateSchema option (feature sql/schemaconfig): the
	// zero SchemaConfig when the option was not given, nil when the feature
	// is disabled. Every
	// generated builder reads it once in its constructor through the
	// generated internal.SchemaConfigFromRuntime and qualifies each table it
	// renders with the configured schema.
	SchemaConfig any
}

// ConfigProvider is anything an entity client can be built from: the
// generated root Client and Tx both implement it. Application packages take
// one in place of the root *Client or *Tx and build the entity clients they
// use from it:
//
//	func NewService(db runtime.ConfigProvider) *Service {
//		return &Service{users: userclient.From(db)}
//	}
//
// Why not take the root *Client: it has one typed field per entity, so a
// package that names it — in a signature or only in a function body — makes
// the compiler load every entity client and every builder they reach. On a
// 453-entity schema that was 2.4 CPU-seconds and 1.0 GB to compile a six-line
// package, against 0.23 s and 98 MB for one that builds a single entity
// client from a Config; a real 371-line service went from 2.9 s and 870 MB
// to 0.8 s and 254 MB. Functions and constants of the generated root package
// (IsNotFound, the Type* names) do not have this cost; its Client and Tx
// types do.
type ConfigProvider interface {
	RuntimeConfig() Config
}

// TxRunner is a ConfigProvider that can also run a function in a
// transaction; the generated root Client implements it. fn receives the
// transaction as a ConfigProvider, so entity clients built from it run
// inside the transaction:
//
//	err := db.RunTx(ctx, func(tx runtime.ConfigProvider) error {
//		return userclient.From(tx).Create().SetName("a").Exec(ctx)
//	})
//
// The transaction is committed when fn returns nil and rolled back when it
// returns an error or panics.
//
// StartTx is for code that ends the transaction itself; the caller must
// Commit or Rollback the Transaction it returns:
//
//	tx, err := db.StartTx(ctx)
//	if err != nil {
//		return err
//	}
//	committed := false
//	defer func() {
//		if !committed {
//			_ = tx.Rollback()
//		}
//	}()
//	...
//	if err := tx.Commit(); err != nil {
//		return err
//	}
//	committed = true
//
// Do not defer an unconditional Rollback: after a Commit it fails at the
// driver, but rollback hooks registered with OnRollback run first.
type TxRunner interface {
	ConfigProvider
	RunTx(ctx context.Context, fn func(tx ConfigProvider) error) error
	StartTx(ctx context.Context) (Transaction, error)
}

// Transaction is an open transaction: a ConfigProvider whose entity clients
// run inside it, plus the two ways to end it. The generated Tx implements it.
type Transaction interface {
	ConfigProvider
	Commit() error
	Rollback() error
}
