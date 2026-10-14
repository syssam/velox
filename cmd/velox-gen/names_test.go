package main

import "testing"

func TestNames(t *testing.T) {
	for _, c := range []struct {
		fn       func(string) string
		in, want string
	}{
		{plural, "Invoice", "Invoices"}, {plural, "Category", "Categories"}, {plural, "Day", "Days"},
		{plural, "Box", "Boxes"}, {plural, "Status", "Statuses"}, {plural, "Batch", "Batches"},
		{plural, "Wish", "Wishes"}, {plural, "Quiz", "Quizes"}, {plural, "Y", "Ys"},
		{lowerCamel, "Invoice", "invoice"}, {lowerCamel, "ID", "id"}, {lowerCamel, "URLRecord", "urlRecord"},
		{lowerCamel, "HTTPServer", "httpServer"}, {lowerCamel, "line", "line"},
		{snake, "Invoice", "invoice"}, {snake, "OrgID", "org_id"}, {snake, "HTTPServer", "http_server"},
		{snake, "URLRecord", "url_record"},
	} {
		if got := c.fn(c.in); got != c.want {
			t.Errorf("(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
