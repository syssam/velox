package gen

import "github.com/syssam/velox"

// UserDescriptor is User's metadata as the generated code builds it, for the
// external test to read the column defaults.
func UserDescriptor() velox.Descriptor[User] { return userDescriptor() }
