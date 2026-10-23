package migrate

import (
	"encoding/json"
	"fmt"

	"github.com/syssam/velox2"
)

// snapshotVersion is the version of the JSON this package reads and writes.
const snapshotVersion = 1

// snapshot is the JSON of a Schema.
type snapshot struct {
	Version int     `json:"version"`
	Dialect string  `json:"dialect"`
	Tables  []Table `json:"tables,omitempty"`
	Enums   []Enum  `json:"enums,omitempty"`
}

// MarshalJSON writes the schema as compact JSON, sorted, without Pos. It
// escapes <, > and & as encoding/json does, so that the bytes are the same
// whether the caller uses this method, json.Marshal or json.MarshalIndent. It
// refuses a Dialect that is not one of the three. It does not change s.
func (s Schema) MarshalJSON() ([]byte, error) {
	if !knownDialect(s.Dialect) {
		return nil, dialectError(s.Dialect.String())
	}
	c := s.clone()
	c.sort()
	return json.Marshal(snapshot{Version: snapshotVersion, Dialect: c.Dialect.String(), Tables: c.Tables, Enums: c.Enums})
}

// knownDialect reports whether d is a dialect a snapshot can hold.
func knownDialect(d velox.Dialect) bool {
	return d == velox.Postgres || d == velox.MySQL || d == velox.SQLite
}

func dialectError(name string) error {
	return &Error{Code: "dialect", Reason: fmt.Sprintf("schema.json has the unknown dialect %q", name)}
}

// UnmarshalJSON reads what MarshalJSON writes. It refuses another version and
// a dialect it does not know.
func (s *Schema) UnmarshalJSON(data []byte) error {
	var in snapshot
	if err := json.Unmarshal(data, &in); err != nil {
		return &Error{Code: "json", Reason: "schema.json: " + err.Error()}
	}
	if in.Version != snapshotVersion {
		return &Error{Code: "version", Reason: fmt.Sprintf("schema.json is version %d; this velox reads version %d", in.Version, snapshotVersion)}
	}
	var d velox.Dialect
	for _, c := range [...]velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
		if c.String() == in.Dialect {
			d = c
		}
	}
	if d == 0 {
		return dialectError(in.Dialect)
	}
	*s = Schema{Dialect: d, Tables: in.Tables, Enums: in.Enums}
	s.sort()
	return nil
}
