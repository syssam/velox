package runtime

import (
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
}
