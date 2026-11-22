package plugin_test

import (
	"os"
	"regexp"
	"strconv"
	"testing"

	"github.com/syssam/velox/plugin"
)

// The package documentation shows the request with the versions velox-gen
// sends.
func TestDocShowsVersions(t *testing.T) {
	src, err := os.ReadFile("doc.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		re   string
		want int
	}{
		{`"model": \{"version": (\d+)`, plugin.ModelVersion},
		{`"plugin_protocol": (\d+)`, plugin.ProtocolVersion},
	} {
		ms := regexp.MustCompile(c.re).FindAllSubmatch(src, -1)
		if len(ms) == 0 {
			t.Errorf("doc.go has no %s", c.re)
		}
		for _, m := range ms {
			if n, _ := strconv.Atoi(string(m[1])); n != c.want {
				t.Errorf("doc.go shows %s, want %d", m[0], c.want)
			}
		}
	}
}
