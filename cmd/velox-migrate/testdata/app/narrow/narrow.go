// Package narrow names the library and one generated package, and not the
// generated root package: what a service does that is handed its handle.
package narrow

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/tests/integration/entity"
)

// Gone reports whether looking a user up found none.
func Gone(_ *entity.User, err error) bool { return velox.IsNotFound(err) }
