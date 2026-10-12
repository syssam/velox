package orm

import "testing"

func TestSnake(t *testing.T) {
	for in, want := range map[string]string{
		"User": "user", "SalesOrder": "sales_order", "HTTPLog": "http_log", "OAuth2Token": "o_auth2_token",
		"user": "user", "A": "a", "": "", "PriceListItem": "price_list_item", "ID": "id",
	} {
		if got := snake(in); got != want {
			t.Errorf("snake(%q) = %q, want %q", in, got, want)
		}
	}
}
