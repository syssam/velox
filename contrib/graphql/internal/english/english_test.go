package english

import "testing"

func TestArticle(t *testing.T) {
	for word, want := range map[string]string{
		"Account": "an", "Event": "an", "Owner": "an", "Item": "an", "Post": "a",
		"Under": "an", "Umbrella": "an", "Unread": "an", "Upper": "an",
		"User": "a", "Unit": "a", "Usage": "a", "Uuid": "a", "Utility": "a",
		"under": "an", "USER": "a", "UNDER": "an",
		"U": "a", "Un": "a", "Ux": "a", "Ubq": "an",
		"Label": "a", "Category": "a", "int32": "an", "uint32": "a", "[]byte": "a",
		"": "a",
	} {
		if got := Article(word); got != want {
			t.Errorf("Article(%q) = %q, want %q", word, got, want)
		}
	}
}
