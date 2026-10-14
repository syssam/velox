package velox

import "strings"

// snake returns the snake_case of a Go identifier: OrgID becomes org_id and
// HTTPServer becomes http_server.
func snake(s string) string {
	lower := func(c byte) bool { return 'a' <= c && c <= 'z' || '0' <= c && c <= '9' }
	upper := func(c byte) bool { return 'A' <= c && c <= 'Z' }
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i := range len(s) {
		c := s[i]
		if upper(c) {
			// A word starts after a lower-case letter or digit, and at the last
			// capital of an acronym followed by a lower-case letter (HTTPServer);
			// a digit after an acronym continues it (OrgID2 → org_id2).
			if i > 0 && (lower(s[i-1]) || upper(s[i-1]) && i+1 < len(s) && 'a' <= s[i+1] && s[i+1] <= 'z') {
				b.WriteByte('_')
			}
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String()
}
