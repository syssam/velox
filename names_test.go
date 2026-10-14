package velox

import "testing"

func TestSnake(t *testing.T) {
	for in, want := range map[string]string{
		"ID": "id", "OrgID": "org_id", "CreatedAt": "created_at", "HTTPServer": "http_server",
		"URLPath": "url_path", "userID": "user_id", "E0001": "e0001", "A": "a", "Invoice": "invoice",
		"lineItem": "line_item", "V2Name": "v2_name", "Id2Name": "id2_name",
	} {
		if got := snake(in); got != want {
			t.Errorf("snake(%q) = %q, want %q", in, got, want)
		}
	}
}
