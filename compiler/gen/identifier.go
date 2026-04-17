package gen

import "regexp"

// safeIdentifierRe matches names that are safe as both Go struct fields
// (after PascalCasing) and SQL column names (when quoted by the dialect's
// Builder.Ident). Deliberately narrower than `token.IsIdentifier`: Go
// reserved keywords like "type", "func", or "range" are accepted here
// because they PascalCase into valid struct fields and Builder.Quote()
// wraps them as dialect-quoted identifiers (`type`, "range").
//
// The SQL-injection concern closed by the original schema-boundary check
// (commit bce8fa3) is preserved — this regex rejects every character that
// could break out of a quoted identifier (quotes, semicolons, whitespace,
// null bytes, unicode smugglers, etc.).
var safeIdentifierRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
