package gen

import "testing"

func TestNames(t *testing.T) {
	for in, want := range map[string]string{"org_id": "OrgID", "user_posts": "UserPosts", "api_key": "APIKey", "url": "URL", "name": "Name", "uuid_v7": "UUIDV7"} {
		if got := pascal(in); got != want {
			t.Errorf("pascal(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"User": "users", "Category": "categories", "Box": "boxes", "OrgUnit": "org_units"} {
		if got := plural(snake(in)); got != want {
			t.Errorf("plural(snake(%q)) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"friends": "friend", "categories": "category", "boxes": "box", "matches": "match", "following": "following"} {
		if got := singular(in); got != want {
			t.Errorf("singular(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]bool{"Done": true, "In-progress": false, "": false, "func": false, "x1": true} {
		if got := ident(in); got != want {
			t.Errorf("ident(%q) = %v, want %v", in, got, want)
		}
	}
}
