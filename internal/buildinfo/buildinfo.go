// Package buildinfo names the velox2 module a program is built from.
package buildinfo

import "runtime/debug"

// modulePath is the import path of the velox library.
const modulePath = "github.com/syssam/velox2"

// Generator is "velox-gen" and the version of the velox2 module the program
// is built from, "(devel)" when the build does not say, as velox-gen version
// prints it.
func Generator() string {
	v := ""
	if bi, ok := debug.ReadBuildInfo(); ok {
		if bi.Main.Path == modulePath {
			v = bi.Main.Version
		}
		for _, d := range bi.Deps {
			if d.Path == modulePath {
				v = d.Version
				if d.Replace != nil && d.Replace.Version != "" {
					v = d.Replace.Version
				}
			}
		}
	}
	if v == "" {
		v = "(devel)"
	}
	return "velox-gen " + v
}
