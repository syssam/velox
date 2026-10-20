package gen

import "go/token"

// ident reports whether s can name a Go declaration.
func ident(s string) bool { return token.IsIdentifier(s) && !token.IsKeyword(s) }
