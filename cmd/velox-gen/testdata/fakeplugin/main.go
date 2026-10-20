// Command fakeplugin is the plugin the velox-gen tests run. It reads the
// request, and answers as the environment says:
//
//	FAKEPLUGIN_DUMP   a file to write the request to
//	FAKEPLUGIN_MODE   "error", "exit" or "malformed" for a failing plugin
//	FAKEPLUGIN_FILES  the files array of the response, as JSON
//	FAKEPLUGIN_NAME   a suffix for the default files, to name them apart
//	FAKEPLUGIN_V      the variant the default files hold, to change their content
//
// Built as a second tool, fakeplugin2, it reads FAKEPLUGIN2_FILES and so on
// before FAKEPLUGIN_FILES, so that two plugins answer differently.
//
// With none of these it writes fake.graphqls and fake_gql.go.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/syssam/velox2/plugin"
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
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if p := os.Getenv("FAKEPLUGIN_DUMP"); p != "" {
		if err := os.WriteFile(p, in, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	var req plugin.Request
	if err := json.Unmarshal(in, &req); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	switch getenv("FAKEPLUGIN_MODE") {
	case "error":
		write(plugin.Response{Error: "the fake plugin refuses"})
		return
	case "exit":
		fmt.Fprintln(os.Stderr, "the fake plugin crashed")
		os.Exit(3)
	case "malformed":
		fmt.Print("{not json")
		return
	}
	if raw := getenv("FAKEPLUGIN_FILES"); raw != "" {
		var files []plugin.File
		if err := json.Unmarshal([]byte(raw), &files); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		write(plugin.Response{Files: files})
		return
	}
	name, v := getenv("FAKEPLUGIN_NAME"), getenv("FAKEPLUGIN_V")
	write(plugin.Response{Files: []plugin.File{
		{Path: "fake" + name + ".graphqls", Content: fmt.Sprintf("# variant %s\ntype Fake%s { id: ID! }\n", v, name)},
		{Path: "fake" + name + "_gql.go", Content: fmt.Sprintf("package %s\n\nconst Fake%sVariant = %q\n", req.OutPackage, name, v)},
	}})
}

func write(r plugin.Response) {
	if err := json.NewEncoder(os.Stdout).Encode(r); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
