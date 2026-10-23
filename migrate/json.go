package migrate

import (
	"bytes"
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

// MarshalJSON writes the schema as compact JSON, sorted, without Pos. It does
// not change s.
func (s *Schema) MarshalJSON() ([]byte, error) {
	c := s.clone()
	c.sort()
	// A predicate or a check holds < and >; the file stays readable.
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(snapshot{Version: snapshotVersion, Dialect: c.Dialect.String(), Tables: c.Tables, Enums: c.Enums}); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// UnmarshalJSON reads what MarshalJSON writes. It refuses another version and
// a dialect it does not know.
func (s *Schema) UnmarshalJSON(data []byte) error {
	var in snapshot
	if err := json.Unmarshal(data, &in); err != nil {
		return err
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
		return &Error{Code: "dialect", Reason: fmt.Sprintf("schema.json has the unknown dialect %q", in.Dialect)}
	}
	*s = Schema{Dialect: d, Tables: in.Tables, Enums: in.Enums}
	s.sort()
	return nil
}
