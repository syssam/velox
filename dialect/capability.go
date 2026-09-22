package dialect

// Capability represents a feature that may or may not be supported by a
// database dialect. Use [Capabilities] to query what a specific dialect supports.
type Capability uint32

// Only flags that generated code or the runtime actually consults are
// declared. Add a flag together with its first reader: a flag nothing reads
// is a promise the dialect layer does not keep.
const (
	// CapForUpdate indicates support for SELECT ... FOR UPDATE row-level locking.
	CapForUpdate Capability = 1 << iota
	// CapForShare indicates support for SELECT ... FOR SHARE.
	CapForShare
	// CapLockWithDistinct indicates that a row-locking clause (FOR UPDATE /
	// FOR SHARE) may be combined with SELECT DISTINCT. Postgres rejects the
	// combination ("FOR UPDATE is not allowed with DISTINCT clause"), so the
	// generated ForUpdate/ForShare builders drop DISTINCT on dialects that
	// support locking but lack this capability. MySQL accepts it. SQLite has
	// no row locking at all (no CapForUpdate), so the flag is not consulted.
	CapLockWithDistinct
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
	Postgres: {CapForUpdate | CapForShare},
	MySQL:    {CapForUpdate | CapForShare | CapLockWithDistinct},
	SQLite:   {},
}

// GetCapabilities returns the capability set for the named dialect.
// Unknown dialects return an empty Capabilities (nothing supported).
func GetCapabilities(name string) Capabilities {
	return dialectCaps[name]
}
