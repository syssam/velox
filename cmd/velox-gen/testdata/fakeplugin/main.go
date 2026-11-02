// Command fakeplugin is the plugin the velox-gen tests run. It reads the
// request, and answers as the environment says:
//
//	FAKEPLUGIN_DUMP      a file to write the request to
//	FAKEPLUGIN_MODE      "error", "exit", "panic" or "malformed" for a failing plugin;
//	                     "protocol1" answers files, and "protocol1error" an
//	                     error, in protocol 1's shape, without plugin_protocol
//	FAKEPLUGIN_FILES     the files array of the response, as JSON
//	FAKEPLUGIN_WARNINGS  the warnings of the response, as a JSON array
//	FAKEPLUGIN_NAME      a suffix for the default files, to name them apart
//	FAKEPLUGIN_V         the variant the default files hold, to change their content
//
// Built as a second tool, fakeplugin2, it reads FAKEPLUGIN2_FILES and so on
// before FAKEPLUGIN_FILES, so that two plugins answer differently.
//
// With none of these it writes fake.graphqls and fake_gql.go. It answers
// through plugin.Main, but for the modes that a plugin of velox cannot
// answer.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/syssam/velox/plugin"
)

// getenv is the setting for this tool: FAKEPLUGIN2_FILES for fakeplugin2,
// else FAKEPLUGIN_FILES.
func getenv(key string) string {
	tool := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	if v := os.Getenv(strings.ToUpper(tool) + strings.TrimPrefix(key, "FAKEPLUGIN")); v != "" {
		return v
	}
	return os.Getenv(key)
}

func main() {
	in, err := io.ReadAll(os.Stdin)
	if err != nil {
		fail(err)
	}
	if p := os.Getenv("FAKEPLUGIN_DUMP"); p != "" {
		if err := os.WriteFile(p, in, 0o644); err != nil {
			fail(err)
		}
	}
	switch getenv("FAKEPLUGIN_MODE") {
	case "malformed":
		fmt.Print("{not json")
		return
	case "protocol1":
		// A plugin of protocol 1 that does not check the request.
		fmt.Println(`{"files":[{"path":"old.go","content":"package gen\n"}]}`)
		return
	case "protocol1error":
		// A plugin of protocol 1 that refuses a request of another protocol.
		fmt.Println(`{"files":null,"error":"plugin protocol 2 is not supported: this plugin speaks 1"}`)
		return
	}
	// plugin.Main reads standard input, which is read already: it gets the
	// request again through a pipe.
	r, w, err := os.Pipe()
	if err != nil {
		fail(err)
	}
	go func() {
		_, _ = w.Write(in)
		_ = w.Close()
	}()
	os.Stdin = r
	plugin.Main(plugin.Supports{Protocol: 2, Model: 3}, generate)
}

func generate(req *plugin.Request) ([]plugin.File, []string, error) {
	var warnings []string
	if raw := getenv("FAKEPLUGIN_WARNINGS"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &warnings); err != nil {
			fail(err)
		}
	}
	switch getenv("FAKEPLUGIN_MODE") {
	case "error":
		return nil, warnings, errors.New("the fake plugin refuses")
	case "exit":
		fmt.Fprintln(os.Stderr, "the fake plugin crashed")
		os.Exit(3)
	case "panic":
		panic("the fake plugin panics")
	}
	if raw := getenv("FAKEPLUGIN_FILES"); raw != "" {
		var files []plugin.File
		if err := json.Unmarshal([]byte(raw), &files); err != nil {
			fail(err)
		}
		return files, warnings, nil
	}
	name, v := getenv("FAKEPLUGIN_NAME"), getenv("FAKEPLUGIN_V")
	return []plugin.File{
		{Path: "fake" + name + ".graphqls", Content: fmt.Sprintf("# variant %s\ntype Fake%s { id: ID! }\n", v, name)},
		{Path: "fake" + name + "_gql.go", Content: fmt.Sprintf("package %s\n\nconst Fake%sVariant = %q\n", req.OutPackage, name, v)},
	}, warnings, nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}
