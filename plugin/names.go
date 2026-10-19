package plugin

import "strings"

// Singular is velox-gen's singular of an English noun, the inverse of the
// plural it names tables and handles with: categories → category, boxes →
// box, friends → friend. A word that does not end in "s", such as children,
// is returned unchanged. velox-gen names join columns and join entities with
// it, and a plugin can name things after edges the same way.
func Singular(s string) string {
	switch {
	case strings.HasSuffix(s, "ies") && len(s) > 3:
		return s[:len(s)-3] + "y"
	case strings.HasSuffix(s, "ses"), strings.HasSuffix(s, "xes"), strings.HasSuffix(s, "zes"),
		strings.HasSuffix(s, "ches"), strings.HasSuffix(s, "shes"):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "s") && len(s) > 1:
		return s[:len(s)-1]
	}
	return s
}
