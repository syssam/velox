package gen

import "github.com/syssam/velox"

// Policy and Hooks, written in the generated package, which velox-gen
// refuses: generated without binding them, the metadata does not call them.
func (*Post) Policy() velox.Policy[Post] { return velox.Policy[Post]{} }

func (*User) Hooks() []velox.Hook[User] { return nil }
