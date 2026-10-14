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
			if i > 0 && (lower(s[i-1]) || upper(s[i-1]) && i+1 < len(s) && lower(s[i+1])) {
				b.WriteByte('_')
			}
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String()
}
