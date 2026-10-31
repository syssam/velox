package codegen

import "fmt"

// suggest is "; did you mean %q?" with the name of names closest to name by
// edit distance, when that distance is 1 or 2 and less than len(name); the
// first in names' order on a tie; "" otherwise. A rule's message appends it
// to an unknown name, which is often a typo.
func suggest(name string, names []string) string {
	best, dist := "", 3
	for _, n := range names {
		if d := editDistance(name, n); d < dist {
			best, dist = n, d
		}
	}
	if best == "" || dist == 0 || dist >= len([]rune(name)) {
		return ""
	}
	return fmt.Sprintf("; did you mean %q?", best)
}

// editDistance is the Levenshtein distance of a and b, in runes: the fewest
// insertions, deletions and substitutions that turn a into b.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev, cur := make([]int, len(rb)+1), make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			sub := prev[j-1]
			if ra[i-1] != rb[j-1] {
				sub++
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, sub)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
