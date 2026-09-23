package dialect

import (
	"context"
	"strconv"
	"strings"
)

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
	// CapWindowFunctions indicates support for window functions — the
	// ROW_NUMBER() OVER (PARTITION BY ...) that sql.Selector.LimitPerPartition
	// renders. Generated eager loaders consult it before limiting a to-many
	// edge per parent, and without it read every row and keep the first n of
	// each parent in memory. MySQL gained window functions in 8.0 (MariaDB in
	// 10.2), so the static MySQL set leaves the flag out and only a driver
	// that asked its server for the version grants it: see DriverCapabilities.
	CapWindowFunctions
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
	Postgres: {CapForUpdate | CapForShare | CapWindowFunctions},
	MySQL:    {CapForUpdate | CapForShare | CapLockWithDistinct},
	SQLite:   {CapWindowFunctions},
}

// GetCapabilities returns the capability set for the named dialect.
// Unknown dialects return an empty Capabilities (nothing supported).
//
// The set is keyed by dialect name only, so a capability that depends on
// the server version is left out of it (CapWindowFunctions on MySQL). Code
// that holds a driver and needs such a capability asks DriverCapabilities,
// which consults the server.
func GetCapabilities(name string) Capabilities {
	return dialectCaps[name]
}

// VersionCapabilities returns the capabilities of a server of the named
// dialect that reports version, as SELECT VERSION() prints it (for MySQL
// "8.0.36", "5.7.44-log" or "10.6.12-MariaDB-..."). A dialect whose
// capabilities do not depend on the version returns its static set.
//
// MySQL starts from its static set and gains CapWindowFunctions on MySQL
// 8.0+ and MariaDB 10.2+. It loses CapForShare below MySQL 8.0.1 and on
// MariaDB, neither of which accepts the FOR SHARE spelling. A version that
// does not parse yields the static set: a missing capability costs a slower
// path, a claimed one the server lacks is a syntax error.
func VersionCapabilities(name, version string) Capabilities {
	caps := GetCapabilities(name)
	if name != MySQL {
		return caps
	}
	maria := strings.Contains(strings.ToLower(version), "mariadb")
	if maria {
		// The protocol handshake prefixes MariaDB's version with "5.5.5-"
		// for old clients; SELECT VERSION() does not, but strip it anyway.
		version = strings.TrimPrefix(version, "5.5.5-")
	}
	v, ok := parseVersion(version)
	if !ok {
		return caps
	}
	if maria {
		caps.flags &^= CapForShare
		if !v.less(version3{10, 2, 0}) {
			caps.flags |= CapWindowFunctions
		}
		return caps
	}
	if v.less(version3{8, 0, 1}) {
		caps.flags &^= CapForShare
	}
	if !v.less(version3{8, 0, 0}) {
		caps.flags |= CapWindowFunctions
	}
	return caps
}

// CapabilityProber is implemented by a driver that knows the capabilities
// of the server it is connected to, not only those of its dialect.
// dialect/sql's *Driver implements it by asking a MySQL server for its
// version once and caching the answer.
type CapabilityProber interface {
	ServerCapabilities(ctx context.Context) (Capabilities, error)
}

// CapabilityProberVia is a CapabilityProber that can run its probe on a
// caller-supplied ExecQuerier — the driver or open transaction the caller
// is using — when it has no cached answer yet. DriverCapabilities prefers
// it over CapabilityProber.
type CapabilityProberVia interface {
	ServerCapabilitiesVia(ctx context.Context, q ExecQuerier) (Capabilities, error)
}

// DriverCapabilities returns the capabilities of the server behind d. It
// looks through DebugDriver and transactional drivers (anything with a
// BaseDriver() Driver method, like the generated txDriver) for a
// CapabilityProber, and falls back to the static set of d's dialect when
// there is none — so an unknown wrapper around a MySQL 8 driver loses
// CapWindowFunctions and takes the slower path, never a syntax error.
func DriverCapabilities(ctx context.Context, d Driver) (Capabilities, error) {
	// A probe runs on the driver the caller holds: inside a transaction that
	// is the transaction, whose connection is already checked out. Probing
	// through the base driver instead needs a second pooled connection and
	// blocks forever on a pool of one.
	var via ExecQuerier = d
	for d != nil {
		switch v := d.(type) {
		case CapabilityProberVia:
			return v.ServerCapabilitiesVia(ctx, via)
		case CapabilityProber:
			return v.ServerCapabilities(ctx)
		case *DebugDriver:
			d = v.Driver
		case interface{ BaseDriver() Driver }:
			d = v.BaseDriver()
		default:
			return GetCapabilities(d.Dialect()), nil
		}
	}
	return Capabilities{}, nil
}

// version3 is a major.minor.patch server version.
type version3 [3]int

func (v version3) less(o version3) bool {
	for i := range v {
		if v[i] != o[i] {
			return v[i] < o[i]
		}
	}
	return false
}

// parseVersion reads the leading "major.minor[.patch]" of a server version,
// ignoring everything from the first character that is neither a digit nor
// a dot ("8.0.36-log" is 8.0.36).
func parseVersion(s string) (version3, bool) {
	if i := strings.IndexFunc(s, func(r rune) bool { return r != '.' && (r < '0' || r > '9') }); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return version3{}, false
	}
	var v version3
	for i := 0; i < len(parts) && i < len(v); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return version3{}, false
		}
		v[i] = n
	}
	return v, true
}
