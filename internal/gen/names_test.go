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
	// ent's snake's own outputs (entc/gen/func.go, run as is).
	for in, want := range map[string]string{
		"User": "user", "Username": "username", "FullName": "full_name", "HTTPCode": "http_code",
		"S3Bucket": "s3bucket", "OAuth2Token": "oauth2token", "HTTPServer": "http_server", "OrgID": "org_id",
		"OrgUnit": "org_unit", "UserInfo": "user_info", "ID": "id", "UUID": "uuid", "APIKey": "api_key",
		"A": "a", "": "", "already_snake": "already_snake", "V2Thing": "v2thing", "MyHTTPServer": "my_http_server",
		"Base64Data": "base64data", "IPv4Addr": "ipv4addr", "ABCd": "ab_cd", "userID": "user_id", "x1Y2": "x1y2",
		"JSONData": "json_data", "clashOwner": "clash_owner",
	} {
		if got := snake(in); got != want {
			t.Errorf("snake(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]bool{"Done": true, "In-progress": false, "": false, "func": false, "x1": true} {
		if got := ident(in); got != want {
			t.Errorf("ident(%q) = %v, want %v", in, got, want)
		}
	}
}
