package engine

import (
	"database/sql"
)

// A JSON column's destination must stay unwrapped: conn wraps no sql.Scanner.
var _ sql.Scanner = (*codecScanner)(nil)
