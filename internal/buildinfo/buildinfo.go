// Package buildinfo names the velox module a program is built from.
package buildinfo

import "runtime/debug"

// modulePath is the import path of the velox library.
const modulePath = "github.com/syssam/velox"

// Generator is "velox-gen" and the version of the velox module the program
// is built from, "(devel)" when the build does not say, as velox-gen version
// prints it.
func Generator() string {
	bi, _ := debug.ReadBuildInfo()
	return generator(bi)
}

// generator is Generator for the build bi, nil when it has none. A dependency
// replaced by a directory is "(devel)": its version is what go.mod requires, a
// release the build does not contain or a placeholder.
func generator(bi *debug.BuildInfo) string {
	v := ""
	if bi != nil {
		if bi.Main.Path == modulePath {
			v = bi.Main.Version
		}
		for _, d := range bi.Deps {
			if d.Path == modulePath {
				v = d.Version
				if d.Replace != nil {
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
