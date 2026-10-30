package plugin_test

import (
	"fmt"
	"os"
	"strings"

	"github.com/syssam/velox2/plugin"
)

// Run reads a Request and writes a Response, and a plugin's main calls Main, which
// is Run on standard input and output. The generator gets the model and returns
// the files to write.
func ExampleRun() {
	in := strings.NewReader(`{"plugin_protocol":2,"generator":"velox-gen","out_package":"gen","out_path":"example.com/app/gen",
		"model":{"version":2,"schema_path":"example.com/app/schema","entities":[{"name":"User"}]}}`)
	err := plugin.Run(plugin.Supports{}, in, os.Stdout, func(req *plugin.Request) ([]plugin.File, []string, error) {
		fmt.Fprintln(os.Stderr, req.Model.Entities[0].Name)
		return nil, nil, nil
	})
	fmt.Println(err)
}

// Snake, GoName and Plural are the names velox-gen derives, for a plugin to agree
// with it.
func ExampleSnake() {
	fmt.Println(plugin.Snake("OrgMember"), plugin.Plural("Category"), plugin.GoName("org_id"))
	// Output:
	// org_member Categories OrgID
}
