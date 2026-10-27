// Package english holds the one English rule the generated godoc and the
// runtime's error messages share: the indefinite article of a name.
package english

import "strings"

// Article returns the indefinite article of word, "a" or "an", by how the word
// is said and without regard to case:
//   - "an" before A, E, I and O (Account, Event, Item, Owner);
//   - before U, "an" only when the next two letters are both consonants
//     (Under, Umbrella, Unread, Upper), and "a" otherwise (User, Unit, Usage,
//     Uuid, Utility);
//   - "a" before every other letter, and for the empty word.
//
// A word is not looked up: Hour and Honest take "a". The rule serves type
// names, which are the names of an application's schema.
func Article(word string) string {
	w := strings.ToLower(word)
	if w == "" {
		return "a"
	}
	switch w[0] {
	case 'a', 'e', 'i', 'o':
		return "an"
	case 'u':
		if len(w) >= 3 && isConsonant(w[1]) && isConsonant(w[2]) {
			return "an"
		}
	}
	return "a"
}

// isConsonant reports whether c is a letter that is not a vowel (y counts as a
// consonant).
func isConsonant(c byte) bool {
	return c >= 'a' && c <= 'z' && !strings.ContainsRune("aeiou", rune(c))
}
