package dialect

// Capability represents a feature that may or may not be supported by a
// database dialect. Use [Capabilities] to query what a specific dialect supports.
type Capability uint32

const (
	// CapReturning indicates support for INSERT ... RETURNING (Postgres, SQLite 3.35+).
	CapReturning Capability = 1 << iota
	// CapUpsert indicates support for INSERT ... ON CONFLICT / ON DUPLICATE KEY.
	CapUpsert
	// CapJSONOperators indicates support for native JSON column operators.
	CapJSONOperators
	// CapForUpdate indicates support for SELECT ... FOR UPDATE row-level locking.
	CapForUpdate
	// CapForShare indicates support for SELECT ... FOR SHARE.
	CapForShare
	// CapForNoKeyUpdate indicates support for FOR NO KEY UPDATE (Postgres only).
	CapForNoKeyUpdate
	// CapForKeyShare indicates support for FOR KEY SHARE (Postgres only).
	CapForKeyShare
	// CapSchemas indicates support for named schemas (e.g., SET search_path, USE schema).
	CapSchemas
	// CapEnumType indicates support for native ENUM column types.
	CapEnumType
	// CapArrayType indicates support for native array column types (Postgres).
	CapArrayType
	// CapCTE indicates support for Common Table Expressions (WITH ... AS).
	CapCTE
	// CapWindowFunctions indicates support for window functions (OVER, PARTITION BY).
	CapWindowFunctions
	// CapLastInsertID indicates support for LastInsertId() on sql.Result (MySQL, SQLite).
	CapLastInsertID
)

// Capabilities describes the feature set of a database dialect.
type Capabilities struct {
	flags Capability
}

// Has reports whether all given capabilities are supported.
func (c Capabilities) Has(caps ...Capability) bool {
	for _, cap := range caps {
		if c.flags&cap == 0 {
			return false
		}
	}
	return true
}

// HasAny reports whether at least one of the given capabilities is supported.
func (c Capabilities) HasAny(caps ...Capability) bool {
	for _, cap := range caps {
		if c.flags&cap != 0 {
			return true
		}
	}
	return false
}

// dialectCaps maps dialect names to their capability sets.
var dialectCaps = map[string]Capabilities{
	Postgres: {
		CapReturning | CapUpsert | CapJSONOperators |
			CapForUpdate | CapForShare | CapForNoKeyUpdate | CapForKeyShare |
			CapSchemas | CapEnumType | CapArrayType |
			CapCTE | CapWindowFunctions,
	},
	MySQL: {
		CapUpsert | CapJSONOperators |
			CapForUpdate | CapForShare |
			CapSchemas | CapEnumType |
			CapCTE | CapWindowFunctions |
			CapLastInsertID,
	},
	SQLite: {
		CapReturning | CapUpsert | CapJSONOperators |
			CapCTE | CapWindowFunctions |
			CapLastInsertID,
	},
}

// GetCapabilities returns the capability set for the named dialect.
// Unknown dialects return an empty Capabilities (nothing supported).
func GetCapabilities(name string) Capabilities {
	return dialectCaps[name]
}
